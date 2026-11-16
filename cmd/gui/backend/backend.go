// Package backend 為本機 GUI 提供給前端的 Wails 服務, 把 internal/monitor,
// internal/settings 與 internal/history 的功能包裝為前端可呼叫的方法, 並以
// 事件通知前端.
//
// 服務方法回傳的錯誤經 Wails 傳給前端時, 錯誤的 cause 為
// {"code": ..., "message": ...}, code 為 monitor.Code 的值, message 為英文說明.
package backend

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gpumon/internal/history"
	"gpumon/internal/monitor"
	"gpumon/internal/protocol"
	"gpumon/internal/recording"
	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

func init() {
	application.RegisterEvent[monitor.HostStatus](monitor.EventHost)
	application.RegisterEvent[monitor.Live](monitor.EventSample)
	application.RegisterEvent[monitor.DevicesEvent](monitor.EventDevices)
	application.RegisterEvent[monitor.ConnectError](monitor.EventError)
	application.RegisterEvent[monitor.Prompt](monitor.EventPrompt)
	application.RegisterEvent[monitor.PromptClosed](monitor.EventPromptClosed)
	application.RegisterEvent[monitor.UploadNotice](monitor.EventUpload)
	application.RegisterEvent[monitor.UploadClosed](monitor.EventUploadClosed)
	application.RegisterEvent[monitor.HostKeyPrompt](monitor.EventHostKey)
	application.RegisterEvent[monitor.HostKeyClosed](monitor.EventHostKeyClosed)
	application.RegisterEvent[monitor.KeySetupPrompt](monitor.EventKeySetup)
	application.RegisterEvent[monitor.KeySetupClosed](monitor.EventKeySetupClosed)
	application.RegisterEvent[monitor.KeySetupResult](monitor.EventKeySetupResult)
	application.RegisterEvent[UISettings](EventUISettings)
	application.RegisterEvent[QuitRequest](EventQuitRequest)
	application.RegisterEvent[ShowPage](EventShowPage)
	application.RegisterEvent[AboutTab](EventAboutTab)
}

// Backend 為各服務共用的狀態.
type Backend struct {
	dataDir string
	store   *settings.Store
	monitor *monitor.Manager
	// shell 為主視窗, 選單與匣圖示的管理者, 由 SetShell 設定; 未設定時相關方法不做事.
	shell atomic.Pointer[Shell]
	// hostsChanged 於任一主機的狀態改變時呼叫, 由 OnHostsChanged 設定.
	hostsChanged atomic.Pointer[func()]
}

// Shell 為主視窗, 選單與匣圖示的管理者所提供的操作.
type Shell interface {
	// ConfirmQuit 於使用者在結束確認視窗按下 "結束" 後呼叫, 不再確認即結束程式.
	ConfirmQuit()
	// UIChanged 於外觀或語言的設定變更後呼叫.
	UIChanged(UISettings)
}

// New 讀取設定並建立連線管理. 設定檔有異常時記錄到日誌, 以預設值或盡可能
// 解析出的內容繼續.
func New(dataDir string, log *slog.Logger) *Backend {
	store, err := settings.Open(dataDir)
	if err != nil {
		log.Warn("settings", "err", err)
	}
	upgradeLegacyHosts(dataDir, store, sshconn.UserConfigDefaults, log)
	b := &Backend{dataDir: dataDir, store: store}
	b.monitor = monitor.New(monitor.Options{
		DataDir:  dataDir,
		Settings: store,
		Notifier: monitor.NotifierFunc(b.notify),
		Logger:   log,
	})
	return b
}

// upgradeLegacyHosts 以 ~/.ssh/config 填入舊版設定檔中各主機空白的欄位一次, 見
// settings.Store.UpgradeLegacyHosts. 主機識別因填入連接埠而改變時, 該主機原有的記錄檔
// 改以新的識別記錄, 使其仍對應到同一台主機; 搬移於設定檔寫回之前進行, 寫回前程式
// 中止時下次啟動再處理一次. ~/.ssh/config 的問題與搬移失敗只記錄到日誌.
func upgradeLegacyHosts(dataDir string, store *settings.Store,
	lookup func(addr string) (sshconn.ConfigDefaults, error), log *slog.Logger) {
	if !store.HasLegacyHosts() {
		return
	}
	defaults := func(addr string) settings.LegacyDefaults {
		d, err := lookup(addr)
		if err != nil {
			log.Warn("cannot read the SSH configuration", "host", addr, "err", err)
			return settings.LegacyDefaults{}
		}
		for _, w := range d.Warnings {
			log.Warn("SSH configuration", "host", addr, "warning", w)
		}
		return settings.LegacyDefaults{User: d.User, Port: d.Port, KeyFile: d.KeyFile, IdentitiesOnly: d.IdentitiesOnly}
	}
	moveRecordings := func(ups []settings.HostUpgrade) {
		for _, u := range ups {
			if u.OldRecordingHost == "" || u.OldRecordingHost == u.NewID {
				continue
			}
			if err := recording.Rehost(dataDir, u.OldRecordingHost, u.NewID); err != nil {
				log.Warn("cannot move recordings to the new host name", "from", u.OldRecordingHost, "to", u.NewID, "err", err)
			}
		}
	}
	ups, err := store.UpgradeLegacyHosts(defaults, moveRecordings)
	for _, u := range ups {
		log.Info("upgraded host settings", "from", u.OldID, "to", u.NewID)
	}
	if err != nil {
		log.Warn("cannot save the upgraded host settings", "err", err)
	}
}

// emit 把通知轉為 Wails 事件.
func emit(name string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data)
	}
}

// notify 把連線管理的通知轉為 Wails 事件, 主機狀態改變時另呼叫 OnHostsChanged 所設的
// 函數. 呼叫端可能持有連線管理的鎖, 該函數不得在此同步查詢主機狀態.
func (b *Backend) notify(name string, data any) {
	emit(name, data)
	if name == monitor.EventHost {
		if fn := b.hostsChanged.Load(); fn != nil {
			(*fn)()
		}
	}
}

// SetShell 設定主視窗, 選單與匣圖示的管理者.
func (b *Backend) SetShell(s Shell) {
	b.shell.Store(&s)
}

// OnHostsChanged 設定任一主機的狀態改變時呼叫的函數. fn 於送出通知的 goroutine 中
// 執行, 可能持有連線管理的鎖, 須立即返回, 不得在其中呼叫 Hosts 等方法.
func (b *Backend) OnHostsChanged(fn func()) {
	b.hostsChanged.Store(&fn)
}

// Hosts 回傳所有主機的狀態, 依新增的先後排列.
func (b *Backend) Hosts() []monitor.HostStatus { return b.monitor.Hosts() }

// ActiveCount 回傳處於連線中或重新連線中狀態的主機數.
func (b *Backend) ActiveCount() int { return b.monitor.ActiveCount() }

// Store 回傳設定的存取物件.
func (b *Backend) Store() *settings.Store { return b.store }

func (b *Backend) uiChanged() {
	d := b.store.Snapshot()
	ui := UISettings{Appearance: d.Appearance, Language: d.Language}
	emit(EventUISettings, ui)
	if s := b.shell.Load(); s != nil {
		(*s).UIChanged(ui)
	}
}

// Services 回傳要註冊至 Wails 的所有服務.
func (b *Backend) Services() []application.Service {
	return []application.Service{
		application.NewService(&HostService{b: b}),
		application.NewService(&RecordingService{b: b}),
		application.NewService(&SettingsService{b: b}),
		application.NewService(&AppService{b: b}),
	}
}

// Close 中斷所有連線, 把尚未存檔的取樣寫入記錄檔, 並銷毀所有密碼.
func (b *Backend) Close() {
	b.monitor.Close()
}

// toErr 把錯誤轉為帶代碼的 *monitor.Error.
func toErr(err error) error {
	var me *monitor.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &me):
		return me
	case errors.Is(err, history.ErrActiveFile):
		return &monitor.Error{Code: monitor.CodeActive, Message: err.Error()}
	case errors.Is(err, history.ErrOutsideHistory), errors.Is(err, settings.ErrInvalidValue):
		return &monitor.Error{Code: monitor.CodeInvalid, Message: err.Error()}
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, settings.ErrHostNotFound):
		return &monitor.Error{Code: monitor.CodeNotFound, Message: err.Error()}
	case errors.Is(err, settings.ErrDuplicateHost):
		return &monitor.Error{Code: monitor.CodeDuplicate, Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &monitor.Error{Code: monitor.CodeClosed, Message: err.Error()}
	}
	return &monitor.Error{Code: monitor.CodeFailed, Message: err.Error()}
}

// HostService 提供主機管理, 連線, 密碼輸入, 主機金鑰的確認, 免密碼登入的詢問與
// 監看畫面所需的資料.
type HostService struct{ b *Backend }

// ServiceShutdown 於程式結束時中斷所有連線, 存檔並銷毀所有密碼.
func (s *HostService) ServiceShutdown() error {
	s.b.Close()
	return nil
}

// Hosts 回傳所有主機的設定與狀態, 依新增的先後排列.
func (s *HostService) Hosts() []monitor.HostStatus { return s.b.monitor.Hosts() }

// AddHost 新增主機, 回傳其狀態.
func (s *HostService) AddHost(conn settings.HostConn) (monitor.HostStatus, error) {
	st, err := s.b.monitor.AddHost(conn)
	return st, toErr(err)
}

// UpdateHost 編輯主機的表單欄位, 回傳編輯後的狀態; 主機的 ID 可能改變.
func (s *HostService) UpdateHost(id string, conn settings.HostConn) (monitor.HostStatus, error) {
	st, err := s.b.monitor.UpdateHost(id, conn)
	return st, toErr(err)
}

// RemoveHost 刪除主機.
func (s *HostService) RemoveHost(id string) error { return toErr(s.b.monitor.RemoveHost(id)) }

// Connect 開始連線, 立即回傳.
func (s *HostService) Connect(id string) error { return toErr(s.b.monitor.Connect(id)) }

// Disconnect 中斷連線或停止自動重新連線, 該主機轉為未連線後回傳.
func (s *HostService) Disconnect(id string) error { return toErr(s.b.monitor.Disconnect(id)) }

// AnswerPrompt 回覆登入密碼或金鑰密碼的輸入要求. 收到後立即轉為位元組陣列, 不另存字串.
func (s *HostService) AnswerPrompt(promptID string, secret string) error {
	return toErr(s.b.monitor.AnswerPrompt(promptID, []byte(secret)))
}

// CancelPrompt 取消輸入要求, 該次連線中止且不顯示錯誤.
func (s *HostService) CancelPrompt(promptID string) error {
	return toErr(s.b.monitor.CancelPrompt(promptID))
}

// ConfirmUpload 確認上傳遠端程式的通知, 該次連線隨即開始上傳.
func (s *HostService) ConfirmUpload(noticeID string) error {
	return toErr(s.b.monitor.ConfirmUpload(noticeID))
}

// AnswerHostKey 回覆主機金鑰的確認 ("未知的主機" 或 "主機金鑰已變更" 視窗). accept
// 為真表示 "連線" 或 "更新並連線", 為假表示 "取消"; 取消時該次連線中止且不顯示錯誤.
func (s *HostService) AnswerHostKey(promptID string, accept bool) error {
	return toErr(s.b.monitor.AnswerHostKey(promptID, accept))
}

// AnswerKeySetup 回覆免密碼登入的詢問. choice 為 "now" (立即設定), "later" (暫不
// 設定) 或 "never" (不再詢問).
func (s *HostService) AnswerKeySetup(promptID string, choice monitor.KeySetupChoice) error {
	return toErr(s.b.monitor.AnswerKeySetup(promptID, choice))
}

// Live 回傳主機的 devices 資訊, 最新一筆讀數與程序列表.
func (s *HostService) Live(id string) (monitor.Live, error) {
	lv, err := s.b.monitor.Live(id)
	return lv, toErr(err)
}

// LiveAll 回傳所有主機的即時資料, 順序與 Hosts 相同.
func (s *HostService) LiveAll() []monitor.Live {
	var out []monitor.Live
	for _, h := range s.b.monitor.Hosts() {
		if lv, err := s.b.monitor.Live(h.ID); err == nil {
			out = append(out, lv)
		}
	}
	return out
}

// Query 查詢主機於 [from, to] (Unix 微秒) 的曲線資料, width 為曲線圖的像素寬度.
// 含該主機先前各次連線的記錄與尚未存檔的最新取樣. fields 為空時回傳全部欄位. stats 為
// 真時降採樣的結果另附各像素範圍的統計, 供監看畫面把之後的取樣併入, 見 history.Query.
func (s *HostService) Query(ctx context.Context, id string, from, to int64, width int, fields []history.Field, stats bool) (*history.Result, error) {
	res, err := s.b.monitor.Query(ctx, id, history.Query{From: from, To: to, Width: width, Fields: fields, Stats: stats})
	return res, toErr(err)
}

// ActiveCount 回傳處於連線中或重新連線中狀態的主機數, 供結束前的確認視窗使用.
func (s *HostService) ActiveCount() int { return s.b.monitor.ActiveCount() }

// ChooseKeyFile 開啟系統的檔案選擇視窗選擇金鑰檔, 回傳路徑; 使用者取消時回傳空字串.
// title 為視窗標題, 由前端依語言設定提供.
func (s *HostService) ChooseKeyFile(title string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", nil
	}
	d := app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).CanChooseDirectories(false).ShowHiddenFiles(true)
	if dir, err := sshDir(); err == nil {
		d.SetDirectory(dir)
	}
	path, err := d.PromptForSingleSelection()
	return path, toErr(err)
}

// SSHConfigDefaults 為 ~/.ssh/config 對某個位址的設定, 供主機表單預填. Port 為 0 與
// KeyFile 為空字串表示無設定; KeyFile 已展開 ~ 與 OpenSSH 的 % 記號. IdentitiesOnlySet
// 為設定檔是否有 IdentitiesOnly 的設定. Warnings 為讀取時略過之處的英文警告.
type SSHConfigDefaults struct {
	Port              int      `json:"port"`
	KeyFile           string   `json:"key_file"`
	IdentitiesOnly    bool     `json:"identities_only"`
	IdentitiesOnlySet bool     `json:"identities_only_set"`
	Warnings          []string `json:"warnings"`
}

// SSHConfigDefaults 回傳 ~/.ssh/config 以 addr 為 Host 名稱的設定, 供主機表單預填.
// addr 去除前後空白後為空時回傳全部為零值的結果. 設定檔的問題一律放在 Warnings,
// 只有無法取得本機家目錄等內部錯誤才回傳錯誤.
func (s *HostService) SSHConfigDefaults(addr string) (SSHConfigDefaults, error) {
	return sshConfigDefaults(addr, sshconn.UserConfigDefaults)
}

func sshConfigDefaults(addr string, lookup func(string) (sshconn.ConfigDefaults, error)) (SSHConfigDefaults, error) {
	out := SSHConfigDefaults{Warnings: []string{}}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return out, nil
	}
	d, err := lookup(addr)
	if err != nil {
		return out, toErr(err)
	}
	out.Port, out.KeyFile = d.Port, d.KeyFile
	out.IdentitiesOnly, out.IdentitiesOnlySet = d.IdentitiesOnly, d.IdentitiesOnlySet
	if d.Warnings != nil {
		out.Warnings = d.Warnings
	}
	return out, nil
}

// RecordingInfo 為記錄檔的說明區內容與起訖時刻.
type RecordingInfo struct {
	Path string `json:"path"`
	// Host 為說明區的 host 成員.
	Host    string           `json:"host"`
	Devices protocol.Devices `json:"devices"`
	// Start 與 End 為第一筆與最後一筆記錄的時刻 (Unix 微秒); HasData 為假時
	// 兩者皆為說明區的 t_wall_0.
	Start   int64 `json:"start"`
	End     int64 `json:"end"`
	HasData bool  `json:"has_data"`
	// Active 表示該檔案正在寫入中.
	Active bool `json:"active"`
}

// DeleteResult 為刪除一個記錄檔的結果; Error 為空字串表示成功.
type DeleteResult struct {
	Path  string       `json:"path"`
	Code  monitor.Code `json:"code"`
	Error string       `json:"error"`
}

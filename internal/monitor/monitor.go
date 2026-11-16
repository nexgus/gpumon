// Package monitor 管理本機 GUI 對各台主機的連線工作階段, 即規格第 4 章的連線
// 流程與 6.2, 6.3 的連線相關功能: 連線與狀態, 讀取遠端訊息, 心跳, 斷線判定,
// 自動重新連線, 登入密碼的暫留與銷毀, 時間基準, 記錄檔的寫入, 以及即時曲線與
// 最新讀數的查詢.
//
// 本套件不引用 Wails. 對前端的通知經由 Notifier 送出, 連線經由 Dialer 建立,
// 皆可於測試中替換.
package monitor

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gpumon/internal/history"
	"gpumon/internal/protocol"
	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

// Timing 為連線管理所用的各項時間與次數. 正式環境使用 DefaultTiming, 測試可縮短.
type Timing struct {
	// Heartbeat 為送出 ping 的間隔, 亦為自動重新連線的重試節奏.
	Heartbeat time.Duration
	// Watchdog 為判定斷線的無訊息時間.
	Watchdog time.Duration
	// MaxRetries 為自動重新連線的重試次數上限.
	MaxRetries int
	// ConnectTimeout 與 ReconnectTimeout 依序為使用者按下連線與自動重新連線時
	// 建立 SSH 連線的逾時上限.
	ConnectTimeout   time.Duration
	ReconnectTimeout time.Duration
	// DevicesTimeout 為遠端程式啟動後等待 devices 訊息的上限.
	DevicesTimeout time.Duration
	// Flush 為記錄檔批次附加的間隔.
	Flush time.Duration
}

// IntervalMS 為 GUI 啟動遠端程式時以 --interval 傳入的取樣間隔, 固定不變; GUI
// 不送出 set_interval.
const IntervalMS = 200

// DefaultTiming 回傳規格所定的時間與次數.
func DefaultTiming() Timing {
	return Timing{
		Heartbeat:        protocol.HeartbeatInterval,
		Watchdog:         protocol.WatchdogTimeout,
		MaxRetries:       5,
		ConnectTimeout:   sshconn.DefaultConnectTimeout,
		ReconnectTimeout: 5 * time.Second,
		DevicesTimeout:   protocol.WatchdogTimeout,
		Flush:            5 * time.Second,
	}
}

// Options 為 Manager 的設定.
type Options struct {
	// DataDir 為應用程式資料目錄, 記錄檔位於其下的 history/.
	DataDir string
	// Settings 為設定的存取物件, 主機清單取自此處.
	Settings *settings.Store
	// Dialer 建立連線; nil 時使用 SSHDialer, 程式自己的 known_hosts 為 DataDir 下的
	// known_hosts, 其餘欄位為零值.
	Dialer Dialer
	// Notifier 接收通知; nil 時捨棄通知.
	Notifier Notifier
	// Logger 為日誌; nil 時使用 slog.Default.
	Logger *slog.Logger
	// Now 為本機時鐘, 用於時間基準; nil 時使用 time.Now.
	Now func() time.Time
	// Timing 的零值表示 DefaultTiming.
	Timing Timing
}

// Manager 管理所有主機的連線工作階段. 所有方法皆可由多個 goroutine 同時呼叫.
type Manager struct {
	dataDir   string
	store     *settings.Store
	dialer    Dialer
	notify    Notifier
	log       *slog.Logger
	now       func() time.Time
	timing    Timing
	ids       idSeq
	prompts   promptBroker
	uploads   uploadBroker
	hostKeys  replyBroker[bool]
	keySetups replyBroker[KeySetupChoice]
	sessions  map[string]*session

	mu     sync.Mutex
	closed bool
}

// New 建立 Manager.
func New(opts Options) *Manager {
	m := &Manager{
		dataDir:  opts.DataDir,
		store:    opts.Settings,
		dialer:   opts.Dialer,
		notify:   opts.Notifier,
		log:      opts.Logger,
		now:      opts.Now,
		timing:   opts.Timing,
		sessions: map[string]*session{},
	}
	if m.dialer == nil {
		m.dialer = SSHDialer{KnownHostsPath: settings.KnownHostsPath(m.dataDir)}
	}
	if m.notify == nil {
		m.notify = NotifierFunc(func(string, any) {})
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.timing == (Timing{}) {
		m.timing = DefaultTiming()
	}
	m.prompts.notify, m.prompts.ids = m.notify, &m.ids
	m.uploads.notify, m.uploads.ids = m.notify, &m.ids
	m.hostKeys = replyBroker[bool]{
		notify: m.notify, ids: &m.ids, event: EventHostKey, closed: EventHostKeyClosed,
		closedData: func(id string) any { return HostKeyClosed{ID: id} }, what: "host key confirmation",
	}
	m.keySetups = replyBroker[KeySetupChoice]{
		notify: m.notify, ids: &m.ids, event: EventKeySetup, closed: EventKeySetupClosed,
		closedData: func(id string) any { return KeySetupClosed{ID: id} }, what: "passwordless login prompt",
	}
	return m
}

// sessionLocked 回傳主機的工作階段, 不存在時建立. 呼叫端須持有 m.mu.
func (m *Manager) sessionLocked(id string) *session {
	s := m.sessions[id]
	if s == nil {
		s = newSession(m, id)
		m.sessions[id] = s
	}
	return s
}

// displayName 回傳主機的顯示名稱, 即主機的識別. 使用者名稱空白 (舊版設定檔升級後
// 仍無使用者名稱) 時, 取最近一次連線實際登入的使用者名稱, 仍沒有時回傳 ID.
func (m *Manager) displayName(h settings.Host, s *session) string {
	if h.User == "" && s != nil {
		if u := s.lastUser(); u != "" {
			c := h.HostConn
			c.User = u
			return c.ID()
		}
	}
	return h.ID()
}

// Hosts 回傳所有主機的狀態, 依新增的先後排列.
func (m *Manager) Hosts() []HostStatus {
	hosts := m.store.Hosts()
	m.mu.Lock()
	sessions := make([]*session, len(hosts))
	for i, h := range hosts {
		sessions[i] = m.sessions[h.ID()]
	}
	m.mu.Unlock()
	out := make([]HostStatus, len(hosts))
	for i, h := range hosts {
		out[i] = m.status(h, sessions[i])
	}
	return out
}

// Host 回傳一台主機的狀態.
func (m *Manager) Host(id string) (HostStatus, error) {
	h, ok := m.store.Host(id)
	if !ok {
		return HostStatus{}, newErr(CodeNotFound, "Host %s not found", id)
	}
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	return m.status(h, s), nil
}

func (m *Manager) status(h settings.Host, s *session) HostStatus {
	st := HostStatus{ID: h.ID(), Host: h, State: StateDisconnected, MaxRetries: m.timing.MaxRetries}
	if s != nil {
		s.fill(&st)
	}
	st.Name = m.displayName(h, s)
	return st
}

// checkHost 檢查主機表單; skip 為編輯中主機的 ID, 不與自己比對.
func (m *Manager) checkHost(c settings.HostConn, skip string) error {
	c = normalized(c)
	if c.Addr == "" {
		return newErr(CodeInvalid, "The address is required")
	}
	if c.User == "" {
		return newErr(CodeUserRequired, "The user name is required for %s", c.Addr)
	}
	key := c.Key()
	for _, h := range m.store.Hosts() {
		if h.ID() == skip {
			continue
		}
		if h.Key() == key {
			return newErr(CodeDuplicate, "%s already exists", c.ID())
		}
	}
	return nil
}

func settingsErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, settings.ErrDuplicateHost):
		return newErr(CodeDuplicate, "%v", err)
	case errors.Is(err, settings.ErrHostNotFound):
		return newErr(CodeNotFound, "%v", err)
	case errors.Is(err, settings.ErrInvalidValue):
		return newErr(CodeInvalid, "%v", err)
	}
	return newErr(CodeFailed, "%v", err)
}

// AddHost 新增主機. 使用者名稱空白時回傳 CodeUserRequired, 與既有主機相同 (位址不分
// 大小寫, 使用者名稱區分大小寫, 連接埠 0 與 22 視為相同) 時回傳 CodeDuplicate.
func (m *Manager) AddHost(c settings.HostConn) (HostStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkHost(c, ""); err != nil {
		return HostStatus{}, err
	}
	if err := m.store.AddHost(c); err != nil {
		return HostStatus{}, settingsErr(err)
	}
	hosts := m.store.Hosts()
	h := hosts[len(hosts)-1]
	return m.status(h, m.sessions[h.ID()]), nil
}

// UpdateHost 編輯主機的表單欄位. 主機不是未連線狀態時回傳 CodeBusy.
func (m *Manager) UpdateHost(id string, c settings.HostConn) (HostStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil && s.state() != StateDisconnected {
		return HostStatus{}, newErr(CodeBusy, "Disconnect %s before editing it", id)
	}
	if _, ok := m.store.Host(id); !ok {
		return HostStatus{}, newErr(CodeNotFound, "Host %s not found", id)
	}
	if err := m.checkHost(c, id); err != nil {
		return HostStatus{}, err
	}
	if err := m.store.UpdateHost(id, c); err != nil {
		return HostStatus{}, settingsErr(err)
	}
	delete(m.sessions, id)
	h, _ := m.store.Host(normalized(c).ID())
	return m.status(h, nil), nil
}

func normalized(c settings.HostConn) settings.HostConn {
	c.Addr = strings.TrimSpace(c.Addr)
	c.User = strings.TrimSpace(c.User)
	c.KeyFile = strings.TrimSpace(c.KeyFile)
	return c
}

// RemoveHost 刪除主機. 主機不是未連線狀態時回傳 CodeBusy. 既有的記錄檔不刪除.
func (m *Manager) RemoveHost(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil && s.state() != StateDisconnected {
		return newErr(CodeBusy, "Disconnect %s before deleting it", id)
	}
	if err := m.store.RemoveHost(id); err != nil {
		return settingsErr(err)
	}
	delete(m.sessions, id)
	return nil
}

// Connect 開始連線. 立即回傳, 連線結果以通知送出. 主機不是未連線狀態時不做任何事.
func (m *Manager) Connect(id string) error {
	if _, ok := m.store.Host(id); !ok {
		return newErr(CodeNotFound, "Host %s not found", id)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return newErr(CodeClosed, "GPU Monitor is quitting")
	}
	s := m.sessionLocked(id)
	s.start()
	m.mu.Unlock()
	s.notifyStatus()
	return nil
}

// Disconnect 中斷連線或停止自動重新連線, 等待該主機轉為未連線狀態後回傳.
func (m *Manager) Disconnect(id string) error {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		if _, ok := m.store.Host(id); !ok {
			return newErr(CodeNotFound, "Host %s not found", id)
		}
		return nil
	}
	s.stop()
	return nil
}

// Live 回傳主機的 devices 資訊, 最新一筆取樣與程序列表.
func (m *Manager) Live(id string) (Live, error) {
	if _, ok := m.store.Host(id); !ok {
		return Live{}, newErr(CodeNotFound, "Host %s not found", id)
	}
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return Live{Host: id}, nil
	}
	return s.liveCopy(), nil
}

// Query 查詢主機的曲線資料, 條件見 history.Query. 只查詢寫入中的記錄檔 (含尚未存檔的
// 取樣), 且不早於本次連線第一筆取樣的時刻; 先前各次連線的記錄檔與自動重新連線前的資料
// 不在結果中. 沒有寫入中的記錄檔時回傳空的結果.
func (m *Manager) Query(ctx context.Context, id string, q history.Query) (*history.Result, error) {
	if _, ok := m.store.Host(id); !ok {
		return nil, newErr(CodeNotFound, "Host %s not found", id)
	}
	if q.Width < 1 || q.Width > history.MaxWidth || q.To < q.From {
		return nil, newErr(CodeInvalid, "Invalid query range or width")
	}
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	var res *history.Result
	var err error
	if s != nil {
		q.NotBefore = max(q.NotBefore, s.connStartTime())
		res, err = s.recorderRef().query(ctx, q)
	} else {
		res, err = history.QueryRange(ctx, nil, q)
	}
	if err != nil {
		return nil, newErr(CodeFailed, "%v", err)
	}
	return res, nil
}

// ActiveRecordings 回傳寫入中的記錄檔路徑, 這些檔案不可刪除.
func (m *Manager) ActiveRecordings() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range m.sessions {
		if p := s.recordingPath(); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ActiveCount 回傳處於連線中或重新連線中狀態的主機數, 供結束前的確認使用.
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if st := s.state(); st == StateConnected || st == StateReconnecting {
			n++
		}
	}
	return n
}

// AnswerPrompt 回覆密碼的輸入要求. secret 交由本套件保管與銷毀, 呼叫端不得再
// 使用; 找不到 id 時 secret 立即以零覆寫.
func (m *Manager) AnswerPrompt(id string, secret []byte) error {
	return m.prompts.answer(id, secret)
}

// CancelPrompt 取消密碼的輸入要求, 該次連線隨即中止, 不顯示錯誤.
func (m *Manager) CancelPrompt(id string) error {
	return m.prompts.cancel(id)
}

// ConfirmUpload 確認上傳通知, 該次連線隨即開始上傳遠端程式. 通知已失效時回傳
// CodeNotFound.
func (m *Manager) ConfirmUpload(id string) error {
	return m.uploads.confirm(id)
}

// AnswerHostKey 回覆主機金鑰的確認. accept 為真時把主機金鑰寫入程式自己的
// known_hosts 並繼續連線; 為假時中止該次連線, 不顯示錯誤. 詢問已失效時回傳
// CodeNotFound.
func (m *Manager) AnswerHostKey(id string, accept bool) error {
	return m.hostKeys.reply(id, accept)
}

// AnswerKeySetup 回覆免密碼登入的詢問. choice 不合法時回傳 CodeInvalid, 詢問已
// 失效時回傳 CodeNotFound.
func (m *Manager) AnswerKeySetup(id string, choice KeySetupChoice) error {
	switch choice {
	case KeySetupNow, KeySetupLater, KeySetupNever:
	default:
		return newErr(CodeInvalid, "Invalid answer %q", choice)
	}
	return m.keySetups.reply(id, choice)
}

// Close 中斷所有連線, 把尚未存檔的取樣寫入記錄檔, 並銷毀所有密碼. 之後不可再
// 連線.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.stop()
		}()
	}
	wg.Wait()
}

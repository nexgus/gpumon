package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gpumon/internal/appinfo"
	"gpumon/internal/history"
	"gpumon/internal/monitor"
	"gpumon/internal/recording"
	"gpumon/internal/settings"
	"gpumon/internal/version"
)

func sshDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh"), nil
}

// RecordingService 提供記錄檔清單與歷史畫面所需的查詢.
type RecordingService struct{ b *Backend }

// List 列出所有記錄檔, 依主機分組; 寫入中的檔案標示為 active.
func (s *RecordingService) List() (*history.Listing, error) {
	l, err := history.List(s.b.dataDir, s.b.monitor.ActiveRecordings())
	return l, toErr(err)
}

// Delete 刪除記錄檔, 逐檔回傳結果. 寫入中的檔案不刪除, code 為 active.
func (s *RecordingService) Delete(paths []string) []DeleteResult {
	active := s.b.monitor.ActiveRecordings()
	out := make([]DeleteResult, 0, len(paths))
	for _, p := range paths {
		r := DeleteResult{Path: p}
		if err := toErr(history.Delete(s.b.dataDir, p, active)); err != nil {
			r.Code, r.Error = monitor.CodeOf(err), err.Error()
		}
		out = append(out, r)
	}
	return out
}

// Query 查詢 paths 所列記錄檔 (應屬同一台主機) 於 [from, to] (Unix 微秒) 的曲線
// 資料, width 為曲線圖的像素寬度, fields 為空時回傳全部欄位. 寫入中的檔案只含
// 已存檔的內容.
func (s *RecordingService) Query(ctx context.Context, paths []string, from, to int64, width int, fields []history.Field) (*history.Result, error) {
	res, err := history.QueryRange(ctx, paths, history.Query{From: from, To: to, Width: width, Fields: fields})
	return res, toErr(err)
}

// ProcessesAt 回傳記錄檔中時刻不晚於 t (Unix 微秒) 的最近一筆程序記錄; 沒有時回傳 null.
func (s *RecordingService) ProcessesAt(path string, t int64) (*history.ProcessList, error) {
	p, err := history.ProcessesAt(path, t)
	return p, toErr(err)
}

// Info 回傳記錄檔的主機, devices 資訊與起訖時刻.
func (s *RecordingService) Info(path string) (RecordingInfo, error) {
	f, err := recording.Open(path)
	if err != nil {
		return RecordingInfo{}, toErr(err)
	}
	defer f.Close()
	h := f.Header()
	info := RecordingInfo{Path: path, Host: h.Host, Start: h.TWall0, End: h.TWall0,
		Active: slices.Contains(s.b.monitor.ActiveRecordings(), path)}
	if err := json.Unmarshal(h.Devices, &info.Devices); err != nil {
		return RecordingInfo{}, toErr(err)
	}
	first, last, ok, err := f.TimeRange()
	if err != nil {
		return RecordingInfo{}, toErr(err)
	}
	if ok {
		info.Start, info.End, info.HasData = first, last, true
	}
	return info, nil
}

// SettingsService 提供設定的讀取與變更. 主機相關的設定見 HostService.
type SettingsService struct{ b *Backend }

// Choices 為各項設定的可選值.
type Choices struct {
	MonitorRanges []settings.TimeRange `json:"monitor_ranges"`
	HistoryRanges []settings.TimeRange `json:"history_ranges"`
	Curves        []settings.Curve     `json:"curves"`
	MaxCurves     int                  `json:"max_curves"`
	MinIntervalMS int                  `json:"min_interval_ms"`
	MaxIntervalMS int                  `json:"max_interval_ms"`
}

// Get 回傳全部設定.
func (s *SettingsService) Get() settings.Data { return s.b.store.Snapshot() }

// Choices 回傳各項設定的可選值.
func (s *SettingsService) Choices() Choices {
	return Choices{
		MonitorRanges: settings.MonitorRanges(), HistoryRanges: settings.HistoryRanges(),
		Curves: settings.AllCurves(), MaxCurves: settings.MaxCurves,
		MinIntervalMS: settings.MinIntervalMS, MaxIntervalMS: settings.MaxIntervalMS,
	}
}

// SetAppearance 變更外觀.
func (s *SettingsService) SetAppearance(v settings.Appearance) error {
	if err := s.b.store.SetAppearance(v); err != nil {
		return toErr(err)
	}
	s.b.uiChanged()
	return nil
}

// SetLanguage 變更介面語言.
func (s *SettingsService) SetLanguage(v settings.Language) error {
	if err := s.b.store.SetLanguage(v); err != nil {
		return toErr(err)
	}
	s.b.uiChanged()
	return nil
}

// SetMonitorRange 變更監看畫面的顯示範圍, 所有主機共用.
func (s *SettingsService) SetMonitorRange(v settings.TimeRange) error {
	return toErr(s.b.store.SetMonitorRange(v))
}

// SetHistoryRange 變更歷史畫面的顯示範圍, 可為 "all".
func (s *SettingsService) SetHistoryRange(v settings.TimeRange) error {
	return toErr(s.b.store.SetHistoryRange(v))
}

// SetCurves 變更曲線的選擇, 依傳入的順序保留, 不得重複, 至多 MaxCurves 條, 可為空.
func (s *SettingsService) SetCurves(curves []settings.Curve) error {
	if curves == nil {
		curves = []settings.Curve{}
	}
	return toErr(s.b.store.SetCurves(curves))
}

// SetGPUCollapsed 記住主機 (以 HostStatus.id 指定) 的某張 GPU (devices 訊息的 id)
// 的橫列是否收合.
func (s *SettingsService) SetGPUCollapsed(hostID string, gpuID int, collapsed bool) error {
	return toErr(s.b.store.SetGPUCollapsed(hostID, gpuID, collapsed))
}

// AppService 提供關於視窗所需的程式資訊與結束程式的確認.
type AppService struct{ b *Backend }

// AboutInfo 為程式資訊.
type AboutInfo struct {
	Name string `json:"name"`
	// Version 為完整版本字串 x.y.z[-stage.N]+<commit>[-dirty].
	Version string `json:"version"`
	// BuildDate 為建置時間, ISO 8601 格式; 未經建置腳本時為 unknown.
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	// OSName 與 OSVersion 為作業系統的名稱與版本.
	OSName    string `json:"os_name"`
	OSVersion string `json:"os_version"`
	// Platform 為 <GOOS>/<GOARCH>, 例如 darwin/arm64.
	Platform string `json:"platform"`
	// WebView2Version 為 Windows 的 WebView2 執行期版本, 其他平台與取不到時為空字串.
	// WebView 本身的版本由前端自 user agent 取得.
	WebView2Version string `json:"webview2_version"`
}

// About 回傳程式資訊.
func (AppService) About() AboutInfo {
	info := AboutInfo{
		Name: appinfo.Name, Version: version.Full(), BuildDate: version.BuildDate,
		GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if app := application.Get(); app != nil {
		env := app.Env.Info()
		if env.OSInfo != nil {
			info.OSName, info.OSVersion = env.OSInfo.Name, env.OSInfo.Version
			// Wails 於 macOS 回報的名稱為 "MacOS", 改為 Apple 的正式寫法.
			if runtime.GOOS == "darwin" {
				info.OSName = "macOS"
			}
		}
		if v, ok := env.PlatformInfo["WebView2"].(string); ok {
			info.WebView2Version = v
		}
	}
	return info
}

// ConfirmQuit 於使用者在結束確認視窗按下 "結束" 後呼叫, 中斷所有連線並結束程式.
func (s *AppService) ConfirmQuit() {
	if sh := s.b.shell.Load(); sh != nil {
		(*sh).ConfirmQuit()
	}
}

// 以下事件由 Go 端送給前端.
const (
	// EventUISettings 於外觀或語言的設定變更後送給所有視窗, 內容為 UISettings.
	EventUISettings = "app:ui-settings"
	// EventQuitRequest 於有主機連線中或重新連線中而要求結束時送出, 內容為
	// QuitRequest; 主視窗據此顯示結束確認視窗.
	EventQuitRequest = "app:quit-request"
	// EventShowPage 要求主視窗顯示某個畫面, 內容為 ShowPage.
	EventShowPage = "app:show-page"
	// EventAboutTab 要求已開啟的關於視窗切換分頁, 內容為 AboutTab.
	EventAboutTab = "app:about-tab"
)

// UISettings 為外觀與語言的設定.
type UISettings struct {
	Appearance settings.Appearance `json:"appearance"`
	Language   settings.Language   `json:"language"`
}

// QuitRequest 為結束確認視窗的內容; Count 為將中斷連線的主機數.
type QuitRequest struct {
	Count int `json:"count"`
}

// ShowPage 的 Page 為主視窗的畫面名稱, 目前只有 "settings".
type ShowPage struct {
	Page string `json:"page"`
}

// AboutTab 的 Tab 為關於視窗的分頁, "about" 或 "licenses".
type AboutTab struct {
	Tab string `json:"tab"`
}

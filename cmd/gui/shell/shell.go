// Package shell 管理本機 GUI 的主視窗, 關於視窗, 主選單與匣圖示, 並負責結束程式前的
// 確認與主視窗大小與位置的記憶. 規格見 docs/gpumon-design.md 的 6.1 與 6.2.
package shell

import (
	"log/slog"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"gpumon/cmd/gui/backend"
	"gpumon/frontend/src/i18n"
	"gpumon/internal/appinfo"
	"gpumon/internal/monitor"
	"gpumon/internal/settings"
)

const (
	// saveDelay 為主視窗最後一次移動或縮放後寫入設定檔前的等待時間, 拖曳過程中不寫入.
	saveDelay = 500 * time.Millisecond
	// screenWait 為啟動時等待 Wails 取得螢幕資訊的上限.
	screenWait = time.Second
	// 關於視窗首次開啟的大小與可縮小的下限.
	aboutWidth     = 600
	aboutHeight    = 600
	aboutMinWidth  = 420
	aboutMinHeight = 360
)

// 關於視窗的分頁.
const (
	TabAbout    = "about"
	TabLicenses = "licenses"
)

// Shell 為主視窗, 關於視窗, 主選單與匣圖示的管理者. 以 New 建立, 把 ShouldQuit 傳入
// application.Options, 再於 application.New 之後呼叫 Attach.
type Shell struct {
	b   *backend.Backend
	log *slog.Logger
	app *application.App

	main  atomic.Pointer[application.WebviewWindow]
	lang  atomic.Value // settings.Language
	tray  *application.SystemTray
	trayM sync.Mutex
	// monitoring 為匣圖示目前是否處於眨眼狀態, blink 為進行中的眨眼循環, 非監看中時為
	// nil; reconnecting 為提示文字目前是否為重新連線中的說明. 三者皆由 trayM 保護.
	monitoring   bool
	reconnecting bool
	blink        *blinker
	kick         chan struct{}

	// aboutM 保護 about, 並使關於視窗同一時間只有一個. 持有 aboutM 期間可呼叫 Wails,
	// 主執行緒上的程式不取用 aboutM.
	aboutM sync.Mutex
	about  *application.WebviewWindow

	// quitApproved 表示使用者已在結束確認視窗按下 "結束"; quitting 表示已開始結束,
	// 此後關閉視窗不再攔截.
	quitApproved atomic.Bool
	quitting     atomic.Bool

	// saveM 保護 saveTimer. 持有 saveM 期間不呼叫 Wails.
	saveM     sync.Mutex
	saveTimer *time.Timer
}

// New 建立 Shell 並向 b 登記, 使設定變更與主機狀態改變時通知 Shell.
func New(b *backend.Backend, log *slog.Logger) *Shell {
	s := &Shell{b: b, log: log, kick: make(chan struct{}, 1)}
	s.lang.Store(b.Store().Snapshot().Language)
	b.SetShell(s)
	b.OnHostsChanged(s.kickTray)
	return s
}

func (s *Shell) language() settings.Language { return s.lang.Load().(settings.Language) }

func (s *Shell) text(key string) string { return i18n.Text(string(s.language()), key) }

// Attach 於 application.New 之後, app.Run 之前呼叫, 建立主選單與匣圖示, 並於程式
// 啟動後開啟主視窗.
func (s *Shell) Attach(app *application.App) {
	s.app = app
	if runtime.GOOS == "darwin" {
		app.Menu.Set(s.appMenu())
	}
	s.setupTray()
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		s.openMain()
		go s.trayLoop()
		s.kickTray()
	})
}

// ShouldQuit 為 application.Options 的 ShouldQuit, 於任何方式要求結束程式時呼叫 (關閉
// 主視窗, 選單的 "Quit GPU Monitor" 與 "Exit", 匣圖示選單的 "結束", macOS 的 Cmd+Q 與
// Dock 選單). 有主機連線中或重新連線中而使用者尚未確認時, 要求主視窗顯示結束確認
// 視窗並回傳 false; 否則回傳 true, 程式隨即結束. 可能於主執行緒呼叫.
func (s *Shell) ShouldQuit() bool {
	if s.quitting.Load() {
		return true
	}
	n := s.b.ActiveCount()
	if needsQuitConfirm(n, s.quitApproved.Load()) {
		go s.requestQuitConfirm(n)
		return false
	}
	s.quitting.Store(true)
	s.flushWindowBounds()
	return true
}

// needsQuitConfirm 回報結束前是否須跳出確認視窗: 有主機處於連線中或重新連線中狀態
// (active 為其數目), 而使用者尚未在確認視窗按下 "結束".
func needsQuitConfirm(active int, approved bool) bool {
	return active > 0 && !approved
}

func (s *Shell) requestQuitConfirm(n int) {
	s.ShowMain()
	s.app.Event.Emit(backend.EventQuitRequest, backend.QuitRequest{Count: n})
}

// Quit 要求結束程式, 適用與關閉視窗相同的確認.
func (s *Shell) Quit() {
	go s.app.Quit()
}

// ConfirmQuit 實作 backend.Shell, 於使用者在結束確認視窗按下 "結束" 後結束程式.
func (s *Shell) ConfirmQuit() {
	s.quitApproved.Store(true)
	s.Quit()
}

// UIChanged 實作 backend.Shell. 語言變更時重建主選單與匣圖示選單, 並更新關於視窗的
// 標題; 外觀由各視窗的前端自行套用.
func (s *Shell) UIChanged(ui backend.UISettings) {
	if ui.Language == s.language() {
		return
	}
	s.lang.Store(ui.Language)
	if s.app == nil {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		s.app.Menu.Set(s.appMenu())
	case "windows":
		if w := s.main.Load(); w != nil {
			w.SetMenu(s.windowMenu())
		}
	}
	s.updateTrayText()
	s.aboutM.Lock()
	if s.about != nil {
		s.about.SetTitle(s.text("menu.about"))
	}
	s.aboutM.Unlock()
}

// openMain 依記住的大小與位置開啟主視窗.
func (s *Shell) openMain() {
	saved := s.b.Store().Snapshot().Window
	r, ok := placeMainWindow(saved, s.screens())
	opts := application.WebviewWindowOptions{
		Name:     "main",
		Title:    appinfo.Name,
		Width:    r.W,
		Height:   r.H,
		MinWidth: mainMinWidth,
		URL:      "/",
	}
	if ok {
		opts.InitialPosition = application.WindowXY
		opts.X, opts.Y = r.X, r.Y
	} else {
		opts.InitialPosition = application.WindowCentered
	}
	if runtime.GOOS == "windows" {
		opts.Windows.Menu = s.windowMenu()
	}
	w := s.app.Window.NewWithOptions(opts)
	// 記住的是含標題列與邊框的外框大小與位置 (Bounds). macOS 建立視窗時的 Width 與
	// Height 為內容區的大小, Windows 的位置為 (0, 0) 時改由系統決定, 故建立後再以
	// SetBounds 設定外框, 使兩個平台的記住與還原一致.
	if ok {
		w.SetBounds(application.Rect{X: r.X, Y: r.Y, Width: r.W, Height: r.H})
	}

	// 關閉主視窗即結束程式, 是否須確認由 ShouldQuit 決定. 已開始結束時不攔截, 使結束
	// 過程得以關閉視窗.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if s.quitting.Load() {
			return
		}
		e.Cancel()
		s.Quit()
	})
	// 最小化時隱藏視窗, 只留匣圖示; 連線與取樣不受影響.
	w.OnWindowEvent(events.Common.WindowMinimise, func(*application.WindowEvent) {
		w.Hide()
	})
	w.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) { s.scheduleSave() })
	w.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) { s.scheduleSave() })
	s.main.Store(w)
}

// screens 回傳所有螢幕的工作區域. macOS 於程式啟動後才取得螢幕資訊, 故最多等待
// screenWait.
func (s *Shell) screens() []screenArea {
	deadline := time.Now().Add(screenWait)
	for {
		all := s.app.Screen.GetAll()
		if len(all) > 0 || time.Now().After(deadline) {
			out := make([]screenArea, 0, len(all))
			for _, sc := range all {
				work := sc.WorkArea
				if work.Width <= 0 || work.Height <= 0 {
					work = sc.Bounds
				}
				out = append(out, screenArea{
					Work:    rect{X: work.X, Y: work.Y, W: work.Width, H: work.Height},
					Primary: sc.IsPrimary,
				})
			}
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ShowMain 顯示主視窗並移至最前, 視窗最小化或隱藏於匣圖示時一併還原.
func (s *Shell) ShowMain() {
	w := s.main.Load()
	if w == nil {
		return
	}
	if w.IsMinimised() {
		w.UnMinimise()
	}
	w.Show()
	w.Focus()
}

// ShowSettings 顯示主視窗的設定頁面, 與側欄的 "設定" 入口相同.
func (s *Shell) ShowSettings() {
	s.ShowMain()
	s.app.Event.Emit(backend.EventShowPage, backend.ShowPage{Page: "settings"})
}

// OpenAbout 開啟關於視窗並顯示 tab 分頁. 視窗已開啟時只切換分頁並移至最前.
func (s *Shell) OpenAbout(tab string) {
	s.aboutM.Lock()
	defer s.aboutM.Unlock()
	if w := s.about; w != nil {
		s.app.Event.Emit(backend.EventAboutTab, backend.AboutTab{Tab: tab})
		if w.IsMinimised() {
			w.UnMinimise()
		}
		w.Show()
		w.Focus()
		return
	}
	q := url.Values{"window": {"about"}, "tab": {tab}}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "about",
		Title:           s.text("menu.about"),
		Width:           aboutWidth,
		Height:          aboutHeight,
		MinWidth:        aboutMinWidth,
		MinHeight:       aboutMinHeight,
		InitialPosition: application.WindowCentered,
		URL:             "/?" + q.Encode(),
	})
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		s.aboutM.Lock()
		if s.about == w {
			s.about = nil
		}
		s.aboutM.Unlock()
	})
	s.about = w
}

// scheduleSave 於主視窗移動或縮放後延遲寫入其大小與位置; 期間再有變動則重新計時,
// 拖曳過程中不寫入設定檔.
func (s *Shell) scheduleSave() {
	s.saveM.Lock()
	defer s.saveM.Unlock()
	if s.saveTimer == nil {
		s.saveTimer = time.AfterFunc(saveDelay, s.saveWindowBounds)
		return
	}
	s.saveTimer.Reset(saveDelay)
}

// flushWindowBounds 於結束前立即寫入尚未寫入的大小與位置.
func (s *Shell) flushWindowBounds() {
	s.saveM.Lock()
	pending := s.saveTimer != nil && s.saveTimer.Stop()
	s.saveM.Unlock()
	if pending {
		s.writeWindowBounds()
	}
}

func (s *Shell) saveWindowBounds() {
	if s.quitting.Load() {
		return
	}
	s.writeWindowBounds()
}

// writeWindowBounds 記住主視窗目前的大小與位置. 最小化, 最大化, 全螢幕或隱藏時的
// 大小與位置不是使用者調整的結果, 不記住.
func (s *Shell) writeWindowBounds() {
	w := s.main.Load()
	if w == nil || w.IsMinimised() || w.IsMaximised() || w.IsFullscreen() || !w.IsVisible() {
		return
	}
	b := w.Bounds()
	next := settings.Window{Width: b.Width, Height: b.Height, HasPosition: true, X: b.X, Y: b.Y}
	if next == s.b.Store().Snapshot().Window {
		return
	}
	if err := s.b.Store().SetWindow(next); err != nil {
		s.log.Warn("save window bounds", "err", err)
	}
}

// kickTray 要求重新判斷匣圖示與提示文字; 主機狀態改變時由 backend 呼叫, 立即返回.
func (s *Shell) kickTray() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Shell) trayLoop() {
	ctx := s.app.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			hosts := s.b.Hosts()
			s.updateTrayIcon(anyMonitoring(hosts), anyReconnecting(hosts))
		}
	}
}

// anyMonitoring 回報是否有主機處於連線中狀態 (正在監看), 為真時匣圖示眨眼. 重新連線中
// 的主機不算監看中.
func anyMonitoring(hosts []monitor.HostStatus) bool {
	for _, h := range hosts {
		if h.State == monitor.StateConnected {
			return true
		}
	}
	return false
}

// anyReconnecting 回報是否有主機處於重新連線中狀態, 為真時匣圖示的提示文字改為重新
// 連線中的說明.
func anyReconnecting(hosts []monitor.HostStatus) bool {
	for _, h := range hosts {
		if h.State == monitor.StateReconnecting {
			return true
		}
	}
	return false
}

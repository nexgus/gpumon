package shell

// setupTray 建立匣圖示. 點選圖示顯示主視窗; macOS 以右鍵 (或 Control 加點選), Windows
// 以右鍵開啟選單.
func (s *Shell) setupTray() {
	s.tray = s.app.SystemTray.New()
	s.applyTrayFrame(0)
	s.tray.SetMenu(s.trayMenu())
	s.tray.SetTooltip(s.text("tray.tooltip"))
	s.tray.OnClick(func() { go s.ShowMain() })
}

// updateTrayIcon 依主機狀態更新匣圖示與提示文字. 有主機監看中 (連線並取樣中) 時啟動
// 眨眼循環, 否則停止循環並恢復影格 0; 有主機重新連線中時提示文字改為重新連線中的
// 說明. 兩者各自判斷. 停止循環時等待 goroutine 返回, 該 goroutine 只呼叫 Wails 的
// 設定方法, 不取用 trayM, 主執行緒上的程式亦不取用 trayM, 故持有 trayM 等待不會死結.
func (s *Shell) updateTrayIcon(monitoring, reconnecting bool) {
	s.trayM.Lock()
	defer s.trayM.Unlock()
	if monitoring != s.monitoring {
		s.monitoring = monitoring
		if monitoring {
			s.blink = s.startBlink()
		} else if s.blink != nil {
			s.blink.stop()
			s.blink = nil
			s.applyTrayFrame(0)
		}
	}
	if reconnecting != s.reconnecting {
		s.reconnecting = reconnecting
		s.tray.SetTooltip(s.trayTooltip())
	}
}

func (s *Shell) trayTooltip() string {
	if s.reconnecting {
		return s.text("tray.tooltipReconnecting")
	}
	return s.text("tray.tooltip")
}

// updateTrayText 於語言變更後更新匣圖示的選單與提示文字.
func (s *Shell) updateTrayText() {
	s.trayM.Lock()
	defer s.trayM.Unlock()
	s.tray.SetMenu(s.trayMenu())
	s.tray.SetTooltip(s.trayTooltip())
}

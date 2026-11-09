package shell

import (
	"context"
	"runtime"
	"time"

	"gpumon/assets/icon"
)

// 監看中的眨眼循環. 影格編號 0 為睜眼, 3 為閉眼; 一個循環依序顯示下列影格, 每格
// 顯示 blinkInterval, 循環不斷重複. 沒有主機監看中時顯示影格 0 的靜態圖示.
var blinkSequence = [...]int{0, 1, 2, 3, 3, 2, 1, 0}

const blinkInterval = 150 * time.Millisecond

// applyTrayFrame 將匣圖示設為指定的影格. macOS 設定範本圖示, 由系統依選單列的外觀
// 配色; Windows 設定淺色與深色兩張, 由 Wails 依系統的淺色或深色模式切換. Wails 的
// 設定方法於主執行緒同步執行, 呼叫端不得持有主執行緒會等待的鎖.
func (s *Shell) applyTrayFrame(frame int) {
	if runtime.GOOS == "darwin" {
		s.tray.SetTemplateIcon(icon.MacFrames[frame])
		return
	}
	s.tray.SetIcon(icon.WinLightFrames[frame])
	s.tray.SetDarkModeIcon(icon.WinDarkFrames[frame])
}

// blinker 為眨眼循環的計時器 goroutine, 以 startBlink 建立, 以 stop 停止.
type blinker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// stop 通知 goroutine 結束並等待其返回, 返回後不會再有任何影格被設定.
func (b *blinker) stop() {
	b.cancel()
	<-b.done
}

// startBlink 啟動眨眼循環, 於應用程式的 context 結束時自動停止. 呼叫端須持有 trayM,
// goroutine 本身不取用 trayM.
func (s *Shell) startBlink() *blinker {
	ctx, cancel := context.WithCancel(s.app.Context())
	b := &blinker{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(b.done)
		ticker := time.NewTicker(blinkInterval)
		defer ticker.Stop()
		shown := blinkSequence[0]
		for i := 1; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			frame := blinkSequence[i%len(blinkSequence)]
			if frame == shown {
				continue
			}
			shown = frame
			s.applyTrayFrame(frame)
		}
	}()
	return b
}

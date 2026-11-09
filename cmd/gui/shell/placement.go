package shell

import "gpumon/internal/settings"

// rect 為以 DIP 表示的矩形, 座標系與 Wails 的視窗位置及螢幕範圍相同, 原點為主螢幕
// 的左上角.
type rect struct {
	X, Y, W, H int
}

// screenArea 為一個螢幕的工作區域 (扣除選單列, Dock 或工作列的範圍).
type screenArea struct {
	Work    rect
	Primary bool
}

// 記住的位置是否仍在螢幕範圍內, 以視窗頂端標題列的可見部分判斷: 標題列與某個螢幕的
// 工作區域重疊至少 minVisibleWidth 寬, minVisibleHeight 高, 使用者才能拖曳視窗.
const (
	titleBarHeight   = 32
	minVisibleWidth  = 100
	minVisibleHeight = 16
)

// overlap 回傳兩矩形重疊部分的寬與高, 不重疊時為 0.
func overlap(a, b rect) (int, int) {
	w := min(a.X+a.W, b.X+b.W) - max(a.X, b.X)
	h := min(a.Y+a.H, b.Y+b.H) - max(a.Y, b.Y)
	return max(w, 0), max(h, 0)
}

// titleBarVisible 回報視窗 r 的標題列是否落在任一螢幕的工作區域內.
func titleBarVisible(r rect, screens []screenArea) bool {
	bar := rect{X: r.X, Y: r.Y, W: r.W, H: titleBarHeight}
	for _, s := range screens {
		w, h := overlap(bar, s.Work)
		if w >= min(minVisibleWidth, r.W) && h >= minVisibleHeight {
			return true
		}
	}
	return false
}

// centerIn 回傳大小為 w x h 而置中於 area 的矩形. 視窗大於 area 時對齊 area 的左上角,
// 使標題列可見.
func centerIn(area rect, w, h int) rect {
	r := rect{W: w, H: h, X: area.X, Y: area.Y}
	if w < area.W {
		r.X += (area.W - w) / 2
	}
	if h < area.H {
		r.Y += (area.H - h) / 2
	}
	return r
}

// placeMainWindow 依記住的大小與位置決定主視窗開啟時的位置與大小. 記住的位置仍在
// 螢幕範圍內時照原樣還原; 已不在任何螢幕的範圍內時, 改以首次開啟的大小顯示於主螢幕
// 中央; 尚未記住位置時以記住的大小顯示於主螢幕中央. screens 為空時無從判斷, ok 為
// false, 由呼叫端交由作業系統決定位置.
func placeMainWindow(saved settings.Window, screens []screenArea) (r rect, ok bool) {
	if len(screens) == 0 {
		return rect{W: saved.Width, H: saved.Height}, false
	}
	if saved.HasPosition {
		r = rect{X: saved.X, Y: saved.Y, W: saved.Width, H: saved.Height}
		if titleBarVisible(r, screens) {
			return r, true
		}
		return centerIn(primaryArea(screens), settings.DefaultWindowWidth, settings.DefaultWindowHeight), true
	}
	return centerIn(primaryArea(screens), saved.Width, saved.Height), true
}

// primaryArea 回傳主螢幕的工作區域; 沒有標示為主螢幕者時取第一個螢幕.
func primaryArea(screens []screenArea) rect {
	for _, s := range screens {
		if s.Primary {
			return s.Work
		}
	}
	return screens[0].Work
}

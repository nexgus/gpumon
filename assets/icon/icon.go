// Package icon 內嵌應用程式圖示與系統匣圖示的眨眼影格. 圖檔由同目錄的 generate.sh
// 自圖示母版產生, 母版更動後以 bash assets/icon/generate.sh 重新產生並一併提交.
package icon

import _ "embed"

// App 為應用程式圖示的 1024 x 1024 點陣圖, 保留透明的外緣.
//
//go:embed app-1024.png
var App []byte

// macOS 選單列的範本圖示 (黑色加 alpha), 編號 0 為睜眼, 3 為閉眼.
var (
	//go:embed tray-mac-0.png
	mac0 []byte
	//go:embed tray-mac-1.png
	mac1 []byte
	//go:embed tray-mac-2.png
	mac2 []byte
	//go:embed tray-mac-3.png
	mac3 []byte
)

// Windows 系統匣的淺色模式圖示. 每個 .ico 含 16, 20, 24, 32, 48 像素五種尺寸, Wails
// 依系統的小圖示尺寸挑選其中一張.
var (
	//go:embed tray-win-light-0.ico
	winLight0 []byte
	//go:embed tray-win-light-1.ico
	winLight1 []byte
	//go:embed tray-win-light-2.ico
	winLight2 []byte
	//go:embed tray-win-light-3.ico
	winLight3 []byte
)

// Windows 系統匣的深色模式圖示.
var (
	//go:embed tray-win-dark-0.ico
	winDark0 []byte
	//go:embed tray-win-dark-1.ico
	winDark1 []byte
	//go:embed tray-win-dark-2.ico
	winDark2 []byte
	//go:embed tray-win-dark-3.ico
	winDark3 []byte
)

// FrameCount 為眨眼影格的種類數.
const FrameCount = 4

// MacFrames 為 macOS 選單列範本圖示的四個眨眼影格, 索引為影格編號.
var MacFrames = [FrameCount][]byte{mac0, mac1, mac2, mac3}

// WinLightFrames 為 Windows 淺色模式圖示的四個眨眼影格, 索引為影格編號.
var WinLightFrames = [FrameCount][]byte{winLight0, winLight1, winLight2, winLight3}

// WinDarkFrames 為 Windows 深色模式圖示的四個眨眼影格, 索引為影格編號.
var WinDarkFrames = [FrameCount][]byte{winDark0, winDark1, winDark2, winDark3}

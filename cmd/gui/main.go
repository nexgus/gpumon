// Command gpumon 為本機 GUI "GPU Monitor", 以 Wails 3 開啟主視窗.
package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gpumon/assets/icon"
	"gpumon/cmd/gui/backend"
	"gpumon/cmd/gui/shell"
	"gpumon/frontend"
	"gpumon/internal/appinfo"
	"gpumon/internal/remotebin"
	"gpumon/internal/settings"
)

func main() {
	// 內嵌的遠端程式為 GUI 上傳至遠端的唯一來源, 缺少即為建置錯誤. 此處的引用
	// 同時避免連結器將未被引用的內嵌資料移除.
	if len(remotebin.Binary) == 0 {
		log.Fatal("embedded remote program is empty")
	}

	// 環境變數 GPUMON_DATA_DIR 有值時以其為應用程式資料目錄, 供測試使用, 避免寫入
	// 真實的資料目錄.
	dataDir := os.Getenv("GPUMON_DATA_DIR")
	if dataDir == "" {
		var err error
		dataDir, err = settings.DefaultDir()
		if err != nil {
			log.Fatal(err)
		}
	}
	b := backend.New(dataDir, slog.Default())
	sh := shell.New(b, slog.Default())

	app := application.New(application.Options{
		Name: appinfo.Name,
		Icon: appIcon(),
		Services: append([]application.Service{
			application.NewService(&appinfo.Service{}),
		}, b.Services()...),
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontend.Assets()),
		},
		// 任何方式要求結束時, 有主機連線中或重新連線中即先由主視窗確認.
		ShouldQuit: sh.ShouldQuit,
		// 終端機的 Ctrl+C 與 SIGTERM 不經由視窗, 無人可回應確認視窗, 改由下方的
		// 處理直接結束.
		DisableDefaultSignalHandler: true,
		Mac: application.MacOptions{
			// 主視窗隱藏於選單列時關閉關於視窗不得結束程式; 關閉主視窗即結束程式
			// 由 shell 處理.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	// 主視窗於程式啟動後依記住的大小與位置開啟, 見 shell.Attach.
	sh.Attach(app)

	// 收到 SIGINT 或 SIGTERM 時不經確認即結束; 結束過程照常中斷連線, 存檔並銷毀密碼.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		sh.ConfirmQuit()
	}()

	err := app.Run()
	// 服務的 ServiceShutdown 已於結束時中斷連線並存檔; 再呼叫一次以涵蓋 Run 異常
	// 返回的情形, 重複呼叫不做任何事.
	b.Close()
	if err != nil {
		log.Fatal(err)
	}
}

// appIcon 傳回交給 Wails 的應用程式圖示. macOS 直接執行未包裝於 .app 的執行檔時, Dock
// 沒有圖示可用, 以內嵌的應用程式圖示補上. 執行檔位於 .app 之內時傳回 nil, 由 .app 內的
// Assets.car 或 gpumon.icns 提供圖示, 以保留 macOS 26 起的圖示樣式; Wails 一經設定圖示
// 即覆蓋 .app 的圖示. Windows 的視窗圖示取自執行檔內的圖示資源, 此圖示僅在資源缺少時
// 才被採用.
func appIcon() []byte {
	if runtime.GOOS == "darwin" {
		exe, err := os.Executable()
		if err == nil && strings.HasSuffix(filepath.Dir(exe), ".app/Contents/MacOS") {
			return nil
		}
	}
	return icon.App
}

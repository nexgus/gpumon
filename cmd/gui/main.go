// Command gpumon 為本機 GUI "GPU Monitor", 以 Wails 3 開啟主視窗.
package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"

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

// Command gpumon 為本機 GUI "GPU Monitor", 以 Wails 3 開啟主視窗.
//
// 目前只開啟一個顯示應用程式名稱與版本的主視窗.
package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gpumon/frontend"
	"gpumon/internal/appinfo"
	"gpumon/internal/remotebin"
)

func main() {
	// 內嵌的遠端程式為 GUI 上傳至遠端的唯一來源, 缺少即為建置錯誤. 此處的引用
	// 同時避免連結器將未被引用的內嵌資料移除.
	if len(remotebin.Binary) == 0 {
		log.Fatal("embedded remote program is empty")
	}

	app := application.New(application.Options{
		Name: appinfo.Name,
		Services: []application.Service{
			application.NewService(&appinfo.Service{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontend.Assets()),
		},
		Mac: application.MacOptions{
			// 不設系統匣, 關閉主視窗即結束程式.
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  appinfo.Name,
		Width:  1280,
		Height: 800,
		URL:    "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

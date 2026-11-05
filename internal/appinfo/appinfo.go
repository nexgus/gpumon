// Package appinfo 提供前端查詢應用程式名稱與版本的 Wails service.
package appinfo

import "gpumon/internal/version"

// Name 為應用程式顯示名稱, 用於視窗標題與安裝包的產品名稱.
const Name = "GPU Monitor"

// Service 為綁定至前端的 service, 不含狀態.
type Service struct{}

// Name 回傳應用程式顯示名稱.
func (Service) Name() string {
	return Name
}

// Version 回傳完整版本字串.
func (Service) Version() string {
	return version.Full()
}

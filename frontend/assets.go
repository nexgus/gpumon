// Package frontend 內嵌前端打包產出 dist/, 供本機 GUI 的 asset server 使用.
//
// dist/ 由 build.sh 以 Vite 建置, 不入版控.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets 回傳以 dist/ 為根目錄的前端檔案系統.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

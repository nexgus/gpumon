package settings

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// dirName 為應用程式資料目錄的名稱.
const dirName = "gpumon"

// HistoryDirName 為記錄檔在應用程式資料目錄下的子目錄名稱.
const HistoryDirName = "history"

// DefaultDir 回傳應用程式資料目錄, 設定檔與記錄檔皆存於其下.
//
// macOS 為 ~/Library/Application Support/gpumon. Windows 為
// %LocalAppData%\gpumon, 不用漫遊的 %AppData%, 因記錄檔可達數十 MB 以上,
// 不宜隨使用者設定檔漫遊. 其他平台依 os.UserConfigDir. 目錄不一定已存在.
func DefaultDir() (string, error) {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LocalAppData"); base != "" {
			return filepath.Join(base, dirName), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if base == "" {
		return "", errors.New("cannot determine application data directory")
	}
	return filepath.Join(base, dirName), nil
}

// HistoryDir 回傳指定應用程式資料目錄下存放記錄檔的目錄.
func HistoryDir(appDir string) string {
	return filepath.Join(appDir, HistoryDirName)
}

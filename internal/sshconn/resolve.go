package sshconn

import (
	"os"
	"path/filepath"
)

// ConfigUser 回傳 ~/.ssh/config 為 Host 名稱 alias 設定的使用者名稱, 不建立連線.
// 設定檔不存在或沒有適用的 User 設定時回傳空字串.
func ConfigUser(alias string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg, err := loadHostConfig(filepath.Join(home, ".ssh", "config"), alias, home)
	if err != nil {
		return "", err
	}
	return cfg.User, nil
}

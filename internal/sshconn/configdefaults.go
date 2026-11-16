package sshconn

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ConfigDefaults 為 ~/.ssh/config 對某個 Host 名稱的設定, 供主機表單預填與舊版設定檔
// 升級時填入空白的欄位. 連線時不使用, 連線只取 HostName.
type ConfigDefaults struct {
	// User 為 User 的設定, 無設定為空字串.
	User string
	// Port 為 Port 的設定, 無設定為 0.
	Port int
	// KeyFile 為第一個不是 none 的 IdentityFile, 已展開 ~ 與 OpenSSH 的 % 記號,
	// 為絕對路徑; 無設定為空字串.
	KeyFile string
	// IdentitiesOnly 為 IdentitiesOnly 的值, IdentitiesOnlySet 為設定檔是否有此設定.
	IdentitiesOnly    bool
	IdentitiesOnlySet bool
	// Warnings 為讀取時略過之處的英文警告, 不為 nil.
	Warnings []string
}

// LookupConfig 讀取 configPath 中適用於 Host 名稱 alias 的設定, home 為本機的家目錄.
// 設定檔的問題一律放在 Warnings, 不回傳錯誤.
//
// IdentityFile 的 % 記號依連線時的規則展開: %h 為 HostName (無設定時為 alias),
// %p 為 Port (無設定時為 22), %r 為 User (無設定時為本機使用者名稱), %n 為 alias,
// %d 為家目錄, %u 為本機使用者名稱.
func LookupConfig(configPath, home, alias string) ConfigDefaults {
	hc, warnings := loadHostConfig(configPath, alias, home)
	d := ConfigDefaults{
		User: hc.User, Port: hc.Port,
		IdentitiesOnly: hc.IdentitiesOnly, IdentitiesOnlySet: hc.identitiesOnlySet,
		Warnings: warnings,
	}
	for _, f := range hc.IdentityFiles {
		if strings.EqualFold(f, "none") {
			continue
		}
		hostname := alias
		if hc.HostName != "" {
			hostname = expandTokens(hc.HostName, map[byte]string{'h': alias})
		}
		port := hc.Port
		if port == 0 {
			port = 22
		}
		local := localUserName()
		user := hc.User
		if user == "" {
			user = local
		}
		tokens := map[byte]string{
			'h': hostname, 'p': strconv.Itoa(port), 'r': user, 'n': alias, 'd': home, 'u': local,
		}
		d.KeyFile = resolveKeyPath(f, tokens, home)
		break
	}
	return d
}

// UserConfigDefaults 讀取使用者的 ~/.ssh/config 中適用於 alias 的設定. 只有無法取得
// 本機家目錄時回傳錯誤.
func UserConfigDefaults(alias string) (ConfigDefaults, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ConfigDefaults{Warnings: []string{}}, err
	}
	return LookupConfig(filepath.Join(home, ".ssh", "config"), home, alias), nil
}

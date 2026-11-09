package sshconn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

// knownHostsMu 使同一程式內多台主機同時連線時, 對 known_hosts 的寫入不互相交錯.
var knownHostsMu sync.Mutex

// hostKeyMismatchError 表示遠端的 host key 與 known_hosts 所收錄者不符.
type hostKeyMismatchError struct {
	host        string
	fingerprint string
}

func (e *hostKeyMismatchError) Error() string {
	return fmt.Sprintf("host key for %s does not match known_hosts (offered %s)", e.host, e.fingerprint)
}

// hostKeyChecker 以 accept-new 語意檢查 host key: 未收錄的主機自動寫入
// known_hosts, 已收錄但不符者拒絕.
type hostKeyChecker struct {
	path string
	db   *knownhosts.HostKeyDB
}

// newHostKeyChecker 讀取 known_hosts. 檔案不存在時視為空檔, 寫入時才建立.
func newHostKeyChecker(path string) (*hostKeyChecker, error) {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	var files []string
	if _, err := os.Stat(path); err == nil {
		files = append(files, path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	db, err := knownhosts.NewDB(files...)
	if err != nil {
		return nil, err
	}
	return &hostKeyChecker{path: path, db: db}, nil
}

// algorithms 回傳 known_hosts 為 addr 收錄的 host key 演算法, 供協商時優先採用,
// 避免主機另有其他種類的 host key 時被誤判為不符. 未收錄時回傳 nil.
func (c *hostKeyChecker) algorithms(addr string) []string {
	return c.db.HostKeyAlgorithms(addr)
}

// callback 回傳供 ssh.ClientConfig 使用的 host key 回呼函數.
func (c *hostKeyChecker) callback() ssh.HostKeyCallback {
	inner := c.db.HostKeyCallback()
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := inner(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *xknownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) > 0 {
			return &hostKeyMismatchError{host: hostname, fingerprint: ssh.FingerprintSHA256(key)}
		}
		// 寫入失敗時仍允許連線, 與 OpenSSH 的 accept-new 相同; 下次連線會再次寫入.
		_ = c.append(hostname, key)
		return nil
	}
}

// append 把 host key 附加至 known_hosts. 檔案與其目錄不存在時建立, 權限分別為
// 0600 與 0700.
func (c *hostKeyChecker) append(hostname string, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(c.path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			prefix = "\n"
		}
	} else if err != nil {
		return err
	}
	_, err = io.WriteString(f, prefix+knownhosts.Line([]string{hostname}, key)+"\n")
	return err
}

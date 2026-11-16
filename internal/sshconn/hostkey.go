package sshconn

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

// knownHostsMu 使同一程式內多台主機同時連線時, 對程式自己的 known_hosts 的讀寫
// 不互相交錯.
var knownHostsMu sync.Mutex

// HostKeyFingerprint 為一把主機金鑰的種類與指紋. Fingerprint 為 OpenSSH 格式的
// SHA256 指紋, 例如 "SHA256:...".
type HostKeyFingerprint struct {
	Type        string
	Fingerprint string
}

func fingerprintOf(key ssh.PublicKey) HostKeyFingerprint {
	return HostKeyFingerprint{Type: key.Type(), Fingerprint: ssh.FingerprintSHA256(key)}
}

// HostKeyRequest 為遠端主機的主機金鑰需要使用者確認時提供給回呼函數的資訊.
type HostKeyRequest struct {
	// Host 為連線目標的顯示名稱, 即 Config.Host; Address 為比對 known_hosts 所用的
	// "主機:連接埠".
	Host    string
	Address string
	// Key 為遠端主機提供的主機金鑰.
	Key HostKeyFingerprint
	// Changed 為假表示兩份 known_hosts 皆沒有該主機的記錄; 為真表示有記錄但皆與
	// Key 不同, Old 為所有記錄的主機金鑰, 相同者只列一次.
	Changed bool
	Old     []HostKeyFingerprint
}

// HostKeyFunc 詢問使用者是否接受遠端主機的主機金鑰. 回傳 nil 表示接受: 本套件把
// 主機金鑰寫入程式自己的 known_hosts, 刪除其中該主機的舊記錄, 然後繼續登入.
// 回傳 ErrCanceled 或其他錯誤時中止連線, 錯誤種類為 KindCanceled. 等待回呼函數的
// 時間不計入建立連線的逾時.
type HostKeyFunc func(ctx context.Context, req HostKeyRequest) error

// hostKeyChecker 以程式自己的 known_hosts 與 OpenSSH 的 known_hosts 比對主機金鑰.
// 任一筆該主機的記錄與遠端主機的金鑰相同即通過; 沒有記錄或皆不相同時詢問使用者.
type hostKeyChecker struct {
	own string
	db  *knownhosts.HostKeyDB
}

// newHostKeyChecker 讀取兩份 known_hosts. 程式自己的檔案不存在時建立空檔案, 所在
// 目錄不存在時一併建立. 兩份檔案無法取得檔案資訊 (例如不存在, 或建立失敗) 時
// 視為空檔案; 存在而無法讀取或解析時回傳 KindConfig 的 *Error.
func newHostKeyChecker(own, openssh string) (*hostKeyChecker, error) {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	if _, err := os.Stat(own); errors.Is(err, os.ErrNotExist) {
		if os.MkdirAll(filepath.Dir(own), 0o700) == nil {
			if f, err := os.OpenFile(own, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
				f.Close()
			}
		}
	}
	var files []string
	for _, p := range []string{own, openssh} {
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	db, err := knownhosts.NewDB(files...)
	if err != nil {
		return nil, newError(KindConfig, err, "Cannot read the known_hosts files")
	}
	return &hostKeyChecker{own: own, db: db}, nil
}

// algorithms 回傳兩份 known_hosts 為 addr 記錄的主機金鑰種類, 供協商時優先採用,
// 避免主機另有其他種類的主機金鑰時被誤判為不符. 未記錄時回傳 nil.
func (c *hostKeyChecker) algorithms(addr string) []string {
	return c.db.HostKeyAlgorithms(addr)
}

// check 比對遠端主機的主機金鑰, 需要時以 ask 詢問使用者. ask 為 nil 時不詢問,
// 回傳說明需要使用者確認的 KindHostKey 錯誤. 詢問期間暫停 clock.
func (c *hostKeyChecker) check(ctx context.Context, hostname string, remote net.Addr, key ssh.PublicKey,
	display string, ask HostKeyFunc, clock *budget) error {
	err := c.db.HostKeyCallback()(hostname, remote, key)
	if err == nil {
		return nil
	}
	var revoked *xknownhosts.RevokedError
	if errors.As(err, &revoked) {
		return newError(KindHostKey, err, "The host key of %s is marked as revoked in %s", display, revoked.Revoked.Filename)
	}
	var keyErr *xknownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return newError(KindHostKey, err, "Cannot verify the host key of %s", display)
	}
	req := HostKeyRequest{Host: display, Address: hostname, Key: fingerprintOf(key), Changed: len(keyErr.Want) > 0}
	seen := map[string]bool{}
	for _, k := range keyErr.Want {
		fp := fingerprintOf(k.Key)
		if !seen[fp.Fingerprint] {
			seen[fp.Fingerprint] = true
			req.Old = append(req.Old, fp)
		}
	}
	if ask == nil {
		if req.Changed {
			return newError(KindHostKey, err,
				"The host key of %s has changed, and its new fingerprint %s needs your confirmation. "+
					"Connect to the host again to review it", display, req.Key.Fingerprint)
		}
		return newError(KindHostKey, err,
			"The host key of %s is unknown, and its fingerprint %s needs your confirmation. "+
				"Connect to the host again to review it", display, req.Key.Fingerprint)
	}
	clock.pause()
	err = ask(ctx, req)
	clock.resume()
	if err != nil {
		return err
	}
	// 寫入失敗時仍繼續連線; 下次連線會再次詢問.
	_ = c.record(hostname, remote, key)
	return nil
}

// record 把主機金鑰寫入程式自己的 known_hosts, 並刪除其中該主機的舊記錄. 該主機
// 的記錄依 golang.org/x/crypto/ssh/knownhosts 的比對規則判定, 含雜湊過的主機名稱
// 與萬用字元. 檔案與其目錄不存在時建立, 權限分別為 0600 與 0700.
func (c *hostKeyChecker) record(hostname string, remote net.Addr, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	line := knownhosts.Line([]string{hostname}, key)
	data, err := os.ReadFile(c.own)
	if errors.Is(err, os.ErrNotExist) {
		return appendLine(c.own, line)
	}
	if err != nil {
		return err
	}
	stale, err := staleLines(c.own, hostname, remote, key)
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		return appendLine(c.own, line)
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		if stale[n] {
			continue
		}
		out.Write(sc.Bytes())
		out.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	out.WriteString(line + "\n")
	return replaceFile(c.own, out.Bytes())
}

// staleLines 回傳 path 中與 hostname 相符的記錄的行號, 由 1 起算, 與
// golang.org/x/crypto/ssh/knownhosts 的行號相同.
func staleLines(path, hostname string, remote net.Addr, key ssh.PublicKey) (map[int]bool, error) {
	cb, err := xknownhosts.New(path)
	if err != nil {
		return nil, err
	}
	err = cb(hostname, remote, key)
	var keyErr *xknownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return nil, nil
	}
	lines := map[int]bool{}
	for _, k := range keyErr.Want {
		if k.Filename == path {
			lines[k.Line] = true
		}
	}
	return lines, nil
}

// appendLine 把一行附加至 path, 檔尾沒有換行時先補換行. 檔案與其目錄不存在時
// 建立, 權限分別為 0600 與 0700.
func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			prefix = "\n"
		}
	}
	_, err = io.WriteString(f, prefix+line+"\n")
	return err
}

// replaceFile 先寫入同一目錄的暫存檔再更名取代 path, 權限為 0600.
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

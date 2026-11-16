package sshconn

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

// maxKnownHostsLine 為 known_hosts 單行的長度上限, 超過者視為無法解析. 合法的記錄
// 遠短於此, 且 golang.org/x/crypto/ssh/knownhosts 讀取 64 KiB 以上的行會失敗.
const maxKnownHostsLine = 32 << 10

// 與 golang.org/x/crypto/ssh/knownhosts 相同的標記.
const (
	markerCertAuthority = "@cert-authority"
	markerRevoked       = "@revoked"
)

// validKnownHostsLine 回報 golang.org/x/crypto/ssh/knownhosts 能否解析這一行, 規則
// 與該套件的 parseLine 相同. 空白列與註解列視為可解析. line 不含換行字元.
func validKnownHostsLine(line []byte) bool {
	if len(line) > maxKnownHostsLine {
		return false
	}
	line = bytes.Trim(line, " \t")
	if len(line) == 0 || line[0] == '#' {
		return true
	}
	marker := ""
	if w, next := nextKnownHostsWord(line); w == markerCertAuthority || w == markerRevoked {
		marker, line = w, next
	}
	host, line := nextKnownHostsWord(line)
	if len(host) > 0 && host[0] == '@' {
		return false
	}
	if len(line) == 0 {
		return false
	}
	wantType, line := nextKnownHostsWord(line)
	if len(line) == 0 {
		return false
	}
	keyBlob, _ := nextKnownHostsWord(line)
	keyBytes, err := base64.StdEncoding.DecodeString(keyBlob)
	if err != nil {
		return false
	}
	key, err := ssh.ParsePublicKey(keyBytes)
	if err != nil || key.Type() != wantType {
		return false
	}
	if marker == markerRevoked {
		return true
	}
	if host[0] == '|' {
		return validHashedHost(host)
	}
	return validHostPatterns(host)
}

func nextKnownHostsWord(line []byte) (string, []byte) {
	i := bytes.IndexAny(line, "\t ")
	if i == -1 {
		return string(line), nil
	}
	return string(line[:i]), bytes.Trim(line[i:], " \t")
}

func validHashedHost(encoded string) bool {
	parts := strings.Split(encoded, "|")
	if len(parts) != 4 || parts[1] != "1" {
		return false
	}
	for _, p := range parts[2:] {
		if _, err := base64.StdEncoding.DecodeString(p); err != nil {
			return false
		}
	}
	return true
}

func validHostPatterns(pattern string) bool {
	for _, p := range strings.Split(pattern, ",") {
		if p == "" {
			continue
		}
		p = strings.TrimPrefix(p, "!")
		if p == "" {
			return false
		}
		if p[0] == '[' {
			if _, _, err := net.SplitHostPort(p); err != nil {
				return false
			}
		}
	}
	return true
}

// filterKnownHosts 檢查 data 的每一行, 回傳處理後的內容與無法解析的行數. blank 為
// 真時無法解析的行改為空白列, 使其餘各行的行號不變; 為假時刪除該行.
func filterKnownHosts(data []byte, blank bool) ([]byte, int) {
	var out bytes.Buffer
	bad := 0
	lines := bytes.SplitAfter(data, []byte("\n"))
	for _, raw := range lines {
		if len(raw) == 0 {
			continue
		}
		content := bytes.TrimSuffix(bytes.TrimSuffix(raw, []byte("\n")), []byte("\r"))
		if validKnownHostsLine(content) {
			out.Write(raw)
			continue
		}
		bad++
		if blank {
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), bad
}

// knownHostsSource 為建立主機金鑰資料庫所用的一份 known_hosts. data 為 nil 時直接
// 讀取 path; 否則以 data 的內容代替 path 的內容.
type knownHostsSource struct {
	path string
	data []byte
}

// ownKnownHosts 檢查並修正程式自己的 known_hosts, 回傳建立資料庫所用的來源; 視為
// 空檔案時 ok 為假. 呼叫端須持有 knownHostsMu.
//
// 檔案不存在時建立空檔案, 所在目錄不存在時一併建立. 檔案存在但讀不到時刪除並重建
// 空檔案. 有無法解析的行時以其餘各行改寫檔案, 不另留備份; 改寫失敗時以其餘各行的
// 內容代替檔案內容. 刪除或建立失敗時視為空檔案.
func ownKnownHosts(path string) (src knownHostsSource, ok bool) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return knownHostsSource{path: path}, createEmptyFile(path, false)
	case err != nil:
		return knownHostsSource{path: path}, createEmptyFile(path, true)
	}
	kept, bad := filterKnownHosts(data, false)
	if bad == 0 {
		return knownHostsSource{path: path}, true
	}
	if replaceFile(path, kept) == nil {
		return knownHostsSource{path: path}, true
	}
	return knownHostsSource{path: path, data: kept}, true
}

// createEmptyFile 建立權限 0600 的空檔案, 所在目錄不存在時以 0700 建立. remove 為真時
// 先刪除既有的檔案. 回傳檔案是否已建立.
func createEmptyFile(path string, remove bool) bool {
	if remove {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

// opensshKnownHosts 讀取 OpenSSH 的 known_hosts, 回傳建立資料庫所用的來源; 檔案讀不到
// 時 ok 為假, 視為空檔案. 無法解析的行改為空白列, 檔案本身永不寫入.
func opensshKnownHosts(path string) (src knownHostsSource, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return knownHostsSource{}, false
	}
	cleaned, bad := filterKnownHosts(data, true)
	if bad == 0 {
		return knownHostsSource{path: path}, true
	}
	return knownHostsSource{path: path, data: cleaned}, true
}

// openKnownHostsDB 以 sources 建立主機金鑰資料庫. 以內容代替的來源先寫入 tmpDir 的
// 暫存檔, 資料庫建立後即刪除; names 把暫存檔名對應回原本的路徑. 某份來源仍無法讀取
// 時, 該來源視為空檔案.
func openKnownHostsDB(sources []knownHostsSource, tmpDir string) (*knownhosts.HostKeyDB, map[string]string) {
	names := map[string]string{}
	var files []string
	for _, s := range sources {
		if s.data == nil {
			files = append(files, s.path)
			continue
		}
		name, err := writeTempKnownHosts(tmpDir, s.data)
		if err != nil {
			continue
		}
		defer os.Remove(name)
		names[name] = s.path
		files = append(files, name)
	}
	if db, err := knownhosts.NewDB(files...); err == nil {
		return db, names
	}
	var usable []string
	for _, f := range files {
		if _, err := knownhosts.NewDB(f); err == nil {
			usable = append(usable, f)
		}
	}
	db, err := knownhosts.NewDB(usable...)
	if err != nil {
		db, _ = knownhosts.NewDB()
	}
	return db, names
}

// writeTempKnownHosts 把 data 寫入 dir 的暫存檔, dir 無法寫入時改用系統的暫存目錄.
// 權限為 0600.
func writeTempKnownHosts(dir string, data []byte) (string, error) {
	var lastErr error
	for _, d := range []string{dir, os.TempDir()} {
		f, err := os.CreateTemp(d, ".known_hosts.tmp*")
		if err != nil {
			lastErr = err
			continue
		}
		name := f.Name()
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(name, 0o600)
		}
		if err != nil {
			os.Remove(name)
			lastErr = err
			continue
		}
		return name, nil
	}
	return "", lastErr
}

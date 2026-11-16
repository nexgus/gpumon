package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Rehost 把說明區 host 成員為 oldHost 的記錄檔改為 newHost: 以新的說明區另寫一份
// 至 newHost 的主機目錄, 檔名不變 (同名時依序加 `-2`, `-3`), 寫入完成後才刪除原檔;
// 原主機目錄因而成為空目錄時一併刪除. 說明區的其他成員與各筆記錄原樣保留.
// 原主機目錄中 host 成員不是 oldHost 的檔案不動. 個別檔案失敗時繼續處理其餘檔案,
// 回傳所有失敗的原因. 呼叫端須確保這些記錄檔不在寫入中.
func Rehost(dataDir, oldHost, newHost string) error {
	if oldHost == newHost {
		return nil
	}
	srcDir := HostDir(dataDir, oldHost)
	dstDir := HostDir(dataDir, newHost)
	entries, err := os.ReadDir(srcDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !e.Type().IsRegular() || filepath.Ext(e.Name()) != ".gpurec" {
			continue
		}
		if err := rehostFile(filepath.Join(srcDir, e.Name()), dstDir, oldHost, newHost); err != nil {
			errs = append(errs, err)
		}
	}
	if srcDir != dstDir {
		// 目錄不是空的時刪除失敗, 屬預期情形.
		_ = os.Remove(srcDir)
	}
	return errors.Join(errs...)
}

// rehostFile 處理一個記錄檔, host 成員不是 oldHost 或不是記錄檔時不動.
func rehostFile(path, dstDir, oldHost, newHost string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	rf, err := openFile(f)
	if err != nil || rf.hdr.Host != oldHost {
		return nil
	}
	meta := make([]byte, rf.base-fixedSize)
	if _, err := f.ReadAt(meta, fixedSize); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	newMeta, err := replaceHostMember(meta, newHost)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	fixed := make([]byte, fixedSize)
	if _, err := f.ReadAt(fixed, 0); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	le.PutUint32(fixed[12:], uint32(len(newMeta)))

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dstDir, ".rehost-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, err := tmp.Write(fixed); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(newMeta); err != nil {
		return fail(err)
	}
	if _, err := io.Copy(tmp, io.NewSectionReader(f, rf.base, 1<<62)); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("%s: %w", path, err)
	}
	dst, err := freeName(dstDir, filepath.Base(path))
	if err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("%s: %w", path, err)
	}
	f.Close()
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// replaceHostMember 回傳把說明區的 host 成員換成 host 之後的內容, 其他成員原樣保留.
func replaceHostMember(meta []byte, host string) ([]byte, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(meta, &members); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(host); err != nil {
		return nil, err
	}
	members["host"] = json.RawMessage(bytes.TrimRight(b.Bytes(), "\n"))
	b.Reset()
	if err := enc.Encode(members); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// freeName 回傳 dir 中尚未使用的檔名, name 已存在時依序加 `-2`, `-3`.
func freeName(dir, name string) (string, error) {
	stem := strings.TrimSuffix(name, ".gpurec")
	for i := 1; ; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s-%d.gpurec", stem, i)
		}
		p := filepath.Join(dir, n)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p, nil
		} else if err != nil {
			return "", err
		}
	}
}

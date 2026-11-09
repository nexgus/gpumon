package gpu

import (
	"bufio"
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// maxCmdLen 為程序命令列輸出的最大位元組數, 超過者截斷, 避免極長的命令列
// (例如帶完整 classpath 的 java) 使每筆 sample 過大.
const maxCmdLen = 512

// procInfo 自 /proc 讀取程序的使用者與命令列, 並快取 UID 對應的使用者名稱.
type procInfo struct {
	root string

	mu    sync.Mutex
	users map[int]string
}

func newProcInfo(root string) *procInfo {
	return &procInfo{root: root, users: map[int]string{}}
}

// user 回傳程序的使用者名稱; 名稱查不到時回傳 UID 的十進位字串, 程序不存在
// 時回傳空字串.
func (p *procInfo) user(pid int) string {
	uid, ok := readProcUID(filepath.Join(p.root, strconv.Itoa(pid), "status"))
	if !ok {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if name, ok := p.users[uid]; ok {
		return name
	}
	name := strconv.Itoa(uid)
	// CGO_ENABLED=0 時 os/user 只讀 /etc/passwd, 由 LDAP 等管理的帳號查不到,
	// 此時以 UID 代替.
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	p.users[uid] = name
	return name
}

// readProcUID 自 /proc/<pid>/status 取出實際 UID, 即 Uid 列的第一個值.
func readProcUID(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line[len("Uid:"):])
		if len(fields) == 0 {
			return 0, false
		}
		uid, err := strconv.Atoi(fields[0])
		return uid, err == nil
	}
	return 0, false
}

// cmd 回傳程序的命令列, 參數間以空白分隔. 命令列為空 (例如 zombie 程序) 時
// 改用 comm 並加上方括號; 皆讀不到時回傳空字串.
func (p *procInfo) cmd(pid int) string {
	dir := filepath.Join(p.root, strconv.Itoa(pid))
	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		if s := formatCmdline(b); s != "" {
			return s
		}
	}
	if s, err := readTrim(filepath.Join(dir, "comm")); err == nil && s != "" {
		return "[" + s + "]"
	}
	return ""
}

// formatCmdline 將以 NUL 分隔的 /proc/<pid>/cmdline 內容轉為以空白分隔的字串,
// 並截斷至 maxCmdLen.
func formatCmdline(b []byte) string {
	b = bytes.TrimRight(b, "\x00")
	s := strings.TrimSpace(string(bytes.ReplaceAll(b, []byte{0}, []byte{' '})))
	if len(s) > maxCmdLen {
		s = strings.ToValidUTF8(s[:maxCmdLen], "")
	}
	return s
}

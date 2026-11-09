package sshconn

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// hostConfig 為 ~/.ssh/config 中適用於某個 Host 名稱的設定, 只含規格所列的六項.
// 字串為空表示未設定; Port 為 0 表示未設定.
type hostConfig struct {
	HostName      string
	User          string
	Port          int
	IdentityFiles []string
	// ProxyJump 與 ProxyCommand 依 OpenSSH 的規則以先出現者為準, 另一項忽略.
	// 值為 "none" 時視為未設定, 但仍阻擋之後出現的設定.
	ProxyJump    string
	ProxyCommand string
	proxySet     bool
}

// maxIncludeDepth 與 OpenSSH 的 READCONF_MAX_DEPTH 相同.
const maxIncludeDepth = 16

// configParser 依 OpenSSH 的規則, 針對單一 Host 名稱讀取設定檔. 每個設定項目以
// 第一個取得的值為準, IdentityFile 則累積所有值.
//
// 與 OpenSSH 相同, 相對路徑的 Include 以使用者設定檔所在目錄 (正式環境即
// ~/.ssh) 為基準, ~ 展開為家目錄. Match 區塊不支援, 其中的設定一律忽略.
type configParser struct {
	alias   string
	home    string
	baseDir string
	cfg     hostConfig
}

// loadHostConfig 讀取 configPath 中適用於 alias 的設定. 設定檔不存在時回傳空設定.
func loadHostConfig(configPath, alias, home string) (hostConfig, error) {
	p := &configParser{alias: alias, home: home, baseDir: filepath.Dir(configPath)}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return hostConfig{}, nil
		}
		return hostConfig{}, err
	}
	if err := p.parse(configPath, data, false, 0); err != nil {
		return hostConfig{}, err
	}
	return p.cfg, nil
}

// parse 處理一個設定檔的內容. neverMatch 為真時, 檔中的 Host 一律不符合, 對應
// OpenSSH 在不適用的 Host 區塊中遇到 Include 的處理方式.
func (p *configParser) parse(name string, data []byte, neverMatch bool, depth int) error {
	if depth > maxIncludeDepth {
		return fmt.Errorf("%s: too many nested Include directives", name)
	}
	active := !neverMatch
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		keyword, rest, ok := splitKeyword(sc.Text())
		if !ok {
			continue
		}
		where := fmt.Sprintf("%s line %d", name, lineNo)
		switch keyword {
		case "host":
			args, err := splitArgs(rest)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			active = !neverMatch && hostMatches(p.alias, args)
		case "match":
			active = false
		case "include":
			args, err := splitArgs(rest)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			for _, arg := range args {
				if err := p.include(arg, !active, depth); err != nil {
					return fmt.Errorf("%s: %w", where, err)
				}
			}
		default:
			if !active {
				continue
			}
			if err := p.apply(keyword, rest); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		}
	}
	return sc.Err()
}

func (p *configParser) include(pattern string, neverMatch bool, depth int) error {
	pattern = expandTilde(pattern, p.home)
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(p.baseDir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.IsDir() {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			return err
		}
		if err := p.parse(m, data, neverMatch, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (p *configParser) apply(keyword, rest string) error {
	switch keyword {
	case "hostname", "user", "port", "identityfile", "proxyjump":
	case "proxycommand":
		if !p.cfg.proxySet {
			p.cfg.proxySet = true
			if v := strings.TrimSpace(rest); !strings.EqualFold(v, "none") {
				p.cfg.ProxyCommand = v
			}
		}
		return nil
	default:
		return nil
	}
	args, err := splitArgs(rest)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("missing argument for %s", keyword)
	}
	v := args[0]
	switch keyword {
	case "hostname":
		if p.cfg.HostName == "" {
			p.cfg.HostName = v
		}
	case "user":
		if p.cfg.User == "" {
			p.cfg.User = v
		}
	case "port":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port %q", v)
		}
		if p.cfg.Port == 0 {
			p.cfg.Port = n
		}
	case "identityfile":
		p.cfg.IdentityFiles = append(p.cfg.IdentityFiles, v)
	case "proxyjump":
		if !p.cfg.proxySet {
			p.cfg.proxySet = true
			if !strings.EqualFold(v, "none") {
				p.cfg.ProxyJump = v
			}
		}
	}
	return nil
}

// splitKeyword 自一行設定取出小寫的關鍵字與其後的原始文字. 空白列與註解列回傳
// ok 為假. 關鍵字與參數之間可為空白或一個等號.
func splitKeyword(line string) (keyword, rest string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", "", false
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return strings.ToLower(line), "", true
	}
	keyword = strings.ToLower(line[:i])
	rest = strings.TrimLeft(line[i:], " \t")
	if strings.HasPrefix(rest, "=") {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	return keyword, rest, true
}

// splitArgs 以空白分割參數, 支援雙引號包住含空白的參數.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg, quoted := false, false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			inArg = true
		case (r == ' ' || r == '\t') && !quoted:
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quoted {
		return nil, fmt.Errorf("unterminated quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

// hostMatches 依 OpenSSH 的規則判斷 alias 是否符合 Host 列的模式清單. 任一否定
// 模式 (以 ! 開頭) 符合即不符合; 否則任一肯定模式符合即符合. 比對不分大小寫.
func hostMatches(alias string, patterns []string) bool {
	alias = strings.ToLower(alias)
	matched := false
	for _, pat := range patterns {
		negate := strings.HasPrefix(pat, "!")
		if negate {
			pat = pat[1:]
		}
		if !wildcardMatch(strings.ToLower(pat), alias) {
			continue
		}
		if negate {
			return false
		}
		matched = true
	}
	return matched
}

// wildcardMatch 支援 * 與 ? 兩種萬用字元.
func wildcardMatch(pattern, s string) bool {
	// path.Match 的 [ 與 \ 有特殊意義, OpenSSH 沒有, 故先跳脫.
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	// path.Match 的 * 不跨越 /, 主機名稱不含 /, 不影響結果.
	ok, err := path.Match(b.String(), s)
	return err == nil && ok
}

// expandTilde 把開頭的 ~ 或 ~/ 展開為家目錄.
func expandTilde(s, home string) string {
	if s == "~" {
		return home
	}
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		return filepath.Join(home, s[2:])
	}
	return s
}

// expandTokens 展開 OpenSSH 設定值中的 %% 與 tokens 所列的 % 代碼. 不認得的代碼
// 原樣保留.
func expandTokens(s string, tokens map[byte]string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		c := s[i+1]
		if c == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		if v, ok := tokens[c]; ok {
			b.WriteString(v)
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

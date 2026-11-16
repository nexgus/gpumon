package sshconn

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// hostConfig 為 ~/.ssh/config 中適用於某個 Host 名稱的設定, 只含規格所列的五項.
// 字串為空表示未設定; Port 為 0 表示未設定. ProxyJump 與 ProxyCommand 不支援,
// 與其他項目同樣忽略, 直接連線至該主機.
type hostConfig struct {
	HostName      string
	User          string
	Port          int
	IdentityFiles []string
	// IdentitiesOnly 為真時, ssh-agent 的金鑰只保留與金鑰檔相同者.
	IdentitiesOnly    bool
	identitiesOnlySet bool
}

// maxIncludeDepth 與 OpenSSH 的 READCONF_MAX_DEPTH 相同.
const maxIncludeDepth = 16

// maxConfigLine 為設定檔單行的長度上限, 超過時該檔案其後的內容略過.
const maxConfigLine = 1 << 20

// configParser 依 OpenSSH 的規則, 針對單一 Host 名稱讀取設定檔. 每個設定項目以
// 第一個取得的值為準, IdentityFile 則累積所有值.
//
// 與 OpenSSH 相同, 相對路徑的 Include 以使用者設定檔所在目錄 (正式環境即
// ~/.ssh) 為基準, ~ 展開為家目錄. Match 區塊不支援, 其中的設定一律忽略.
//
// 設定檔只作參考, 不要求完全正確: 有錯誤的行與讀不到的 Include 略過, 其餘照常
// 使用, 每個略過之處於 warnings 加入一則英文警告.
type configParser struct {
	alias    string
	home     string
	baseDir  string
	cfg      hostConfig
	warnings []string
}

// loadHostConfig 讀取 configPath 中適用於 alias 的設定, 並回傳略過之處的英文警告.
// 設定檔不存在時回傳空設定且沒有警告; 存在但讀不到時視為沒有設定, 並回傳一則警告.
// 本函數不回傳錯誤, 回傳的警告不為 nil.
func loadHostConfig(configPath, alias, home string) (hostConfig, []string) {
	p := &configParser{alias: alias, home: home, baseDir: filepath.Dir(configPath), warnings: []string{}}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			p.warn("Ignored %s because it cannot be read: %v", p.display(configPath), err)
		}
		return hostConfig{}, p.warnings
	}
	p.parse(configPath, data, false, 0)
	return p.cfg, p.warnings
}

func (p *configParser) warn(format string, args ...any) {
	p.warnings = append(p.warnings, fmt.Sprintf(format, args...))
}

// display 回傳警告中顯示的檔案路徑.
func (p *configParser) display(name string) string {
	return displayPath(name, p.home)
}

// displayPath 把位於 home 之下的路徑改以 ~ 開頭表示, 其餘原樣回傳.
func displayPath(name, home string) string {
	if home == "" {
		return name
	}
	rel, err := filepath.Rel(home, name)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return name
	}
	if rel == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + rel
}

// parse 處理一個設定檔的內容. neverMatch 為真時, 檔中的 Host 一律不符合, 對應
// OpenSSH 在不適用的 Host 區塊中遇到 Include 的處理方式.
func (p *configParser) parse(name string, data []byte, neverMatch bool, depth int) {
	active := !neverMatch
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxConfigLine)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		keyword, rest, ok := splitKeyword(sc.Text())
		if !ok {
			continue
		}
		switch keyword {
		case "host":
			args, err := splitArgs(rest)
			if err != nil {
				// 無法確定此區塊適用哪些主機, 區塊內的設定一律不採用.
				p.warn("Skipped line %d of %s: %v", lineNo, p.display(name), err)
				active = false
				continue
			}
			active = !neverMatch && hostMatches(p.alias, args)
		case "match":
			active = false
		case "include":
			args, err := splitArgs(rest)
			if err != nil {
				p.warn("Skipped line %d of %s: %v", lineNo, p.display(name), err)
				continue
			}
			for _, arg := range args {
				p.include(arg, !active, depth, name, lineNo)
			}
		default:
			if !active {
				continue
			}
			if err := p.apply(keyword, rest); err != nil {
				p.warn("Skipped line %d of %s: %v", lineNo, p.display(name), err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		p.warn("Skipped the rest of %s after line %d: %v", p.display(name), lineNo, err)
	}
}

// include 處理 Include 的一個參數. 讀不到的檔案與超過巢狀層數上限的 Include 略過.
// from 與 lineNo 為 Include 所在的檔案與行號, 供警告使用.
func (p *configParser) include(pattern string, neverMatch bool, depth int, from string, lineNo int) {
	where := fmt.Sprintf("the Include on line %d of %s", lineNo, p.display(from))
	if depth+1 > maxIncludeDepth {
		p.warn("Skipped %s: Include directives are nested more than %d levels deep", where, maxIncludeDepth)
		return
	}
	pattern = expandTilde(pattern, p.home)
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(p.baseDir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		p.warn("Skipped %s: invalid pattern %q", where, pattern)
		return
	}
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.IsDir() {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			p.warn("Skipped %s for %s: %v", where, p.display(m), err)
			continue
		}
		p.parse(m, data, neverMatch, depth+1)
	}
}

// canonicalKeyword 為警告中顯示的關鍵字寫法.
var canonicalKeyword = map[string]string{
	"hostname": "HostName", "user": "User", "port": "Port",
	"identityfile": "IdentityFile", "identitiesonly": "IdentitiesOnly",
}

func (p *configParser) apply(keyword, rest string) error {
	if _, ok := canonicalKeyword[keyword]; !ok {
		return nil
	}
	args, err := splitArgs(rest)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("missing argument for %s", canonicalKeyword[keyword])
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
	case "identitiesonly":
		var on bool
		switch strings.ToLower(v) {
		case "yes", "true":
			on = true
		case "no", "false":
		default:
			return fmt.Errorf("invalid IdentitiesOnly value %q", v)
		}
		if !p.cfg.identitiesOnlySet {
			p.cfg.identitiesOnlySet = true
			p.cfg.IdentitiesOnly = on
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
		return nil, errors.New("unterminated quote")
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

// Command licgen 產生第三方授權清單, 輸出為前端的 JSON 資料檔, 由 build.sh 於每次
// 建置時呼叫.
//
// 用法:
//
//	go run . -root <專案根目錄> -npm <npm 套件清單>
//
// 清單分為三類, 產生過程不需網路:
//   - Go 的相依套件: 以 go list -deps 列出實際編譯進 GUI (darwin/arm64 與
//     windows/amd64) 與遠端程式 (linux/amd64) 的 module, 自本機 module cache
//     讀取授權檔. go.mod 的 require 未標為 indirect 者為直接引用.
//   - 前端的 npm 相依套件: npm 套件清單由 Vite 建置時寫出, 每行為
//     "<名稱>\t<目錄>", 只含實際打包進前端產出者. frontend/package.json 的
//     dependencies 所列者為直接引用. 套件本身未附授權檔者, 改讀
//     licenses/npm/<名稱>/ 內由專案維護的授權檔.
//   - 其他素材: licenses/other-materials.json 為人工維護的清單, 授權條文存於
//     licenses/ 之下, 一律為直接引用.
//
// 授權條文逐字取自授權檔, 附有 NOTICE 檔時一併納入. 輸出依固定順序排列, 不含
// 本機路徑, 相依套件未變時輸出逐位元組相同. 找不到授權檔時以非零碼結束.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/licensecheck"
)

// entry 為清單中的單一元件.
type entry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	License string `json:"license"`
	Text    string `json:"text"`
}

// group 依直接引用與間接引用分開存放元件.
type group struct {
	Direct   []entry `json:"direct"`
	Indirect []entry `json:"indirect"`
}

// output 為資料檔的頂層結構.
type output struct {
	Go    group   `json:"go"`
	Npm   group   `json:"npm"`
	Other []entry `json:"other"`
}

// target 為一個實際建置的 Go 目標, 其環境須與 build.sh 的建置設定一致.
type target struct {
	pkg  string
	goos string
	arch string
	cgo  string
}

var targets = []target{
	{"./cmd/cli", "linux", "amd64", "0"},
	{"./cmd/gui", "darwin", "arm64", "1"},
	{"./cmd/gui", "windows", "amd64", "0"},
}

// buildTags 須與 build.sh 傳給 go build 的 -tags 一致.
const buildTags = "production"

// licenseNameRe 比對常見的授權檔名.
var licenseNameRe = regexp.MustCompile(`(?i)^(LICEN[SC]E|COPYING|COPYRIGHT)([.\-].*)?$`)

// noticeNameRe 比對 NOTICE 檔名.
var noticeNameRe = regexp.MustCompile(`(?i)^NOTICE([.\-].*)?$`)

func main() {
	root := flag.String("root", "", "project root directory")
	npmList := flag.String("npm", "", "list of bundled npm packages written by the Vite build")
	flag.Parse()
	if *root == "" || *npmList == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: licgen -root <dir> -npm <file>")
		os.Exit(2)
	}

	var out output
	var err error
	if out.Go, err = collectGo(*root); err != nil {
		fail(err)
	}
	if out.Npm, err = collectNpm(*root, *npmList); err != nil {
		fail(err)
	}
	if out.Other, err = collectOther(*root); err != nil {
		fail(err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fail(err)
	}
	os.Stdout.Write(buf.Bytes())
}

// fail 輸出錯誤訊息後以非零碼結束.
func fail(err error) {
	fmt.Fprintf(os.Stderr, "licgen: %v\n", err)
	os.Exit(1)
}

// goModule 為 go list -json 輸出中的 Module 欄位.
type goModule struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *goModule
}

// collectGo 列出所有建置目標實際編譯進執行檔的 module 聯集並讀取其授權.
func collectGo(root string) (group, error) {
	modules := map[string]goModule{}
	for _, t := range targets {
		cmd := exec.Command("go", "list", "-e", "-deps", "-tags", buildTags, "-json=Module", t.pkg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.arch, "CGO_ENABLED="+t.cgo)
		cmd.Stderr = os.Stderr
		data, err := cmd.Output()
		if err != nil {
			return group{}, fmt.Errorf("go list %s (%s/%s): %w", t.pkg, t.goos, t.arch, err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p struct{ Module *goModule }
			if err := dec.Decode(&p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return group{}, fmt.Errorf("parse go list output: %w", err)
			}
			if p.Module == nil || p.Module.Main {
				continue
			}
			m := *p.Module
			if m.Replace != nil {
				m.Dir = m.Replace.Dir
				m.Version = m.Replace.Version
			}
			modules[m.Path] = m
		}
	}

	direct, err := goDirect(root)
	if err != nil {
		return group{}, err
	}

	g := group{Direct: []entry{}, Indirect: []entry{}}
	for _, m := range modules {
		text, err := licenseText(m.Dir)
		if err != nil {
			return group{}, fmt.Errorf("go module %s: %w", m.Path, err)
		}
		e := entry{
			Name:    m.Path,
			Version: m.Version,
			URL:     "https://pkg.go.dev/" + m.Path,
			License: classify(text),
			Text:    text,
		}
		if direct[m.Path] {
			g.Direct = append(g.Direct, e)
		} else {
			g.Indirect = append(g.Indirect, e)
		}
	}
	sortEntries(g.Direct)
	sortEntries(g.Indirect)
	return g, nil
}

// goDirect 回傳 go.mod 的 require 中未標為 indirect 的 module 路徑集合.
func goDirect(root string) (map[string]bool, error) {
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go mod edit -json: %w", err)
	}
	var mod struct {
		Require []struct {
			Path     string
			Indirect bool
		}
	}
	if err := json.Unmarshal(data, &mod); err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	direct := map[string]bool{}
	for _, r := range mod.Require {
		if !r.Indirect {
			direct[r.Path] = true
		}
	}
	return direct, nil
}

// packageJSON 為 package.json 中本工具使用的欄位.
type packageJSON struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	License      json.RawMessage   `json:"license"`
	Dependencies map[string]string `json:"dependencies"`
}

// readPackageJSON 讀取 dir 內的 package.json.
func readPackageJSON(dir string) (packageJSON, error) {
	var p packageJSON
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(data, &p)
	return p, err
}

// spdxOf 取出 package.json 的 license 欄位, 支援字串與舊式 {"type": ...} 物件.
func spdxOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct{ Type string }
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Type
	}
	return ""
}

// collectNpm 讀取 Vite 寫出的 npm 套件清單並讀取各套件的授權.
func collectNpm(root, listPath string) (group, error) {
	top, err := readPackageJSON(filepath.Join(root, "frontend"))
	if err != nil {
		return group{}, fmt.Errorf("frontend/package.json: %w", err)
	}
	f, err := os.Open(listPath)
	if err != nil {
		return group{}, err
	}
	defer f.Close()

	g := group{Direct: []entry{}, Indirect: []entry{}}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		name, dir, ok := strings.Cut(line, "\t")
		if !ok {
			return group{}, fmt.Errorf("malformed line in %s: %q", listPath, line)
		}
		p, err := readPackageJSON(dir)
		if err != nil {
			return group{}, fmt.Errorf("npm package %s: %w", name, err)
		}
		text, err := licenseText(dir)
		if errors.Is(err, errNoLicense) {
			// 套件未附授權檔時改讀專案維護的授權檔.
			text, err = licenseText(filepath.Join(root, "licenses", "npm", filepath.FromSlash(name)))
		}
		if err != nil {
			return group{}, fmt.Errorf("npm package %s: %w", name, err)
		}
		license := spdxOf(p.License)
		if license == "" {
			license = classify(text)
		}
		e := entry{
			Name:    name,
			Version: p.Version,
			URL:     "https://www.npmjs.com/package/" + name,
			License: license,
			Text:    text,
		}
		if _, ok := top.Dependencies[name]; ok {
			g.Direct = append(g.Direct, e)
		} else {
			g.Indirect = append(g.Indirect, e)
		}
	}
	if err := sc.Err(); err != nil {
		return group{}, err
	}
	sortEntries(g.Direct)
	sortEntries(g.Indirect)
	return g, nil
}

// otherMaterial 為 licenses/other-materials.json 中的單筆素材. File 為授權條文
// 檔案相對於 licenses/ 的路徑.
type otherMaterial struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	License string `json:"license"`
	File    string `json:"file"`
}

// collectOther 讀取人工維護的其他素材清單.
func collectOther(root string) ([]entry, error) {
	dir := filepath.Join(root, "licenses")
	data, err := os.ReadFile(filepath.Join(dir, "other-materials.json"))
	if err != nil {
		return nil, err
	}
	var items []otherMaterial
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse licenses/other-materials.json: %w", err)
	}
	entries := []entry{}
	for _, it := range items {
		if it.Name == "" || it.License == "" || it.File == "" {
			return nil, fmt.Errorf("licenses/other-materials.json: entry %q must provide name, license and file", it.Name)
		}
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(it.File)))
		if err != nil {
			return nil, fmt.Errorf("material %s: %w", it.Name, err)
		}
		entries = append(entries, entry{
			Name:    it.Name,
			Version: it.Version,
			URL:     it.URL,
			License: it.License,
			Text:    strings.TrimRight(string(body), "\n"),
		})
	}
	sortEntries(entries)
	return entries, nil
}

var errNoLicense = errors.New("no license file found")

// licenseText 讀取 dir 頂層的授權檔, 有 NOTICE 檔時附加於後. 名為 LICENSE 的
// 檔案優先, 其次依檔名的字典序.
func licenseText(dir string) (string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoLicense
	} else if err != nil {
		return "", err
	}
	var licenses, notices []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		switch {
		case licenseNameRe.MatchString(e.Name()):
			licenses = append(licenses, e.Name())
		case noticeNameRe.MatchString(e.Name()):
			notices = append(notices, e.Name())
		}
	}
	if len(licenses) == 0 {
		return "", errNoLicense
	}
	sort.Slice(licenses, func(i, j int) bool {
		bi := strings.EqualFold(strings.SplitN(licenses[i], ".", 2)[0], "LICENSE")
		bj := strings.EqualFold(strings.SplitN(licenses[j], ".", 2)[0], "LICENSE")
		if bi != bj {
			return bi
		}
		return licenses[i] < licenses[j]
	})
	sort.Strings(notices)

	body, err := os.ReadFile(filepath.Join(dir, licenses[0]))
	if err != nil {
		return "", err
	}
	text := strings.TrimRight(string(body), "\n")
	for _, n := range notices {
		nb, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "", err
		}
		if s := strings.TrimRight(string(nb), "\n"); strings.TrimSpace(s) != "" {
			text += "\n\n--- NOTICE ---\n\n" + s
		}
	}
	return text, nil
}

// classify 以 licensecheck 判定授權種類, 取涵蓋字元數最多的單一授權, 無法判定
// 時回傳空字串.
func classify(text string) string {
	cov := licensecheck.Scan([]byte(text))
	span := map[string]int{}
	for _, m := range cov.Match {
		span[m.ID] += m.End - m.Start
	}
	best, bestSpan := "", -1
	for id, s := range span {
		if s > bestSpan || (s == bestSpan && id < best) {
			best, bestSpan = id, s
		}
	}
	return best
}

// sortEntries 依名稱 (不分大小寫, 相同時再依原字串) 排序.
func sortEntries(es []entry) {
	sort.Slice(es, func(i, j int) bool {
		li, lj := strings.ToLower(es[i].Name), strings.ToLower(es[j].Name)
		if li != lj {
			return li < lj
		}
		return es[i].Name < es[j].Name
	})
}

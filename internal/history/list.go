package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gpumon/internal/recording"
)

const fileExt = ".gpurec"

// 刪除被拒絕的原因.
var (
	// ErrActiveFile 表示該檔案正由連線中的主機寫入, 不可刪除.
	ErrActiveFile = errors.New("history: file is being recorded")
	// ErrOutsideHistory 表示路徑不是記錄檔目錄內的 .gpurec 檔案.
	ErrOutsideHistory = errors.New("history: path is not a recording file inside the history directory")
)

// FileInfo 為清單中的一個記錄檔.
type FileInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Start 與 End 為檔案內第一筆與最後一筆記錄的時刻 (微秒); 檔案尚無記錄時 HasData 為假, 兩者皆取說明區的 TWall0.
	Start   int64 `json:"start"`
	End     int64 `json:"end"`
	HasData bool  `json:"has_data"`
	Size    int64 `json:"size"`
	NumGPUs int   `json:"num_gpus"`
	// Active 表示該檔案為連線中, 不可刪除.
	Active bool `json:"active"`
}

// HostGroup 為同一台主機的記錄檔, 依起始時刻由早到晚排列. TotalSize 為這些檔案的大小總計.
type HostGroup struct {
	Host      string     `json:"host"`
	Files     []FileInfo `json:"files"`
	TotalSize int64      `json:"total_size"`
}

// SkippedFile 為無法開啟或格式不符而未列入清單的檔案.
type SkippedFile struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Error string `json:"error"`
}

// Listing 為記錄檔清單. 無法讀取的檔案不使整個清單失敗, 改列於 Skipped, 且不計入 TotalSize.
type Listing struct {
	Hosts   []HostGroup   `json:"hosts"`
	Skipped []SkippedFile `json:"skipped"`
}

func historyRoot(dataDir string) string { return filepath.Join(dataDir, "history") }

// List 列出 `<dataDir>/history/` 下所有記錄檔, 依說明區的主機分組, 主機依名稱排序.
// 起訖時刻取自檔案內容. active 為連線中的記錄檔路徑, 對應的檔案標示為 Active.
// 記錄檔目錄不存在時回傳空清單.
func List(dataDir string, active []string) (*Listing, error) {
	out := &Listing{Hosts: []HostGroup{}, Skipped: []SkippedFile{}}
	dirs, err := os.ReadDir(historyRoot(dataDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	groups := map[string]*HostGroup{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(historyRoot(dataDir), d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			out.Skipped = append(out.Skipped, SkippedFile{Path: dir, Error: err.Error()})
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || filepath.Ext(e.Name()) != fileExt {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, host, err := inspect(path)
			if err != nil {
				sk := SkippedFile{Path: path, Error: err.Error()}
				if st, serr := e.Info(); serr == nil {
					sk.Size = st.Size()
				}
				out.Skipped = append(out.Skipped, sk)
				continue
			}
			info.Active = isActive(path, active)
			g := groups[host]
			if g == nil {
				g = &HostGroup{Host: host, Files: []FileInfo{}}
				groups[host] = g
			}
			g.Files = append(g.Files, info)
			g.TotalSize += info.Size
		}
	}
	for _, g := range groups {
		sort.Slice(g.Files, func(i, j int) bool {
			a, b := g.Files[i], g.Files[j]
			if a.Start != b.Start {
				return a.Start < b.Start
			}
			return a.Path < b.Path
		})
		out.Hosts = append(out.Hosts, *g)
	}
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].Host < out.Hosts[j].Host })
	sort.Slice(out.Skipped, func(i, j int) bool { return out.Skipped[i].Path < out.Skipped[j].Path })
	return out, nil
}

func inspect(path string) (FileInfo, string, error) {
	f, err := recording.Open(path)
	if err != nil {
		return FileInfo{}, "", err
	}
	defer f.Close()
	st, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, "", err
	}
	hdr := f.Header()
	info := FileInfo{
		Path:    path,
		Name:    filepath.Base(path),
		Start:   hdr.TWall0,
		End:     hdr.TWall0,
		Size:    st.Size(),
		NumGPUs: f.NumGPUs(),
	}
	first, last, ok, err := f.TimeRange()
	if err != nil {
		return FileInfo{}, "", err
	}
	if ok {
		info.Start, info.End, info.HasData = first, last, true
	}
	return info, hdr.Host, nil
}

func isActive(path string, active []string) bool {
	if len(active) == 0 {
		return false
	}
	st, err := os.Stat(path)
	for _, a := range active {
		if filepath.Clean(a) == filepath.Clean(path) {
			return true
		}
		if err == nil {
			if ast, aerr := os.Stat(a); aerr == nil && os.SameFile(st, ast) {
				return true
			}
		}
	}
	return false
}

// Delete 刪除一個記錄檔. 路徑必須是 `<dataDir>/history/<主機目錄>/` 內副檔名為 .gpurec 的一般檔案
// (不跟隨符號連結), 否則回傳 ErrOutsideHistory; 路徑在 active 之中時回傳 ErrActiveFile. 兩種情形皆不刪除任何東西.
func Delete(dataDir, path string, active []string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(historyRoot(dataDir))
	if err != nil {
		return err
	}
	if filepath.Ext(abs) != fileExt || filepath.Base(abs) == fileExt || filepath.Dir(filepath.Dir(abs)) != root {
		return fmt.Errorf("%w: %s", ErrOutsideHistory, path)
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrOutsideHistory, path)
	}
	// 主機目錄本身若是符號連結, 檔案實際位於記錄檔目錄之外.
	realDir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if filepath.Dir(realDir) != realRoot {
		return fmt.Errorf("%w: %s", ErrOutsideHistory, path)
	}
	if isActive(abs, active) {
		return fmt.Errorf("%w: %s", ErrActiveFile, path)
	}
	return os.Remove(abs)
}

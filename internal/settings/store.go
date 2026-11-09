package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Store 為設定的存取物件. 所有方法皆可由多個 goroutine 同時呼叫,
// 每次變更成功即寫入磁碟; 寫入失敗時記憶體中的內容維持不變.
type Store struct {
	mu   sync.RWMutex
	path string
	data Data
}

// Open 讀取 dir 下的設定檔並回傳 Store, dir 不需已存在, 首次寫入時才建立.
//
// 回傳的 Store 一律可用. 檔案不存在時內容為預設值且 error 為 nil. 檔案無法
// 讀取或讀取過程有異常時, error 描述原因, Store 內容為預設值或盡可能
// 解析出的內容. 檔案無法解析時, 原檔更名為 settings.json.corrupt-<時間>
// 保留, 不刪除.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName)}
	var err error
	s.data, err = load(s.path)
	return s, err
}

// Path 回傳設定檔的路徑.
func (s *Store) Path() string {
	return s.path
}

// Snapshot 回傳目前完整設定的副本.
func (s *Store) Snapshot() Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.clone()
}

func load(path string) (Data, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("read settings: %w", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return Default(), quarantine(path, "corrupt", err)
	}

	d := Default()
	var typeErr *json.UnmarshalTypeError
	if err := json.Unmarshal(raw, &d); err != nil && !errors.As(err, &typeErr) {
		return Default(), quarantine(path, "corrupt", err)
	}

	// 舊版設定檔沒有 monitor_range 時, 以 shared_range 的值承接. 舊版的 view_mode
	// 與各主機的 range 不在 Data 結構內, 讀入時自然忽略.
	if _, ok := top["monitor_range"]; !ok {
		var legacy struct {
			SharedRange TimeRange `json:"shared_range"`
		}
		if json.Unmarshal(raw, &legacy) == nil && validMonitorRange(legacy.SharedRange) {
			d.MonitorRange = legacy.SharedRange
		}
	}

	var notice error
	if d.Version > CurrentVersion {
		notice = quarantine(path, fmt.Sprintf("v%d", d.Version),
			fmt.Errorf("format version %d is newer than supported version %d", d.Version, CurrentVersion))
	}
	d.sanitize()
	return d, notice
}

// quarantine 把無法使用的設定檔更名保留, 回傳說明處理結果的錯誤.
func quarantine(path, tag string, cause error) error {
	stamp := time.Now().Format("20060102-150405")
	dst := fmt.Sprintf("%s.%s-%s", path, tag, stamp)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		dst = fmt.Sprintf("%s.%s-%s-%d", path, tag, stamp, i)
	}
	if err := os.Rename(path, dst); err != nil {
		return fmt.Errorf("settings file unusable (%v) and could not be preserved: %w", cause, err)
	}
	return fmt.Errorf("settings file unusable (%v), moved to %s", cause, dst)
}

// save 以先寫暫存檔再更名的方式寫入, 程式於中途異常結束時不會留下不完整的設定檔.
func save(path string, d Data) error {
	buf, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	buf = append(buf, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write settings: %w", err)
	}
	if _, err := tmp.Write(buf); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace settings file: %w", err)
	}
	return nil
}

// update 在副本上套用變更, 寫入成功後才取代記憶體中的內容.
func (s *Store) update(fn func(d *Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.data.clone()
	if err := fn(&c); err != nil {
		return err
	}
	if err := save(s.path, c); err != nil {
		return err
	}
	s.data = c
	return nil
}

func findHost(d *Data, id string) int {
	for i, h := range d.Hosts {
		if h.ID() == id {
			return i
		}
	}
	return -1
}

func findDuplicate(d *Data, c HostConn, skip int) bool {
	for i, h := range d.Hosts {
		if i != skip && h.key() == c.key() {
			return true
		}
	}
	return false
}

// Hosts 回傳主機清單的副本, 依新增的先後排列.
func (s *Store) Hosts() []Host {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Host{}, s.data.Hosts...)
}

// Host 回傳指定識別字串的主機.
func (s *Store) Host(id string) (Host, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i := findHost(&s.data, id); i >= 0 {
		return s.data.Hosts[i], true
	}
	return Host{}, false
}

// AddHost 於清單末端新增主機,
// 已有相同識別的主機時回傳 ErrDuplicateHost.
func (s *Store) AddHost(c HostConn) error {
	c, err := normalizeConn(c)
	if err != nil {
		return err
	}
	return s.update(func(d *Data) error {
		if findDuplicate(d, c, -1) {
			return fmt.Errorf("%w: %s", ErrDuplicateHost, c.ID())
		}
		d.Hosts = append(d.Hosts, Host{HostConn: c})
		return nil
	})
}

// UpdateHost 編輯識別字串為 id 的主機. 只更換表單的四個欄位, 排列位置與
// 收合狀態皆保留. 新識別與其他主機重複時回傳
// ErrDuplicateHost.
func (s *Store) UpdateHost(id string, c HostConn) error {
	c, err := normalizeConn(c)
	if err != nil {
		return err
	}
	return s.update(func(d *Data) error {
		i := findHost(d, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrHostNotFound, id)
		}
		if findDuplicate(d, c, i) {
			return fmt.Errorf("%w: %s", ErrDuplicateHost, c.ID())
		}
		d.Hosts[i].HostConn = c
		if newID := c.ID(); newID != id {
			if ids, ok := d.CollapsedGPUs[id]; ok {
				delete(d.CollapsedGPUs, id)
				d.CollapsedGPUs[newID] = ids
			}
			for j, p := range d.CollapsedProcs {
				if p == id {
					d.CollapsedProcs[j] = newID
				}
			}
			d.CollapsedProcs = dedupStrings(d.CollapsedProcs)
		}
		return nil
	})
}

// RemoveHost 刪除主機及其收合狀態, 其餘主機的順序不變.
func (s *Store) RemoveHost(id string) error {
	return s.update(func(d *Data) error {
		i := findHost(d, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrHostNotFound, id)
		}
		d.Hosts = append(d.Hosts[:i], d.Hosts[i+1:]...)
		delete(d.CollapsedGPUs, id)
		d.CollapsedProcs = removeString(d.CollapsedProcs, id)
		return nil
	})
}

// Interval 回傳所有主機共用的取樣間隔, 單位為毫秒.
func (s *Store) Interval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.IntervalMS
}

// SetInterval 設定所有主機共用的取樣間隔, 單位為毫秒, 範圍 200 至 10000.
func (s *Store) SetInterval(ms int) error {
	if ms < MinIntervalMS || ms > MaxIntervalMS {
		return invalid("interval %d ms out of range %d-%d", ms, MinIntervalMS, MaxIntervalMS)
	}
	return s.update(func(d *Data) error { d.IntervalMS = ms; return nil })
}

// SetWindow 記住主視窗的大小與位置.
func (s *Store) SetWindow(w Window) error {
	if w.Width < minWindowSize || w.Width > maxWindowSize ||
		w.Height < minWindowSize || w.Height > maxWindowSize {
		return invalid("window size %dx%d out of range", w.Width, w.Height)
	}
	if w.HasPosition && (w.X < -maxWindowCoord || w.X > maxWindowCoord ||
		w.Y < -maxWindowCoord || w.Y > maxWindowCoord) {
		return invalid("window position (%d,%d) out of range", w.X, w.Y)
	}
	if !w.HasPosition {
		w.X, w.Y = 0, 0
	}
	return s.update(func(d *Data) error {
		d.Window = w
		return nil
	})
}

// SetAppearance 設定外觀.
func (s *Store) SetAppearance(v Appearance) error {
	if !validAppearance(v) {
		return invalid("appearance %q", v)
	}
	return s.update(func(d *Data) error { d.Appearance = v; return nil })
}

// SetLanguage 設定介面語言.
func (s *Store) SetLanguage(v Language) error {
	if !validLanguage(v) {
		return invalid("language %q", v)
	}
	return s.update(func(d *Data) error { d.Language = v; return nil })
}

// SetMonitorRange 設定監看畫面的顯示範圍, 所有主機共用.
func (s *Store) SetMonitorRange(v TimeRange) error {
	if !validMonitorRange(v) {
		return invalid("time range %q", v)
	}
	return s.update(func(d *Data) error { d.MonitorRange = v; return nil })
}

// SetHistoryRange 設定歷史畫面的顯示範圍, 可為 RangeAll.
func (s *Store) SetHistoryRange(v TimeRange) error {
	if !validHistoryRange(v) {
		return invalid("time range %q", v)
	}
	return s.update(func(d *Data) error { d.HistoryRange = v; return nil })
}

// SetCurves 設定曲線的選擇. 曲線不得重複, 順序依傳入者保留.
func (s *Store) SetCurves(curves []Curve) error {
	seen := map[Curve]bool{}
	for _, c := range curves {
		if !validCurve(c) {
			return invalid("curve %q", c)
		}
		if seen[c] {
			return invalid("duplicate curve %q", c)
		}
		seen[c] = true
	}
	if len(curves) > MaxCurves {
		return invalid("%d curves exceeds limit %d", len(curves), MaxCurves)
	}
	return s.update(func(d *Data) error {
		d.Curves = append([]Curve{}, curves...)
		return nil
	})
}

// GPUCollapsed 回傳指定主機的指定 GPU 橫列是否已收合.
func (s *Store) GPUCollapsed(hostID string, gpuID int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, id := range s.data.CollapsedGPUs[hostID] {
		if id == gpuID {
			return true
		}
	}
	return false
}

// SetGPUCollapsed 記住指定主機的指定 GPU 橫列是否收合. gpuID 為 devices
// 訊息的 id.
func (s *Store) SetGPUCollapsed(hostID string, gpuID int, collapsed bool) error {
	if hostID == "" || gpuID < 0 {
		return invalid("host %q gpu %d", hostID, gpuID)
	}
	return s.update(func(d *Data) error {
		ids := d.CollapsedGPUs[hostID]
		pos := -1
		for i, id := range ids {
			if id == gpuID {
				pos = i
			}
		}
		switch {
		case collapsed && pos < 0:
			ids = append(ids, gpuID)
			sort.Ints(ids)
		case !collapsed && pos >= 0:
			ids = append(ids[:pos], ids[pos+1:]...)
		}
		if len(ids) == 0 {
			delete(d.CollapsedGPUs, hostID)
		} else {
			d.CollapsedGPUs[hostID] = ids
		}
		return nil
	})
}

// ProcsCollapsed 回傳指定主機的程序卡片是否已收合.
func (s *Store) ProcsCollapsed(hostID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, id := range s.data.CollapsedProcs {
		if id == hostID {
			return true
		}
	}
	return false
}

// SetProcsCollapsed 記住指定主機的程序卡片是否收合.
func (s *Store) SetProcsCollapsed(hostID string, collapsed bool) error {
	if hostID == "" {
		return invalid("host %q", hostID)
	}
	return s.update(func(d *Data) error {
		if collapsed {
			d.CollapsedProcs = dedupStrings(append(d.CollapsedProcs, hostID))
		} else {
			d.CollapsedProcs = removeString(d.CollapsedProcs, hostID)
		}
		return nil
	})
}

// removeString 回傳移除所有 v 之後的新切片.
func removeString(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// dedupStrings 回傳去除重複後的新切片, 保留首次出現的順序.
func dedupStrings(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, x := range list {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

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

	applyLegacyRanges(&d, top, raw)

	var notice error
	if d.Version > CurrentVersion {
		notice = quarantine(path, fmt.Sprintf("v%d", d.Version),
			fmt.Errorf("format version %d is newer than supported version %d", d.Version, CurrentVersion))
	}
	d.sanitize()
	return d, notice
}

// legacyRangeMS 為舊版以選項表示的顯示範圍及其長度, 單位為毫秒. 歷史畫面另有
// "all", 由 applyLegacyRanges 處理.
var legacyRangeMS = map[string]int64{
	"1m": 60_000, "5m": 300_000, "15m": 900_000, "30m": 1_800_000, "60m": 3_600_000,
	"3h": 10_800_000, "6h": 21_600_000, "12h": 43_200_000, "24h": 86_400_000,
}

// applyLegacyRanges 以舊版設定檔的顯示範圍承接新版的顯示範圍長度. 監看畫面的長度
// 依序取 monitor_span_ms, monitor_range, 更早版本的 shared_range; 歷史畫面沒有
// history_span_ms 而有 history_range 時換算其長度, 其值為 "all" 時處於顯示全部
// 狀態, 長度維持預設值. 舊版成員不在 Data 結構內, 下次寫入後即不再保留.
func applyLegacyRanges(d *Data, top map[string]json.RawMessage, raw []byte) {
	var legacy struct {
		SharedRange  string `json:"shared_range"`
		MonitorRange string `json:"monitor_range"`
		HistoryRange string `json:"history_range"`
	}
	// 型別不符的舊版成員視為不存在, 其餘成員照常取用.
	_ = json.Unmarshal(raw, &legacy)
	if _, ok := top["monitor_span_ms"]; !ok {
		src := legacy.MonitorRange
		if _, has := top["monitor_range"]; !has {
			src = legacy.SharedRange
		}
		if ms, ok := legacyRangeMS[src]; ok {
			d.MonitorSpanMS = ms
		}
	}
	if _, ok := top["history_span_ms"]; !ok {
		if legacy.HistoryRange == "all" {
			if _, has := top["history_all"]; !has {
				d.HistoryAll = true
			}
		} else if ms, ok := legacyRangeMS[legacy.HistoryRange]; ok {
			d.HistorySpanMS = ms
		}
	}
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

// UpdateHost 編輯識別字串為 id 的主機. 只更換表單的四個欄位, 排列位置, 收合狀態
// 與程序卡片的排序方式及過濾條件皆保留. 新識別與其他主機重複時回傳
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
			for j, p := range d.CollapsedMaxima {
				if p == id {
					d.CollapsedMaxima[j] = newID
				}
			}
			d.CollapsedMaxima = dedupStrings(d.CollapsedMaxima)
			if v, ok := d.ProcSorts[id]; ok {
				delete(d.ProcSorts, id)
				d.ProcSorts[newID] = v
			}
			if v, ok := d.ProcFilters[id]; ok {
				delete(d.ProcFilters, id)
				d.ProcFilters[newID] = v
			}
		}
		return nil
	})
}

// RemoveHost 刪除主機及其收合狀態與程序卡片的排序方式及過濾條件, 其餘主機的順序
// 不變.
func (s *Store) RemoveHost(id string) error {
	return s.update(func(d *Data) error {
		i := findHost(d, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrHostNotFound, id)
		}
		d.Hosts = append(d.Hosts[:i], d.Hosts[i+1:]...)
		delete(d.CollapsedGPUs, id)
		d.CollapsedProcs = removeString(d.CollapsedProcs, id)
		d.CollapsedMaxima = removeString(d.CollapsedMaxima, id)
		delete(d.ProcSorts, id)
		delete(d.ProcFilters, id)
		return nil
	})
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

// SetSizeUnits 設定容量的顯示單位制.
func (s *Store) SetSizeUnits(v SizeUnits) error {
	if !validSizeUnits(v) {
		return invalid("size units %q", v)
	}
	return s.update(func(d *Data) error { d.SizeUnits = v; return nil })
}

// SetMonitorSpan 設定監看畫面的顯示範圍長度, 所有主機共用, 單位為毫秒,
// 範圍 MinSpanMS 至 MaxMonitorSpanMS.
func (s *Store) SetMonitorSpan(ms int64) error {
	if ms < MinSpanMS || ms > MaxMonitorSpanMS {
		return invalid("monitor span %d ms out of range %d-%d", ms, MinSpanMS, MaxMonitorSpanMS)
	}
	return s.update(func(d *Data) error { d.MonitorSpanMS = ms; return nil })
}

// SetHistorySpan 設定歷史畫面的顯示範圍長度 (毫秒, 不短於 MinSpanMS) 與是否處於
// 顯示全部狀態.
func (s *Store) SetHistorySpan(ms int64, all bool) error {
	if ms < MinSpanMS {
		return invalid("history span %d ms shorter than %d", ms, MinSpanMS)
	}
	return s.update(func(d *Data) error {
		d.HistorySpanMS, d.HistoryAll = ms, all
		return nil
	})
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

// MaximaCollapsed 回傳指定主機的最大值卡片是否已收合.
func (s *Store) MaximaCollapsed(hostID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, id := range s.data.CollapsedMaxima {
		if id == hostID {
			return true
		}
	}
	return false
}

// SetMaximaCollapsed 記住指定主機的最大值卡片是否收合.
func (s *Store) SetMaximaCollapsed(hostID string, collapsed bool) error {
	if hostID == "" {
		return invalid("host %q", hostID)
	}
	return s.update(func(d *Data) error {
		if collapsed {
			d.CollapsedMaxima = dedupStrings(append(d.CollapsedMaxima, hostID))
		} else {
			d.CollapsedMaxima = removeString(d.CollapsedMaxima, hostID)
		}
		return nil
	})
}

// ProcSort 回傳指定主機的程序卡片排序方式, 未設定時為預設排序方式.
func (s *Store) ProcSort(hostID string) ProcSort {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.data.ProcSorts[hostID]; ok {
		return v
	}
	return DefaultProcSort()
}

// SetProcSort 記住指定主機的程序卡片排序方式. 欄位或方向不合法時回傳
// ErrInvalidValue.
func (s *Store) SetProcSort(hostID string, v ProcSort) error {
	if hostID == "" || !validProcColumn(v.Column) || !validSortOrder(v.Order) {
		return invalid("host %q sort %q %q", hostID, v.Column, v.Order)
	}
	return s.update(func(d *Data) error {
		if v == DefaultProcSort() {
			delete(d.ProcSorts, hostID)
		} else {
			d.ProcSorts[hostID] = v
		}
		return nil
	})
}

// ProcFilter 回傳指定主機的程序卡片過濾條件, 未設定時各項皆為空.
func (s *Store) ProcFilter(hostID string) ProcFilter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.data.ProcFilters[hostID]
	v.GPUs = append([]int{}, v.GPUs...)
	return v
}

// SetProcFilter 記住指定主機的程序卡片過濾條件. 文字去除前後空白, GPU 編號去除
// 重複並排序; 含負數的 GPU 編號時回傳 ErrInvalidValue. 沒有任何條件時清除該主機
// 的記錄.
func (s *Store) SetProcFilter(hostID string, f ProcFilter) error {
	if hostID == "" {
		return invalid("host %q", hostID)
	}
	for _, id := range f.GPUs {
		if id < 0 {
			return invalid("gpu %d", id)
		}
	}
	f = f.normalize()
	return s.update(func(d *Data) error {
		if f.empty() {
			delete(d.ProcFilters, hostID)
		} else {
			d.ProcFilters[hostID] = f
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

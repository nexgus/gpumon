// Package settings 負責本機 GUI 設定的儲存與讀取, 含主機清單與各項使用者偏好.
//
// 設定存為應用程式資料目錄下的單一 JSON 檔案. 設定檔不含任何密碼, 金鑰密碼
// 或其他憑證, 資料結構亦不提供此類欄位. 本套件可供多個 goroutine 同時使用.
package settings

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// FileName 為設定檔的檔名.
const FileName = "settings.json"

// CurrentVersion 為本套件寫出的設定檔格式版本.
const CurrentVersion = 1

// 顯示範圍長度的預設值與限制, 單位為毫秒. 監看畫面的長度為 MinSpanMS 至
// MaxMonitorSpanMS; 歷史畫面的長度於設定檔中只限制不短於 MinSpanMS, 上限依開啟的
// 記錄檔而定, 由畫面自行限制.
const (
	DefaultSpanMS    int64 = 5 * 60 * 1000
	MinSpanMS        int64 = 60 * 1000
	MaxMonitorSpanMS int64 = 24 * 60 * 60 * 1000
)

// 主視窗大小的預設值與接受的範圍, 單位為像素.
const (
	DefaultWindowWidth  = 1280
	DefaultWindowHeight = 800
	minWindowSize       = 200
	maxWindowSize       = 20000
	maxWindowCoord      = 100000
)

// Appearance 為外觀設定.
type Appearance string

// 外觀的可用值.
const (
	AppearanceSystem Appearance = "system"
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
)

// Language 為介面語言.
type Language string

// 介面語言的可用值.
const (
	LanguageEnglish            Language = "en"
	LanguageTraditionalChinese Language = "zh-TW"
)

// SizeUnits 為容量的顯示單位制.
type SizeUnits string

// 容量單位制的可用值. 二進位以 1024 進位, 單位為 KiB, MiB, GiB; 十進位以 1000
// 進位, 單位為 kB, MB, GB.
const (
	SizeUnitsBinary  SizeUnits = "binary"
	SizeUnitsDecimal SizeUnits = "decimal"
)

// Curve 為曲線圖可選的曲線.
type Curve string

// 曲線的可用值.
const (
	CurveUtil     Curve = "util"
	CurveMem      Curve = "mem"
	CurveTemp     Curve = "temp"
	CurveFan      Curve = "fan"
	CurvePower    Curve = "power"
	CurveClockGPU Curve = "clock_gpu"
	CurveClockMem Curve = "clock_mem"
)

// MaxCurves 為同一張圖的曲線數上限, 等於曲線顏色的數目.
const MaxCurves = 7

// 各項可選值的完整清單. 呼叫端不得修改回傳的切片, 故以函數提供副本.

// AllCurves 回傳所有可選的曲線.
func AllCurves() []Curve {
	return []Curve{CurveUtil, CurveMem, CurveTemp, CurveFan, CurvePower,
		CurveClockGPU, CurveClockMem}
}

// DefaultCurves 回傳預設的曲線, 為使用率與記憶體佔比.
func DefaultCurves() []Curve {
	return []Curve{CurveUtil, CurveMem}
}

// ProcColumn 為程序卡片可排序的欄位.
type ProcColumn string

// 程序卡片欄位的可用值, 依序為 GPU, PID, USER, GPU UTIL, GPU MEM 與 COMMAND.
const (
	ProcColumnGPU     ProcColumn = "gpu"
	ProcColumnPID     ProcColumn = "pid"
	ProcColumnUser    ProcColumn = "user"
	ProcColumnUtil    ProcColumn = "util"
	ProcColumnMem     ProcColumn = "mem"
	ProcColumnCommand ProcColumn = "command"
)

// SortOrder 為排序方向.
type SortOrder string

// 排序方向的可用值.
const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// ProcSort 為程序卡片的排序方式. 預設為依 GPU 遞增.
type ProcSort struct {
	Column ProcColumn `json:"column"`
	Order  SortOrder  `json:"order"`
}

// DefaultProcSort 回傳程序卡片的預設排序方式.
func DefaultProcSort() ProcSort {
	return ProcSort{Column: ProcColumnGPU, Order: SortAsc}
}

// ProcFilter 為程序卡片的過濾條件, 各項為空時表示未設定. GPUs 為 GPU 編號,
// 由小到大且不重複; User 與 Command 為部分相符的文字, 不含前後空白.
type ProcFilter struct {
	GPUs    []int  `json:"gpus"`
	User    string `json:"user"`
	Command string `json:"command"`
}

// empty 回傳是否沒有任何條件.
func (f ProcFilter) empty() bool {
	return len(f.GPUs) == 0 && f.User == "" && f.Command == ""
}

// normalize 去除文字的前後空白, GPU 編號去除重複並排序, 捨棄負數的編號.
func (f ProcFilter) normalize() ProcFilter {
	f.User = strings.TrimSpace(f.User)
	f.Command = strings.TrimSpace(f.Command)
	f.GPUs = sortedIDs(f.GPUs)
	return f
}

// sortedIDs 回傳 ids 中非負的編號, 去除重複並由小到大排列.
func sortedIDs(ids []int) []int {
	set := map[int]bool{}
	for _, id := range ids {
		if id >= 0 {
			set[id] = true
		}
	}
	list := make([]int, 0, len(set))
	for id := range set {
		list = append(list, id)
	}
	sort.Ints(list)
	return list
}

func validProcColumn(v ProcColumn) bool {
	switch v {
	case ProcColumnGPU, ProcColumnPID, ProcColumnUser, ProcColumnUtil, ProcColumnMem, ProcColumnCommand:
		return true
	}
	return false
}

func validSortOrder(v SortOrder) bool {
	return v == SortAsc || v == SortDesc
}

func validAppearance(v Appearance) bool {
	return v == AppearanceSystem || v == AppearanceLight || v == AppearanceDark
}

func validLanguage(v Language) bool {
	return v == LanguageEnglish || v == LanguageTraditionalChinese
}

func validSizeUnits(v SizeUnits) bool {
	return v == SizeUnitsBinary || v == SizeUnitsDecimal
}

func validCurve(v Curve) bool {
	for _, c := range AllCurves() {
		if c == v {
			return true
		}
	}
	return false
}

// Window 為主視窗的大小與位置.
type Window struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	// HasPosition 為 false 時表示尚未記錄位置, X 與 Y 無意義.
	HasPosition bool `json:"has_position"`
	X           int  `json:"x"`
	Y           int  `json:"y"`
}

// HostConn 為主機表單的四個欄位.
type HostConn struct {
	// Addr 為必填, 可為主機名稱, IP 位址或 ~/.ssh/config 的 Host 名稱.
	Addr string `json:"addr"`
	// Port 為 0 時表示留空.
	Port int `json:"port"`
	// User 留空時表示取 ~/.ssh/config 的設定.
	User string `json:"user"`
	// KeyFile 留空時表示取 ~/.ssh/config 的設定或預設金鑰.
	KeyFile string `json:"key_file"`
}

// Host 為一台主機的設定. 舊版設定檔中各主機的 interval_ms 與 range 讀取時略過.
type Host struct {
	HostConn
}

// ID 回傳主機的識別字串. 使用者名稱非空時為 user@addr; 留空時只有位址,
// 因使用者名稱要到連線時才由 ~/.ssh/config 解析.
func (h HostConn) ID() string {
	if h.User == "" {
		return h.Addr
	}
	return h.User + "@" + h.Addr
}

// key 回傳用於判定重複的鍵. 主機名稱不分大小寫, 使用者名稱分大小寫.
func (h HostConn) key() string {
	return h.User + "@" + strings.ToLower(h.Addr)
}

// Data 為完整的設定內容, 亦為設定檔的 JSON 結構.
type Data struct {
	Version int `json:"version"`

	Window     Window     `json:"window"`
	Appearance Appearance `json:"appearance"`
	Language   Language   `json:"language"`
	// SizeUnits 為記憶體與檔案大小的顯示單位制.
	SizeUnits SizeUnits `json:"size_units"`

	// MonitorSpanMS 為監看畫面的顯示範圍長度, 所有主機共用, 單位為毫秒.
	MonitorSpanMS int64 `json:"monitor_span_ms"`
	// HistorySpanMS 為歷史畫面的顯示範圍長度, 單位為毫秒. HistoryAll 為歷史畫面
	// 是否處於顯示全部狀態.
	HistorySpanMS int64 `json:"history_span_ms"`
	HistoryAll    bool  `json:"history_all"`

	Curves []Curve `json:"curves"`

	// Hosts 依新增的先後排列.
	Hosts []Host `json:"hosts"`

	// CollapsedGPUs 以主機識別字串為鍵, 值為已收合的 GPU id, 由小到大.
	CollapsedGPUs map[string][]int `json:"collapsed_gpus"`

	// CollapsedProcs 為程序卡片已收合的主機識別字串, 不含空字串與重複.
	CollapsedProcs []string `json:"collapsed_procs"`

	// ProcSorts 以主機識別字串為鍵, 值為程序卡片的排序方式; 預設排序方式不存放.
	ProcSorts map[string]ProcSort `json:"proc_sorts"`

	// ProcFilters 以主機識別字串為鍵, 值為程序卡片的過濾條件; 沒有任何條件時不存放.
	ProcFilters map[string]ProcFilter `json:"proc_filters"`
}

// Default 回傳預設設定.
func Default() Data {
	return Data{
		Version:        CurrentVersion,
		Window:         Window{Width: DefaultWindowWidth, Height: DefaultWindowHeight},
		Appearance:     AppearanceSystem,
		Language:       LanguageEnglish,
		SizeUnits:      SizeUnitsBinary,
		MonitorSpanMS:  DefaultSpanMS,
		HistorySpanMS:  DefaultSpanMS,
		Curves:         DefaultCurves(),
		Hosts:          []Host{},
		CollapsedGPUs:  map[string][]int{},
		CollapsedProcs: []string{},
		ProcSorts:      map[string]ProcSort{},
		ProcFilters:    map[string]ProcFilter{},
	}
}

func (d Data) clone() Data {
	c := d
	c.Curves = append([]Curve{}, d.Curves...)
	c.Hosts = append([]Host{}, d.Hosts...)
	c.CollapsedGPUs = make(map[string][]int, len(d.CollapsedGPUs))
	for k, v := range d.CollapsedGPUs {
		c.CollapsedGPUs[k] = append([]int{}, v...)
	}
	c.CollapsedProcs = append([]string{}, d.CollapsedProcs...)
	c.ProcSorts = make(map[string]ProcSort, len(d.ProcSorts))
	for k, v := range d.ProcSorts {
		c.ProcSorts[k] = v
	}
	c.ProcFilters = make(map[string]ProcFilter, len(d.ProcFilters))
	for k, v := range d.ProcFilters {
		v.GPUs = append([]int{}, v.GPUs...)
		c.ProcFilters[k] = v
	}
	return c
}

func clampInt[T int | int64](v, lo, hi T) T {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sanitize 把讀入的內容修正為合法值. 數值超出範圍時取最接近的合法值,
// 列舉值不合法時改用預設值, 不合法的主機與曲線予以捨棄.
func (d *Data) sanitize() {
	def := Default()
	d.Version = CurrentVersion

	if d.Window.Width <= 0 || d.Window.Height <= 0 {
		d.Window.Width, d.Window.Height = def.Window.Width, def.Window.Height
	}
	d.Window.Width = clampInt(d.Window.Width, minWindowSize, maxWindowSize)
	d.Window.Height = clampInt(d.Window.Height, minWindowSize, maxWindowSize)
	if !d.Window.HasPosition ||
		d.Window.X < -maxWindowCoord || d.Window.X > maxWindowCoord ||
		d.Window.Y < -maxWindowCoord || d.Window.Y > maxWindowCoord {
		d.Window.HasPosition, d.Window.X, d.Window.Y = false, 0, 0
	}

	if !validAppearance(d.Appearance) {
		d.Appearance = def.Appearance
	}
	if !validLanguage(d.Language) {
		d.Language = def.Language
	}
	if !validSizeUnits(d.SizeUnits) {
		d.SizeUnits = def.SizeUnits
	}
	d.MonitorSpanMS = clampInt(d.MonitorSpanMS, MinSpanMS, MaxMonitorSpanMS)
	d.HistorySpanMS = max(d.HistorySpanMS, MinSpanMS)

	if d.Curves == nil {
		d.Curves = DefaultCurves()
	} else {
		seen := map[Curve]bool{}
		curves := []Curve{}
		for _, c := range d.Curves {
			if validCurve(c) && !seen[c] {
				seen[c] = true
				curves = append(curves, c)
			}
		}
		d.Curves = curves
	}

	hosts := []Host{}
	seenHost := map[string]bool{}
	for _, h := range d.Hosts {
		h.Addr = strings.TrimSpace(h.Addr)
		h.User = strings.TrimSpace(h.User)
		h.KeyFile = strings.TrimSpace(h.KeyFile)
		if validateIdentity(h.HostConn) != nil || seenHost[h.key()] {
			continue
		}
		seenHost[h.key()] = true
		if h.Port < 0 || h.Port > 65535 {
			h.Port = 0
		}
		hosts = append(hosts, h)
	}
	d.Hosts = hosts

	collapsed := map[string][]int{}
	for k, ids := range d.CollapsedGPUs {
		if list := sortedIDs(ids); len(list) > 0 {
			collapsed[k] = list
		}
	}
	d.CollapsedGPUs = collapsed

	procs := []string{}
	seenProc := map[string]bool{}
	for _, id := range d.CollapsedProcs {
		if id != "" && !seenProc[id] {
			seenProc[id] = true
			procs = append(procs, id)
		}
	}
	d.CollapsedProcs = procs

	// 排序方式的欄位或方向不合法時分別改用預設值, 結果為預設排序方式者不存放.
	sorts := map[string]ProcSort{}
	for k, v := range d.ProcSorts {
		if !validProcColumn(v.Column) {
			v.Column = ProcColumnGPU
		}
		if !validSortOrder(v.Order) {
			v.Order = SortAsc
		}
		if k != "" && v != DefaultProcSort() {
			sorts[k] = v
		}
	}
	d.ProcSorts = sorts

	filters := map[string]ProcFilter{}
	for k, v := range d.ProcFilters {
		v = v.normalize()
		if k != "" && !v.empty() {
			filters[k] = v
		}
	}
	d.ProcFilters = filters
}

// 設定操作可能回傳的錯誤.
var (
	// ErrDuplicateHost 表示已有相同識別的主機.
	ErrDuplicateHost = errors.New("host already exists")
	// ErrHostNotFound 表示找不到指定的主機.
	ErrHostNotFound = errors.New("host not found")
	// ErrInvalidValue 表示傳入的值不合法.
	ErrInvalidValue = errors.New("invalid value")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidValue, fmt.Sprintf(format, args...))
}

// validateIdentity 檢查位址與使用者名稱. 兩者皆不得含空白字元或 '@',
// 否則 user@addr 的識別字串會有歧義.
func validateIdentity(c HostConn) error {
	if c.Addr == "" {
		return invalid("address is required")
	}
	if strings.ContainsAny(c.Addr, "@ \t\r\n") {
		return invalid("address must not contain '@' or whitespace")
	}
	if strings.ContainsAny(c.User, "@ \t\r\n") {
		return invalid("user name must not contain '@' or whitespace")
	}
	return nil
}

// normalizeConn 去除前後空白並檢查表單欄位.
func normalizeConn(c HostConn) (HostConn, error) {
	c.Addr = strings.TrimSpace(c.Addr)
	c.User = strings.TrimSpace(c.User)
	c.KeyFile = strings.TrimSpace(c.KeyFile)
	if err := validateIdentity(c); err != nil {
		return c, err
	}
	if c.Port < 0 || c.Port > 65535 {
		return c, invalid("port %d out of range 0-65535", c.Port)
	}
	return c, nil
}

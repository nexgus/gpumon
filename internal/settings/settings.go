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

// 取樣間隔的合法範圍與預設值, 單位為毫秒.
const (
	MinIntervalMS     = 200
	MaxIntervalMS     = 10000
	DefaultIntervalMS = 1000
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

// ViewMode 為監看畫面的顯示方式.
type ViewMode string

// 監看畫面顯示方式的可用值.
const (
	ViewTabs ViewMode = "tabs"
	ViewAll  ViewMode = "all"
)

// TimeRange 為曲線圖的顯示範圍.
type TimeRange string

// 顯示範圍的可用值. RangeAll 僅供歷史畫面使用.
const (
	Range1Min    TimeRange = "1m"
	Range5Min    TimeRange = "5m"
	Range15Min   TimeRange = "15m"
	Range30Min   TimeRange = "30m"
	Range60Min   TimeRange = "60m"
	Range3Hours  TimeRange = "3h"
	Range6Hours  TimeRange = "6h"
	Range12Hours TimeRange = "12h"
	Range24Hours TimeRange = "24h"
	RangeAll     TimeRange = "all"
)

// DefaultRange 為所有顯示範圍的預設值.
const DefaultRange = Range5Min

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

// MonitorRanges 回傳監看畫面可選的顯示範圍, 由短至長.
func MonitorRanges() []TimeRange {
	return []TimeRange{Range1Min, Range5Min, Range15Min, Range30Min, Range60Min,
		Range3Hours, Range6Hours, Range12Hours, Range24Hours}
}

// HistoryRanges 回傳歷史畫面可選的顯示範圍, 比監看畫面多 "全部".
func HistoryRanges() []TimeRange {
	return append(MonitorRanges(), RangeAll)
}

// AllCurves 回傳所有可選的曲線.
func AllCurves() []Curve {
	return []Curve{CurveUtil, CurveMem, CurveTemp, CurveFan, CurvePower,
		CurveClockGPU, CurveClockMem}
}

// DefaultCurves 回傳預設的曲線, 為使用率與記憶體佔比.
func DefaultCurves() []Curve {
	return []Curve{CurveUtil, CurveMem}
}

func validAppearance(v Appearance) bool {
	return v == AppearanceSystem || v == AppearanceLight || v == AppearanceDark
}

func validLanguage(v Language) bool {
	return v == LanguageEnglish || v == LanguageTraditionalChinese
}

func validViewMode(v ViewMode) bool {
	return v == ViewTabs || v == ViewAll
}

func validMonitorRange(v TimeRange) bool {
	for _, r := range MonitorRanges() {
		if r == v {
			return true
		}
	}
	return false
}

func validHistoryRange(v TimeRange) bool {
	return v == RangeAll || validMonitorRange(v)
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

// Host 為一台主機的設定.
type Host struct {
	HostConn
	// IntervalMS 為取樣間隔, 單位為毫秒.
	IntervalMS int `json:"interval_ms"`
	// Range 為分頁顯示時此主機的顯示範圍.
	Range TimeRange `json:"range"`
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

	ViewMode ViewMode `json:"view_mode"`
	// SharedRange 為同時顯示時所有主機共用的顯示範圍.
	SharedRange TimeRange `json:"shared_range"`
	// HistoryRange 為歷史畫面的顯示範圍.
	HistoryRange TimeRange `json:"history_range"`

	Curves []Curve `json:"curves"`

	// Hosts 依新增的先後排列.
	Hosts []Host `json:"hosts"`

	// CollapsedGPUs 以主機識別字串為鍵, 值為已收合的 GPU id, 由小到大.
	CollapsedGPUs map[string][]int `json:"collapsed_gpus"`
}

// Default 回傳預設設定.
func Default() Data {
	return Data{
		Version:       CurrentVersion,
		Window:        Window{Width: DefaultWindowWidth, Height: DefaultWindowHeight},
		Appearance:    AppearanceSystem,
		Language:      LanguageEnglish,
		ViewMode:      ViewTabs,
		SharedRange:   DefaultRange,
		HistoryRange:  DefaultRange,
		Curves:        DefaultCurves(),
		Hosts:         []Host{},
		CollapsedGPUs: map[string][]int{},
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
	return c
}

func clampInt(v, lo, hi int) int {
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
	if !validViewMode(d.ViewMode) {
		d.ViewMode = def.ViewMode
	}
	if !validMonitorRange(d.SharedRange) {
		d.SharedRange = def.SharedRange
	}
	if !validHistoryRange(d.HistoryRange) {
		d.HistoryRange = def.HistoryRange
	}

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
		if h.IntervalMS == 0 {
			h.IntervalMS = DefaultIntervalMS
		}
		h.IntervalMS = clampInt(h.IntervalMS, MinIntervalMS, MaxIntervalMS)
		if !validMonitorRange(h.Range) {
			h.Range = DefaultRange
		}
		hosts = append(hosts, h)
	}
	d.Hosts = hosts

	collapsed := map[string][]int{}
	for k, ids := range d.CollapsedGPUs {
		set := map[int]bool{}
		for _, id := range ids {
			if id >= 0 {
				set[id] = true
			}
		}
		if len(set) == 0 {
			continue
		}
		list := make([]int, 0, len(set))
		for id := range set {
			list = append(list, id)
		}
		sort.Ints(list)
		collapsed[k] = list
	}
	d.CollapsedGPUs = collapsed
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

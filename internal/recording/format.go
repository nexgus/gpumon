// Package recording 實作記錄檔 (.gpurec) 的寫入與讀取. 格式定義見 docs/recording-format-v1.md
// 與 docs/recording-format-v2.md; 寫入一律為版本 2, 版本 1 只供讀取.
package recording

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	magic     = "GPUMONRC"
	fixedSize = 16

	// version1 與 version2 為固定區位移 8 的格式版本.
	version1 = 1
	version2 = 2
)

// Kind 為記錄的種類.
type Kind uint8

const (
	// KindSample 為取樣記錄.
	KindSample Kind = 1
	// KindError 為錯誤記錄, Text 為 error 訊息的文字.
	KindError Kind = 2
	// KindProcesses 為版本 1 的程序記錄, Text 為該次取樣完整的程序 JSON 陣列.
	KindProcesses Kind = 4
	// KindDisconnect 為斷線記錄, Text 為斷線原因.
	KindDisconnect Kind = 5
	// KindGPUProcs 為版本 2 的 GPU 程序變動記錄, 內容在 GPUProcs.
	KindGPUProcs Kind = 6
	// KindCPUProcs 為版本 2 的 CPU 程序變動記錄, 內容在 CPUProcs.
	KindCPUProcs Kind = 7
)

var (
	// ErrNotRecording 表示檔案的識別字串不符.
	ErrNotRecording = errors.New("recording: not a gpurec file (bad magic)")
	// ErrNoGPU 表示 devices 訊息的 GPU 張數為 0, 此時不建立記錄檔.
	ErrNoGPU = errors.New("recording: devices message has no GPU")
)

// UnsupportedVersionError 表示記錄檔的格式版本不認得.
type UnsupportedVersionError struct {
	Version uint16
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("recording: unsupported format version %d", e.Version)
}

// Header 為說明區的內容.
type Header struct {
	// Host 為該主機的識別, 連接埠為 22 時為 user@addr, 其他連接埠為 user@addr:port.
	Host string
	// TWall0 為本機收到第一次連線第一筆 sample 訊息的時刻, 單位為微秒.
	TWall0 int64
	// TS0 為該筆 sample 訊息的 ts 原值.
	TS0 float64
	// Devices 為第一次連線收到的 devices 訊息原始 JSON.
	Devices json.RawMessage
}

type headerJSON struct {
	Host    string          `json:"host"`
	TWall0  int64           `json:"t_wall_0"`
	TS0     float64         `json:"ts_0"`
	Devices json.RawMessage `json:"devices"`
}

// GPUSample 為寫入時單張 GPU 的取樣數值, 欄位為 nil 表示該欄位在 sample 訊息中被省略.
type GPUSample struct {
	ID       int
	Util     *int64
	Fan      *int64
	Enc      *int64
	Dec      *int64
	Temp     *int64
	PowerW   *int64
	ClockGPU *int64
	ClockMem *int64
	MemUsed  *uint64
}

// Metric 為取樣記錄中單張 GPU 的數值欄位.
type Metric uint8

const (
	MetricUtil Metric = iota
	MetricFan
	MetricEnc
	MetricDec
	MetricTemp
	MetricPowerW
	MetricClockGPU
	MetricClockMem
	MetricMemUsed
	numMetrics
)

// GPUValues 為讀取時單張 GPU 的取樣數值. Has 的第 m 個位元為 1 表示 Metric m 有值,
// 無值的欄位其數值為 0.
type GPUValues struct {
	ID       int
	Has      uint16
	Util     int64
	Fan      int64
	Enc      int64
	Dec      int64
	Temp     int64
	PowerW   int64
	ClockGPU int64
	ClockMem int64
	MemUsed  uint64
}

// Value 回傳欄位 m 的數值與是否有值.
func (g *GPUValues) Value(m Metric) (float64, bool) {
	if g.Has&(1<<m) == 0 {
		return 0, false
	}
	switch m {
	case MetricUtil:
		return float64(g.Util), true
	case MetricFan:
		return float64(g.Fan), true
	case MetricEnc:
		return float64(g.Enc), true
	case MetricDec:
		return float64(g.Dec), true
	case MetricTemp:
		return float64(g.Temp), true
	case MetricPowerW:
		return float64(g.PowerW), true
	case MetricClockGPU:
		return float64(g.ClockGPU), true
	case MetricClockMem:
		return float64(g.ClockMem), true
	case MetricMemUsed:
		return float64(g.MemUsed), true
	}
	return 0, false
}

// Int 回傳欄位 m 的整數值, 無值時為 nil. MemUsed 超過 int64 範圍時亦為 nil, 改用 MemUsed 欄位讀取.
func (g *GPUValues) Int(m Metric) *int64 {
	if g.Has&(1<<m) == 0 {
		return nil
	}
	var v int64
	switch m {
	case MetricUtil:
		v = g.Util
	case MetricFan:
		v = g.Fan
	case MetricEnc:
		v = g.Enc
	case MetricDec:
		v = g.Dec
	case MetricTemp:
		v = g.Temp
	case MetricPowerW:
		v = g.PowerW
	case MetricClockGPU:
		v = g.ClockGPU
	case MetricClockMem:
		v = g.ClockMem
	default:
		return nil
	}
	return &v
}

func (g *GPUValues) set(m Metric, v int64) {
	g.Has |= 1 << m
	switch m {
	case MetricUtil:
		g.Util = v
	case MetricFan:
		g.Fan = v
	case MetricEnc:
		g.Enc = v
	case MetricDec:
		g.Dec = v
	case MetricTemp:
		g.Temp = v
	case MetricPowerW:
		g.PowerW = v
	case MetricClockGPU:
		g.ClockGPU = v
	case MetricClockMem:
		g.ClockMem = v
	case MetricMemUsed:
		g.MemUsed = uint64(v)
	}
}

// SampleValues 依說明區的 GPU 順序 ids 把寫入用的取樣換算為讀取時的數值, 配對規則與
// Writer.AddSample 相同, 以 id 配對, 同一 id 出現多次時取第一個, 缺少的 GPU 所有欄位皆無值.
func SampleValues(ids []int, gpus []GPUSample) []GPUValues {
	byID := make(map[int]*GPUSample, len(gpus))
	for i := range gpus {
		if _, dup := byID[gpus[i].ID]; !dup {
			byID[gpus[i].ID] = &gpus[i]
		}
	}
	out := make([]GPUValues, len(ids))
	for i, id := range ids {
		out[i].ID = id
		g := byID[id]
		if g == nil {
			continue
		}
		for m, p := range []*int64{g.Util, g.Fan, g.Enc, g.Dec, g.Temp, g.PowerW, g.ClockGPU, g.ClockMem} {
			if p != nil {
				out[i].set(Metric(m), *p)
			}
		}
		if g.MemUsed != nil {
			out[i].Has |= 1 << MetricMemUsed
			out[i].MemUsed = *g.MemUsed
		}
	}
	return out
}

// Record 為讀取端取得的一筆記錄. 版本 1 的接續記錄已併入所屬的記錄.
type Record struct {
	Kind Kind
	// Time 為時刻, 單位為微秒.
	Time int64
	// IntervalMS 為取樣間隔 (毫秒), 僅取樣記錄有值.
	IntervalMS uint32
	// GPUs 僅取樣記錄有值, 順序與說明區 devices.gpus 相同.
	GPUs []GPUValues
	// Text 在錯誤記錄與斷線記錄為完整文字, 在版本 1 的程序記錄為完整的 JSON 陣列.
	Text string
	// GPUProcs 僅 GPU 程序變動記錄有值.
	GPUProcs []GPUProc
	// CPUProcs 僅 CPU 程序變動記錄有值.
	CPUProcs []CPUProc
}

var le = binary.LittleEndian

type devicesInfo struct {
	GPUs []struct {
		ID *int `json:"id"`
	} `json:"gpus"`
}

// parseGPUIDs 取出 devices 訊息各 GPU 的 id. strict 為真時缺少 id 即回報錯誤.
func parseGPUIDs(devices json.RawMessage, strict bool) ([]int, error) {
	var d devicesInfo
	if err := json.Unmarshal(devices, &d); err != nil {
		return nil, fmt.Errorf("recording: invalid devices message: %w", err)
	}
	ids := make([]int, len(d.GPUs))
	for i, g := range d.GPUs {
		switch {
		case g.ID != nil:
			ids[i] = *g.ID
		case strict:
			return nil, fmt.Errorf("recording: devices.gpus[%d] has no id", i)
		default:
			ids[i] = i
		}
	}
	return ids, nil
}

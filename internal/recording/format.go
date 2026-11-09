// Package recording 實作記錄檔 (.gpurec) 的二進位格式的寫入與讀取, 格式定義見設計文件 6.4 節.
package recording

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	magic             = "GPUMONRC"
	formatVersion     = 1
	fixedSize         = 16
	recordHeaderSize  = 11
	gpuBlockSize      = 20
	maxMessageBytes   = 1024
	maxProcessesBytes = 65535
)

// Kind 為記錄的種類. 接續記錄在讀取時已併入所屬的記錄, 不對外呈現.
type Kind uint8

const (
	KindSample     Kind = 1
	KindError      Kind = 2
	KindContinue   Kind = 3
	KindProcesses  Kind = 4
	KindDisconnect Kind = 5
)

// 各欄位代表無值的數值.
const (
	NoValue8   uint8  = 255
	NoValueI16 int16  = 32767
	NoValue16  uint16 = 65535
	NoValue64  uint64 = 18446744073709551615
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
	// Host 為該主機的 user@addr.
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

// GPUValues 為讀取時單張 GPU 的取樣數值, 值等於 NoValue 系列常數者表示無值.
type GPUValues struct {
	ID       int
	Util     uint8
	Fan      uint8
	Enc      uint8
	Dec      uint8
	Temp     int16
	PowerW   uint16
	ClockGPU uint16
	ClockMem uint16
	MemUsed  uint64
}

// GPUProcs 為單張 GPU 在一筆 sample 訊息內的程序列表, Procs 的每個元素為一個 JSON 物件.
type GPUProcs struct {
	GPU   int
	Procs []json.RawMessage
}

// Record 為讀取端取得的一筆完整記錄, 接續記錄已重組.
type Record struct {
	Kind Kind
	// Time 為時刻, 單位為微秒.
	Time int64
	// IntervalMS 為取樣間隔 (毫秒), 僅取樣記錄有值.
	IntervalMS uint16
	// GPUs 僅取樣記錄有值, 順序與說明區 devices.gpus 相同.
	GPUs []GPUValues
	// Text 在錯誤記錄與斷線記錄為完整文字, 在程序記錄為完整的 JSON 陣列.
	Text string
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

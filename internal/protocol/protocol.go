// Package protocol 定義遠端程式與本機 GUI 之間 JSON Lines 協定的共用內容.
//
// 本套件只含型別, 常數與純資料的處理函數, 不引用任何只能在 Linux 編譯的程式,
// 遠端程式 (linux) 與 GUI (darwin, windows) 皆引用本套件.
//
// 遠端程式送往本機的每一行為一則訊息, 以 type 欄位區分種類; 本機送往遠端的
// 每一行為一則指令, 以 cmd 欄位區分種類. 兩端皆忽略不認得的欄位.
package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Version 為協定版本, 以十進位整數表示, 協定有不相容變更時遞增.
const Version = 1

// 遠端送往本機的訊息種類, 即訊息的 type 欄位.
const (
	TypeDevices = "devices"
	TypeSample  = "sample"
	TypeAck     = "ack"
	TypeError   = "error"
	TypePong    = "pong"
)

// 本機送往遠端的指令種類, 即指令的 cmd 欄位.
const (
	CmdSetInterval = "set_interval"
	CmdSampleNow   = "sample_now"
	CmdSetFields   = "set_fields"
	CmdPing        = "ping"
)

// 取樣間隔的範圍與預設值, 單位為毫秒; set_interval 與 --interval 共用.
const (
	MinIntervalMs     = 200
	MaxIntervalMs     = 10000
	DefaultIntervalMs = 1000
)

// HeartbeatInterval 為本機送出 ping 的間隔.
const HeartbeatInterval = 5 * time.Second

// WatchdogTimeout 為遠端看門狗的逾時, 即連續 3 次心跳未到; 本機判定斷線亦用
// 同一時間.
const WatchdogTimeout = 15 * time.Second

// sample 訊息內每張 GPU 可由 set_fields 選取的欄位名稱.
const (
	FieldUtil     = "util"
	FieldMemUsed  = "mem_used"
	FieldTemp     = "temp"
	FieldPowerW   = "power_w"
	FieldClockGPU = "clock_gpu"
	FieldClockMem = "clock_mem"
	FieldFan      = "fan"
	FieldEnc      = "enc"
	FieldDec      = "dec"
	FieldProcs    = "procs"
)

// SampleFields 依協定所列順序列出所有可選取的欄位名稱. id 恆會輸出, 不在此列.
var SampleFields = []string{
	FieldUtil, FieldMemUsed, FieldTemp, FieldPowerW, FieldClockGPU,
	FieldClockMem, FieldFan, FieldEnc, FieldDec, FieldProcs,
}

// IsSampleField 回報 name 是否為可由 set_fields 選取的欄位名稱.
func IsSampleField(name string) bool {
	for _, f := range SampleFields {
		if f == name {
			return true
		}
	}
	return false
}

// GPU 廠牌, 即 devices 訊息內 vendor 欄位的值.
const (
	VendorNVIDIA = "nvidia"
	VendorAMD    = "amd"
	VendorIntel  = "intel"
)

// Timestamp 為遠端打上的時間戳記, 以 Unix 秒數表示, 編碼為帶 3 位小數的
// JSON 數字, 例如 1730700000.123.
type Timestamp float64

// NewTimestamp 將 t 換算為精確到毫秒的 Timestamp.
func NewTimestamp(t time.Time) Timestamp {
	return Timestamp(float64(t.UnixMilli()) / 1000)
}

// MarshalJSON 以固定 3 位小數輸出, 避免浮點數的最短表示法產生多餘位數.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	f := float64(t)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fmt.Errorf("protocol: invalid timestamp %v", f)
	}
	return []byte(fmt.Sprintf("%.3f", f)), nil
}

// Time 將 Timestamp 換回 time.Time.
func (t Timestamp) Time() time.Time {
	return time.UnixMilli(int64(math.Round(float64(t) * 1000)))
}

// Devices 為遠端程式啟動後輸出的第一行訊息, 列出所有偵測到的 GPU.
type Devices struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	// Version 為遠端程式的程式版號, 不含 commit 與 dirty 字樣.
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	// GPUs 於找不到任何 GPU 時為空陣列, 編碼為 [] 而非 null.
	GPUs []Device `json:"gpus"`
}

// Device 為 devices 訊息內一張 GPU 的靜態資訊. 指標欄位取不到時為 nil, 自
// 物件中省略.
type Device struct {
	ID       int     `json:"id"`
	Vendor   string  `json:"vendor"`
	Name     string  `json:"name"`
	MemTotal *uint64 `json:"mem_total,omitempty"`
	UUID     string  `json:"uuid,omitempty"`
	// PowerMaxW 為功耗上限, 單位為瓦.
	PowerMaxW *int `json:"power_max_w,omitempty"`
	// ClockGPUMax 與 ClockMemMax 為時脈上限, 單位為 MHz.
	ClockGPUMax *int `json:"clock_gpu_max,omitempty"`
	ClockMemMax *int `json:"clock_mem_max,omitempty"`
	// 以下四者為 PCI 代碼, 4 位小寫十六進位數字, 不含 0x 前綴; 讀不到時為
	// 空字串, 不省略.
	PCIVendor       string `json:"pci_vendor"`
	PCIDevice       string `json:"pci_device"`
	PCISubsysVendor string `json:"pci_subsys_vendor"`
	PCISubsysDevice string `json:"pci_subsys_device"`
}

// Sample 為每個取樣週期輸出一行的訊息.
type Sample struct {
	Type string      `json:"type"`
	TS   Timestamp   `json:"ts"`
	GPUs []GPUSample `json:"gpus"`
}

// GPUSample 為一張 GPU 的一筆取樣. 數值欄位取不到, 或未被 set_fields 選取時
// 為 nil, 自物件中省略.
type GPUSample struct {
	ID       int     `json:"id"`
	Util     *int    `json:"util,omitempty"`
	MemUsed  *uint64 `json:"mem_used,omitempty"`
	Temp     *int    `json:"temp,omitempty"`
	PowerW   *int    `json:"power_w,omitempty"`
	ClockGPU *int    `json:"clock_gpu,omitempty"`
	ClockMem *int    `json:"clock_mem,omitempty"`
	// Fan 為 NVIDIA 驅動設定的目標轉速百分比時可能超過 100, 照原值回傳.
	Fan *int `json:"fan,omitempty"`
	Enc *int `json:"enc,omitempty"`
	Dec *int `json:"dec,omitempty"`
	// Procs 為程序列表. nil 表示取不到或未被選取, 自物件中省略; 取得但沒有
	// 程序時為非 nil 的空 slice, 編碼為 [].
	Procs []Process `json:"procs,omitzero"`
}

// Process 為使用某張 GPU 的一個程序. 取不到的欄位自物件中省略.
//
// AMD 與 Intel 沒有遠端可直接讀取的程序使用率, 改以各引擎的原始累計值回傳,
// 由本機保留上一筆自行相減; 遠端不保留前一筆.
type Process struct {
	PID  int     `json:"pid"`
	User string  `json:"user,omitempty"`
	Mem  *uint64 `json:"mem,omitempty"`
	// Util 為程序的 GPU 使用率百分比, 只有 NVIDIA 提供.
	Util *int   `json:"util,omitempty"`
	Cmd  string `json:"cmd,omitempty"`
	// Engines 為 fdinfo 的 drm-engine-<引擎> 累計忙碌時間, 單位為奈秒, 以引擎
	// 名稱為鍵. i915 與 amdgpu 提供.
	Engines map[string]uint64 `json:"engines,omitempty"`
	// EngineCapacity 為 fdinfo 的 drm-engine-capacity-<引擎>, 即該類引擎的數量,
	// 換算使用率時作為分母的倍數; fdinfo 未列出時省略, 意為 1.
	EngineCapacity map[string]uint64 `json:"engine_capacity,omitempty"`
	// Cycles 與 TotalCycles 為 xe 驅動 fdinfo 的 drm-cycles-<引擎> 與
	// drm-total-cycles-<引擎>, 兩者各自的差值相除即為使用率.
	Cycles      map[string]uint64 `json:"cycles,omitempty"`
	TotalCycles map[string]uint64 `json:"total_cycles,omitempty"`
}

// Ack 為遠端對設定類指令的確認回應, 帶有實際套用的值.
type Ack struct {
	Type string `json:"type"`
	Cmd  string `json:"cmd"`
	// Ms 為 set_interval 實際套用的間隔.
	Ms *int `json:"ms,omitempty"`
	// Fields 為 set_fields 實際套用的欄位, 依 SampleFields 的順序排列.
	Fields []string `json:"fields,omitzero"`
}

// Error 為遠端回報錯誤的訊息.
type Error struct {
	Type string    `json:"type"`
	TS   Timestamp `json:"ts"`
	Msg  string    `json:"msg"`
}

// Pong 為遠端對 ping 的回應.
type Pong struct {
	Type string `json:"type"`
}

// Command 為本機送往遠端的指令.
type Command struct {
	Cmd string `json:"cmd"`
	// Ms 只用於 set_interval, nil 表示省略, 等同 DefaultIntervalMs.
	Ms *int `json:"ms,omitempty"`
	// Fields 只用於 set_fields, nil 表示省略, 等同全部欄位; 非 nil 的空
	// slice 表示不輸出任何可選取的欄位.
	Fields []string `json:"fields,omitzero"`
}

// Envelope 只取出訊息的 type 欄位, 供本機判斷訊息種類後再解碼為對應型別.
type Envelope struct {
	Type string `json:"type"`
}

// MessageType 回傳一行訊息的 type 欄位.
func MessageType(line []byte) (string, error) {
	var e Envelope
	if err := json.Unmarshal(line, &e); err != nil {
		return "", err
	}
	return e.Type, nil
}

// ValidInterval 回報 ms 是否在 MinIntervalMs 至 MaxIntervalMs 的範圍內.
func ValidInterval(ms int) bool {
	return ms >= MinIntervalMs && ms <= MaxIntervalMs
}

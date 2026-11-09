// Package history 提供歷史畫面所需的查詢功能, 建立在 internal/recording 之上, 包括記錄檔清單與刪除,
// 曲線資料的降採樣查詢, 以及指定時刻的程序列表. 本套件不含畫面, 連線與記錄檔的寫入.
package history

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Field 為取樣記錄中的數值欄位, 名稱與通訊協定 sample 訊息的欄位名稱相同.
type Field string

const (
	FieldUtil     Field = "util"
	FieldMemUsed  Field = "mem_used"
	FieldTemp     Field = "temp"
	FieldPowerW   Field = "power_w"
	FieldClockGPU Field = "clock_gpu"
	FieldClockMem Field = "clock_mem"
	FieldFan      Field = "fan"
	FieldEnc      Field = "enc"
	FieldDec      Field = "dec"
)

// AllFields 為所有數值欄位, 查詢未指定欄位時採用.
var AllFields = []Field{
	FieldUtil, FieldMemUsed, FieldTemp, FieldPowerW, FieldClockGPU,
	FieldClockMem, FieldFan, FieldEnc, FieldDec,
}

// Values 為緊湊的數值陣列. 留空的位置在記憶體中為 NaN, 編碼為 JSON 時輸出 null, 可與數值 0 區分.
type Values []float64

// MarshalJSON 將 NaN 與無限大輸出為 null.
func (v Values) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(v)*6+2)
	b = append(b, '[')
	for i, x := range v {
		if i > 0 {
			b = append(b, ',')
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b = append(b, "null"...)
		} else {
			b = strconv.AppendFloat(b, x, 'f', -1, 64)
		}
	}
	return append(b, ']'), nil
}

// UnmarshalJSON 將 null 還原為 NaN.
func (v *Values) UnmarshalJSON(data []byte) error {
	var p []*float64
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	out := make(Values, len(p))
	for i, x := range p {
		if x == nil {
			out[i] = math.NaN()
		} else {
			out[i] = *x
		}
	}
	*v = out
	return nil
}

// Curve 為單張 GPU 單一欄位的曲線. 降採樣時 Avg, Min, Max 皆有值且長度等於點數;
// 回傳原始取樣點時只有 Avg, Min 與 Max 為空. 查詢條件的 Stats 為真且降採樣時, Sum 與
// Count 為各像素範圍內該欄位有值的取樣的總和與筆數, Avg 即為 Sum 除以 Count.
type Curve struct {
	Avg   Values   `json:"avg"`
	Min   Values   `json:"min,omitempty"`
	Max   Values   `json:"max,omitempty"`
	Sum   Values   `json:"sum,omitempty"`
	Count []uint32 `json:"count,omitempty"`
}

// GPUCurves 為單張 GPU 的各欄位曲線, 鍵為欄位名稱.
type GPUCurves struct {
	ID     int              `json:"id"`
	Fields map[Field]*Curve `json:"fields"`
}

// GapReason 為無資料時段的原因種類, 供畫面依語言設定顯示.
type GapReason string

const (
	// GapUnknown 表示原因不明.
	GapUnknown GapReason = "unknown"
	// GapNotConnected 表示時段位於兩個記錄檔之間, 即未連線.
	GapNotConnected GapReason = "not_connected"
	// GapError 表示時段內有遠端送出的 error 訊息, Message 為其英文原文.
	GapError GapReason = "error"
	// GapDisconnect 表示時段內有斷線記錄, Message 為其斷線原因.
	GapDisconnect GapReason = "disconnect"
)

// Gap 為一段無資料時段, Start 與 End 為前一筆與後一筆取樣的時刻 (微秒), 可能超出查詢範圍, 畫面繪製時自行裁切.
// Message 只在 GapError 與 GapDisconnect 時有值.
type Gap struct {
	Start   int64     `json:"start"`
	End     int64     `json:"end"`
	Reason  GapReason `json:"reason"`
	Message string    `json:"message,omitempty"`
}

// Result 為曲線查詢的結果.
//
// Binned 為假時, Times 為範圍內每筆取樣的時刻, 各曲線的點與之逐一對應.
// Binned 為真時, 範圍被均分為 Width 個像素範圍, Times[i] 為第 i 個像素範圍的起點,
// 各曲線的第 i 點為該像素範圍的統計, 沒有任何取樣的像素範圍為留空.
//
// Breaks 的每個元素 i 表示第 i 點與第 i+1 點之間為無資料時段, 曲線須在此斷開;
// 兩點之間若有留空的點, 曲線本來就會斷開, 不列入 Breaks.
//
// Last 為範圍內最後一筆取樣的時刻, Samples 為 0 時為 0. 查詢條件的 Stats 為真且降採樣時,
// Counts 為各像素範圍內的取樣筆數.
type Result struct {
	From    int64       `json:"from"`
	To      int64       `json:"to"`
	Width   int         `json:"width"`
	Binned  bool        `json:"binned"`
	Samples int         `json:"samples"`
	Last    int64       `json:"last"`
	Counts  []uint32    `json:"counts,omitempty"`
	Times   []int64     `json:"times"`
	Breaks  []int       `json:"breaks"`
	GPUs    []GPUCurves `json:"gpus"`
	Gaps    []Gap       `json:"gaps"`
}

func validateFields(fields []Field) ([]Field, error) {
	if len(fields) == 0 {
		return AllFields, nil
	}
	seen := make(map[Field]bool, len(fields))
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		known := false
		for _, a := range AllFields {
			if a == f {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("history: unknown field %q", f)
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out, nil
}

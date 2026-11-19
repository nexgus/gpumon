package history

import (
	"encoding/json"
	"fmt"
	"slices"

	"gpumon/internal/recording"
)

// ProcessList 為某次取樣的 GPU 程序列表. Procs 為 JSON 陣列, 每個元素為一個程序,
// 含 gpu 成員 (該程序所在 GPU 的 id).
type ProcessList struct {
	Time  int64           `json:"time"`
	Procs json.RawMessage `json:"procs"`
}

// ProcessesAt 回傳記錄檔中時刻不晚於 t 的最近一次取樣 (含 t) 的程序列表; 沒有, 或該次取樣的
// 程序列表因檔案損壞而無法還原時回傳 nil. 元素帶有整數成員 util (0 至 100) 時原樣保留; 沒有
// util 而有累計值 (Intel 與 AMD) 的元素, 以該次與前一次取樣的列表相減算出後加入 util, 算法
// 與監看畫面相同 (5.4). 沒有前一次取樣, 兩次之間有斷線記錄, 該程序不在前一次的列表內, 或
// 累計值減少時, 不加入 util. 版本 1 的記錄檔以程序記錄為單位, 規則相同.
func ProcessesAt(path string, t int64) (*ProcessList, error) {
	f, err := recording.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cur, prev, err := f.ProcessesAt(t)
	if err != nil || cur == nil {
		return nil, err
	}
	list := &ProcessList{Time: cur.Time, Procs: cur.Procs}
	if prev != nil {
		list.Procs = addComputedUtil(list.Procs, prev.Procs, cur.Time-prev.Time)
	}
	return list, nil
}

// procElem 為程序記錄中一個元素供換算使用率的成員.
type procElem struct {
	GPU            int               `json:"gpu"`
	PID            int               `json:"pid"`
	Util           *int              `json:"util"`
	Engines        map[string]uint64 `json:"engines"`
	EngineCapacity map[string]uint64 `json:"engine_capacity"`
	Cycles         map[string]uint64 `json:"cycles"`
	TotalCycles    map[string]uint64 `json:"total_cycles"`
}

func (e procElem) counters() Counters {
	return Counters{Engines: e.Engines, Capacity: e.EngineCapacity, Cycles: e.Cycles, TotalCycles: e.TotalCycles}
}

// addComputedUtil 對 cur 中沒有 util 而有累計值的元素, 以 prev 中同一個程序相減算出
// util 並加入. dtUS 為兩筆程序記錄相隔的微秒數. 沒有元素需要變更時回傳原內容.
func addComputedUtil(cur, prev json.RawMessage, dtUS int64) json.RawMessage {
	var curElems, prevElems []json.RawMessage
	if json.Unmarshal(cur, &curElems) != nil || json.Unmarshal(prev, &prevElems) != nil {
		return cur
	}
	type key struct{ gpu, pid int }
	prevByKey := map[key]Counters{}
	for _, raw := range prevElems {
		var e procElem
		if json.Unmarshal(raw, &e) == nil {
			prevByKey[key{e.GPU, e.PID}] = e.counters()
		}
	}
	changed := false
	for i, raw := range curElems {
		var e procElem
		if json.Unmarshal(raw, &e) != nil || e.Util != nil || (len(e.Engines) == 0 && len(e.Cycles) == 0) {
			continue
		}
		p, ok := prevByKey[key{e.GPU, e.PID}]
		if !ok {
			continue
		}
		v, ok := ProcUtil(p, e.counters(), float64(dtUS)*1000)
		if !ok || len(raw) < 2 || raw[len(raw)-1] != '}' {
			continue
		}
		withUtil := append(slices.Clone(raw[:len(raw)-1]), fmt.Sprintf(`,"util":%d}`, v)...)
		curElems[i] = withUtil
		changed = true
	}
	if !changed {
		return cur
	}
	out := []byte{'['}
	for i, e := range curElems {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	return append(out, ']')
}

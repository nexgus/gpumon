package history

import (
	"encoding/json"
	"fmt"
	"slices"

	"gpumon/internal/recording"
)

// ProcessList 為一筆程序記錄. Procs 為記錄內存的 JSON 陣列, 每個元素為一個程序,
// 含 gpu 成員 (該程序所在 GPU 的 id).
type ProcessList struct {
	Time  int64           `json:"time"`
	Procs json.RawMessage `json:"procs"`
}

// ProcessesAt 回傳記錄檔中時刻不晚於 t 的最近一筆程序記錄 (含 t); 沒有時回傳 nil.
// 元素帶有整數成員 util (0 至 100) 時原樣保留; 沒有 util 而有累計值 (Intel 與 AMD)
// 的元素, 以該筆與前一筆程序記錄相減算出後加入 util, 算法與監看畫面相同 (5.4).
// 沒有前一筆程序記錄, 兩筆之間有斷線記錄, 該程序不在前一筆內, 或累計值減少時, 不加入
// util.
func ProcessesAt(path string, t int64) (*ProcessList, error) {
	f, err := recording.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := f.Count()
	if err != nil {
		return nil, err
	}
	hi := n
	if t < maxTime {
		if hi, err = f.LowerBound(t + 1); err != nil {
			return nil, err
		}
	}
	var cur, prev *recording.Record
	done := false
	for c := scanRecords; hi > 0 && !done; c *= 2 {
		start := max(0, hi-c)
		recs, err := f.ReadIndexRange(start, hi)
		if err != nil {
			return nil, err
		}
		hi = start
		for i := len(recs) - 1; i >= 0; i-- {
			switch recs[i].Kind {
			case recording.KindProcesses:
				if cur == nil {
					cur = &recs[i]
				} else {
					prev, done = &recs[i], true
				}
			case recording.KindDisconnect:
				// 斷線前後的累計值不可相減, 不必再找前一筆.
				done = cur != nil
			}
			if done {
				break
			}
		}
	}
	if cur == nil {
		return nil, nil
	}
	list := &ProcessList{Time: cur.Time, Procs: json.RawMessage(cur.Text)}
	if prev != nil {
		list.Procs = addComputedUtil(list.Procs, json.RawMessage(prev.Text), cur.Time-prev.Time)
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

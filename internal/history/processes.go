package history

import (
	"encoding/json"

	"gpumon/internal/recording"
)

// ProcessList 為一筆程序記錄. Procs 為記錄內存的 JSON 陣列, 每個元素為一個程序,
// 含 gpu 成員 (該程序所在 GPU 的 id).
type ProcessList struct {
	Time  int64           `json:"time"`
	Procs json.RawMessage `json:"procs"`
}

// ProcessesAt 回傳記錄檔中時刻不晚於 t 的最近一筆程序記錄 (含 t); 沒有時回傳 nil.
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
	for c := scanRecords; ; c *= 2 {
		start := max(0, hi-c)
		recs, err := f.ReadIndexRange(start, hi)
		if err != nil {
			return nil, err
		}
		for i := len(recs) - 1; i >= 0; i-- {
			if recs[i].Kind == recording.KindProcesses {
				return &ProcessList{Time: recs[i].Time, Procs: json.RawMessage(recs[i].Text)}, nil
			}
		}
		if start == 0 {
			return nil, nil
		}
	}
}

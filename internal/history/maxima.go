package history

import (
	"slices"

	"gpumon/internal/recording"
)

// maximaFields 為整個記錄檔最大值所涵蓋的欄位.
var maximaFields = []Field{
	FieldUtil, FieldMemUsed, FieldTemp, FieldFan, FieldPowerW, FieldClockGPU, FieldClockMem,
}

// FieldMax 為單張 GPU 單一欄位於整個記錄檔的最大值. Runs 為最大值的出現處, 依時刻遞增,
// 每個元素為 [起, 迄] 兩筆取樣的時刻 (Unix 微秒).
type FieldMax struct {
	Value float64    `json:"value"`
	Runs  [][2]int64 `json:"runs"`
}

// GPUMaxima 為單張 GPU 各欄位的最大值. 整個記錄檔沒有任何數值的欄位不在 Fields 之中.
type GPUMaxima struct {
	ID     int                 `json:"id"`
	Fields map[Field]*FieldMax `json:"fields"`
}

// MaximaResult 為整個記錄檔的最大值, GPUs 依 id 遞增.
type MaximaResult struct {
	GPUs []GPUMaxima `json:"gpus"`
}

// maxTracker 為單一欄位掃描中的狀態.
type maxTracker struct {
	has  bool
	max  float64
	runs [][2]int64
	// open 為真時, 前一筆取樣屬於 runs 的最後一處, 本筆取樣同值即可延長該處.
	open bool
}

func (m *maxTracker) add(v float64, t int64, broken bool) {
	switch {
	case !m.has || v > m.max:
		m.has, m.max = true, v
		m.runs = [][2]int64{{t, t}}
		m.open = true
	case v == m.max:
		if m.open && !broken {
			m.runs[len(m.runs)-1][1] = t
		} else {
			m.runs = append(m.runs, [2]int64{t, t})
			m.open = true
		}
	default:
		m.open = false
	}
}

// Maxima 掃描記錄檔的全部取樣一次, 回傳每張 GPU 每個欄位的最大值與出現處. 數值等於最大值
// 的連續取樣為一處, 下一筆取樣的該欄位數值不同或無值, 或兩筆取樣之間有斷線記錄時斷開.
// 數值與曲線查詢未降採樣時回傳的原始取樣值相同. 記憶體用量不隨取樣筆數增加, 出現處清單
// 除外.
func Maxima(path string) (*MaximaResult, error) {
	f, err := recording.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := f.Count()
	if err != nil {
		return nil, err
	}
	ids := f.GPUIDs()
	tr := make([][]maxTracker, len(ids))
	for i := range tr {
		tr[i] = make([]maxTracker, len(maximaFields))
	}
	broken := false
	for i := 0; i < n; i += chunkRecords {
		recs, err := f.ReadIndexRange(i, min(i+chunkRecords, n))
		if err != nil {
			return nil, err
		}
		for k := range recs {
			rec := &recs[k]
			switch rec.Kind {
			case recording.KindDisconnect:
				broken = true
			case recording.KindSample:
				for gi := range rec.GPUs {
					if gi >= len(tr) {
						break
					}
					for fi, fld := range maximaFields {
						if v, ok := fieldValue(&rec.GPUs[gi], fld); ok {
							tr[gi][fi].add(v, rec.Time, broken)
						} else {
							tr[gi][fi].open = false
						}
					}
				}
				broken = false
			}
		}
	}
	res := &MaximaResult{GPUs: make([]GPUMaxima, 0, len(ids))}
	seen := map[int]bool{}
	for gi, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		g := GPUMaxima{ID: id, Fields: map[Field]*FieldMax{}}
		for fi, fld := range maximaFields {
			if m := &tr[gi][fi]; m.has {
				g.Fields[fld] = &FieldMax{Value: m.max, Runs: m.runs}
			}
		}
		res.GPUs = append(res.GPUs, g)
	}
	slices.SortFunc(res.GPUs, func(a, b GPUMaxima) int { return a.ID - b.ID })
	return res, nil
}

// GPUFileMax 為單張 GPU 各欄位於整個記錄檔至目前為止的最大值, 不含出現處. 沒有任何數值
// 的欄位不在 Fields 之中.
type GPUFileMax struct {
	ID     int               `json:"id"`
	Fields map[Field]float64 `json:"fields"`
}

// MaxTracker 逐筆累計寫入中記錄檔的取樣, 維護每張 GPU 每個欄位的最大值, 數值與 Maxima
// 相同但不記錄出現處. 每筆取樣的累計成本與記錄檔長度無關. 不可同時由多個 goroutine 使用.
type MaxTracker struct {
	ids []int
	max [][]float64
	has [][]bool
}

// NewMaxTracker 回傳尚未累計任何取樣的 MaxTracker, ids 為記錄檔的 GPU 順序.
func NewMaxTracker(ids []int) *MaxTracker {
	m := &MaxTracker{ids: slices.Clone(ids), max: make([][]float64, len(ids)), has: make([][]bool, len(ids))}
	for i := range ids {
		m.max[i] = make([]float64, len(maximaFields))
		m.has[i] = make([]bool, len(maximaFields))
	}
	return m
}

// Add 累計一筆記錄; 取樣以外的記錄不影響最大值. rec.GPUs 須依記錄檔的 GPU 順序排列.
func (m *MaxTracker) Add(rec *recording.Record) {
	if rec.Kind != recording.KindSample {
		return
	}
	for gi := range rec.GPUs {
		if gi >= len(m.ids) {
			break
		}
		for fi, fld := range maximaFields {
			if v, ok := fieldValue(&rec.GPUs[gi], fld); ok && (!m.has[gi][fi] || v > m.max[gi][fi]) {
				m.has[gi][fi], m.max[gi][fi] = true, v
			}
		}
	}
}

// Result 回傳目前的最大值, GPU 依 id 遞增; 重複的 GPU id 只取第一張, 與 Maxima 相同.
func (m *MaxTracker) Result() []GPUFileMax {
	out := make([]GPUFileMax, 0, len(m.ids))
	seen := map[int]bool{}
	for gi, id := range m.ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		g := GPUFileMax{ID: id, Fields: map[Field]float64{}}
		for fi, fld := range maximaFields {
			if m.has[gi][fi] {
				g.Fields[fld] = m.max[gi][fi]
			}
		}
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b GPUFileMax) int { return a.ID - b.ID })
	return out
}

// SampleGPU 為單筆取樣中單張 GPU 的數值, 無值的欄位為 nil.
type SampleGPU struct {
	ID       int     `json:"id"`
	Util     *uint8  `json:"util"`
	MemUsed  *uint64 `json:"mem_used"`
	Temp     *int16  `json:"temp"`
	Fan      *uint8  `json:"fan"`
	PowerW   *uint16 `json:"power_w"`
	ClockGPU *uint16 `json:"clock_gpu"`
	ClockMem *uint16 `json:"clock_mem"`
}

// SampleAtResult 為 SampleAt 選到的取樣.
type SampleAtResult struct {
	Time int64       `json:"time"`
	GPUs []SampleGPU `json:"gpus"`
}

func optional[T comparable](v, none T) *T {
	if v == none {
		return nil
	}
	return &v
}

// SampleAt 回傳記錄檔中時刻不晚於 t (含 t) 的最近一筆取樣, 選取規則與 ProcessesAt 相同.
// 沒有這樣的取樣, t 與該筆取樣相隔超過其取樣間隔的 2 倍, 或兩者之間有斷線記錄時回傳 nil.
func SampleAt(path string, t int64) (*SampleAtResult, error) {
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
	for c := scanRecords; hi > 0; c *= 2 {
		start := max(0, hi-c)
		recs, err := f.ReadIndexRange(start, hi)
		if err != nil {
			return nil, err
		}
		hi = start
		for i := len(recs) - 1; i >= 0; i-- {
			switch recs[i].Kind {
			case recording.KindDisconnect:
				return nil, nil
			case recording.KindSample:
				return sampleResult(&recs[i], t), nil
			}
		}
	}
	return nil, nil
}

func sampleResult(rec *recording.Record, t int64) *SampleAtResult {
	iv := int64(rec.IntervalMS)
	if iv == 0 {
		iv = defaultIntervalMS
	}
	if t-rec.Time > 2*iv*1000 {
		return nil
	}
	res := &SampleAtResult{Time: rec.Time, GPUs: make([]SampleGPU, len(rec.GPUs))}
	for i := range rec.GPUs {
		g := &rec.GPUs[i]
		res.GPUs[i] = SampleGPU{
			ID:       g.ID,
			Util:     optional(g.Util, recording.NoValue8),
			MemUsed:  optional(g.MemUsed, recording.NoValue64),
			Temp:     optional(g.Temp, recording.NoValueI16),
			Fan:      optional(g.Fan, recording.NoValue8),
			PowerW:   optional(g.PowerW, recording.NoValue16),
			ClockGPU: optional(g.ClockGPU, recording.NoValue16),
			ClockMem: optional(g.ClockMem, recording.NoValue16),
		}
	}
	return res
}

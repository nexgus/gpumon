package history

import (
	"math"
	"slices"

	"gpumon/internal/recording"
)

// maximaFields 為整個記錄檔最大值所涵蓋的欄位.
var maximaFields = []Field{
	FieldUtil, FieldMemUsed, FieldTemp, FieldFan, FieldPowerW, FieldClockGPU, FieldClockMem,
}

// maximaMetrics 為 maximaFields 各欄位對應的記錄檔數值欄位.
var maximaMetrics = metricsOf(maximaFields)

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
	ids := f.GPUIDs()
	tr := make([][]maxTracker, len(ids))
	for i := range tr {
		tr[i] = make([]maxTracker, len(maximaFields))
	}
	it, err := f.SeekTime(math.MinInt64, false)
	if err != nil {
		return nil, err
	}
	broken := false
	for {
		recs, ok, err := it.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
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
					for fi, m := range maximaMetrics {
						if v, ok := rec.GPUs[gi].Value(m); ok {
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
		for fi, mt := range maximaMetrics {
			if v, ok := rec.GPUs[gi].Value(mt); ok && (!m.has[gi][fi] || v > m.max[gi][fi]) {
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
	Util     *int64  `json:"util"`
	MemUsed  *uint64 `json:"mem_used"`
	Temp     *int64  `json:"temp"`
	Fan      *int64  `json:"fan"`
	PowerW   *int64  `json:"power_w"`
	ClockGPU *int64  `json:"clock_gpu"`
	ClockMem *int64  `json:"clock_mem"`
}

// SampleAtResult 為 SampleAt 選到的取樣.
type SampleAtResult struct {
	Time int64       `json:"time"`
	GPUs []SampleGPU `json:"gpus"`
}

// SampleAt 回傳記錄檔中時刻不晚於 t (含 t) 的最近一筆取樣, 選取規則與 ProcessesAt 相同.
// 沒有這樣的取樣, t 與該筆取樣相隔超過其取樣間隔的 2 倍, 或兩者之間有斷線記錄時回傳 nil.
func SampleAt(path string, t int64) (*SampleAtResult, error) {
	f, err := recording.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var res *SampleAtResult
	err = f.Backward(t, false, func(rec *recording.Record) bool {
		switch rec.Kind {
		case recording.KindDisconnect:
			return false
		case recording.KindSample:
			res = sampleResult(rec, t)
			return false
		}
		return true
	})
	return res, err
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
			Util:     g.Int(recording.MetricUtil),
			Temp:     g.Int(recording.MetricTemp),
			Fan:      g.Int(recording.MetricFan),
			PowerW:   g.Int(recording.MetricPowerW),
			ClockGPU: g.Int(recording.MetricClockGPU),
			ClockMem: g.Int(recording.MetricClockMem),
		}
		if g.Has&(1<<recording.MetricMemUsed) != 0 {
			mem := g.MemUsed
			res.GPUs[i].MemUsed = &mem
		}
	}
	return res
}

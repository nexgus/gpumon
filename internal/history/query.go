package history

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"

	"gpumon/internal/recording"
)

const (
	// MaxWidth 為像素寬度的上限, 避免呼叫端傳入過大的值而配置大量記憶體.
	MaxWidth = 16384

	maxTime           = int64(math.MaxInt64)
	gapFactor         = 3
	defaultIntervalMS = 1000
	// 主要範圍每次讀取的記錄筆數, 決定串流時的暫存用量.
	chunkRecords = 2048
	// 範圍後尋找下一筆取樣時每次讀取的筆數.
	scanRecords = 4096
	// 範圍前尋找前一筆取樣時第一次讀取的筆數, 之後每次加倍. 監看畫面每筆取樣的增量查詢
	// 範圍很短, 起始筆數小可避免每次都讀取大量記錄.
	scanBackRecords = 64
)

// Query 為曲線查詢的條件.
type Query struct {
	// From 與 To 為時間範圍的閉區間, 單位為微秒.
	From, To int64
	// Width 為曲線圖的像素寬度, 1 到 MaxWidth.
	Width int
	// Fields 為要回傳的欄位, 為空時回傳全部欄位.
	Fields []Field
	// Stats 為真時, 降採樣的結果另附各像素範圍的取樣筆數與各曲線的總和及筆數, 供呼叫端
	// 把之後的取樣併入同一組像素範圍.
	Stats bool
}

type queryFile struct {
	f           recordSource
	order       int
	first, last int64
	overlap     bool
	gpuMap      []int
}

type prevSample struct {
	t       int64
	file    int
	inRange bool
	raw     int
	bin     int
}

type engine struct {
	q      Query
	span   uint64
	fields []Field
	nGPU   int
	nSer   int
	gpuIDs []int

	cur *queryFile

	samples int
	last    int64
	rawOK   bool
	times   []int64
	rows    []float64
	rawBrk  []int

	sum, mn, mx []float64
	cnt         []uint32
	binSamples  []uint32
	binBrk      []int

	hasPrev bool
	prev    prevSample
	hasErr  bool
	errMsg  string
	hasDisc bool
	discMsg string
	gaps    []Gap
	done    bool
}

// QueryRange 讀取 paths 所列的記錄檔 (應屬同一台主機, 順序不拘), 回傳時間範圍內每張 GPU 每個欄位的曲線與無資料時段.
//
// 範圍內取樣筆數不多於 Width 時回傳原始取樣點, 否則依像素分桶取最大, 最小與平均. 讀取以串流方式進行,
// 記憶體用量與 Width 成正比, 與取樣筆數無關. 多個檔案依時刻先後處理, GPU 以 id 合併,
// 某檔案沒有的 GPU 在該檔案的時段留空. 連線中的記錄檔亦可查詢. 以 ctx 取消時回傳 ctx.Err().
func QueryRange(ctx context.Context, paths []string, q Query) (*Result, error) {
	return QueryRangePending(ctx, paths, nil, q)
}

// QueryRangePending 與 QueryRange 相同, 另把 pending 所列尚未寫入磁碟的記錄接續於對應記錄檔的檔尾一併查詢.
// pending 的 Path 須為 paths 之一, 不在 paths 之中者忽略. 查詢期間該記錄檔不得被附加, 否則新附加的記錄與
// pending 可能重複計入.
func QueryRangePending(ctx context.Context, paths []string, pending []Pending, q Query) (*Result, error) {
	if q.Width < 1 || q.Width > MaxWidth {
		return nil, fmt.Errorf("history: width %d out of range 1..%d", q.Width, MaxWidth)
	}
	if q.To < q.From {
		return nil, errors.New("history: query range ends before it starts")
	}
	span := uint64(q.To) - uint64(q.From) + 1
	if span == 0 {
		return nil, errors.New("history: query range too large")
	}
	fields, err := validateFields(q.Fields)
	if err != nil {
		return nil, err
	}
	files, err := openFiles(paths, pending, q)
	defer func() {
		for _, qf := range files {
			qf.f.Close()
		}
	}()
	if err != nil {
		return nil, err
	}
	e := &engine{q: q, span: span, fields: fields, rawOK: true}
	e.setup(files)
	for _, qf := range files {
		if e.done {
			break
		}
		if err := e.runFile(ctx, qf); err != nil {
			return nil, err
		}
	}
	return e.result(files), nil
}

// openFiles 開啟全部檔案, 略過沒有記錄的檔案, 依起始時刻排序, 並只保留與範圍相關的檔案:
// 與範圍重疊者, 以及供判定無資料時段之用, 範圍前最後一個與範圍後第一個檔案.
func openFiles(paths []string, pending []Pending, q Query) ([]*queryFile, error) {
	var all []*queryFile
	var firstErr error
	for _, p := range paths {
		rf, err := recording.Open(p)
		if err != nil {
			firstErr = err
			break
		}
		var f recordSource = rf
		if recs := pendingFor(pending, p); len(recs) > 0 {
			if f, err = newPendingSource(rf, recs); err != nil {
				rf.Close()
				firstErr = err
				break
			}
		}
		first, last, ok, err := f.TimeRange()
		if err != nil {
			f.Close()
			firstErr = err
			break
		}
		if !ok {
			f.Close()
			continue
		}
		all = append(all, &queryFile{f: f, first: first, last: last,
			overlap: last >= q.From && first <= q.To})
	}
	if firstErr != nil {
		return all, firstErr
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].first < all[j].first })
	skip := 0
	for i, qf := range all {
		if qf.last < q.From {
			skip = i
		}
	}
	for _, qf := range all[:skip] {
		qf.f.Close()
	}
	all = all[skip:]
	for i, qf := range all {
		qf.order = i
	}
	return all, nil
}

func (e *engine) setup(files []*queryFile) {
	index := map[int]int{}
	var ids []int
	for _, qf := range files {
		if !qf.overlap {
			continue
		}
		for _, id := range qf.f.GPUIDs() {
			if _, ok := index[id]; !ok {
				index[id] = len(ids)
				ids = append(ids, id)
			}
		}
	}
	for _, qf := range files {
		qf.gpuMap = make([]int, qf.f.NumGPUs())
		seen := map[int]bool{}
		for i, id := range qf.f.GPUIDs() {
			u, ok := index[id]
			if !ok || seen[id] {
				u = -1
			}
			seen[id] = true
			qf.gpuMap[i] = u
		}
	}
	e.nGPU = len(ids)
	e.nSer = e.nGPU * len(e.fields)
	n := e.nSer * e.q.Width
	e.sum = make([]float64, n)
	e.mn = make([]float64, n)
	e.mx = make([]float64, n)
	e.cnt = make([]uint32, n)
	if e.q.Stats {
		e.binSamples = make([]uint32, e.q.Width)
	}
	e.gpuIDs = ids
}

func (e *engine) binOf(t int64) int {
	hi, lo := bits.Mul64(uint64(t)-uint64(e.q.From), uint64(e.q.Width))
	q, _ := bits.Div64(hi, lo, e.span)
	return int(q)
}

func (e *engine) binStart(i int) int64 {
	hi, lo := bits.Mul64(uint64(i), e.span)
	q, r := bits.Div64(hi, lo, uint64(e.q.Width))
	if r > 0 {
		q++
	}
	return e.q.From + int64(q)
}

func (e *engine) runFile(ctx context.Context, qf *queryFile) error {
	e.cur = qf
	n, err := qf.f.Count()
	if err != nil {
		return err
	}
	lo, err := qf.f.LowerBound(e.q.From)
	if err != nil {
		return err
	}
	hi := n
	if e.q.To < maxTime {
		if hi, err = qf.f.LowerBound(e.q.To + 1); err != nil {
			return err
		}
	}
	if lo > 0 {
		if err := e.scanBack(ctx, qf, lo); err != nil {
			return err
		}
	}
	for i := lo; i < hi && !e.done; i += chunkRecords {
		if err := ctx.Err(); err != nil {
			return err
		}
		recs, err := qf.f.ReadIndexRange(i, min(i+chunkRecords, hi))
		if err != nil {
			return err
		}
		for k := range recs {
			e.handle(&recs[k])
		}
	}
	for i := hi; i < n && !e.done; i += scanRecords {
		if err := ctx.Err(); err != nil {
			return err
		}
		recs, err := qf.f.ReadIndexRange(i, min(i+scanRecords, n))
		if err != nil {
			return err
		}
		for k := range recs {
			e.handle(&recs[k])
		}
	}
	return nil
}

// scanBack 往前讀取範圍起點之前的記錄, 直到取得至少一筆取樣, 以得知範圍前最近的取樣與其後的事件.
func (e *engine) scanBack(ctx context.Context, qf *queryFile, lo int) error {
	for c := scanBackRecords; ; c *= 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := max(0, lo-c)
		recs, err := qf.f.ReadIndexRange(start, lo)
		if err != nil {
			return err
		}
		found := false
		for k := range recs {
			if recs[k].Kind == recording.KindSample {
				found = true
				break
			}
		}
		if found || start == 0 {
			for k := range recs {
				e.handle(&recs[k])
			}
			return nil
		}
	}
}

func (e *engine) handle(rec *recording.Record) {
	switch rec.Kind {
	case recording.KindError:
		if e.hasPrev && !e.hasErr {
			e.hasErr, e.errMsg = true, rec.Text
		}
	case recording.KindDisconnect:
		if e.hasPrev && !e.hasDisc {
			e.hasDisc, e.discMsg = true, rec.Text
		}
	case recording.KindSample:
		e.sample(rec)
	}
}

func (e *engine) sample(rec *recording.Record) {
	t := rec.Time
	gap := e.checkGap(rec)
	cur := prevSample{t: t, file: e.cur.order}
	if t >= e.q.From && t <= e.q.To {
		cur.inRange = true
		cur.bin = e.binOf(t)
		cur.raw = e.samples
		e.addSample(rec, cur.bin)
		if gap && e.prev.inRange {
			if e.rawOK && e.prev.raw == cur.raw-1 {
				e.rawBrk = append(e.rawBrk, e.prev.raw)
			}
			if cur.bin == e.prev.bin+1 {
				e.binBrk = append(e.binBrk, e.prev.bin)
			}
		}
	} else if t > e.q.To {
		e.done = true
	}
	e.prev, e.hasPrev = cur, true
	e.hasErr, e.hasDisc, e.errMsg, e.discMsg = false, false, "", ""
}

// checkGap 判定本筆與前一筆取樣之間是否為無資料時段, 是的話且與查詢範圍重疊時記錄之.
func (e *engine) checkGap(rec *recording.Record) bool {
	if !e.hasPrev || rec.Time < e.prev.t {
		return false
	}
	cross := e.prev.file != e.cur.order
	iv := int64(rec.IntervalMS)
	if iv == 0 {
		iv = defaultIntervalMS
	}
	dt := rec.Time - e.prev.t
	if !(dt > gapFactor*iv*1000 || (!cross && e.hasDisc)) {
		return false
	}
	g := Gap{Start: e.prev.t, End: rec.Time, Reason: GapUnknown}
	switch {
	case cross:
		g.Reason = GapNotConnected
	case e.hasErr:
		g.Reason, g.Message = GapError, e.errMsg
	case e.hasDisc:
		g.Reason, g.Message = GapDisconnect, e.discMsg
	}
	if g.End >= e.q.From && g.Start <= e.q.To {
		e.gaps = append(e.gaps, g)
	}
	return true
}

func fieldValue(g *recording.GPUValues, f Field) (float64, bool) {
	switch f {
	case FieldUtil:
		return float64(g.Util), g.Util != recording.NoValue8
	case FieldMemUsed:
		return float64(g.MemUsed), g.MemUsed != recording.NoValue64
	case FieldTemp:
		return float64(g.Temp), g.Temp != recording.NoValueI16
	case FieldPowerW:
		return float64(g.PowerW), g.PowerW != recording.NoValue16
	case FieldClockGPU:
		return float64(g.ClockGPU), g.ClockGPU != recording.NoValue16
	case FieldClockMem:
		return float64(g.ClockMem), g.ClockMem != recording.NoValue16
	case FieldFan:
		return float64(g.Fan), g.Fan != recording.NoValue8
	case FieldEnc:
		return float64(g.Enc), g.Enc != recording.NoValue8
	case FieldDec:
		return float64(g.Dec), g.Dec != recording.NoValue8
	}
	return 0, false
}

func (e *engine) addSample(rec *recording.Record, bin int) {
	e.samples++
	e.last = rec.Time
	if e.binSamples != nil {
		e.binSamples[bin]++
	}
	if e.rawOK && e.samples > e.q.Width {
		e.rawOK = false
		e.times, e.rows, e.rawBrk = nil, nil, nil
	}
	rowStart := 0
	if e.rawOK {
		e.times = append(e.times, rec.Time)
		rowStart = len(e.rows)
		for i := 0; i < e.nSer; i++ {
			e.rows = append(e.rows, math.NaN())
		}
	}
	nf := len(e.fields)
	w := e.q.Width
	for i := range rec.GPUs {
		u := e.cur.gpuMap[i]
		if u < 0 {
			continue
		}
		for fi, f := range e.fields {
			v, ok := fieldValue(&rec.GPUs[i], f)
			if !ok {
				continue
			}
			s := u*nf + fi
			if e.rawOK {
				e.rows[rowStart+s] = v
			}
			k := s*w + bin
			if e.cnt[k] == 0 {
				e.mn[k], e.mx[k] = v, v
			} else {
				e.mn[k] = math.Min(e.mn[k], v)
				e.mx[k] = math.Max(e.mx[k], v)
			}
			e.sum[k] += v
			e.cnt[k]++
		}
	}
}

func (e *engine) result(files []*queryFile) *Result {
	res := &Result{
		From: e.q.From, To: e.q.To, Width: e.q.Width, Samples: e.samples, Last: e.last,
		Binned: !e.rawOK, Breaks: []int{}, Gaps: e.gaps, GPUs: make([]GPUCurves, e.nGPU),
	}
	if res.Gaps == nil {
		res.Gaps = []Gap{}
	}
	nf := len(e.fields)
	for u := range res.GPUs {
		res.GPUs[u] = GPUCurves{ID: e.gpuIDs[u], Fields: make(map[Field]*Curve, nf)}
	}
	if e.rawOK {
		res.Times = append([]int64{}, e.times...)
		if e.rawBrk != nil {
			res.Breaks = e.rawBrk
		}
		k := len(e.times)
		for u := 0; u < e.nGPU; u++ {
			for fi, f := range e.fields {
				avg := make(Values, k)
				for r := 0; r < k; r++ {
					avg[r] = e.rows[r*e.nSer+u*nf+fi]
				}
				res.GPUs[u].Fields[f] = &Curve{Avg: avg}
			}
		}
		return res
	}
	w := e.q.Width
	res.Times = make([]int64, w)
	for i := range res.Times {
		res.Times[i] = e.binStart(i)
	}
	if e.binBrk != nil {
		res.Breaks = e.binBrk
	}
	if e.q.Stats {
		res.Counts = e.binSamples
	}
	for u := 0; u < e.nGPU; u++ {
		for fi, f := range e.fields {
			s := u*nf + fi
			c := &Curve{Avg: make(Values, w), Min: make(Values, w), Max: make(Values, w)}
			if e.q.Stats {
				c.Sum = Values(e.sum[s*w : (s+1)*w])
				c.Count = e.cnt[s*w : (s+1)*w]
			}
			for i := 0; i < w; i++ {
				k := s*w + i
				if e.cnt[k] == 0 {
					c.Avg[i], c.Min[i], c.Max[i] = math.NaN(), math.NaN(), math.NaN()
					continue
				}
				c.Avg[i] = e.sum[k] / float64(e.cnt[k])
				c.Min[i], c.Max[i] = e.mn[k], e.mx[k]
			}
			res.GPUs[u].Fields[f] = c
		}
	}
	return res
}

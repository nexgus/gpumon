package history

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"gpumon/internal/recording"
)

func TestRawBelowWidth(t *testing.T) {
	dir := t.TempDir()
	w := newRec(t, dir, "a@b", 0, 0, 1)
	for k := 0; k < 5; k++ {
		add(w, float64(k), 1000, int64(10*k), 0, 1)
	}
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+4*sec, 10, FieldUtil, FieldMemUsed)
	if r.Binned || r.Samples != 5 || len(r.Times) != 5 {
		t.Fatalf("result = %+v", r)
	}
	for k, tm := range r.Times {
		if tm != twall+int64(k)*sec {
			t.Fatalf("times[%d] = %d", k, tm)
		}
	}
	if len(r.GPUs) != 2 || r.GPUs[1].ID != 1 {
		t.Fatalf("gpus = %+v", r.GPUs)
	}
	util := r.GPUs[0].Fields[FieldUtil]
	if len(util.Avg) != 5 || util.Avg[3] != 30 || util.Min != nil || util.Max != nil {
		t.Fatalf("util = %+v", util)
	}
	mem := r.GPUs[0].Fields[FieldMemUsed]
	for _, v := range mem.Avg {
		if !nan(v) {
			t.Fatalf("mem should be blank: %v", mem.Avg)
		}
	}
	if len(r.GPUs[0].Fields) != 2 {
		t.Fatalf("fields = %v", r.GPUs[0].Fields)
	}
	b, err := json.Marshal(r.GPUs[0].Fields[FieldMemUsed])
	if err != nil || string(b) != `{"avg":[null,null,null,null,null]}` {
		t.Fatalf("json = %s, %v", b, err)
	}
	b, _ = json.Marshal(util)
	if string(b) != `{"avg":[0,10,20,30,40]}` {
		t.Fatalf("json = %s", b)
	}
	var back Curve
	if err := json.Unmarshal([]byte(`{"avg":[1,null,0]}`), &back); err != nil || back.Avg[0] != 1 || !nan(back.Avg[1]) || back.Avg[2] != 0 {
		t.Fatalf("unmarshal = %+v, %v", back, err)
	}
}

func TestWidthEqualsSamplesIsRaw(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for k := 0; k < 4; k++ {
		add(w, float64(k), 1000, 1, 0)
	}
	flush(t, w)
	if r := query(t, []string{w.Path()}, twall, twall+10*sec, 4); r.Binned {
		t.Fatal("4 samples on width 4 should be raw")
	}
	if r := query(t, []string{w.Path()}, twall, twall+10*sec, 3); !r.Binned {
		t.Fatal("4 samples on width 3 should be binned")
	}
}

func TestBinnedStats(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for k := 0; k < 10; k++ {
		add(w, float64(k), 1000, int64(10*(k+1)), 0)
	}
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+10*sec-1, 5, FieldUtil)
	if !r.Binned || r.Samples != 10 || len(r.Times) != 5 {
		t.Fatalf("result = %+v", r)
	}
	c := r.GPUs[0].Fields[FieldUtil]
	wantAvg := []float64{15, 35, 55, 75, 95}
	wantMin := []float64{10, 30, 50, 70, 90}
	wantMax := []float64{20, 40, 60, 80, 100}
	for i := range wantAvg {
		if c.Avg[i] != wantAvg[i] || c.Min[i] != wantMin[i] || c.Max[i] != wantMax[i] {
			t.Fatalf("bin %d = %v %v %v", i, c.Avg[i], c.Min[i], c.Max[i])
		}
		if r.Times[i] != twall+int64(i)*2*sec {
			t.Fatalf("times[%d] = %d", i, r.Times[i])
		}
	}
}

func TestSpikeSurvivesBinning(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for k := 0; k < 2000; k++ {
		u := int64(0)
		if k == 1234 {
			u = 99
		}
		add(w, float64(k)*0.1, 100, u, 0)
	}
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+200*sec, 50, FieldUtil)
	c := r.GPUs[0].Fields[FieldUtil]
	maxMax, maxAvg := 0.0, 0.0
	for i := range c.Max {
		maxMax = math.Max(maxMax, c.Max[i])
		maxAvg = math.Max(maxAvg, c.Avg[i])
	}
	if maxMax != 99 || maxAvg >= 99 || len(r.Gaps) != 0 {
		t.Fatalf("max=%v avg=%v gaps=%v", maxMax, maxAvg, r.Gaps)
	}
}

func TestEmptyBinsAreBlank(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for _, rel := range []float64{0, 1, 2, 8, 9, 10} {
		add(w, rel, 1000, 50, 0)
	}
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+10*sec, 5, FieldUtil)
	if !r.Binned {
		t.Fatal("expected binned")
	}
	c := r.GPUs[0].Fields[FieldUtil]
	// 範圍長 10 秒 + 1 微秒, 每個像素範圍略長於 2 秒: 第 0 個含 0, 1, 2 秒, 第 1 與第 2 個為空, 第 3 個含 8 秒, 第 4 個含 9 與 10 秒.
	blank := 0
	for i := range c.Avg {
		if nan(c.Avg[i]) {
			blank++
			if !nan(c.Min[i]) || !nan(c.Max[i]) {
				t.Fatalf("bin %d partially blank", i)
			}
		}
	}
	if blank != 2 || !nan(c.Avg[1]) || !nan(c.Avg[2]) {
		t.Fatalf("avg = %v", c.Avg)
	}
	b, _ := json.Marshal(c.Avg)
	if !strings.Contains(string(b), "null") {
		t.Fatalf("json = %s", b)
	}
	if len(r.Gaps) != 1 || r.Gaps[0].Start != twall+2*sec || r.Gaps[0].End != twall+8*sec {
		t.Fatalf("gaps = %+v", r.Gaps)
	}
}

func TestNoValueExcludedFromStats(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	mem := func(v uint64) *uint64 { return &v }
	// 前兩筆同一像素範圍: 第一筆使用率有值記憶體無值, 第二筆兩者皆有值.
	w.AddSample(ts0+0, 1000, []recording.GPUSample{{ID: 0, Util: iv(40)}})
	w.AddSample(ts0+1, 1000, []recording.GPUSample{{ID: 0, Util: iv(60), MemUsed: mem(1000)}})
	// 後兩筆同一像素範圍: 全無值.
	w.AddSample(ts0+2, 1000, []recording.GPUSample{{ID: 0}})
	w.AddSample(ts0+3, 1000, []recording.GPUSample{{ID: 0}})
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+4*sec-1, 2, FieldUtil, FieldMemUsed)
	if !r.Binned {
		t.Fatal("expected binned")
	}
	u, m := r.GPUs[0].Fields[FieldUtil], r.GPUs[0].Fields[FieldMemUsed]
	if u.Avg[0] != 50 || u.Min[0] != 40 || u.Max[0] != 60 {
		t.Fatalf("util bin0 = %v %v %v", u.Avg[0], u.Min[0], u.Max[0])
	}
	if m.Avg[0] != 1000 || m.Min[0] != 1000 || m.Max[0] != 1000 {
		t.Fatalf("mem bin0 = %v %v %v", m.Avg[0], m.Min[0], m.Max[0])
	}
	if !nan(u.Avg[1]) || !nan(m.Avg[1]) || !nan(u.Min[1]) || !nan(m.Max[1]) {
		t.Fatalf("bin1 should be blank: %v %v", u.Avg, m.Avg)
	}
}

// Stats 為真時, 降採樣的結果另附各像素範圍的取樣筆數與各曲線的總和及筆數, 平均值即為
// 總和除以筆數; 原始取樣點與未要求時不附.
func TestQueryStats(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	mem := func(v uint64) *uint64 { return &v }
	w.AddSample(ts0+0, 1000, []recording.GPUSample{{ID: 0, Util: iv(40)}})
	w.AddSample(ts0+1, 1000, []recording.GPUSample{{ID: 0, Util: iv(60), MemUsed: mem(1000)}})
	w.AddSample(ts0+2, 1000, []recording.GPUSample{{ID: 0}})
	w.AddSample(ts0+3, 1000, []recording.GPUSample{{ID: 0, Util: iv(7)}})
	w.AddSample(ts0+6, 1000, []recording.GPUSample{{ID: 0, Util: iv(9)}})
	flush(t, w)
	paths := []string{w.Path()}
	q := Query{From: twall, To: twall + 4*sec - 1, Width: 2, Fields: []Field{FieldUtil, FieldMemUsed}, Stats: true}
	r, err := QueryRange(t.Context(), paths, q)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Binned || r.Samples != 4 || r.Last != twall+3*sec {
		t.Fatalf("result = %+v", r)
	}
	if len(r.Counts) != 2 || r.Counts[0] != 2 || r.Counts[1] != 2 {
		t.Fatalf("counts = %v", r.Counts)
	}
	u, m := r.GPUs[0].Fields[FieldUtil], r.GPUs[0].Fields[FieldMemUsed]
	if u.Sum[0] != 100 || u.Count[0] != 2 || u.Sum[1] != 7 || u.Count[1] != 1 {
		t.Fatalf("util sum %v count %v", u.Sum, u.Count)
	}
	if m.Sum[0] != 1000 || m.Count[0] != 1 || m.Sum[1] != 0 || m.Count[1] != 0 || !nan(m.Avg[1]) {
		t.Fatalf("mem sum %v count %v avg %v", m.Sum, m.Count, m.Avg)
	}
	for i := range u.Avg {
		if u.Avg[i] != u.Sum[i]/float64(u.Count[i]) {
			t.Fatalf("avg %v differs from sum/count", u.Avg)
		}
	}
	b, _ := json.Marshal(u)
	if string(b) != `{"avg":[50,7],"min":[40,7],"max":[60,7],"sum":[100,7],"count":[2,1]}` {
		t.Fatalf("json = %s", b)
	}

	q.Stats = false
	r, _ = QueryRange(t.Context(), paths, q)
	if r.Counts != nil || r.GPUs[0].Fields[FieldUtil].Sum != nil || r.GPUs[0].Fields[FieldUtil].Count != nil {
		t.Fatalf("stats without request: %+v", r)
	}
	q.Stats, q.Width = true, 10
	r, _ = QueryRange(t.Context(), paths, q)
	if r.Binned || r.Counts != nil || r.GPUs[0].Fields[FieldUtil].Sum != nil || r.Last != twall+3*sec {
		t.Fatalf("raw result with stats: %+v", r)
	}
	r, _ = QueryRange(t.Context(), paths, Query{From: twall + 4*sec, To: twall + 5*sec, Width: 10, Stats: true})
	if r.Samples != 0 || r.Last != 0 {
		t.Fatalf("empty range: %+v", r)
	}
}

func TestIrregularSamplesAndIntervalChange(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	rels := []float64{0, 0.2, 0.4, 5.4, 10.4, 15.4}
	for k, rel := range rels {
		interval := 200
		if k >= 3 {
			interval = 5000
		}
		add(w, rel, interval, int64(k), 0)
	}
	// 間隔 200 ms 時, 0.4 秒後下一筆在 1.4 秒處, 差 1 秒大於 0.6 秒.
	add(w, 16.4, 200, 6, 0)
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+20*sec, 100, FieldUtil)
	if r.Binned || len(r.Times) != 7 {
		t.Fatalf("result = %+v", r)
	}
	for k, rel := range append(rels, 16.4) {
		want := twall + int64(math.Round(rel*1e6))
		if r.Times[k] != want {
			t.Fatalf("times[%d] = %d, want %d", k, r.Times[k], want)
		}
	}
	// 0.4 到 5.4 的後一筆間隔為 5000 ms, 不是無資料; 15.4 到 16.4 的後一筆間隔為 200 ms, 是無資料.
	if len(r.Gaps) != 1 || r.Gaps[0].Start != twall+int64(15.4e6) || r.Gaps[0].Reason != GapUnknown {
		t.Fatalf("gaps = %+v", r.Gaps)
	}
	if len(r.Breaks) != 1 || r.Breaks[0] != 5 {
		t.Fatalf("breaks = %v", r.Breaks)
	}
}

func TestGapReasonsAndMultiFile(t *testing.T) {
	dir := t.TempDir()
	a := newRec(t, dir, "a@b", 0, 0, 1)
	add(a, 0, 1000, 1, 0, 1)
	add(a, 1, 1000, 1, 0, 1)
	a.AddError(ts0+1.5, "nvml timeout")
	add(a, 10, 1000, 1, 0, 1) // 有錯誤的時段
	add(a, 11, 1000, 1, 0, 1)
	a.AddDisconnect(twall+int64(11.2e6), "ssh: connection reset")
	add(a, 11.5, 1000, 1, 0, 1) // 時刻差小, 但有斷線記錄
	add(a, 12, 1000, 1, 0, 1)
	add(a, 20, 1000, 1, 0, 1) // 原因不明
	flush(t, a)
	b := newRec(t, dir, "a@b", 30*sec, 1, 2) // GPU 0 消失, GPU 2 出現
	add(b, 0, 1000, 7, 1, 2)
	add(b, 1, 1000, 7, 1, 2)
	flush(t, b)

	// 路徑順序顛倒, 結果仍依時刻處理.
	r := query(t, []string{b.Path(), a.Path()}, twall, twall+40*sec, 100, FieldUtil)
	if r.Binned || r.Samples != 9 {
		t.Fatalf("samples = %d binned = %v", r.Samples, r.Binned)
	}
	want := []Gap{
		{Start: twall + sec, End: twall + 10*sec, Reason: GapError, Message: "nvml timeout"},
		{Start: twall + 11*sec, End: twall + int64(11.5e6), Reason: GapDisconnect, Message: "ssh: connection reset"},
		{Start: twall + 12*sec, End: twall + 20*sec, Reason: GapUnknown},
		{Start: twall + 20*sec, End: twall + 30*sec, Reason: GapNotConnected},
	}
	if len(r.Gaps) != len(want) {
		t.Fatalf("gaps = %+v", r.Gaps)
	}
	for i := range want {
		if r.Gaps[i] != want[i] {
			t.Fatalf("gap %d = %+v, want %+v", i, r.Gaps[i], want[i])
		}
	}
	// 取樣點 0..8 依序為: a 的 0,1,10,11,11.5,12,20 與 b 的 0,1; 斷點在每個無資料時段之前的點.
	wantBreaks := []int{1, 3, 5, 6}
	if len(r.Breaks) != len(wantBreaks) {
		t.Fatalf("breaks = %v", r.Breaks)
	}
	for i := range wantBreaks {
		if r.Breaks[i] != wantBreaks[i] {
			t.Fatalf("breaks = %v, want %v", r.Breaks, wantBreaks)
		}
	}
	if len(r.GPUs) != 3 || r.GPUs[0].ID != 0 || r.GPUs[1].ID != 1 || r.GPUs[2].ID != 2 {
		t.Fatalf("gpus = %+v", r.GPUs)
	}
	g0, g2 := r.GPUs[0].Fields[FieldUtil].Avg, r.GPUs[2].Fields[FieldUtil].Avg
	if len(g0) != 9 || g0[0] != 1 || !nan(g0[7]) || !nan(g2[0]) || g2[7] != 7 || r.GPUs[1].Fields[FieldUtil].Avg[8] != 7 {
		t.Fatalf("g0 = %v g2 = %v", g0, g2)
	}
}

func TestGapSurvivesBinning(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	// 兩個叢集, 無資料時段恰橫跨兩個相鄰像素範圍的交界.
	for k := 0; k < 40; k++ {
		add(w, float64(k)*0.1, 100, 10, 0)
	}
	for k := 0; k < 40; k++ {
		add(w, 4.0+0.45+float64(k)*0.1, 100, 20, 0)
	}
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+8*sec, 8, FieldUtil)
	if !r.Binned || len(r.Gaps) != 1 {
		t.Fatalf("binned=%v gaps=%+v", r.Binned, r.Gaps)
	}
	c := r.GPUs[0].Fields[FieldUtil]
	g := r.Gaps[0]
	// 每個像素範圍 1 秒. 間隙 3.9 到 4.45 秒, 相鄰的第 3 與第 4 個像素範圍皆有資料, 須列入斷點.
	if nan(c.Avg[3]) || nan(c.Avg[4]) || g.Start != twall+3900000 {
		t.Fatalf("avg = %v gap = %+v", c.Avg, g)
	}
	if len(r.Breaks) != 1 || r.Breaks[0] != 3 {
		t.Fatalf("breaks = %v", r.Breaks)
	}
}

func TestRangeOutsideAndBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	a := newRec(t, dir, "a@b", 0, 0)
	add(a, 0, 1000, 1, 0)
	add(a, 1, 1000, 1, 0)
	flush(t, a)
	b := newRec(t, dir, "a@b", 100*sec, 0)
	add(b, 0, 1000, 2, 0)
	add(b, 1, 1000, 2, 0)
	flush(t, b)
	paths := []string{a.Path(), b.Path()}

	// 完全在所有檔案之前與之後.
	for _, rg := range [][2]int64{{twall - 50*sec, twall - 10*sec}, {twall + 200*sec, twall + 300*sec}} {
		r := query(t, paths, rg[0], rg[1], 10)
		if r.Samples != 0 || len(r.Times) != 0 || len(r.Gaps) != 0 || r.Binned {
			t.Fatalf("outside range = %+v", r)
		}
		if _, err := json.Marshal(r); err != nil {
			t.Fatal(err)
		}
	}
	// 位於兩檔之間: 範圍內沒有取樣, 但整段為未連線.
	r := query(t, paths, twall+20*sec, twall+80*sec, 10)
	if r.Samples != 0 || len(r.Gaps) != 1 || r.Gaps[0].Reason != GapNotConnected ||
		r.Gaps[0].Start != twall+sec || r.Gaps[0].End != twall+100*sec {
		t.Fatalf("between = %+v", r)
	}
	// 範圍起點落在無資料時段內, 只含後檔的取樣.
	r = query(t, paths, twall+50*sec, twall+200*sec, 10, FieldUtil)
	if r.Samples != 2 || len(r.Gaps) != 1 || r.Gaps[0].Start != twall+sec {
		t.Fatalf("straddle = %+v", r)
	}
	// 範圍終點落在無資料時段內, 只含前檔的取樣.
	r = query(t, paths, twall-10*sec, twall+50*sec, 10, FieldUtil)
	if r.Samples != 2 || len(r.Gaps) != 1 || r.Gaps[0].End != twall+100*sec {
		t.Fatalf("straddle end = %+v", r)
	}
}

func TestPartialRangeInsideFile(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for k := 0; k < 100; k++ {
		add(w, float64(k), 1000, int64(k), 0)
	}
	flush(t, w)
	r := query(t, []string{w.Path()}, twall+40*sec, twall+49*sec, 100, FieldUtil)
	if r.Samples != 10 || r.Times[0] != twall+40*sec || r.Times[9] != twall+49*sec || len(r.Gaps) != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestLiveFileIsQueryable(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	add(w, 0, 1000, 1, 0)
	add(w, 1, 1000, 1, 0)
	flush(t, w)
	r := query(t, []string{w.Path()}, twall, twall+100*sec, 10)
	if r.Samples != 2 {
		t.Fatalf("samples = %d", r.Samples)
	}
	add(w, 2, 1000, 1, 0)
	flush(t, w)
	if r = query(t, []string{w.Path()}, twall, twall+100*sec, 10); r.Samples != 3 {
		t.Fatalf("samples = %d", r.Samples)
	}
}

func TestQueryValidation(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	add(w, 0, 1000, 1, 0)
	flush(t, w)
	p := []string{w.Path()}
	for _, q := range []Query{
		{From: 0, To: 10, Width: 0},
		{From: 0, To: 10, Width: MaxWidth + 1},
		{From: 10, To: 0, Width: 10},
		{From: 0, To: 10, Width: 10, Fields: []Field{"bogus"}},
	} {
		if _, err := QueryRange(t.Context(), p, q); err == nil {
			t.Fatalf("query %+v should fail", q)
		}
	}
	if _, err := QueryRange(t.Context(), []string{p[0] + ".missing"}, Query{From: 0, To: 10, Width: 10}); err == nil {
		t.Fatal("missing file should fail")
	}
	// 全範圍.
	r, err := QueryRange(t.Context(), p, Query{From: math.MinInt64 / 2, To: math.MaxInt64, Width: 10})
	if err != nil || r.Samples != 1 {
		t.Fatalf("full range = %+v, %v", r, err)
	}
	ctx, cancel := context_cancelled()
	defer cancel()
	if _, err := QueryRange(ctx, p, Query{From: twall, To: twall + sec, Width: 10}); err == nil {
		t.Fatal("cancelled context should fail")
	}
}

func TestQueryNotBefore(t *testing.T) {
	dir := t.TempDir()
	w := newRec(t, dir, "a@b", 0, 0)
	add(w, 0, 1000, 1, 0)
	add(w, 1, 1000, 2, 0)
	w.AddError(ts0+1.5, "nvml timeout")
	w.AddDisconnect(twall+int64(10e6), "lost")
	w.BeginConnection(twall+20*sec, ts0+100)
	add(w, 100, 1000, 3, 0)
	add(w, 101, 1000, 4, 0)
	flush(t, w)

	run := func(from, to, notBefore int64) *Result {
		t.Helper()
		r, err := QueryRange(context.Background(), []string{w.Path()}, Query{From: from, To: to, Width: 100, Fields: []Field{FieldUtil}, NotBefore: notBefore})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	all := run(twall, twall+30*sec, 0)
	if all.Samples != 4 || len(all.Gaps) != 1 {
		t.Fatalf("without a floor: %d samples, gaps %+v", all.Samples, all.Gaps)
	}
	for _, from := range []int64{twall - 5*sec, twall + 20*sec, twall + 20*sec + 1} {
		r := run(from, twall+30*sec, twall+20*sec)
		wantSamples := 2
		if from > twall+20*sec {
			wantSamples = 1
		}
		if r.Samples != wantSamples || len(r.Gaps) != 0 || r.From != from {
			t.Fatalf("from %d: %d samples, gaps %+v", from, r.Samples, r.Gaps)
		}
	}
}

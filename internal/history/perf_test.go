package history

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpumon/internal/recording"
)

const worstSamples = 432000

// worstCaseFile 產生 24 小時, 取樣間隔 200 ms, 8 張 GPU 的記錄檔.
func worstCaseFile(tb testing.TB) string {
	tb.Helper()
	ids := []int{0, 1, 2, 3, 4, 5, 6, 7}
	w, err := recording.Create(tb.TempDir(), recording.Header{Host: "a@b", TWall0: twall, TS0: ts0, Devices: devices(ids...)})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { w.Close() })
	gs := make([]recording.GPUSample, len(ids))
	var mem uint64
	for k := 0; k < worstSamples; k++ {
		for i := range gs {
			mem = uint64(k%977+i) << 20
			gs[i] = recording.GPUSample{ID: i, Util: iv(int64((k + i*13) % 101)), Temp: iv(int64(40 + k%50)),
				PowerW: iv(int64(100 + k%300)), ClockGPU: iv(1500), ClockMem: iv(9000), Fan: iv(30), Enc: iv(0), Dec: iv(0), MemUsed: &mem}
		}
		w.AddSample(ts0+float64(k)*0.2, 200, gs)
		if k%1000 == 999 {
			if err := w.Flush(); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	return w.Path()
}

func BenchmarkWorstCase(b *testing.B) {
	path := worstCaseFile(b)
	q := Query{From: twall, To: twall + 24*3600*sec, Width: 1920}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := QueryRange(b.Context(), []string{path}, q)
		if err != nil || r.Samples != worstSamples {
			b.Fatalf("samples = %v, %v", r, err)
		}
	}
}

// 以背景 goroutine 取樣堆積用量的峰值, 確認不隨取樣筆數成長.
func TestWorstCaseMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("large file")
	}
	path := worstCaseFile(t)
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak.Load() {
				peak.Store(ms.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	r := query(t, []string{path}, twall, twall+24*3600*sec, 1920)
	elapsed := time.Since(start)
	close(stop)
	wg.Wait()
	if !r.Binned || r.Samples != worstSamples || len(r.Times) != 1920 || len(r.Gaps) != 0 {
		t.Fatalf("result: binned=%v samples=%d gaps=%d", r.Binned, r.Samples, len(r.Gaps))
	}
	grown := int64(peak.Load()) - int64(base.HeapAlloc)
	t.Logf("worst case: %v elapsed, peak heap growth %.1f MiB (file %d MiB)", elapsed, float64(grown)/(1<<20), worstSamples*(11+160)>>20)
	if grown > 48<<20 {
		t.Fatalf("peak heap growth %d MiB too large", grown>>20)
	}
}

// liveGrid 回傳監看畫面於顯示範圍 [to - span, to] 所用的對齊絕對時刻的像素範圍, 規則與
// frontend/src/monitor/liveCurves.ts 的 gridFor 相同: 每個像素範圍長 bin 微秒, 第 k 個
// 為 [k * bin, (k + 1) * bin - 1].
func liveGrid(to, span int64, width int) (bin, first, last int64) {
	bin = max(1, (span+int64(width)-1)/int64(width))
	return bin, (to - span) / bin, to / bin
}

// liveTail 回傳監看畫面於最新一筆取樣到來時所做的增量查詢: 自前一筆取樣之後到最後一個
// 像素範圍的結尾, 只含新取樣.
func liveTail(prev, to, span int64, width int) Query {
	bin, _, last := liveGrid(to, span, width)
	return Query{From: prev + 1, To: (last+1)*bin - 1, Width: 32}
}

// 監看畫面每筆取樣的更新只查詢新取樣, 其成本與顯示範圍長度無關. 以 24 小時, 8 張 GPU,
// 200 ms 的記錄檔比較顯示範圍為 1 分鐘, 1 小時與 24 小時時的增量查詢, 並與重新查詢整段
// 24 小時比較.
func TestLiveTailCost(t *testing.T) {
	if testing.Short() {
		t.Skip("large file")
	}
	path := worstCaseFile(t)
	paths := []string{path}
	const width = 1920
	lastT := twall + int64(worstSamples-1)*200_000
	prevT := lastT - 200_000

	timeIt := func(q Query, n int) time.Duration {
		start := time.Now()
		for i := 0; i < n; i++ {
			r, err := QueryRange(t.Context(), paths, q)
			if err != nil {
				t.Fatal(err)
			}
			if q.Width == 32 && (r.Binned || r.Samples != 1 || r.Times[0] != lastT) {
				t.Fatalf("tail result: binned=%v samples=%d", r.Binned, r.Samples)
			}
		}
		return time.Since(start) / time.Duration(n)
	}

	bin, first, last := liveGrid(lastT, 24*3600*sec, width)
	full := timeIt(Query{From: first * bin, To: (last+1)*bin - 1, Width: int(last - first + 1), Stats: true}, 1)
	var tails []time.Duration
	for _, span := range []int64{60 * sec, 3600 * sec, 24 * 3600 * sec} {
		d := timeIt(liveTail(prevT, lastT, span, width), 200)
		tails = append(tails, d)
		t.Logf("span %v: tail query %v per sample", time.Duration(span)*time.Microsecond, d)
	}
	t.Logf("full 24 h query %v", full)
	for _, d := range tails {
		if d*20 > full {
			t.Fatalf("tail query %v is not much cheaper than the full query %v", d, full)
		}
	}
	if tails[2] > 4*tails[0]+time.Millisecond {
		t.Fatalf("tail query grows with the span: %v", tails)
	}
}

func BenchmarkLiveTail(b *testing.B) {
	path := worstCaseFile(b)
	lastT := twall + int64(worstSamples-1)*200_000
	for _, span := range []int64{60 * sec, 3600 * sec, 24 * 3600 * sec} {
		b.Run(time.Duration(span*1000).String(), func(b *testing.B) {
			q := liveTail(lastT-200_000, lastT, span, 1920)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if r, err := QueryRange(b.Context(), []string{path}, q); err != nil || r.Samples != 1 {
					b.Fatalf("result %v, %v", r, err)
				}
			}
		})
	}
}

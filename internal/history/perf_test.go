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

package history

import (
	"context"
	"encoding/json"
	"testing"

	"gpumon/internal/recording"
)

// TestQueryRangePending 確認尚未存檔的記錄接續於檔尾查詢時, 結果與全部存檔後相同.
func TestQueryRangePending(t *testing.T) {
	for _, flushed := range []int{0, 3, 6} {
		dir := t.TempDir()
		w := newRec(t, dir, "alice@gpu01", 0, 0, 1)
		var pending []recording.Record
		for i := 0; i < 6; i++ {
			rel := float64(i)
			if i == 4 {
				rel = 20
				w.AddError(ts0+10, "nvml timeout")
				if i >= flushed {
					pending = append(pending, recording.Record{Kind: recording.KindError, Time: twall + 10*sec, Text: "nvml timeout"})
				}
			}
			tm := add(w, rel, 1000, int64(i*10), 0, 1)
			if i == flushed-1 {
				flush(t, w)
			}
			if i >= flushed {
				v := uint8(i * 10)
				pending = append(pending, recording.Record{Kind: recording.KindSample, Time: tm, IntervalMS: 1000, GPUs: []recording.GPUValues{
					{ID: 0, Util: v, Fan: 255, Enc: 255, Dec: 255, Temp: 32767, PowerW: 65535, ClockGPU: 65535, ClockMem: 65535, MemUsed: recording.NoValue64},
					{ID: 1, Util: v, Fan: 255, Enc: 255, Dec: 255, Temp: 32767, PowerW: 65535, ClockGPU: 65535, ClockMem: 65535, MemUsed: recording.NoValue64},
				}})
			}
		}
		path := w.Path()
		for _, width := range []int{100, 3} {
			q := Query{From: twall - 5*sec, To: twall + 30*sec, Width: width}
			got, err := QueryRangePending(context.Background(), []string{path}, []Pending{{Path: path, Records: pending}}, q)
			if err != nil {
				t.Fatal(err)
			}
			if flushed == 0 && got.Samples != 6 {
				t.Fatalf("unsaved samples not queried: %d", got.Samples)
			}
			// 與全部存檔後的結果比對.
			dir2 := t.TempDir()
			w2 := newRec(t, dir2, "alice@gpu01", 0, 0, 1)
			for i := 0; i < 6; i++ {
				rel := float64(i)
				if i == 4 {
					rel = 20
					w2.AddError(ts0+10, "nvml timeout")
				}
				add(w2, rel, 1000, int64(i*10), 0, 1)
			}
			flush(t, w2)
			want, err := QueryRange(context.Background(), []string{w2.Path()}, q)
			if err != nil {
				t.Fatal(err)
			}
			jg, _ := json.Marshal(got)
			jw, _ := json.Marshal(want)
			if string(jg) != string(jw) {
				t.Fatalf("flushed %d width %d:\ngot  %s\nwant %s", flushed, width, jg, jw)
			}
		}
	}
}

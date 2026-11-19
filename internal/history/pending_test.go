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
				v := int64(i * 10)
				pending = append(pending, recording.Record{Kind: recording.KindSample, Time: tm, IntervalMS: 1000,
					GPUs: recording.SampleValues([]int{0, 1}, []recording.GPUSample{{ID: 0, Util: iv(v)}, {ID: 1, Util: iv(v)}})})
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

// TestQueryRangePendingTail 確認查詢範圍只涵蓋尚未存檔的取樣時, 仍往前讀取已存檔的區塊以判定
// 無資料時段, 結果與全部存檔後相同.
func TestQueryRangePendingTail(t *testing.T) {
	write := func(w *recording.Writer, flushAll bool) []recording.Record {
		var pending []recording.Record
		for i, rel := range []float64{0, 1, 2, 10, 11} {
			tm := addProcs(w, rel, 1000, int64(i), procs(0, i+1, "x"), 0)
			if i == 1 || i == 2 || flushAll {
				flush(t, w)
			}
			if i >= 3 {
				pending = append(pending, recording.Record{Kind: recording.KindSample, Time: tm, IntervalMS: 1000,
					GPUs: recording.SampleValues([]int{0}, []recording.GPUSample{{ID: 0, Util: iv(int64(i))}})})
			}
		}
		return pending
	}
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	pending := write(w, false)
	w2 := newRec(t, t.TempDir(), "a@b", 0, 0)
	write(w2, true)
	for _, q := range []Query{
		{From: twall + 10*sec, To: twall + 20*sec, Width: 100},
		{From: twall + 3*sec, To: twall + 20*sec, Width: 1},
		{From: twall + 11*sec + 1, To: twall + 20*sec, Width: 32},
	} {
		got, err := QueryRangePending(context.Background(), []string{w.Path()}, []Pending{{Path: w.Path(), Records: pending}}, q)
		if err != nil {
			t.Fatal(err)
		}
		want, err := QueryRange(context.Background(), []string{w2.Path()}, q)
		if err != nil {
			t.Fatal(err)
		}
		jg, _ := json.Marshal(got)
		jw, _ := json.Marshal(want)
		if string(jg) != string(jw) {
			t.Fatalf("query %+v:\ngot  %s\nwant %s", q, jg, jw)
		}
	}
	if got, _ := QueryRangePending(context.Background(), []string{w.Path()}, []Pending{{Path: w.Path(), Records: pending}},
		Query{From: twall + 10*sec, To: twall + 20*sec, Width: 100}); len(got.Gaps) != 1 || got.Gaps[0].Start != twall+2*sec {
		t.Fatalf("gap before the unsaved samples: %+v", got.Gaps)
	}
}

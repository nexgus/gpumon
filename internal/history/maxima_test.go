package history

import (
	"reflect"
	"testing"

	"gpumon/internal/recording"
)

func runsOf(rel ...int64) [][2]int64 {
	var out [][2]int64
	for i := 0; i+1 < len(rel); i += 2 {
		out = append(out, [2]int64{twall + rel[i]*sec, twall + rel[i+1]*sec})
	}
	return out
}

func utilOnly(id int, u int64) recording.GPUSample {
	return recording.GPUSample{ID: id, Util: iv(u)}
}

func fieldOf(t *testing.T, r *MaximaResult, gpu int, f Field) *FieldMax {
	t.Helper()
	for _, g := range r.GPUs {
		if g.ID == gpu {
			return g.Fields[f]
		}
	}
	t.Fatalf("gpu %d missing", gpu)
	return nil
}

func TestMaximaRunsAndBreaks(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0, 1)
	// 使用率 5 連續兩筆, 數值不同而斷開, 再出現一筆, 無值斷開, 再出現, 斷線記錄斷開, 再出現.
	seq := []struct {
		rel  float64
		util *int64
	}{
		{0, iv(5)}, {1, iv(5)}, {2, iv(4)}, {3, iv(5)}, {4, nil}, {5, iv(5)}, {6, iv(5)},
	}
	for _, s := range seq {
		w.AddSample(ts0+s.rel, 1000, []recording.GPUSample{{ID: 0, Util: s.util}, utilOnly(1, 1)})
	}
	w.AddDisconnect(twall+6*sec+500000, "lost")
	w.AddSample(ts0+7, 1000, []recording.GPUSample{utilOnly(0, 5), utilOnly(1, 1)})
	w.AddSample(ts0+8, 1000, []recording.GPUSample{utilOnly(0, 5), utilOnly(1, 1)})
	flush(t, w)
	r, err := Maxima(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.GPUs) != 2 || r.GPUs[0].ID != 0 || r.GPUs[1].ID != 1 {
		t.Fatalf("gpus = %+v", r.GPUs)
	}
	u := fieldOf(t, r, 0, FieldUtil)
	want := runsOf(0, 1, 3, 3, 5, 6, 7, 8)
	if u.Value != 5 || !reflect.DeepEqual(u.Runs, want) {
		t.Fatalf("util = %v %v, want 5 %v", u.Value, u.Runs, want)
	}
	u1 := fieldOf(t, r, 1, FieldUtil)
	if u1.Value != 1 || !reflect.DeepEqual(u1.Runs, runsOf(0, 6, 7, 8)) {
		t.Fatalf("gpu1 util = %+v", u1)
	}
	if _, ok := r.GPUs[0].Fields[FieldTemp]; ok {
		t.Fatal("temp without values should be absent")
	}
	if len(r.GPUs[0].Fields) != 1 {
		t.Fatalf("fields = %v", r.GPUs[0].Fields)
	}
}

func TestMaximaReplacedByLarger(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for i, u := range []int64{3, 3, 2, 3, 7, 7, 1, 7, 6} {
		w.AddSample(ts0+float64(i), 1000, []recording.GPUSample{utilOnly(0, u)})
	}
	flush(t, w)
	r, err := Maxima(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	u := fieldOf(t, r, 0, FieldUtil)
	if u.Value != 7 || !reflect.DeepEqual(u.Runs, runsOf(4, 5, 7, 7)) {
		t.Fatalf("util = %+v", u)
	}
}

func TestMaximaAllFields(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	mem := func(v uint64) *uint64 { return &v }
	w.AddSample(ts0, 1000, []recording.GPUSample{{ID: 0, Util: iv(1), MemUsed: mem(900), Temp: iv(-5),
		Fan: iv(30), PowerW: iv(100), ClockGPU: iv(2000), ClockMem: iv(9000)}})
	w.AddSample(ts0+1, 1000, []recording.GPUSample{{ID: 0, Util: iv(2), MemUsed: mem(800), Temp: iv(-4),
		Fan: iv(30), PowerW: iv(90), ClockGPU: iv(2100), ClockMem: iv(9000)}})
	flush(t, w)
	r, err := Maxima(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[Field]float64{FieldUtil: 2, FieldMemUsed: 900, FieldTemp: -4, FieldFan: 30,
		FieldPowerW: 100, FieldClockGPU: 2100, FieldClockMem: 9000}
	for f, v := range cases {
		if m := fieldOf(t, r, 0, f); m == nil || m.Value != v {
			t.Fatalf("%s = %+v, want %v", f, m, v)
		}
	}
	if m := fieldOf(t, r, 0, FieldFan); !reflect.DeepEqual(m.Runs, runsOf(0, 1)) {
		t.Fatalf("fan runs = %v", m.Runs)
	}
}

func TestMaximaEmptyAndMissing(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	flush(t, w)
	r, err := Maxima(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.GPUs) != 1 || len(r.GPUs[0].Fields) != 0 {
		t.Fatalf("result = %+v", r)
	}
	if _, err := Maxima(t.TempDir() + "/none.gpurec"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSampleAt(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0, 1)
	mem := func(v uint64) *uint64 { return &v }
	w.AddSample(ts0, 1000, []recording.GPUSample{
		{ID: 0, Util: iv(95), MemUsed: mem(88691000000), Temp: iv(58), Fan: iv(34), PowerW: iv(160), ClockGPU: iv(2872), ClockMem: iv(13365)},
		{ID: 1, Util: iv(3)},
	})
	w.AddSample(ts0+1, 1000, []recording.GPUSample{utilOnly(0, 10), utilOnly(1, 20)})
	w.AddDisconnect(twall+1*sec+100000, "lost")
	w.AddSample(ts0+10, 1000, []recording.GPUSample{utilOnly(0, 50), utilOnly(1, 60)})
	flush(t, w)
	p := w.Path()
	at := func(t0 int64) *SampleAtResult {
		t.Helper()
		r, err := SampleAt(p, t0)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := at(twall)
	if r == nil || r.Time != twall || len(r.GPUs) != 2 {
		t.Fatalf("exact = %+v", r)
	}
	g := r.GPUs[0]
	if *g.Util != 95 || *g.MemUsed != 88691000000 || *g.Temp != 58 || *g.Fan != 34 ||
		*g.PowerW != 160 || *g.ClockGPU != 2872 || *g.ClockMem != 13365 {
		t.Fatalf("gpu0 = %+v", g)
	}
	g1 := r.GPUs[1]
	if g1.ID != 1 || *g1.Util != 3 || g1.MemUsed != nil || g1.Temp != nil || g1.Fan != nil ||
		g1.PowerW != nil || g1.ClockGPU != nil || g1.ClockMem != nil {
		t.Fatalf("gpu1 = %+v", g1)
	}
	if r := at(twall + 500000); r == nil || r.Time != twall {
		t.Fatalf("between = %+v", r)
	}
	if r := at(twall - 1); r != nil {
		t.Fatalf("before first = %+v", r)
	}
	if r := at(twall + 1*sec + 500000); r != nil {
		t.Fatalf("after disconnect = %+v", r)
	}
	if r := at(twall + 10*sec); r == nil || r.Time != twall+10*sec || *r.GPUs[0].Util != 50 {
		t.Fatalf("after reconnect = %+v", r)
	}
	if _, err := SampleAt(t.TempDir()+"/none.gpurec", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestSampleAtInterval(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	w.AddSample(ts0, 1000, []recording.GPUSample{utilOnly(0, 1)})
	w.AddSample(ts0+10, 500, []recording.GPUSample{utilOnly(0, 2)})
	flush(t, w)
	cases := []struct {
		t    int64
		want int64
	}{
		{twall + 2*sec, twall},
		{twall + 2*sec + 1, 0},
		{twall + 10*sec + 1*sec, twall + 10*sec},
		{twall + 10*sec + 1*sec + 1, 0},
	}
	for _, c := range cases {
		r, err := SampleAt(w.Path(), c.t)
		if err != nil {
			t.Fatal(err)
		}
		if c.want == 0 && r != nil || c.want != 0 && (r == nil || r.Time != c.want) {
			t.Fatalf("SampleAt(%d) = %+v, want time %d", c.t-twall, r, c.want)
		}
	}
}

package recording_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gpumon/internal/recording"
	"gpumon/internal/recording/recv1"
)

const v1t0 = int64(1730700000123456)

func v1Devices(n int) json.RawMessage {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":%d,"vendor":"nvidia","name":"GPU %d"}`, i, i))
	}
	return json.RawMessage(`{"type":"devices","protocol":1,"gpus":[` + strings.Join(parts, ",") + `]}`)
}

func newV1(t *testing.T, dir, host string, n int) *recv1.Writer {
	t.Helper()
	w, err := recv1.Create(dir, recording.Header{Host: host, TWall0: v1t0, TS0: 1730700000.123, Devices: v1Devices(n)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func pv(v int64) *int64 { return &v }

func openV1(t *testing.T, path string) *recording.File {
	t.Helper()
	f, err := recording.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if f.Version() != 1 {
		t.Fatalf("version = %d", f.Version())
	}
	return f
}

func readAllV1(t *testing.T, f *recording.File) []recording.Record {
	t.Helper()
	recs, err := f.ReadRange(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

// TestV1Read 確認版本 1 的取樣, 錯誤, 程序與斷線記錄皆可讀取, 代表無值的數值讀為無值.
func TestV1Read(t *testing.T) {
	w := newV1(t, t.TempDir(), "alice@10.0.0.5", 2)
	mem := uint64(12884901888)
	tm := w.AddSample(1730700000.623, 500, []recording.GPUSample{{ID: 0, Util: pv(87), Fan: pv(45), Enc: pv(1), Dec: pv(2),
		Temp: pv(-7), PowerW: pv(312), ClockGPU: pv(2520), ClockMem: pv(10501), MemUsed: &mem}})
	w.AddError(1730700000.623, strings.Repeat("e", 100))
	if err := w.AddProcesses(1730700000.623, []recording.GPUProcs{{GPU: 1, Procs: []json.RawMessage{json.RawMessage(`{"pid":2,"cmd":"a b"}`)}}}); err != nil {
		t.Fatal(err)
	}
	w.AddDisconnect(tm+10, "ssh: connection reset")
	w.Close()

	f := openV1(t, w.Path())
	if h := f.Header(); h.Host != "alice@10.0.0.5" || h.TWall0 != v1t0 {
		t.Fatalf("header = %+v", h)
	}
	recs := readAllV1(t, f)
	if len(recs) != 4 {
		t.Fatalf("records = %+v", recs)
	}
	g := recs[0].GPUs[0]
	if recs[0].Kind != recording.KindSample || recs[0].Time != tm || recs[0].IntervalMS != 500 ||
		g.Util != 87 || g.Fan != 45 || g.Enc != 1 || g.Dec != 2 || g.Temp != -7 || g.PowerW != 312 ||
		g.ClockGPU != 2520 || g.ClockMem != 10501 || g.MemUsed != mem || g.Has != 1<<9-1 {
		t.Fatalf("sample = %+v", recs[0])
	}
	if none := recs[0].GPUs[1]; none.Has != 0 || none.ID != 1 {
		t.Fatalf("missing GPU = %+v", none)
	}
	if recs[1].Kind != recording.KindError || recs[1].Text != strings.Repeat("e", 100) {
		t.Fatalf("error = %+v", recs[1])
	}
	if recs[2].Kind != recording.KindProcesses || recs[2].Text != `[{"pid":2,"cmd":"a b","gpu":1}]` {
		t.Fatalf("procs = %+v", recs[2])
	}
	if recs[3].Kind != recording.KindDisconnect || recs[3].Text != "ssh: connection reset" || recs[3].Time != tm+10 {
		t.Fatalf("disconnect = %+v", recs[3])
	}
	first, last, ok, err := f.TimeRange()
	if err != nil || !ok || first != tm || last != tm+10 {
		t.Fatalf("range %d %d %v %v", first, last, ok, err)
	}
}

// TestV1SegmentsAndContinuation 確認跨越段落邊界的接續記錄只讀出一次, 往前與往後讀取結果一致,
// 且檔尾寫入未完成的記錄略過.
func TestV1SegmentsAndContinuation(t *testing.T) {
	w := newV1(t, t.TempDir(), "a@b", 1)
	var want []int64
	for i := 0; i < 5000; i++ {
		ts := 1730700000.123 + float64(i)*0.01
		want = append(want, w.AddSample(ts, 10, []recording.GPUSample{{ID: 0, Util: pv(int64(i % 100))}}))
		if i == 2040 || i == 4090 {
			// 1000 位元組的文字佔 50 筆記錄, 跨越第 2048 與 4096 筆.
			w.AddError(ts, strings.Repeat("x", 1000))
		}
	}
	w.Close()
	fh, _ := os.OpenFile(w.Path(), os.O_APPEND|os.O_WRONLY, 0)
	fh.Write(make([]byte, 17))
	fh.Close()

	f := openV1(t, w.Path())
	recs := readAllV1(t, f)
	var got []int64
	errs := 0
	for _, r := range recs {
		switch r.Kind {
		case recording.KindSample:
			got = append(got, r.Time)
		case recording.KindError:
			errs++
			if r.Text != strings.Repeat("x", 1000) {
				t.Fatalf("error text %d bytes", len(r.Text))
			}
		}
	}
	if !reflect.DeepEqual(got, want) || errs != 2 {
		t.Fatalf("samples %d of %d, errors %d", len(got), len(want), errs)
	}
	var back []recording.Record
	if err := f.Backward(math.MaxInt64, true, func(r *recording.Record) bool { back = append(back, *r); return true }); err != nil {
		t.Fatal(err)
	}
	if len(back) != len(recs) {
		t.Fatalf("backward %d, forward %d", len(back), len(recs))
	}
	for i := range back {
		if back[i].Time != recs[len(recs)-1-i].Time || back[i].Kind != recs[len(recs)-1-i].Kind {
			t.Fatalf("backward record %d differs", i)
		}
	}
	mid := want[3000]
	r, _ := f.ReadRange(mid, mid)
	if len(r) != 1 || r[0].GPUs[0].Util != 0 {
		t.Fatalf("mid = %+v", r)
	}
}

// TestV1ProcessesAt 確認版本 1 的程序記錄依原規則取得目前與前一筆列表.
func TestV1ProcessesAt(t *testing.T) {
	w := newV1(t, t.TempDir(), "a@b", 1)
	add := func(rel float64, procs string) {
		w.AddSample(1730700000.123+rel, 1000, nil)
		w.AddProcesses(1730700000.123+rel, []recording.GPUProcs{{GPU: 0, Procs: []json.RawMessage{json.RawMessage(procs)}}})
	}
	add(0, `{"pid":1,"engines":{"render":0}}`)
	add(1, `{"pid":1,"engines":{"render":5}}`)
	w.AddDisconnect(v1t0+1500000, "lost")
	add(2, `{"pid":1,"engines":{"render":9}}`)
	w.Close()
	f := openV1(t, w.Path())
	cur, prev, err := f.ProcessesAt(v1t0 + 1200000)
	if err != nil || cur == nil || prev == nil || cur.Time != v1t0+1000000 || prev.Time != v1t0 ||
		string(cur.Procs) != `[{"pid":1,"engines":{"render":5},"gpu":0}]` {
		t.Fatalf("cur %+v prev %+v %v", cur, prev, err)
	}
	cur, prev, err = f.ProcessesAt(math.MaxInt64)
	if err != nil || cur == nil || prev != nil || cur.Time != v1t0+2000000 {
		t.Fatalf("across disconnect: cur %+v prev %+v %v", cur, prev, err)
	}
	if cur, _, _ = f.ProcessesAt(v1t0 - 1); cur != nil {
		t.Fatalf("before first: %+v", cur)
	}
}

// TestV1Rehost 確認變更主機識別時版本 1 的檔案仍寫為版本 1, 記錄原樣保留.
func TestV1Rehost(t *testing.T) {
	dir := t.TempDir()
	w := newV1(t, dir, "bob@srv", 1)
	w.AddSample(1730700000.623, 500, []recording.GPUSample{{ID: 0, Util: pv(50)}})
	w.AddError(1730700000.7, strings.Repeat("b", 30))
	w.Close()
	orig := readAllV1(t, openV1(t, w.Path()))
	if err := recording.Rehost(dir, "bob@srv", "bob@srv:2222"); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(recording.HostDir(dir, "bob@srv:2222"), filepath.Base(w.Path()))
	data, err := os.ReadFile(moved)
	if err != nil || !bytes.Equal(data[8:10], []byte{1, 0}) {
		t.Fatalf("version bytes %x %v", data[8:10], err)
	}
	f := openV1(t, moved)
	if f.Header().Host != "bob@srv:2222" {
		t.Fatalf("header %+v", f.Header())
	}
	if got := readAllV1(t, f); !reflect.DeepEqual(got, orig) {
		t.Fatalf("records changed: %+v", got)
	}
}

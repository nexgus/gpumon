package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

func devicesJSON(n int) json.RawMessage {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":%d,"vendor":"nvidia","name":"GPU %d"}`, i, i))
	}
	return json.RawMessage(`{"type":"devices","protocol":1,"gpus":[` + strings.Join(parts, ",") + `]}`)
}

const t0 = int64(1730700000123456)

func newWriter(t *testing.T, n int) (*Writer, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := Create(dir, Header{Host: "alice@10.0.0.5", TWall0: t0, TS0: 1730700000.123, Devices: devicesJSON(n)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, dir
}

func i64(v int64) *int64   { return &v }
func u64(v uint64) *uint64 { return &v }
func flush(t *testing.T, w *Writer) {
	t.Helper()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, path string) *File {
	t.Helper()
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func readAll(t *testing.T, f *File) []Record {
	t.Helper()
	recs, err := f.ReadRange(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestRoundTrip(t *testing.T) {
	w, _ := newWriter(t, 2)
	full := GPUSample{ID: 0, Util: i64(87), Fan: i64(45), Enc: i64(1), Dec: i64(2), Temp: i64(71),
		PowerW: i64(312), ClockGPU: i64(2520), ClockMem: i64(10501), MemUsed: u64(12884901888)}
	tm := w.AddSample(1730700000.123+0.5, 500, []GPUSample{full})
	if tm != t0+500000 {
		t.Fatalf("time = %d", tm)
	}
	w.AddError(1730700000.123+0.5, "nvml timeout")
	if err := w.AddProcesses(1730700000.123+0.5, []GPUProcs{
		{GPU: 0, Procs: []json.RawMessage{json.RawMessage(`{"pid": 1, "cmd":"a b"}`)}},
		{GPU: 1, Procs: []json.RawMessage{json.RawMessage(`{"pid":2}`), json.RawMessage(`{}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	w.AddDisconnect(tm+10, "ssh: connection reset")
	flush(t, w)

	f := open(t, w.Path())
	hdr := f.Header()
	if hdr.Host != "alice@10.0.0.5" || hdr.TWall0 != t0 || hdr.TS0 != 1730700000.123 {
		t.Fatalf("header = %+v", hdr)
	}
	if string(hdr.Devices) != string(devicesJSON(2)) {
		t.Fatalf("devices = %s", hdr.Devices)
	}
	recs := readAll(t, f)
	if len(recs) != 4 {
		t.Fatalf("records = %d", len(recs))
	}
	s := recs[0]
	want0 := GPUValues{ID: 0, Util: 87, Fan: 45, Enc: 1, Dec: 2, Temp: 71, PowerW: 312, ClockGPU: 2520, ClockMem: 10501, MemUsed: 12884901888}
	if s.Kind != KindSample || s.Time != tm || s.IntervalMS != 500 || s.GPUs[0] != want0 {
		t.Fatalf("sample = %+v", s)
	}
	none := GPUValues{ID: 1, Util: 255, Fan: 255, Enc: 255, Dec: 255, Temp: 32767, PowerW: 65535, ClockGPU: 65535, ClockMem: 65535, MemUsed: math.MaxUint64}
	if s.GPUs[1] != none {
		t.Fatalf("missing GPU = %+v", s.GPUs[1])
	}
	if recs[1].Kind != KindError || recs[1].Text != "nvml timeout" || recs[1].Time != tm {
		t.Fatalf("error = %+v", recs[1])
	}
	wantProcs := `[{"pid":1,"cmd":"a b","gpu":0},{"pid":2,"gpu":1},{"gpu":1}]`
	if recs[2].Kind != KindProcesses || recs[2].Text != wantProcs {
		t.Fatalf("procs = %+v", recs[2])
	}
	if recs[3].Kind != KindDisconnect || recs[3].Text != "ssh: connection reset" || recs[3].Time != tm+10 {
		t.Fatalf("disconnect = %+v", recs[3])
	}
}

func TestExactBytes(t *testing.T) {
	dir := t.TempDir()
	dev := json.RawMessage(`{"gpus":[{"id":0},{"id":1}]}`)
	w, err := Create(dir, Header{Host: "a@b", TWall0: 1000000, TS0: 100.5, Devices: dev})
	if err != nil {
		t.Fatal(err)
	}
	w.AddSample(100.5, 500, []GPUSample{{ID: 0, Util: i64(87), Fan: i64(45), Enc: i64(0), Dec: i64(0), Temp: i64(71),
		PowerW: i64(312), ClockGPU: i64(2520), ClockMem: i64(10501), MemUsed: u64(12884901888)}})
	flush(t, w)
	w.Close()
	got, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	meta := `{"host":"a@b","t_wall_0":1000000,"ts_0":100.5,"devices":{"gpus":[{"id":0},{"id":1}]}}`
	want := []byte("GPUMONRC")
	want = append(want, 0x01, 0x00, 0x02, 0x00, byte(len(meta)), 0, 0, 0)
	want = append(want, meta...)
	want = append(want, 0x01, 0x40, 0x42, 0x0F, 0, 0, 0, 0, 0, 0xF4, 0x01)
	want = append(want, 0x57, 0x2D, 0x00, 0x00, 0x47, 0x00, 0x38, 0x01, 0xD8, 0x09, 0x05, 0x29, 0, 0, 0, 0, 3, 0, 0, 0)
	want = append(want, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes differ\n got %x\nwant %x", got, want)
	}
	if len(got) != 16+len(meta)+51 {
		t.Fatalf("size = %d", len(got))
	}
}

func TestValueClamping(t *testing.T) {
	w, _ := newWriter(t, 1)
	w.AddSample(1730700000.123, 70000, []GPUSample{{ID: 0, Util: i64(300), Fan: i64(-5), Enc: i64(254), Dec: i64(255),
		Temp: i64(-40000), PowerW: i64(70000), ClockGPU: i64(65534), ClockMem: i64(65535), MemUsed: u64(math.MaxUint64)}})
	w.AddSample(1730700000.123, 1, []GPUSample{{ID: 0, Temp: i64(50000), Fan: i64(100), MemUsed: u64(math.MaxUint64 - 1)}})
	flush(t, w)
	recs := readAll(t, open(t, w.Path()))
	g := recs[0].GPUs[0]
	want := GPUValues{ID: 0, Util: 254, Fan: 0, Enc: 254, Dec: 254, Temp: -32768, PowerW: 65534, ClockGPU: 65534, ClockMem: 65534, MemUsed: math.MaxUint64 - 1}
	if g != want || recs[0].IntervalMS != 65535 {
		t.Fatalf("clamped = %+v interval %d", g, recs[0].IntervalMS)
	}
	g = recs[1].GPUs[0]
	if g.Temp != 32766 || g.Fan != 100 || g.Util != 255 || g.MemUsed != math.MaxUint64-1 {
		t.Fatalf("second = %+v", g)
	}
}

func TestErrorMessageChunks(t *testing.T) {
	w, _ := newWriter(t, 1) // 每筆放 20 位元組
	cases := []string{"", "x", strings.Repeat("a", 20), strings.Repeat("b", 21), strings.Repeat("c", 100), strings.Repeat("d", 1024)}
	for _, c := range cases {
		w.AddError(1730700000.123, c)
	}
	flush(t, w)
	f := open(t, w.Path())
	recs := readAll(t, f)
	if len(recs) != len(cases) {
		t.Fatalf("records = %d", len(recs))
	}
	for i, c := range cases {
		if recs[i].Text != c {
			t.Errorf("case %d: got %q", i, recs[i].Text)
		}
	}
	n, _ := f.Count()
	wantN := 1 + 1 + 1 + 2 + 5 + 52
	if n != wantN {
		t.Errorf("count = %d want %d", n, wantN)
	}
}

func TestErrorMessageTruncation(t *testing.T) {
	w, _ := newWriter(t, 2)
	// 1023 個位元組後接 3 位元組字元, 字元橫跨 1023..1025.
	msg := strings.Repeat("a", 1023) + "中" + "尾"
	w.AddError(1730700000.123, msg)
	w.AddError(1730700000.123, strings.Repeat("e", 2000))
	w.AddError(1730700000.123, strings.Repeat("a", 1021)+"中"+"尾")
	flush(t, w)
	recs := readAll(t, open(t, w.Path()))
	if recs[0].Text != strings.Repeat("a", 1023) {
		t.Fatalf("len = %d", len(recs[0].Text))
	}
	if recs[1].Text != strings.Repeat("e", 1024) {
		t.Fatalf("len = %d", len(recs[1].Text))
	}
	if recs[2].Text != strings.Repeat("a", 1021)+"中" || !utf8.ValidString(recs[2].Text) || len(recs[2].Text) != 1024 {
		t.Fatalf("len = %d", len(recs[2].Text))
	}
}

func TestProcessesTruncation(t *testing.T) {
	w, _ := newWriter(t, 1)
	var procs []json.RawMessage
	for i := 0; i < 3000; i++ {
		procs = append(procs, json.RawMessage(fmt.Sprintf(`{"pid":%d,"user":"u","cmd":"%s"}`, i, strings.Repeat("x", 30))))
	}
	if err := w.AddProcesses(1730700000.123, []GPUProcs{{GPU: 3, Procs: procs}}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddProcesses(1730700000.123, nil); err != nil {
		t.Fatal(err)
	}
	huge := json.RawMessage(`{"cmd":"` + strings.Repeat("z", 70000) + `"}`)
	if err := w.AddProcesses(1730700000.123, []GPUProcs{{GPU: 0, Procs: []json.RawMessage{huge}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddProcesses(1730700000.123, []GPUProcs{{GPU: 0, Procs: []json.RawMessage{json.RawMessage(`[1]`)}}}); err == nil {
		t.Fatal("expected error for non-object element")
	}
	flush(t, w)
	recs := readAll(t, open(t, w.Path()))
	if len(recs) != 3 {
		t.Fatalf("records = %d", len(recs))
	}
	text := recs[0].Text
	if len(text) > 65535 || len(text) < 65535-100 {
		t.Fatalf("len = %d", len(text))
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(text), &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) >= 3000 || len(arr) < 500 || arr[0]["pid"] != float64(0) || arr[0]["gpu"] != float64(3) {
		t.Fatalf("elements = %d first %v", len(arr), arr[0])
	}
	if arr[len(arr)-1]["pid"] != float64(len(arr)-1) {
		t.Fatal("not truncated from the end")
	}
	if recs[1].Text != "[]" || recs[2].Text != "[]" {
		t.Fatalf("got %q %q", recs[1].Text, recs[2].Text)
	}
}

func TestDisconnectTimeClamp(t *testing.T) {
	w, _ := newWriter(t, 1)
	tm := w.AddSample(1730700000.123+5, 1000, nil)
	w.AddDisconnect(tm-1000, "early")
	w.AddDisconnect(tm+7, "late")
	w.BeginConnection(tm+100, 50)
	w.AddSample(50, 1000, nil)
	flush(t, w)
	recs := readAll(t, open(t, w.Path()))
	if recs[1].Time != tm || recs[2].Time != tm+7 || recs[3].Time != tm+100 {
		t.Fatalf("times = %d %d %d (tm %d)", recs[1].Time, recs[2].Time, recs[3].Time, tm)
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].Time < recs[i-1].Time {
			t.Fatal("not monotonic")
		}
	}
}

func TestTimeFormulaRounding(t *testing.T) {
	w, _ := newWriter(t, 1)
	w.BeginConnection(5000000, 10.0)
	if got := w.Time(10.0000004); got != 5000000 {
		t.Errorf("got %d", got)
	}
	if got := w.Time(10.0000006); got != 5000001 {
		t.Errorf("got %d", got)
	}
}

func TestNoGPU(t *testing.T) {
	dir := t.TempDir()
	_, err := Create(dir, Header{Host: "a@b", TWall0: t0, Devices: json.RawMessage(`{"gpus":[]}`)})
	if !errors.Is(err, ErrNoGPU) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history")); err == nil {
		t.Fatal("directory created")
	}
}

func TestTornTail(t *testing.T) {
	w, _ := newWriter(t, 2)
	for i := 0; i < 3; i++ {
		w.AddSample(1730700000.123+float64(i), 1000, nil)
	}
	flush(t, w)
	w.Close()
	fh, err := os.OpenFile(w.Path(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fh.Write(make([]byte, 30))
	fh.Close()
	f := open(t, w.Path())
	if n, _ := f.Count(); n != 3 {
		t.Fatalf("count = %d", n)
	}
	if recs := readAll(t, f); len(recs) != 3 {
		t.Fatalf("records = %d", len(recs))
	}
}

func TestIncompleteMessageAtTail(t *testing.T) {
	w, _ := newWriter(t, 1)
	w.AddSample(1730700000.123, 1000, nil)
	w.AddError(1730700000.123, strings.Repeat("m", 100))
	flush(t, w)
	w.Close()
	st, _ := os.Stat(w.Path())
	if err := os.Truncate(w.Path(), st.Size()-int64(2*31)-5); err != nil {
		t.Fatal(err)
	}
	recs := readAll(t, open(t, w.Path()))
	if len(recs) != 1 || recs[0].Kind != KindSample {
		t.Fatalf("records = %+v", recs)
	}
}

func TestOpenRejects(t *testing.T) {
	w, _ := newWriter(t, 1)
	w.AddSample(1730700000.123, 1000, nil)
	w.Close()
	data, _ := os.ReadFile(w.Path())
	dir := t.TempDir()

	bad := append([]byte(nil), data...)
	bad[0] = 'X'
	p1 := filepath.Join(dir, "magic.gpurec")
	os.WriteFile(p1, bad, 0o644)
	if _, err := Open(p1); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("err = %v", err)
	}

	bad = append([]byte(nil), data...)
	bad[8] = 7
	p2 := filepath.Join(dir, "ver.gpurec")
	os.WriteFile(p2, bad, 0o644)
	_, err := Open(p2)
	var uv *UnsupportedVersionError
	if !errors.As(err, &uv) || uv.Version != 7 || !strings.Contains(err.Error(), "7") {
		t.Fatalf("err = %v", err)
	}

	p3 := filepath.Join(dir, "short.gpurec")
	os.WriteFile(p3, []byte("GPU"), 0o644)
	if _, err := Open(p3); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownHeaderMemberAndRecordKind(t *testing.T) {
	dir := t.TempDir()
	meta := `{"host":"h","future":{"a":1},"t_wall_0":5,"ts_0":1.5,"devices":{"gpus":[{"id":4}]}}`
	b := []byte("GPUMONRC")
	b = append(b, 1, 0, 1, 0, byte(len(meta)), 0, 0, 0)
	b = append(b, meta...)
	rec := func(kind byte, tm int64) []byte {
		r := make([]byte, 31)
		r[0] = kind
		le.PutUint64(r[1:], uint64(tm))
		return r
	}
	b = append(b, rec(1, 10)...)
	b = append(b, rec(99, 11)...)
	b = append(b, rec(1, 12)...)
	p := filepath.Join(dir, "x.gpurec")
	os.WriteFile(p, b, 0o644)
	f := open(t, p)
	if f.Header().Host != "h" || f.GPUIDs()[0] != 4 {
		t.Fatalf("header = %+v", f.Header())
	}
	recs := readAll(t, f)
	if len(recs) != 2 || recs[0].Time != 10 || recs[1].Time != 12 || recs[0].GPUs[0].ID != 4 {
		t.Fatalf("records = %+v", recs)
	}
}

func TestReadRange(t *testing.T) {
	w, _ := newWriter(t, 1)
	// 時刻 0..9 秒各一筆取樣, 第 5 秒另有長錯誤訊息 (跨 3 筆) 與兩筆同時刻取樣.
	base := t0
	for i := 0; i < 10; i++ {
		w.AddSample(1730700000.123+float64(i), 1000, []GPUSample{{ID: 0, Util: i64(int64(i))}})
		if i == 5 {
			w.AddError(1730700000.123+5, strings.Repeat("q", 50))
			w.AddSample(1730700000.123+5, 1000, []GPUSample{{ID: 0, Util: i64(55)}})
		}
	}
	flush(t, w)
	f := open(t, w.Path())

	first, last, ok, err := f.TimeRange()
	if err != nil || !ok || first != base || last != base+9000000 {
		t.Fatalf("range = %d %d %v %v", first, last, ok, err)
	}

	recs, _ := f.ReadRange(base+2000000, base+3000000)
	if len(recs) != 2 || recs[0].GPUs[0].Util != 2 || recs[1].GPUs[0].Util != 3 {
		t.Fatalf("closed range = %+v", recs)
	}

	recs, _ = f.ReadRange(base+5000000, base+5000000)
	if len(recs) != 3 || recs[0].Kind != KindSample || recs[1].Kind != KindError || recs[1].Text != strings.Repeat("q", 50) || recs[2].GPUs[0].Util != 55 {
		t.Fatalf("same-time range = %+v", recs)
	}

	for _, r := range [][2]int64{{base - 9000000, base - 1}, {base + 9000001, base + 99000000}, {base + 3000500, base + 3000600}, {5, 1}} {
		recs, err = f.ReadRange(r[0], r[1])
		if err != nil || len(recs) != 0 {
			t.Fatalf("out of range %v: %v %v", r, recs, err)
		}
	}
	recs, _ = f.ReadRange(base-1000000, base+100000000)
	if len(recs) != 12 {
		t.Fatalf("covering range = %d", len(recs))
	}
	recs, _ = f.ReadRange(base+9000000, math.MaxInt64)
	if len(recs) != 1 {
		t.Fatalf("to max = %d", len(recs))
	}
	if i, _ := f.LowerBound(base + 5000000); i != 5 {
		t.Fatalf("lower bound = %d", i)
	}
}

func TestReadIndexRangeInsideContinuation(t *testing.T) {
	w, _ := newWriter(t, 1)
	w.AddSample(1730700000.123, 1000, nil)                // 索引 0
	w.AddError(1730700000.123+1, strings.Repeat("k", 70)) // 索引 1..4
	w.AddSample(1730700000.123+2, 1000, nil)              // 索引 5
	w.AddDisconnect(t0+2500000, strings.Repeat("r", 45))  // 索引 6..8
	w.AddSample(1730700000.123+3, 1000, nil)              // 索引 9
	flush(t, w)
	f := open(t, w.Path())

	recs, err := f.ReadIndexRange(3, 6)
	if err != nil || len(recs) != 1 || recs[0].Kind != KindSample {
		t.Fatalf("start in continuation: %+v %v", recs, err)
	}
	recs, _ = f.ReadIndexRange(0, 2)
	if len(recs) != 2 || recs[1].Text != strings.Repeat("k", 70) {
		t.Fatalf("end in message: %+v", recs)
	}
	recs, _ = f.ReadIndexRange(7, 100)
	if len(recs) != 1 || recs[0].Kind != KindSample {
		t.Fatalf("tail: %+v", recs)
	}
}

func TestFileNames(t *testing.T) {
	dir := t.TempDir()
	h := Header{Host: "bob@host", TWall0: time.Date(2026, 11, 5, 13, 4, 9, 123000, time.UTC).UnixMicro(), TS0: 1, Devices: devicesJSON(1)}
	var names []string
	for i := 0; i < 3; i++ {
		w, err := Create(dir, h)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		names = append(names, filepath.Base(w.Path()))
	}
	want := []string{"20261105-130409.gpurec", "20261105-130409-2.gpurec", "20261105-130409-3.gpurec"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v", names)
		}
	}
	if d := filepath.Base(filepath.Dir(filepath.Join(dir, "history", "bob@host", "x"))); d != "bob@host" {
		t.Fatal(d)
	}
}

func TestHostDirName(t *testing.T) {
	cases := map[string]string{
		"alice@10.0.0.5":    "alice@10.0.0.5",
		"a b/c\\d:e*f":      "a_b_c_d_e_f",
		"user@fe80::1%eth0": "user@fe80__1_eth0",
		"使用者@host":          "___@host",
		"a_b-c.d":           "a_b-c.d",
	}
	for in, want := range cases {
		if got := HostDirName(in); got != want {
			t.Errorf("HostDirName(%q) = %q want %q", in, got, want)
		}
	}
	dir := t.TempDir()
	w, err := Create(dir, Header{Host: "u@fe80::1", TWall0: t0, Devices: devicesJSON(1)})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if filepath.Base(filepath.Dir(w.Path())) != "u@fe80__1" {
		t.Fatalf("path = %s", w.Path())
	}
	if _, err := Create(dir, Header{Host: "..", TWall0: t0, Devices: devicesJSON(1)}); err == nil {
		t.Fatal("expected error for dot host")
	}
}

func TestConcurrentAppendAndRead(t *testing.T) {
	w, _ := newWriter(t, 2)
	var wg sync.WaitGroup
	done := make(chan struct{})
	errc := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		f, err := Open(w.Path())
		if err != nil {
			errc <- err
			return
		}
		defer f.Close()
		prev := 0
		for {
			select {
			case <-done:
				return
			default:
			}
			recs, err := f.ReadRange(math.MinInt64, math.MaxInt64)
			if err != nil {
				errc <- err
				return
			}
			if len(recs) < prev {
				errc <- fmt.Errorf("records shrank: %d < %d", len(recs), prev)
				return
			}
			prev = len(recs)
			for i, r := range recs {
				if r.Kind == KindError && r.Text != strings.Repeat("e", 60) {
					errc <- fmt.Errorf("record %d: bad text %q", i, r.Text)
					return
				}
			}
		}
	}()
	total := 0
	for i := 0; i < 300; i++ {
		w.AddSample(1730700000.123+float64(i)/10, 100, nil)
		w.AddError(1730700000.123+float64(i)/10, strings.Repeat("e", 60))
		total += 2
		if i%7 == 0 {
			flush(t, w)
		}
	}
	flush(t, w)
	close(done)
	wg.Wait()
	select {
	case err := <-errc:
		t.Fatal(err)
	default:
	}
	if recs := readAll(t, open(t, w.Path())); len(recs) != total {
		t.Fatalf("records = %d want %d", len(recs), total)
	}
}

func TestLargeRangeAcrossBlocks(t *testing.T) {
	w, _ := newWriter(t, 8)
	const count = 20000
	for i := 0; i < count; i++ {
		w.AddSample(1730700000.123+float64(i)*0.2, 200, []GPUSample{{ID: 7, Util: i64(int64(i % 100))}})
	}
	flush(t, w)
	f := open(t, w.Path())
	recs := readAll(t, f)
	if len(recs) != count {
		t.Fatalf("records = %d", len(recs))
	}
	mid := t0 + 1000*200000
	got, _ := f.ReadRange(mid, mid)
	if len(got) != 1 || got[0].GPUs[7].Util != uint8(1000%100) {
		t.Fatalf("mid = %+v", got)
	}
}

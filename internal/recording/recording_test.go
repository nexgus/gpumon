package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"
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

const (
	t0  = int64(1730700000123456)
	ts0 = 1730700000.123
	sec = int64(1000000)
)

func newWriter(t *testing.T, n int) (*Writer, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := Create(dir, Header{Host: "alice@10.0.0.5", TWall0: t0, TS0: ts0, Devices: devicesJSON(n)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, dir
}

func i64(v int64) *int64   { return &v }
func u64(v uint64) *uint64 { return &v }
func str(v string) *string { return &v }
func flush(t *testing.T, w *Writer) {
	t.Helper()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func addSample(t *testing.T, w *Writer, rel float64, gpus []GPUSample, procs ...GPUProcs) int64 {
	t.Helper()
	tm, err := w.AddSample(ts0+rel, 1000, gpus, procs)
	if err != nil {
		t.Fatal(err)
	}
	return tm
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

func kindsOf(recs []Record) []Kind {
	var out []Kind
	for _, r := range recs {
		out = append(out, r.Kind)
	}
	return out
}

func rawProcs(gpu int, elems ...string) GPUProcs {
	gp := GPUProcs{GPU: gpu}
	for _, e := range elems {
		gp.Procs = append(gp.Procs, json.RawMessage(e))
	}
	return gp
}

func TestRoundTrip(t *testing.T) {
	w, _ := newWriter(t, 2)
	full := GPUSample{ID: 0, Util: i64(300), Fan: i64(-5), Enc: i64(0), Dec: i64(2), Temp: i64(-40000),
		PowerW: i64(70000), ClockGPU: i64(2520), ClockMem: i64(math.MinInt64), MemUsed: u64(math.MaxUint64)}
	tm, err := w.AddSample(ts0+0.5, 70000, []GPUSample{full}, nil)
	if err != nil || tm != t0+500000 {
		t.Fatalf("time = %d %v", tm, err)
	}
	long := strings.Repeat("長", 3000)
	if et := w.AddError(ts0+0.5, long); et != tm {
		t.Fatalf("error time %d", et)
	}
	w.AddError(ts0+0.6, "")
	dt := w.AddDisconnect(tm+10, "ssh: connection reset")
	flush(t, w)

	f := open(t, w.Path())
	if f.Version() != 2 {
		t.Fatalf("version = %d", f.Version())
	}
	hdr := f.Header()
	if hdr.Host != "alice@10.0.0.5" || hdr.TWall0 != t0 || hdr.TS0 != ts0 || string(hdr.Devices) != string(devicesJSON(2)) {
		t.Fatalf("header = %+v", hdr)
	}
	recs := readAll(t, f)
	if got := kindsOf(recs); !reflect.DeepEqual(got, []Kind{KindSample, KindError, KindError, KindDisconnect}) {
		t.Fatalf("kinds = %v", got)
	}
	want := SampleValues([]int{0, 1}, []GPUSample{full})
	if s := recs[0]; s.Time != tm || s.IntervalMS != 70000 || !reflect.DeepEqual(s.GPUs, want) {
		t.Fatalf("sample = %+v", s)
	}
	g := recs[0].GPUs[0]
	if g.Util != 300 || g.Fan != -5 || g.Temp != -40000 || g.PowerW != 70000 || g.ClockMem != math.MinInt64 ||
		g.MemUsed != math.MaxUint64 || g.Has != 1<<9-1 || recs[0].GPUs[1].Has != 0 {
		t.Fatalf("values = %+v", recs[0].GPUs)
	}
	if v, ok := g.Value(MetricEnc); !ok || v != 0 {
		t.Fatal("zero value must be distinguished from no value")
	}
	if recs[1].Text != long || recs[2].Text != "" || recs[3].Text != "ssh: connection reset" || recs[3].Time != dt {
		t.Fatalf("texts = %d %q %q", len(recs[1].Text), recs[2].Text, recs[3].Text)
	}
}

// TestConformanceBytes 以依規格書手算的位元組比對寫入結果.
func TestConformanceBytes(t *testing.T) {
	if got := crc32.Checksum([]byte("123456789"), castagnoli); got != 0xE3069283 {
		t.Fatalf("CRC-32C check value %08X", got)
	}
	dir := t.TempDir()
	dev := json.RawMessage(`{"gpus":[{"id":0},{"id":1}]}`)
	w, err := Create(dir, Header{Host: "a@b", TWall0: 1000000, TS0: 100.5, Devices: dev})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddSample(100.5, 500, []GPUSample{{ID: 0, Util: i64(87), Temp: i64(-5), MemUsed: u64(300)}}, nil); err != nil {
		t.Fatal(err)
	}
	w.Close()
	got, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	meta := `{"host":"a@b","t_wall_0":1000000,"ts_0":100.5,"devices":{"gpus":[{"id":0},{"id":1}]}}`
	head := append([]byte("GPUMONRC"), 0x02, 0x00, 0x02, 0x00, byte(len(meta)), 0, 0, 0)
	head = append(head, meta...)
	if !bytes.HasPrefix(got, head) {
		t.Fatalf("fixed area and header differ:\n got %x\nwant %x", got[:len(head)], head)
	}
	blk := got[len(head):]
	record := []byte{0x14, 0x08, 0xC0, 0x84, 0x3D, 0x12, 0x0E, 0x08, 0xF4, 0x03,
		0x12, 0x07, 0x08, 0x57, 0x28, 0x09, 0x48, 0xAC, 0x02, 0x12, 0x00}
	c := le.Uint32(blk[16:])
	if !bytes.Equal(blk[:16], []byte{0x6B, 0xC6, 0x6F, 0xF3, 0xFA, 0x80, 0x24, 0x0F, 0x68, 0x2B, 0xE4, 0xFB, 0x0C, 0x08, 0x36, 0xBB}) ||
		len(blk) != 56+int(c) || le.Uint32(blk[20:]) != uint32(len(record)) || le.Uint32(blk[24:]) != 0 ||
		le.Uint64(blk[28:]) != 1000000 || le.Uint64(blk[36:]) != 1000000 || le.Uint64(blk[44:]) != 0 {
		t.Fatalf("block header %x", blk[:56])
	}
	sum := crc32.Update(crc32.Checksum(blk[:52], crc32.MakeTable(crc32.Castagnoli)), crc32.MakeTable(crc32.Castagnoli), blk[56:])
	if le.Uint32(blk[52:]) != sum {
		t.Fatal("checksum")
	}
	d, _ := zstd.NewReader(nil)
	defer d.Close()
	plain, err := d.DecodeAll(blk[56:], nil)
	if err != nil || !bytes.Equal(plain, record) {
		t.Fatalf("content %x %v\nwant %x", plain, err, record)
	}
}

// TestProcessChanges 確認程序變動記錄只寫差異: 新程序寫全部欄位, 改變的欄位照寫, 由有值變為
// 無值寫入 cleared, 消失寫 ended, 無變動不寫; 規格以外的成員與型別不符的成員存入 extra.
func TestProcessChanges(t *testing.T) {
	w, _ := newWriter(t, 2)
	addSample(t, w, 0, nil,
		rawProcs(0, `{"pid":1,"user":"u","mem":0,"util":0,"cmd":"","engines":{},"future":[1, 2],"cycles":null}`),
		rawProcs(1, `{"pid":2,"user":"v","engines":{"rcs":5}}`, `{"pid":2,"user":"dup"}`))
	addSample(t, w, 1, nil,
		rawProcs(0, `{"pid":1,"user":"u","mem":0,"util":0,"cmd":"","engines":{},"future":[1,2],"cycles":null}`),
		rawProcs(1, `{"pid":2,"user":"v","engines":{"rcs":5}}`))
	addSample(t, w, 2, nil,
		rawProcs(0, `{"pid":1,"user":"u","mem":7,"cmd":"","engines":{},"future":[1,2]}`),
		rawProcs(1, `{"pid":3,"util":"high"}`))
	_, err := w.AddSample(ts0+3, 1000, nil, []GPUProcs{rawProcs(0, `[1]`, `{"user":"x"}`, `{"pid":1,"user":"u","mem":7,"cmd":"","engines":{},"future":[1,2]}`)})
	if err == nil {
		t.Fatal("expected error for invalid elements")
	}
	flush(t, w)
	recs := readAll(t, open(t, w.Path()))
	if got := kindsOf(recs); !reflect.DeepEqual(got, []Kind{KindSample, KindGPUProcs, KindSample, KindSample, KindGPUProcs, KindSample, KindGPUProcs}) {
		t.Fatalf("kinds = %v", got)
	}
	first := recs[1].GPUProcs
	want1 := GPUProc{GPU: 0, PID: 1, User: str("u"), Mem: u64(0), Util: i64(0), Cmd: str(""), Engines: map[string]uint64{},
		Extra: str(`{"cycles":null,"future":[1,2]}`)}
	want2 := GPUProc{GPU: 1, PID: 2, User: str("v"), Engines: map[string]uint64{"rcs": 5}}
	if !reflect.DeepEqual(first, []GPUProc{want1, want2}) {
		t.Fatalf("first changes = %s", dump(first))
	}
	third := recs[4].GPUProcs
	want := []GPUProc{
		{GPU: 0, PID: 1, Mem: u64(7), Extra: str(`{"future":[1,2]}`), Cleared: []uint32{gpUtil}},
		{GPU: 1, PID: 3, Extra: str(`{"util":"high"}`)},
		{GPU: 1, PID: 2, Ended: true},
	}
	if !reflect.DeepEqual(third, want) {
		t.Fatalf("third changes = %s", dump(third))
	}
	fourth := recs[6].GPUProcs
	if !reflect.DeepEqual(fourth, []GPUProc{{GPU: 1, PID: 3, Ended: true}}) {
		t.Fatalf("fourth changes = %s", dump(fourth))
	}
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func procsAt(t *testing.T, f *File, at int64) (cur, prev string) {
	t.Helper()
	c, p, err := f.ProcessesAt(at)
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		cur = string(c.Procs)
	}
	if p != nil {
		prev = string(p.Procs)
	}
	return cur, prev
}

// TestProcessesRestore 確認程序列表可由列表重設區塊起還原, 跨越列表重設區塊時前一次取樣的
// 列表仍能取得, 斷線記錄不重設比較對象且使前一次取樣不可用.
func TestProcessesRestore(t *testing.T) {
	w, _ := newWriter(t, 1)
	lists := map[int64]string{}
	// 每 60 秒一筆, 每 4 筆存檔一次, 共 30 分鐘; 每 10 分鐘以上出現一個列表重設區塊.
	for i := 0; i < 30; i++ {
		var elems []string
		for p := 1; p <= 3; p++ {
			if (i+p)%5 == 0 {
				continue
			}
			e := fmt.Sprintf(`{"pid":%d,"user":"u%d","mem":%d`, p, p, (i/3)*p)
			if p == 2 && i%2 == 0 {
				e += `,"util":5`
			}
			if p == 3 {
				e += fmt.Sprintf(`,"engines":{"render":%d},"x":"%c"`, i*1000, 'a'+i%3)
			}
			elems = append(elems, e+"}")
		}
		tm := addSample(t, w, float64(i*60), nil, rawProcs(0, elems...))
		var parsed []string
		for _, e := range elems {
			parsed = append(parsed, strings.TrimSuffix(e, "}")+`,"gpu":0}`)
		}
		lists[tm] = "[" + strings.Join(parsed, ",") + "]"
		if i == 20 {
			w.AddDisconnect(tm+sec, "lost")
		}
		if i%4 == 3 {
			flush(t, w)
		}
	}
	flush(t, w)
	f := open(t, w.Path())
	size, _ := f.areaSize()
	resets := 0
	for off := int64(0); off < size; {
		h, _, ok, _ := f.readBlock(off, size)
		if !ok {
			t.Fatalf("block at %d invalid", off)
		}
		if h.isReset() {
			resets++
		}
		off = h.end()
	}
	if resets != 3 {
		t.Fatalf("reset blocks = %d", resets)
	}
	for i := 0; i < 30; i++ {
		tm := t0 + int64(i)*60*sec
		cur, prev := procsAt(t, f, tm+30*sec)
		if !jsonEqual(cur, lists[tm]) {
			t.Fatalf("sample %d: got %s\nwant %s", i, cur, lists[tm])
		}
		switch {
		case i == 0 || i == 21:
			if prev != "" {
				t.Fatalf("sample %d: unexpected previous list %s", i, prev)
			}
		case !jsonEqual(prev, lists[tm-60*sec]):
			t.Fatalf("sample %d: previous %s\nwant %s", i, prev, lists[tm-60*sec])
		}
	}
	if cur, _ := procsAt(t, f, t0-1); cur != "" {
		t.Fatalf("before first sample: %s", cur)
	}
}

func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// blockOffsets 回傳各區塊於檔案中的位移.
func blockOffsets(t *testing.T, f *File) []int64 {
	t.Helper()
	size, _ := f.areaSize()
	var out []int64
	for off := int64(0); off < size; {
		h, _, ok, err := f.readBlock(off, size)
		if err != nil || !ok {
			break
		}
		out = append(out, f.base+off)
		off = h.end()
	}
	return out
}

// TestCorruptBlock 確認中段損壞的區塊只失去該區塊, 程序列表至下一個列表重設區塊之前無法還原,
// 往後與往前讀取皆以同步標記越過損壞處.
func TestCorruptBlock(t *testing.T) {
	w, _ := newWriter(t, 1)
	var times []int64
	for i := 0; i < 40; i++ {
		tm := addSample(t, w, float64(i*30), []GPUSample{{ID: 0, Util: i64(int64(i))}}, rawProcs(0, fmt.Sprintf(`{"pid":1,"mem":%d}`, i)))
		times = append(times, tm)
		flush(t, w)
	}
	f := open(t, w.Path())
	offs := blockOffsets(t, f)
	if len(offs) != 40 {
		t.Fatalf("blocks = %d", len(offs))
	}
	data, _ := os.ReadFile(w.Path())
	// 第 5 個區塊的內容損壞, 第 9 個區塊的同步標記損壞.
	data[offs[5]+60] ^= 0xFF
	data[offs[9]+3] ^= 0xFF
	os.WriteFile(w.Path(), data, 0o644)
	f = open(t, w.Path())

	recs := readAll(t, f)
	var got []int64
	for _, r := range recs {
		if r.Kind == KindSample {
			got = append(got, r.GPUs[0].Util)
		}
	}
	var want []int64
	for i := 0; i < 40; i++ {
		if i != 5 && i != 9 {
			want = append(want, int64(i))
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forward samples %v", got)
	}
	var back []int64
	f.Backward(math.MaxInt64, false, func(r *Record) bool {
		back = append(back, r.GPUs[0].Util)
		return true
	})
	for i, j := 0, len(back)-1; i < j; i, j = i+1, j-1 {
		back[i], back[j] = back[j], back[i]
	}
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("backward samples %v", back)
	}
	// 每 30 秒一筆, 第 0 與第 20 個區塊為列表重設區塊.
	if cur, _ := procsAt(t, f, times[5]); !jsonEqual(cur, `[{"pid":1,"mem":4,"gpu":0}]`) {
		t.Fatalf("lost sample falls back to the previous one: %s", cur)
	}
	for i := 0; i < 40; i++ {
		if i == 5 || i == 9 {
			continue
		}
		cur, _ := procsAt(t, f, times[i])
		restorable := i < 5 || i >= 20
		if restorable != (cur != "") {
			t.Fatalf("sample %d: list %q", i, cur)
		}
		if restorable && !jsonEqual(cur, fmt.Sprintf(`[{"pid":1,"mem":%d,"gpu":0}]`, i)) {
			t.Fatalf("sample %d: list %s", i, cur)
		}
	}
	if r, _ := f.ReadRange(times[5], times[5]); len(r) != 0 {
		t.Fatalf("lost block read: %+v", r)
	}
	if r, _ := f.ReadRange(times[6], times[6]); len(r) != 2 || r[0].GPUs[0].Util != 6 {
		t.Fatalf("block after damage: %+v", r)
	}
}

// TestIncompleteTail 確認寫入中或異常結束留下的不完整區塊略過, 其餘照常讀取.
func TestIncompleteTail(t *testing.T) {
	w, _ := newWriter(t, 2)
	for i := 0; i < 3; i++ {
		addSample(t, w, float64(i), nil, rawProcs(0, `{"pid":1}`))
		flush(t, w)
	}
	addSample(t, w, 3, nil)
	flush(t, w)
	w.Close()
	data, _ := os.ReadFile(w.Path())
	f := open(t, w.Path())
	offs := blockOffsets(t, f)
	for _, cut := range []int64{offs[3] + 10, offs[3] + 60, int64(len(data)) - 1} {
		os.WriteFile(w.Path(), data[:cut], 0o644)
		f := open(t, w.Path())
		first, last, ok, err := f.TimeRange()
		if err != nil || !ok || first != t0 || last != t0+2*sec {
			t.Fatalf("cut %d: range %d %d %v %v", cut, first, last, ok, err)
		}
		if recs := readAll(t, f); len(recs) != 4 {
			t.Fatalf("cut %d: records %v", cut, kindsOf(recs))
		}
		if cur, _ := procsAt(t, f, math.MaxInt64); cur != `[{"pid":1,"gpu":0}]` {
			t.Fatalf("cut %d: processes %s", cut, cur)
		}
	}
	os.WriteFile(w.Path(), append(data, syncMarker...), 0o644)
	if recs := readAll(t, open(t, w.Path())); len(recs) != 6 {
		t.Fatalf("trailing marker: %v", kindsOf(recs))
	}
}

// appendRawBlock 把未壓縮的記錄 raw 寫成一個區塊直接附加於檔尾, 供測試寫入端不會產生的內容.
// reset 為負時本區塊為列表重設區塊. 回傳區塊總長度.
func appendRawBlock(t *testing.T, path string, f *File, raw []byte, tFirst, tLast, reset int64, prevTotal uint32) uint32 {
	t.Helper()
	return appendBlock(t, path, f, encoder().EncodeAll(raw, nil), uint32(len(raw)), tFirst, tLast, reset, prevTotal)
}

// appendBlock 以 content 為區塊內容附加一個校驗碼正確的區塊.
func appendBlock(t *testing.T, path string, f *File, content []byte, u uint32, tFirst, tLast, reset int64, prevTotal uint32) uint32 {
	t.Helper()
	size, _ := f.areaSize()
	blk := make([]byte, blockHeaderSize, blockHeaderSize+len(content))
	copy(blk, syncMarker)
	le.PutUint32(blk[16:], uint32(len(content)))
	le.PutUint32(blk[20:], u)
	le.PutUint32(blk[24:], prevTotal)
	le.PutUint64(blk[28:], uint64(tFirst))
	le.PutUint64(blk[36:], uint64(tLast))
	if reset < 0 {
		reset = size
	}
	le.PutUint64(blk[44:], uint64(reset))
	le.PutUint32(blk[52:], crc32.Update(crc32.Checksum(blk[:52], castagnoli), castagnoli, content))
	blk = append(blk, content...)
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.Write(blk); err != nil {
		t.Fatal(err)
	}
	return uint32(len(blk))
}

// TestUndecodableBlock 確認校驗碼相符但無法解壓縮的區塊視為無效區塊, 往後與往前讀取皆越過,
// 其後至下一個列表重設區塊之前的程序列表無法還原.
func TestUndecodableBlock(t *testing.T) {
	w, _ := newWriter(t, 1)
	for i := 0; i < 3; i++ {
		addSample(t, w, float64(i), []GPUSample{{ID: 0, Util: i64(int64(i))}}, rawProcs(0, fmt.Sprintf(`{"pid":1,"mem":%d}`, i)))
		flush(t, w)
	}
	w.Close()
	f := open(t, w.Path())
	offs := blockOffsets(t, f)
	st, _ := os.Stat(w.Path())
	lastTotal := uint32(st.Size() - offs[2])
	total := appendBlock(t, w.Path(), f, []byte("not a zstd frame"), 100, t0+3*sec, t0+3*sec, 0, lastTotal)
	var raw []byte
	raw = appendRecord(raw, &Record{Kind: KindSample, Time: t0 + 4*sec, IntervalMS: 1000, GPUs: []GPUValues{{Has: 1, Util: 4}}})
	raw = appendRecord(raw, &Record{Kind: KindGPUProcs, Time: t0 + 4*sec, GPUProcs: []GPUProc{{PID: 1, Mem: u64(4)}}})
	appendRawBlock(t, w.Path(), f, raw, t0+4*sec, t0+4*sec, 0, total)

	var fwd, back []int64
	for _, r := range readAll(t, f) {
		if r.Kind == KindSample {
			fwd = append(fwd, r.GPUs[0].Util)
		}
	}
	f.Backward(math.MaxInt64, false, func(r *Record) bool {
		back = append([]int64{r.GPUs[0].Util}, back...)
		return true
	})
	if want := []int64{0, 1, 2, 4}; !reflect.DeepEqual(fwd, want) || !reflect.DeepEqual(back, want) {
		t.Fatalf("forward %v, backward %v", fwd, back)
	}
	if cur, _ := procsAt(t, f, t0+2*sec); cur != `[{"pid":1,"mem":2,"gpu":0}]` {
		t.Fatalf("before the damage: %s", cur)
	}
	if cur, _ := procsAt(t, f, t0+4*sec); cur != "" {
		t.Fatalf("after the damage: %s", cur)
	}
}

// TestProcMessageBytes 以依規格書手算的位元組比對程序變動的編碼.
func TestProcMessageBytes(t *testing.T) {
	gp := GPUProc{GPU: 1, PID: 42, User: str(""), Util: i64(-1), Engines: map[string]uint64{"rcs": 5}, Cleared: []uint32{gpMem, gpExtra}}
	gpBytes := []byte{0x08, 0x01, 0x10, 0x2A, 0x22, 0x00, 0x30, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01,
		0x42, 0x09, 0x0A, 0x07, 0x0A, 0x03, 'r', 'c', 's', 0x10, 0x05, 0x6A, 0x02, 0x05, 0x0C}
	if got := encodeGPUProc(&gp); !bytes.Equal(got, gpBytes) {
		t.Fatalf("GPUProc bytes\n got %x\nwant %x", got, gpBytes)
	}
	if got, err := decodeGPUProc(gpBytes); err != nil || !reflect.DeepEqual(got, gp) {
		t.Fatalf("GPUProc decode %s %v", dump(got), err)
	}
	cp := CPUProc{PID: 7, StartTime: -2, Ended: true, CPUTimeMS: u64(0)}
	cpBytes := []byte{0x08, 0x07, 0x10, 0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01, 0x18, 0x01, 0x38, 0x00}
	if got := encodeCPUProc(&cp); !bytes.Equal(got, cpBytes) {
		t.Fatalf("CPUProc bytes\n got %x\nwant %x", got, cpBytes)
	}
	if got, err := decodeCPUProc(cpBytes); err != nil || !reflect.DeepEqual(got, cp) {
		t.Fatalf("CPUProc decode %s %v", dump(got), err)
	}
}

// TestCPUProcsAndUnknownFields 確認 CPU 程序變動記錄, 未知欄位與 oneof 未設定的記錄皆可讀取.
func TestCPUProcsAndUnknownFields(t *testing.T) {
	w, _ := newWriter(t, 1)
	w.Close()
	f := open(t, w.Path())

	cpu := []CPUProc{
		{PID: 10, StartTime: 777, PPID: i64(1), User: str("root"), Cmd: str("init"), CPUTimeMS: u64(5), Mem: u64(4096), Extra: str(`{"nice":0}`)},
		{PID: 11, StartTime: -3, Mem: u64(0)},
	}
	var raw []byte
	raw = appendRecord(raw, &Record{Kind: KindSample, Time: t0, IntervalMS: 1000, GPUs: []GPUValues{{Has: 1, Util: 3}}})
	raw = appendRecord(raw, &Record{Kind: KindGPUProcs, Time: t0, GPUProcs: []GPUProc{{GPU: 0, PID: 1, User: str("u")}}})
	raw = appendRecord(raw, &Record{Kind: KindCPUProcs, Time: t0, CPUProcs: cpu})
	// 未知欄位, 型別不符的欄位, oneof 未設定的記錄, 以及逐筆編碼的 cleared.
	var unknown []byte
	unknown = appendVarintField(unknown, recTime, uint64(t0+1))
	unknown = appendBytesField(unknown, 15, []byte("future"))
	unknown = appendVarintField(unknown, recSample, 9)
	raw = protowire.AppendBytes(raw, unknown)
	var gp []byte
	gp = appendVarintField(gp, gpPID, 1)
	gp = appendVarintField(gp, gpCleared, gpUser)
	gp = appendVarintField(gp, 99, 1)
	var sample []byte
	sample = appendVarintField(sample, sampleInterval, 1000)
	sample = appendBytesField(sample, sampleGPUs, appendVarintField(appendVarintField(nil, 20, 1), 1, 4))
	sample = appendBytesField(sample, sampleGPUs, nil)
	var r2 []byte
	r2 = appendVarintField(r2, recTime, uint64(t0+2))
	r2 = appendBytesField(r2, recSample, sample)
	raw = protowire.AppendBytes(raw, r2)
	var r3 []byte
	r3 = appendVarintField(r3, recTime, uint64(t0+2))
	r3 = appendBytesField(r3, recGPUProcs, appendBytesField(nil, changesProcs, gp))
	raw = protowire.AppendBytes(raw, r3)
	appendRawBlock(t, w.Path(), f, raw, t0, t0+2, -1, 0)

	recs := readAll(t, f)
	if got := kindsOf(recs); !reflect.DeepEqual(got, []Kind{KindSample, KindGPUProcs, KindCPUProcs, KindSample, KindGPUProcs}) {
		t.Fatalf("kinds = %v", got)
	}
	if !reflect.DeepEqual(recs[2].CPUProcs, cpu) {
		t.Fatalf("cpu procs = %s", dump(recs[2].CPUProcs))
	}
	if g := recs[3].GPUs[0]; g.Util != 4 || g.Has != 1 {
		t.Fatalf("sample with unknown field = %+v", g)
	}
	if !reflect.DeepEqual(recs[4].GPUProcs, []GPUProc{{PID: 1, Cleared: []uint32{gpUser}}}) {
		t.Fatalf("unpacked cleared = %s", dump(recs[4].GPUProcs))
	}
	if cur, prev := procsAt(t, f, math.MaxInt64); cur != `[{"pid":1,"gpu":0}]` || prev != `[{"pid":1,"user":"u","gpu":0}]` {
		t.Fatalf("processes %s / %s", cur, prev)
	}
	cl := cpuList{}
	cl.apply(cpu)
	cl.apply([]CPUProc{{PID: 10, StartTime: 777, Cleared: []uint32{cpCmd}, Mem: u64(1)}, {PID: 11, StartTime: -3, Ended: true}})
	if p := cl[cpuKey{10, 777}]; len(cl) != 1 || p.Cmd != nil || *p.Mem != 1 || *p.User != "root" {
		t.Fatalf("cpu list = %s", dump(cl[cpuKey{10, 777}]))
	}
}

// TestFlushFailureKeepsRecords 確認附加失敗時記錄保留於記憶體, 下次存檔時連同新的記錄寫成一個區塊.
func TestFlushFailureKeepsRecords(t *testing.T) {
	w, _ := newWriter(t, 1)
	addSample(t, w, 0, nil, rawProcs(0, `{"pid":1}`))
	flush(t, w)
	addSample(t, w, 1, nil, rawProcs(0, `{"pid":1}`, `{"pid":2}`))
	real := w.f
	ro, err := os.Open(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	w.f = ro
	if err := w.Flush(); err == nil {
		t.Fatal("expected write error")
	}
	ro.Close()
	w.f = real
	st, _ := os.Stat(w.Path())
	if st.Size() != w.size {
		t.Fatalf("size %d, want %d", st.Size(), w.size)
	}
	addSample(t, w, 2, nil, rawProcs(0, `{"pid":2}`))
	flush(t, w)
	f := open(t, w.Path())
	if n := len(blockOffsets(t, f)); n != 2 {
		t.Fatalf("blocks = %d", n)
	}
	recs := readAll(t, f)
	if got := kindsOf(recs); !reflect.DeepEqual(got, []Kind{KindSample, KindGPUProcs, KindSample, KindGPUProcs, KindSample, KindGPUProcs}) {
		t.Fatalf("kinds = %v", got)
	}
	if cur, _ := procsAt(t, f, math.MaxInt64); cur != `[{"pid":2,"gpu":0}]` {
		t.Fatalf("processes %s", cur)
	}
}

func TestDisconnectTimeClamp(t *testing.T) {
	w, _ := newWriter(t, 1)
	tm := addSample(t, w, 5, nil)
	w.AddDisconnect(tm-1000, "early")
	w.AddDisconnect(tm+7, "late")
	w.BeginConnection(tm+100, 50)
	if _, err := w.AddSample(50, 1000, nil, nil); err != nil {
		t.Fatal(err)
	}
	flush(t, w)
	recs := readAll(t, open(t, w.Path()))
	if recs[1].Time != tm || recs[2].Time != tm+7 || recs[3].Time != tm+100 {
		t.Fatalf("times = %d %d %d (tm %d)", recs[1].Time, recs[2].Time, recs[3].Time, tm)
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

func TestOpenRejects(t *testing.T) {
	w, _ := newWriter(t, 1)
	addSample(t, w, 0, nil)
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
	for _, v := range []byte{0, 3, 7} {
		bad = append([]byte(nil), data...)
		bad[8] = v
		p2 := filepath.Join(dir, "ver.gpurec")
		os.WriteFile(p2, bad, 0o644)
		_, err := Open(p2)
		var uv *UnsupportedVersionError
		if !errors.As(err, &uv) || uv.Version != uint16(v) || !strings.Contains(err.Error(), fmt.Sprint(v)) {
			t.Fatalf("err = %v", err)
		}
	}
	p3 := filepath.Join(dir, "short.gpurec")
	os.WriteFile(p3, []byte("GPU"), 0o644)
	if _, err := Open(p3); !errors.Is(err, ErrNotRecording) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownHeaderMember(t *testing.T) {
	dir := t.TempDir()
	meta := `{"host":"h","future":{"a":1},"t_wall_0":5,"ts_0":1.5,"devices":{"gpus":[{"id":4}]}}`
	b := []byte("GPUMONRC")
	b = append(b, 2, 0, 1, 0, byte(len(meta)), 0, 0, 0)
	b = append(b, meta...)
	p := filepath.Join(dir, "x.gpurec")
	os.WriteFile(p, b, 0o644)
	f := open(t, p)
	if f.Header().Host != "h" || f.GPUIDs()[0] != 4 {
		t.Fatalf("header = %+v", f.Header())
	}
	if _, _, ok, err := f.TimeRange(); ok || err != nil {
		t.Fatalf("empty file range %v %v", ok, err)
	}
	raw := appendRecord(nil, &Record{Kind: KindSample, Time: 10, GPUs: []GPUValues{{Has: 1, Util: 1}}})
	appendRawBlock(t, p, f, raw, 10, 10, -1, 0)
	recs := readAll(t, f)
	if len(recs) != 1 || recs[0].GPUs[0].ID != 4 {
		t.Fatalf("records = %+v", recs)
	}
}

// TestSeek 確認依時刻定位在多個區塊間皆正確, 含同時刻跨區塊的記錄.
func TestSeek(t *testing.T) {
	w, _ := newWriter(t, 1)
	var times []int64
	for i := 0; i < 500; i++ {
		rel := float64(i / 2)
		times = append(times, addSample(t, w, rel, []GPUSample{{ID: 0, Util: i64(int64(i))}}))
		if i%3 == 0 {
			w.AddError(ts0+rel, strings.Repeat("q", i))
		}
		if i%7 == 6 {
			flush(t, w)
		}
	}
	flush(t, w)
	f := open(t, w.Path())
	all := readAll(t, f)
	first, last, ok, err := f.TimeRange()
	if err != nil || !ok || first != times[0] || last != times[len(times)-1] {
		t.Fatalf("range %d %d %v %v", first, last, ok, err)
	}
	for _, r := range [][2]int64{{t0 + 10*sec, t0 + 20*sec}, {t0 - 5, t0}, {t0 + 249*sec, math.MaxInt64},
		{t0 + 100*sec + 1, t0 + 101*sec - 1}, {t0 + 300*sec, t0 + 400*sec}, {t0 + 77*sec, t0 + 77*sec}} {
		got, err := f.ReadRange(r[0], r[1])
		if err != nil {
			t.Fatal(err)
		}
		var want []Record
		for _, rec := range all {
			if rec.Time >= r[0] && rec.Time <= r[1] {
				want = append(want, rec)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("range %v: got %d records, want %d", r, len(got), len(want))
		}
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
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v", names)
	}
}

func TestHostDirName(t *testing.T) {
	cases := map[string]string{
		"alice@10.0.0.5":    "alice@10.0.0.5",
		"a b/c\\d:e*f":      "a_b_c_d_e_f",
		"user@fe80::1%eth0": "user@fe80__1_eth0",
		"使用者@host":          "___@host",
		"a_b-c.d":           "a_b-c.d",
		"alice@host:2222":   "alice@host+2222",
		"alice@[::1]:2222":  "alice@___1_+2222",
		"alice@::1":         "alice@__1",
		"alice@fe80::1:22":  "alice@fe80__1_22",
		"host:2222":         "host+2222",
		"alice@host:":       "alice@host_",
		"alice@host:22a":    "alice@host_22a",
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

// TestConcurrentAppendAndRead 確認寫入中的檔案可同時讀取, 讀到的記錄只增不減且內容完整.
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
			if _, _, err := f.ProcessesAt(math.MaxInt64); err != nil {
				errc <- err
				return
			}
		}
	}()
	total := 0
	for i := 0; i < 300; i++ {
		addSample(t, w, float64(i)/10, nil, rawProcs(0, fmt.Sprintf(`{"pid":%d}`, i%5)))
		w.AddError(ts0+float64(i)/10, strings.Repeat("e", 60))
		total += 3
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

package history

import (
	"encoding/json"
	"fmt"
	"testing"

	"gpumon/internal/recording"
)

func procs(gpu int, n int, tag string) []recording.GPUProcs {
	var ps []json.RawMessage
	for i := 0; i < n; i++ {
		ps = append(ps, json.RawMessage(fmt.Sprintf(`{"pid":%d,"user":"u","mem":1,"cmd":"%s"}`, i+1, tag)))
	}
	return []recording.GPUProcs{{GPU: gpu, Procs: ps}}
}

func TestProcessesAt(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	add(w, 0, 1000, 1, 0)
	if err := w.AddProcesses(ts0+0, procs(0, 1, "first")); err != nil {
		t.Fatal(err)
	}
	for k := 1; k < 30; k++ {
		add(w, float64(k), 1000, 1, 0)
	}
	// 很長的程序記錄, 佔用數千筆接續記錄, 於第 30 秒.
	add(w, 30, 1000, 1, 0)
	if err := w.AddProcesses(ts0+30, procs(0, 1500, "big")); err != nil {
		t.Fatal(err)
	}
	// 其後的記錄多到使第一次往前讀取的範圍切在這筆程序記錄中間.
	for k := 1; k <= 4000; k++ {
		add(w, 30+float64(k)*0.001, 1000, 1, 0)
	}
	add(w, 39, 1000, 1, 0)
	flush(t, w)
	p := w.Path()

	cases := []struct {
		at   int64
		tag  string
		time int64
	}{
		{twall - sec, "", 0},
		{twall, "first", twall},
		{twall + 15*sec, "first", twall},
		{twall + 30*sec - 1, "first", twall},
		{twall + 30*sec, "big", twall + 30*sec},
		{twall + 39*sec, "big", twall + 30*sec},
		{math64Max, "big", twall + 30*sec},
	}
	for _, c := range cases {
		got, err := ProcessesAt(p, c.at)
		if err != nil {
			t.Fatal(err)
		}
		if c.tag == "" {
			if got != nil {
				t.Fatalf("at %d: got %+v", c.at, got)
			}
			continue
		}
		var arr []map[string]any
		if got == nil || json.Unmarshal(got.Procs, &arr) != nil || got.Time != c.time || arr[0]["cmd"] != c.tag {
			t.Fatalf("at %d: got %+v", c.at, got)
		}
		if c.tag == "big" && len(arr) < 1000 {
			t.Fatalf("big procs = %d", len(arr))
		}
		if arr[0]["gpu"] != float64(0) {
			t.Fatalf("gpu member = %v", arr[0])
		}
	}
	b, _ := json.Marshal(&ProcessList{Time: 5, Procs: json.RawMessage(`[{"pid":1,"gpu":0}]`)})
	if string(b) != `{"time":5,"procs":[{"pid":1,"gpu":0}]}` {
		t.Fatalf("json = %s", b)
	}
}

func TestProcessesAtFileWithoutProcesses(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 0)
	for k := 0; k < 10000; k++ {
		add(w, float64(k)*0.001, 1000, 1, 0)
	}
	flush(t, w)
	if got, err := ProcessesAt(w.Path(), twall+100*sec); err != nil || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

const math64Max = int64(1<<63 - 1)

func procsRaw(elems ...string) []recording.GPUProcs {
	var ps []json.RawMessage
	for _, e := range elems {
		ps = append(ps, json.RawMessage(e))
	}
	return []recording.GPUProcs{{GPU: 1, Procs: ps}}
}

func procUtils(t *testing.T, p string, at int64) map[int]*float64 {
	t.Helper()
	got, err := ProcessesAt(p, at)
	if err != nil || got == nil {
		t.Fatalf("at %d: %+v %v", at, got, err)
	}
	var arr []struct {
		PID  int      `json:"pid"`
		Util *float64 `json:"util"`
		Cmd  string   `json:"cmd"`
	}
	if err := json.Unmarshal(got.Procs, &arr); err != nil {
		t.Fatal(err)
	}
	out := map[int]*float64{}
	for _, e := range arr {
		out[e.PID] = e.Util
	}
	return out
}

func TestProcessesAtComputedUtil(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 1)
	add(w, 0, 1000, 1, 1)
	if err := w.AddProcesses(ts0+0, procsRaw(
		`{"pid":1,"engines":{"render":0}}`,
		`{"pid":2,"util":55}`,
		`{"pid":3,"cycles":{"rcs":0},"total_cycles":{"rcs":0}}`,
		`{"pid":4,"engines":{"render":500}}`)); err != nil {
		t.Fatal(err)
	}
	add(w, 0.5, 1000, 1, 1)
	if err := w.AddProcesses(ts0+0.5, procsRaw(
		`{"pid":1,"engines":{"render":150000000},"cmd":"<x>&"}`,
		`{"pid":2,"util":60}`,
		`{"pid":3,"cycles":{"rcs":300},"total_cycles":{"rcs":1000}}`,
		`{"pid":4,"engines":{"render":100}}`,
		`{"pid":5,"engines":{"render":9}}`)); err != nil {
		t.Fatal(err)
	}
	flush(t, w)
	p := w.Path()

	first := procUtils(t, p, twall)
	if first[1] != nil || first[2] == nil || *first[2] != 55 || first[3] != nil {
		t.Fatalf("first record has no previous: %+v", first)
	}
	u := procUtils(t, p, twall+sec)
	if u[1] == nil || *u[1] != 30 {
		t.Fatalf("busy time utilization: %+v", u)
	}
	if u[2] == nil || *u[2] != 60 {
		t.Fatalf("existing util must stay: %+v", u)
	}
	if u[3] == nil || *u[3] != 30 {
		t.Fatalf("cycles utilization: %+v", u)
	}
	if u[4] != nil {
		t.Fatalf("decreased counter: %+v", u)
	}
	if u[5] != nil {
		t.Fatalf("process missing in the previous record: %+v", u)
	}
	got, _ := ProcessesAt(p, twall+sec)
	var arr []map[string]any
	if err := json.Unmarshal(got.Procs, &arr); err != nil || arr[0]["cmd"] != "<x>&" || arr[0]["gpu"] != float64(1) {
		t.Fatalf("other members: %s %v", got.Procs, err)
	}
}

func TestProcessesAtDisconnectBlocksUtil(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 1)
	add(w, 0, 1000, 1, 1)
	w.AddProcesses(ts0, procsRaw(`{"pid":1,"engines":{"render":0}}`))
	add(w, 1, 1000, 1, 1)
	w.AddDisconnect(twall+2*sec, "lost")
	w.BeginConnection(twall+3*sec, ts0+100)
	add(w, 100, 1000, 1, 1)
	w.AddProcesses(ts0+100, procsRaw(`{"pid":1,"engines":{"render":100000000}}`))
	add(w, 100.5, 1000, 1, 1)
	w.AddProcesses(ts0+100.5, procsRaw(`{"pid":1,"engines":{"render":200000000}}`))
	flush(t, w)
	p := w.Path()
	if u := procUtils(t, p, twall+3*sec); u[1] != nil {
		t.Fatalf("across disconnect: %+v", u)
	}
	if u := procUtils(t, p, twall+4*sec); u[1] == nil || *u[1] != 20 {
		t.Fatalf("within one connection: %+v", u)
	}
}

func TestProcessesAtSparseOldRecords(t *testing.T) {
	w := newRec(t, t.TempDir(), "a@b", 0, 1)
	add(w, 0, 1000, 1, 1)
	w.AddProcesses(ts0, procsRaw(`{"pid":1,"engines":{"render":0}}`))
	for k := 1; k < 60; k++ {
		add(w, float64(k), 1000, 1, 1)
	}
	add(w, 60, 1000, 1, 1)
	w.AddProcesses(ts0+60, procsRaw(`{"pid":1,"engines":{"render":6000000000}}`))
	flush(t, w)
	if u := procUtils(t, w.Path(), twall+61*sec); u[1] == nil || *u[1] != 10 {
		t.Fatalf("sparse records: %+v", u)
	}
}

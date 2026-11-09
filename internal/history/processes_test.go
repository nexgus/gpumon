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

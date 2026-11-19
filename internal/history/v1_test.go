package history

import (
	"encoding/json"
	"testing"

	"gpumon/internal/recording"
	"gpumon/internal/recording/recv1"
)

// TestVersion1File 確認既有的版本 1 記錄檔在歷史畫面的各項查詢皆照常運作.
func TestVersion1File(t *testing.T) {
	dir := t.TempDir()
	w, err := recv1.Create(dir, recording.Header{Host: "a@b", TWall0: twall, TS0: ts0, Devices: devices(0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	sample := func(rel float64, util int64, render string) {
		w.AddSample(ts0+rel, 1000, []recording.GPUSample{{ID: 0, Util: iv(util)}, {ID: 1, Temp: iv(-3)}})
		w.AddProcesses(ts0+rel, procsRaw(`{"pid":7,"engines":{"render":`+render+`}}`))
	}
	sample(0, 10, "0")
	sample(1, 90, "500000000")
	w.AddError(ts0+5, "nvml timeout")
	sample(10, 30, "600000000")
	w.AddDisconnect(twall+11*sec, "lost")
	w.BeginConnection(twall+20*sec, ts0+100)
	sample(100, 90, "700000000")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	p := w.Path()

	r := query(t, []string{p}, twall-sec, twall+30*sec, 100, FieldUtil, FieldTemp)
	if r.Samples != 4 || r.Binned || len(r.Gaps) != 2 || r.Gaps[0].Reason != GapError || r.Gaps[0].Message != "nvml timeout" ||
		r.Gaps[1].Reason != GapDisconnect || r.Gaps[1].Message != "lost" {
		t.Fatalf("query = %+v", r)
	}
	if u := r.GPUs[0].Fields[FieldUtil].Avg; u[0] != 10 || u[3] != 90 || r.GPUs[1].Fields[FieldTemp].Avg[0] != -3 || !nan(r.GPUs[1].Fields[FieldUtil].Avg[0]) {
		t.Fatalf("values = %+v", r.GPUs)
	}

	if u := procUtils(t, p, twall+sec); u[7] == nil || *u[7] != 50 {
		t.Fatalf("computed util = %+v", u)
	}
	if u := procUtils(t, p, twall+20*sec); u[7] != nil {
		t.Fatalf("util across disconnect = %+v", u)
	}
	got, err := ProcessesAt(p, twall+sec)
	var arr []map[string]any
	if err != nil || got == nil || got.Time != twall+sec || json.Unmarshal(got.Procs, &arr) != nil || arr[0]["gpu"] != float64(1) {
		t.Fatalf("processes = %+v %v", got, err)
	}

	mx, err := Maxima(p)
	if err != nil || mx.GPUs[0].Fields[FieldUtil].Value != 90 || len(mx.GPUs[0].Fields[FieldUtil].Runs) != 2 {
		t.Fatalf("maxima = %+v %v", mx, err)
	}
	s, err := SampleAt(p, twall+sec+500000)
	if err != nil || s == nil || *s.GPUs[0].Util != 90 || *s.GPUs[1].Temp != -3 || s.GPUs[1].Util != nil {
		t.Fatalf("sample at = %+v %v", s, err)
	}
	list, err := List(dir, nil)
	if err != nil || len(list.Hosts) != 1 || list.Hosts[0].Files[0].Start != twall || list.Hosts[0].Files[0].End != twall+20*sec {
		t.Fatalf("list = %+v %v", list, err)
	}
}

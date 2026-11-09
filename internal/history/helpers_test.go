package history

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"gpumon/internal/recording"
)

const (
	sec   = int64(1000000)
	twall = int64(1730700000) * sec
	ts0   = 1000.0
)

func devices(ids ...int) json.RawMessage {
	var parts []string
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf(`{"id":%d,"vendor":"nvidia","name":"GPU %d"}`, id, id))
	}
	return json.RawMessage(`{"type":"devices","protocol":1,"gpus":[` + strings.Join(parts, ",") + `]}`)
}

// newRec 於 dataDir 建立記錄檔, 第一筆取樣的時刻為 twall+offset, 對應 ts0.
func newRec(t *testing.T, dataDir, host string, offset int64, ids ...int) *recording.Writer {
	t.Helper()
	w, err := recording.Create(dataDir, recording.Header{Host: host, TWall0: twall + offset, TS0: ts0, Devices: devices(ids...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func iv(v int64) *int64 { return &v }

// add 於相對第一筆取樣 rel 秒處加入取樣, 每張 GPU 的使用率皆為 util.
func add(w *recording.Writer, rel float64, intervalMS int, util int64, ids ...int) int64 {
	var gs []recording.GPUSample
	for _, id := range ids {
		gs = append(gs, recording.GPUSample{ID: id, Util: iv(util)})
	}
	return w.AddSample(ts0+rel, intervalMS, gs)
}

func flush(t *testing.T, w *recording.Writer) {
	t.Helper()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func query(t *testing.T, paths []string, from, to int64, width int, fields ...Field) *Result {
	t.Helper()
	r, err := QueryRange(context.Background(), paths, Query{From: from, To: to, Width: width, Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func nan(x float64) bool { return math.IsNaN(x) }

func context_cancelled() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

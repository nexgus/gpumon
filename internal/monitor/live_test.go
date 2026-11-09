package monitor

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gpumon/internal/history"
	"gpumon/internal/recording"
	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

// TestLive 於實際的測試主機端到端驗證連線管理. 只在設定環境變數
// GPUMON_LIVE_HOST=<user>@<host> 時執行. 使用本機的 ~/.ssh/config (唯讀) 與
// ssh-agent, known_hosts 指向暫存目錄內的複本, 設定與記錄檔寫入暫存目錄.
// 結束時刪除遠端的 ~/.gpumon/.
func TestLive(t *testing.T) {
	target := os.Getenv("GPUMON_LIVE_HOST")
	if target == "" {
		t.Skip("GPUMON_LIVE_HOST is not set")
	}
	user, addr, ok := strings.Cut(target, "@")
	if !ok {
		t.Fatal("GPUMON_LIVE_HOST must be user@host")
	}
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	kh := filepath.Join(tmp, "known_hosts")
	if data, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts")); err == nil {
		if err := os.WriteFile(kh, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	run := func(cmd string) string {
		t.Helper()
		c, err := sshconn.Dial(ctx, sshconn.Config{Host: addr, User: user, KnownHostsPath: kh})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		sess, err := c.SSH().NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		var out bytes.Buffer
		sess.Stdout = &out
		sess.Run("sh -c '" + cmd + "'")
		return strings.TrimSpace(out.String())
	}
	if out := run(`test -e "$HOME/.gpumon" && echo exists`); out == "exists" {
		t.Fatal("~/.gpumon already exists on the test host; refusing to touch it")
	}
	t.Cleanup(func() {
		run(`rm -rf "$HOME/.gpumon"`)
		left := run(`test -e "$HOME/.gpumon" && echo exists; pgrep -u "$(id -u)" -x gpumon`)
		t.Logf("cleanup: remaining = %q", left)
		if left != "" {
			t.Errorf("cleanup left %q", left)
		}
	})

	dir := filepath.Join(tmp, "data")
	store, err := settings.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	events := &eventLog{}
	m := New(Options{
		DataDir: dir, Settings: store, Notifier: events,
		Dialer: SSHDialer{KnownHostsPath: kh},
	})
	defer m.Close()
	st, err := m.AddHost(settings.HostConn{Addr: addr, User: user})
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	waitFor := func(what string, d time.Duration, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(d)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; events %v", what, events.all(EventError))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	state := func() HostStatus { s, _ := m.Host(id); return s }

	start := time.Now()
	if err := m.Connect(id); err != nil {
		t.Fatal(err)
	}
	waitFor("connected", 90*time.Second, func() bool { return state().State == StateConnected })
	t.Logf("connected in %v", time.Since(start).Round(time.Millisecond))
	lv, _ := m.Live(id)
	for _, g := range lv.Devices.GPUs {
		t.Logf("device %d: %s %s", g.ID, g.Vendor, g.Name)
	}
	waitFor("samples", 10*time.Second, func() bool { lv, _ := m.Live(id); return lv.HasSample && lv.IntervalMS == 1000 })

	if err := m.SetInterval(500); err != nil {
		t.Fatal(err)
	}
	waitFor("interval ack", 10*time.Second, func() bool { lv, _ := m.Live(id); return lv.IntervalMS == 500 })
	var times []int64
	for len(times) < 6 {
		lv, _ := m.Live(id)
		if len(times) == 0 || lv.Time != times[len(times)-1] {
			times = append(times, lv.Time)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := 2; i < len(times); i++ {
		if d := times[i] - times[i-1]; d < 400_000 || d > 600_000 {
			t.Errorf("sample spacing %d us after set_interval 500", d)
		}
	}
	lv, _ = m.Live(id)
	for _, g := range lv.GPUs {
		util := "N/A"
		if g.Util != nil {
			util = strconv.Itoa(*g.Util)
		}
		t.Logf("gpu %d util %s computed %v", g.ID, util, g.UtilComputed)
	}
	t.Logf("processes: %d", len(lv.Procs))

	path := state().Recording
	if path == "" {
		t.Fatal("no recording")
	}
	time.Sleep(6 * time.Second)
	res, err := history.QueryRange(ctx, []string{path}, history.Query{From: math.MinInt64 / 2, To: math.MaxInt64 / 2, Width: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples == 0 {
		t.Fatal("no samples saved after the batch interval")
	}
	t.Logf("saved samples after the first batch: %d, gpus %d", res.Samples, len(res.GPUs))
	live, err := m.Query(ctx, id, lv.Time-300_000_000, lv.Time+10_000_000, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("monitor query samples (saved and unsaved): %d", live.Samples)

	// 終止本帳號的遠端程式, 製造非預期斷線.
	killed := time.Now()
	run(`pkill -u "$(id -u)" -x gpumon`)
	waitFor("reconnecting", 20*time.Second, func() bool { return state().State == StateReconnecting })
	t.Logf("disconnect detected after %v: %q", time.Since(killed).Round(time.Millisecond), state().Reason)
	waitFor("reconnected", 30*time.Second, func() bool { return state().State == StateConnected })
	t.Logf("reconnected after %v", time.Since(killed).Round(time.Millisecond))
	if p := state().Recording; p != path {
		t.Fatalf("recording changed from %s to %s", path, p)
	}
	before, _ := m.Live(id)
	waitFor("sample after reconnecting", 10*time.Second, func() bool { lv, _ := m.Live(id); return lv.HasSample && lv.Time > before.Time })

	if err := m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	waitFor("remote program ended", 15*time.Second, func() bool { return run(`pgrep -u "$(id -u)" -x gpumon`) == "" })
	t.Log("remote program ended after disconnect")

	f, err := recording.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := f.ReadRange(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[recording.Kind]int{}
	discIdx := -1
	for i, r := range recs {
		counts[r.Kind]++
		if r.Kind == recording.KindDisconnect {
			discIdx = i
			t.Logf("disconnect record: %q", r.Text)
		}
	}
	t.Logf("records: samples %d, processes %d, errors %d, disconnects %d", counts[recording.KindSample],
		counts[recording.KindProcesses], counts[recording.KindError], counts[recording.KindDisconnect])
	if counts[recording.KindDisconnect] != 1 || discIdx < 0 || discIdx == len(recs)-1 || recs[discIdx+1].Kind != recording.KindSample {
		t.Fatal("expected one disconnect record followed by samples")
	}
	files, _ := filepath.Glob(filepath.Join(recording.HostDir(dir, id), "*.gpurec"))
	if len(files) != 1 {
		t.Fatalf("recording files %v", files)
	}
	res, err = history.QueryRange(ctx, files, history.Query{From: math.MinInt64 / 2, To: math.MaxInt64 / 2, Width: 2000})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res.Gaps {
		t.Logf("gap %v (%s: %q)", time.Duration(g.End-g.Start)*time.Microsecond, g.Reason, g.Message)
	}
	hasDisc := false
	for _, g := range res.Gaps {
		hasDisc = hasDisc || g.Reason == history.GapDisconnect
	}
	if !hasDisc {
		t.Fatal("history query shows no disconnect gap")
	}
}

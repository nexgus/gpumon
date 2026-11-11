package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustOpen(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestDefaults(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	d := s.Snapshot()
	if !reflect.DeepEqual(d, Default()) {
		t.Fatalf("snapshot differs from Default: %+v", d)
	}
	if d.Window.Width != 1280 || d.Window.Height != 800 || d.Window.HasPosition {
		t.Errorf("window = %+v", d.Window)
	}
	if d.Appearance != AppearanceSystem || d.Language != LanguageEnglish {
		t.Errorf("appearance/language = %v/%v", d.Appearance, d.Language)
	}
	if d.SizeUnits != SizeUnitsBinary {
		t.Errorf("size units = %v", d.SizeUnits)
	}
	if d.MonitorSpanMS != 300000 || d.HistorySpanMS != 300000 || d.HistoryAll {
		t.Errorf("spans = %d %d %v", d.MonitorSpanMS, d.HistorySpanMS, d.HistoryAll)
	}
	if !reflect.DeepEqual(d.Curves, []Curve{CurveUtil, CurveMem}) {
		t.Errorf("curves = %v", d.Curves)
	}
	if len(AllCurves()) != MaxCurves {
		t.Errorf("option counts wrong")
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open must not create the file: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AddHost(HostConn{Addr: "10.0.0.5", Port: 2222, User: "alice", KeyFile: "~/.ssh/k"}))
	must(s.AddHost(HostConn{Addr: "gpu02"}))
	must(s.SetWindow(Window{Width: 1000, Height: 700, HasPosition: true, X: -50, Y: 20}))
	must(s.SetAppearance(AppearanceDark))
	must(s.SetLanguage(LanguageTraditionalChinese))
	must(s.SetSizeUnits(SizeUnitsDecimal))
	must(s.SetMonitorSpan(MaxMonitorSpanMS))
	must(s.SetHistorySpan(7_654_321, true))
	must(s.SetCurves([]Curve{CurveTemp, CurveUtil, CurveClockMem}))
	must(s.SetGPUCollapsed("alice@10.0.0.5", 1, true))
	must(s.SetGPUCollapsed("alice@10.0.0.5", 0, true))
	must(s.SetGPUCollapsed("gpu02", 0, true))
	must(s.SetGPUCollapsed("gpu02", 0, false))
	must(s.SetProcsCollapsed("alice@10.0.0.5", true))
	must(s.SetProcsCollapsed("gpu02", true))
	must(s.SetProcsCollapsed("gpu02", false))

	s2 := mustOpen(t, dir)
	if !reflect.DeepEqual(s.Snapshot(), s2.Snapshot()) {
		t.Fatalf("mismatch:\n%+v\n%+v", s.Snapshot(), s2.Snapshot())
	}
	if !s2.GPUCollapsed("alice@10.0.0.5", 0) || !s2.GPUCollapsed("alice@10.0.0.5", 1) ||
		s2.GPUCollapsed("gpu02", 0) || s2.GPUCollapsed("alice@10.0.0.5", 2) {
		t.Errorf("collapsed state wrong: %v", s2.Snapshot().CollapsedGPUs)
	}
	if _, ok := s2.Snapshot().CollapsedGPUs["gpu02"]; ok {
		t.Errorf("empty collapsed entry should be removed")
	}
	if s2.Snapshot().SizeUnits != SizeUnitsDecimal {
		t.Errorf("size units = %v", s2.Snapshot().SizeUnits)
	}
	h, _ := s2.Host("alice@10.0.0.5")
	if d := s2.Snapshot(); d.MonitorSpanMS != MaxMonitorSpanMS || d.HistorySpanMS != 7_654_321 || !d.HistoryAll {
		t.Errorf("spans = %d %d %v", d.MonitorSpanMS, d.HistorySpanMS, d.HistoryAll)
	}
	if !s2.ProcsCollapsed("alice@10.0.0.5") || s2.ProcsCollapsed("gpu02") {
		t.Errorf("procs %v", s2.Snapshot().CollapsedProcs)
	}
	if h.Port != 2222 || h.KeyFile != "~/.ssh/k" {
		t.Errorf("host = %+v", h)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the settings file, got %v", entries)
	}
}

func TestHostLifecycleAndOrder(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	for _, a := range []string{"a", "b", "c", "d"} {
		if err := s.AddHost(HostConn{Addr: a, User: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	ids := func() string {
		var out []string
		for _, h := range s.Hosts() {
			out = append(out, h.ID())
		}
		return strings.Join(out, ",")
	}
	if got := ids(); got != "u@a,u@b,u@c,u@d" {
		t.Fatalf("order = %s", got)
	}
	s.SetGPUCollapsed("u@b", 3, true)
	s.SetProcsCollapsed("u@b", true)
	s.SetProcsCollapsed("u@c", true)
	if err := s.UpdateHost("u@b", HostConn{Addr: "bb", User: "v", Port: 22}); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got != "u@a,v@bb,u@c,u@d" {
		t.Fatalf("order after edit = %s", got)
	}
	h, ok := s.Host("v@bb")
	if !ok || h.Port != 22 {
		t.Errorf("host not updated: %+v", h)
	}
	if !s.ProcsCollapsed("v@bb") || s.ProcsCollapsed("u@b") || !s.ProcsCollapsed("u@c") {
		t.Errorf("procs collapse state not migrated on rename: %v", s.Snapshot().CollapsedProcs)
	}
	if !s.GPUCollapsed("v@bb", 3) || s.GPUCollapsed("u@b", 3) {
		t.Errorf("collapse state not migrated on rename")
	}
	if err := s.UpdateHost("v@bb", HostConn{Addr: "c", User: "u"}); !errors.Is(err, ErrDuplicateHost) {
		t.Errorf("edit to duplicate: %v", err)
	}
	if err := s.UpdateHost("nope", HostConn{Addr: "x"}); !errors.Is(err, ErrHostNotFound) {
		t.Errorf("edit missing: %v", err)
	}
	if err := s.RemoveHost("u@a"); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got != "v@bb,u@c,u@d" {
		t.Fatalf("order after remove = %s", got)
	}
	if err := s.RemoveHost("u@a"); !errors.Is(err, ErrHostNotFound) {
		t.Errorf("remove missing: %v", err)
	}
	s.RemoveHost("v@bb")
	if len(s.Snapshot().CollapsedGPUs) != 0 || s.ProcsCollapsed("v@bb") || !s.ProcsCollapsed("u@c") {
		t.Errorf("collapse state not removed with host")
	}
	if err := s.AddHost(HostConn{Addr: "a", User: "u"}); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got != "u@c,u@d,u@a" {
		t.Fatalf("re-added host should go last: %s", got)
	}
}

func TestDuplicateHosts(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	if err := s.AddHost(HostConn{Addr: "Host1", User: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHost(HostConn{Addr: "host1", User: "alice", Port: 99}); !errors.Is(err, ErrDuplicateHost) {
		t.Errorf("same user/addr: %v", err)
	}
	if err := s.AddHost(HostConn{Addr: "Host1", User: "bob"}); err != nil {
		t.Errorf("different user must be a different host: %v", err)
	}
	if err := s.AddHost(HostConn{Addr: "Host1", User: "Alice"}); err != nil {
		t.Errorf("user names are case sensitive: %v", err)
	}
	if err := s.AddHost(HostConn{Addr: "host1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHost(HostConn{Addr: "HOST1"}); !errors.Is(err, ErrDuplicateHost) {
		t.Errorf("empty user duplicate: %v", err)
	}
	if n := len(s.Hosts()); n != 4 {
		t.Errorf("hosts = %d", n)
	}
}

func TestHostValidation(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	bad := []HostConn{
		{},
		{Addr: "  "},
		{Addr: "a b"},
		{Addr: "a@b"},
		{Addr: "a", User: "x@y"},
		{Addr: "a", Port: 70000},
		{Addr: "a", Port: -1},
	}
	for _, c := range bad {
		if err := s.AddHost(c); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("AddHost(%+v) = %v", c, err)
		}
	}
	if err := s.AddHost(HostConn{Addr: " a ", User: " u "}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Host("u@a"); !ok {
		t.Errorf("fields should be trimmed")
	}
}

func TestSetterRanges(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	s.AddHost(HostConn{Addr: "a", User: "u"})
	for _, ms := range []int64{59_999, 86_400_001, 0, -5} {
		if err := s.SetMonitorSpan(ms); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("monitor span %d: %v", ms, err)
		}
	}
	for _, ms := range []int64{60_000, 86_400_000, 123_456} {
		if err := s.SetMonitorSpan(ms); err != nil {
			t.Errorf("monitor span %d: %v", ms, err)
		}
	}
	for _, ms := range []int64{59_999, 0, -5} {
		if err := s.SetHistorySpan(ms, false); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("history span %d: %v", ms, err)
		}
	}
	if err := s.SetHistorySpan(30*86_400_000, true); err != nil {
		t.Errorf("history span has no upper limit in the settings file: %v", err)
	}
	if err := s.SetAppearance("blue"); err == nil {
		t.Error("bad appearance accepted")
	}
	if err := s.SetLanguage("fr"); err == nil {
		t.Error("bad language accepted")
	}
	if err := s.SetSizeUnits("si"); err == nil {
		t.Error("bad size units accepted")
	}
	if err := s.SetCurves([]Curve{CurveUtil, CurveUtil}); err == nil {
		t.Error("duplicate curve accepted")
	}
	if err := s.SetCurves([]Curve{"bogus"}); err == nil {
		t.Error("unknown curve accepted")
	}
	if err := s.SetCurves(AllCurves()); err != nil {
		t.Errorf("all 7 curves: %v", err)
	}
	if err := s.SetCurves(nil); err != nil || len(s.Snapshot().Curves) != 0 {
		t.Errorf("empty curves: %v", err)
	}
	if err := s.SetWindow(Window{Width: 10, Height: 800}); err == nil {
		t.Error("tiny window accepted")
	}
	if err := s.SetGPUCollapsed("u@a", -1, true); err == nil {
		t.Error("negative gpu id accepted")
	}
	if err := s.SetProcsCollapsed("", true); err == nil {
		t.Error("empty host id accepted")
	}
	if d := s.Snapshot(); d.MonitorSpanMS != 123_456 || d.HistorySpanMS != 30*86_400_000 || !d.HistoryAll {
		t.Errorf("failed setters must not change state: %+v", d)
	}
}

func writeFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadOutOfRangeAndInvalid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{
	  "version": 1,
	  "window": {"width": -3, "height": 99999999, "has_position": true, "x": 5000000, "y": 1},
	  "appearance": "neon", "language": "xx", "size_units": "si",
	  "monitor_range": "all", "history_range": "7m",
	  "curves": ["util", "bogus", "util", "power"],
	  "hosts": [
	    {"addr": "a", "user": "u", "port": 99999, "interval_ms": 50},
	    {"addr": "b", "user": "u", "interval_ms": 99999},
	    {"addr": "A", "user": "u"},
	    {"addr": "", "user": "u"},
	    {"addr": "c d"},
	    {"addr": "e"}
	  ],
	  "collapsed_gpus": {"u@a": [2, 1, 2, -4], "u@b": [], "x": [-1]},
	  "collapsed_procs": ["u@a", "", "u@b", "u@a"]
	}`)
	s := mustOpen(t, dir)
	d := s.Snapshot()
	if d.Window.Width != DefaultWindowWidth || d.Window.Height != DefaultWindowHeight {
		t.Errorf("window = %+v", d.Window)
	}
	if d.Window.HasPosition {
		t.Errorf("out-of-range position should be dropped: %+v", d.Window)
	}
	if d.Appearance != AppearanceSystem || d.Language != LanguageEnglish || d.SizeUnits != SizeUnitsBinary {
		t.Errorf("enums not defaulted: %v %v %v", d.Appearance, d.Language, d.SizeUnits)
	}
	if d.MonitorSpanMS != DefaultSpanMS || d.HistorySpanMS != DefaultSpanMS || d.HistoryAll {
		t.Errorf("invalid legacy ranges must give defaults: %d %d %v", d.MonitorSpanMS, d.HistorySpanMS, d.HistoryAll)
	}
	if !reflect.DeepEqual(d.Curves, []Curve{CurveUtil, CurvePower}) {
		t.Errorf("curves = %v", d.Curves)
	}
	if len(d.Hosts) != 3 {
		t.Fatalf("hosts = %+v", d.Hosts)
	}
	a, e := d.Hosts[0], d.Hosts[2]
	if a.Port != 0 {
		t.Errorf("a = %+v", a)
	}
	if e.ID() != "e" {
		t.Errorf("e = %+v", e)
	}
	if !reflect.DeepEqual(d.CollapsedProcs, []string{"u@a", "u@b"}) {
		t.Errorf("collapsed procs = %v", d.CollapsedProcs)
	}
	if !reflect.DeepEqual(d.CollapsedGPUs, map[string][]int{"u@a": {1, 2}}) {
		t.Errorf("collapsed = %v", d.CollapsedGPUs)
	}

	for in, want := range map[string][2]int64{
		`"monitor_span_ms": 1000, "history_span_ms": 1000`:               {60_000, 60_000},
		`"monitor_span_ms": -5, "history_span_ms": 0`:                    {60_000, 60_000},
		`"monitor_span_ms": 99999999999, "history_span_ms": 99999999999`: {86_400_000, 99999999999},
		`"monitor_span_ms": 90000, "history_span_ms": 4000000`:           {90_000, 4_000_000},
		`"monitor_span_ms": "5m", "history_span_ms": 1.5`:                {DefaultSpanMS, DefaultSpanMS},
		`"version": 1`: {DefaultSpanMS, DefaultSpanMS},
	} {
		writeFile(t, dir, "{"+in+"}")
		d := mustOpen(t, dir).Snapshot()
		if got := [2]int64{d.MonitorSpanMS, d.HistorySpanMS}; got != want {
			t.Errorf("%s -> %v, want %v", in, got, want)
		}
	}

	writeFile(t, dir, `{"window":{"width":1,"height":50}}`)
	d = mustOpen(t, dir).Snapshot()
	if d.Window.Width != 200 || d.Window.Height != 200 {
		t.Errorf("small window should clamp: %+v", d.Window)
	}
}

func TestLoadLegacyRanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{
	  "version": 1,
	  "view_mode": "all", "shared_range": "15m", "interval_ms": 500,
	  "hosts": [{"addr": "a", "user": "u", "range": "3h", "interval_ms": 2000}]
	}`)
	s := mustOpen(t, dir)
	if d := s.Snapshot(); d.MonitorSpanMS != 900_000 || d.HistorySpanMS != DefaultSpanMS || d.HistoryAll {
		t.Errorf("shared_range not carried over: %+v", d)
	}
	if err := s.SetHistorySpan(60_000, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"view_mode", "shared_range", `"range"`, "interval_ms", "monitor_range", "history_range"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("rewritten file still has %s: %s", key, raw)
		}
	}
	if !strings.Contains(string(raw), `"monitor_span_ms": 900000`) {
		t.Errorf("monitor_span_ms missing: %s", raw)
	}

	cases := []struct {
		in       string
		mon, his int64
		all      bool
	}{
		{`{"monitor_range": "24h", "history_range": "12h"}`, 86_400_000, 43_200_000, false},
		{`{"monitor_range": "1m", "history_range": "all"}`, 60_000, DefaultSpanMS, true},
		{`{"shared_range": "all", "monitor_range": "1h"}`, DefaultSpanMS, DefaultSpanMS, false},
		{`{"shared_range": "30m", "monitor_range": "6h"}`, 21_600_000, DefaultSpanMS, false},
		{`{"shared_range": "30m"}`, 1_800_000, DefaultSpanMS, false},
		{`{"monitor_range": "all", "history_range": "7m"}`, DefaultSpanMS, DefaultSpanMS, false},
		{`{"monitor_range": 5, "history_range": ["all"]}`, DefaultSpanMS, DefaultSpanMS, false},
		{`{"monitor_span_ms": 120000, "monitor_range": "24h", "shared_range": "3h"}`, 120_000, DefaultSpanMS, false},
		{`{"history_span_ms": 180000, "history_range": "all"}`, DefaultSpanMS, 180_000, false},
		{`{"history_all": false, "history_range": "all"}`, DefaultSpanMS, DefaultSpanMS, false},
		{`{"history_span_ms": 180000, "history_all": true, "history_range": "60m"}`, DefaultSpanMS, 180_000, true},
	}
	for _, c := range cases {
		writeFile(t, dir, c.in)
		d := mustOpen(t, dir).Snapshot()
		if d.MonitorSpanMS != c.mon || d.HistorySpanMS != c.his || d.HistoryAll != c.all {
			t.Errorf("%s -> %d %d %v, want %d %d %v", c.in, d.MonitorSpanMS, d.HistorySpanMS, d.HistoryAll, c.mon, c.his, c.all)
		}
	}
}

func TestLoadPartialAndWrongTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{"language":"zh-TW","appearance":5,"size_units":3,"hosts":"nope","curves":[]}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := s.Snapshot()
	if d.Language != LanguageTraditionalChinese {
		t.Errorf("valid member lost: %v", d.Language)
	}
	if d.Appearance != AppearanceSystem || d.SizeUnits != SizeUnitsBinary || len(d.Hosts) != 0 {
		t.Errorf("wrong-typed members should fall back: %+v", d)
	}
	if d.Curves == nil || len(d.Curves) != 0 {
		t.Errorf("explicit empty curves should be kept: %#v", d.Curves)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*corrupt*"))
	if len(matches) != 0 {
		t.Errorf("wrong types must not quarantine the file")
	}
}

func TestUnknownMembersIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{"version":1,"future_thing":{"a":1},"language":"zh-TW",
	  "hosts":[{"addr":"a","user":"u","color":"red","interval_ms":400}]}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := s.Snapshot()
	if d.Language != LanguageTraditionalChinese || len(d.Hosts) != 1 {
		t.Errorf("data = %+v", d)
	}
}

func TestCorruptFileIsPreserved(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":   "\x00\x01 not json",
		"truncated": `{"version":1,"hosts":[{"addr":"a"`,
		"empty":     "",
		"array":     `[1,2,3]`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeFile(t, dir, content)
			s, err := Open(dir)
			if err == nil || !strings.Contains(err.Error(), "moved to") {
				t.Fatalf("expected a notice, got %v", err)
			}
			if !reflect.DeepEqual(s.Snapshot(), Default()) {
				t.Error("corrupt file should yield defaults")
			}
			matches, _ := filepath.Glob(p + ".corrupt-*")
			if len(matches) != 1 {
				t.Fatalf("backup files = %v", matches)
			}
			got, _ := os.ReadFile(matches[0])
			if string(got) != content {
				t.Error("backup content differs from the original")
			}
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Error("corrupt original should be moved away")
			}
			if err := s.SetLanguage(LanguageTraditionalChinese); err != nil {
				t.Fatal(err)
			}
			if err := s.SetAppearance(AppearanceDark); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); err != nil {
				t.Errorf("reopen after recovery: %v", err)
			}
		})
	}
}

func TestNewerVersionIsPreserved(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, `{"version":99,"language":"zh-TW"}`)
	s, err := Open(dir)
	if err == nil {
		t.Fatal("expected a notice for newer version")
	}
	if s.Snapshot().Language != LanguageTraditionalChinese || s.Snapshot().Version != CurrentVersion {
		t.Errorf("data = %+v", s.Snapshot())
	}
	if m, _ := filepath.Glob(p + ".v99-*"); len(m) != 1 {
		t.Errorf("backup = %v", m)
	}
}

func TestVersionWritten(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	s.SetLanguage(LanguageEnglish)
	raw, _ := os.ReadFile(s.Path())
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != float64(CurrentVersion) {
		t.Errorf("version = %v", m["version"])
	}
	t.Logf("%s", raw)
}

func TestSaveFailureKeepsMemory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, nil, 0o600)
	s, _ := Open(filepath.Join(blocker, "sub"))
	if err := s.SetLanguage(LanguageTraditionalChinese); err == nil {
		t.Fatal("expected save error")
	}
	if s.Snapshot().Language != LanguageEnglish {
		t.Error("memory changed despite failed save")
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	for i := 0; i < 20; i++ {
		if err := s.AddHost(HostConn{Addr: fmt.Sprintf("h%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Errorf("entries = %v", entries)
	}
	raw, _ := os.ReadFile(s.Path())
	if !json.Valid(raw) {
		t.Error("file is not valid JSON")
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	s.AddHost(HostConn{Addr: "a"})
	s.SetGPUCollapsed("a", 0, true)
	s.SetProcsCollapsed("a", true)
	d := s.Snapshot()
	d.Hosts[0].Addr = "zzz"
	d.Curves[0] = CurveFan
	d.CollapsedGPUs["a"][0] = 9
	d.CollapsedProcs[0] = "zzz"
	d2 := s.Snapshot()
	if d2.Hosts[0].Addr != "a" || d2.Curves[0] != CurveUtil || d2.CollapsedGPUs["a"][0] != 0 || d2.CollapsedProcs[0] != "a" {
		t.Error("snapshot aliases internal state")
	}
	hosts := s.Hosts()
	hosts[0].Addr = "q"
	if s.Hosts()[0].Addr != "a" {
		t.Error("Hosts aliases internal state")
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				id := fmt.Sprintf("h%d-%d", g, i)
				if err := s.AddHost(HostConn{Addr: id, User: "u"}); err != nil {
					t.Error(err)
				}
				s.SetMonitorSpan(60_000 + int64(i))
				s.SetProcsCollapsed("u@"+id, true)
				s.SetGPUCollapsed("u@"+id, g, true)
				s.SetLanguage(LanguageTraditionalChinese)
				s.Snapshot()
				s.Hosts()
				s.GPUCollapsed("u@"+id, g)
			}
		}(g)
	}
	wg.Wait()
	if n := len(s.Hosts()); n != 8*15 {
		t.Errorf("hosts = %d", n)
	}
	s2 := mustOpen(t, dir)
	if !reflect.DeepEqual(s.Snapshot(), s2.Snapshot()) {
		t.Error("on-disk state differs from memory")
	}
}

var forbidden = []string{"password", "passwd", "passphrase", "pwd", "secret", "token", "credential"}

func checkNoSecretKeys(t *testing.T, where string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			for _, f := range forbidden {
				if strings.Contains(strings.ToLower(k), f) {
					t.Errorf("%s has forbidden key %q", where, k)
				}
			}
			checkNoSecretKeys(t, where+"."+k, child)
		}
	case []any:
		for _, child := range x {
			checkNoSecretKeys(t, where+"[]", child)
		}
	}
}

func checkNoSecretFields(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map || typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return
	}
	seen[typ] = true
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		for _, w := range forbidden {
			if strings.Contains(strings.ToLower(f.Name), w) {
				t.Errorf("%s has forbidden field %s", typ, f.Name)
			}
		}
		checkNoSecretFields(t, f.Type, seen)
	}
}

func TestNoCredentialFields(t *testing.T) {
	checkNoSecretFields(t, reflect.TypeOf(Data{}), map[reflect.Type]bool{})

	dir := t.TempDir()
	s := mustOpen(t, dir)
	s.AddHost(HostConn{Addr: "a", User: "u", KeyFile: "/k"})
	s.SetGPUCollapsed("u@a", 0, true)
	raw, _ := os.ReadFile(s.Path())
	var m any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	checkNoSecretKeys(t, "root", m)
}

func TestDefaultDir(t *testing.T) {
	d, err := DefaultDir()
	if err != nil {
		t.Skip(err)
	}
	if filepath.Base(d) != "gpumon" || !filepath.IsAbs(d) {
		t.Errorf("DefaultDir = %s", d)
	}
	if got := HistoryDir("/x"); got != filepath.Join("/x", "history") {
		t.Errorf("HistoryDir = %s", got)
	}
}

func TestProcSortAndFilter(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	for _, a := range []string{"a", "b"} {
		if err := s.AddHost(HostConn{Addr: a, User: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.ProcSort("u@a"); got != DefaultProcSort() {
		t.Errorf("default sort = %+v", got)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetProcSort("u@a", ProcSort{Column: ProcColumnMem, Order: SortDesc}))
	must(s.SetProcSort("u@b", ProcSort{Column: ProcColumnUser, Order: SortAsc}))
	must(s.SetProcSort("u@b", DefaultProcSort()))
	must(s.SetProcFilter("u@a", ProcFilter{GPUs: []int{2, 0, 2}, User: "  mlin ", Command: "python"}))
	must(s.SetProcFilter("u@b", ProcFilter{User: "x"}))
	must(s.SetProcFilter("u@b", ProcFilter{GPUs: []int{}, User: "   "}))

	s2 := mustOpen(t, dir)
	d := s2.Snapshot()
	if !reflect.DeepEqual(d.ProcSorts, map[string]ProcSort{"u@a": {Column: ProcColumnMem, Order: SortDesc}}) {
		t.Errorf("sorts = %+v", d.ProcSorts)
	}
	want := ProcFilter{GPUs: []int{0, 2}, User: "mlin", Command: "python"}
	if !reflect.DeepEqual(d.ProcFilters, map[string]ProcFilter{"u@a": want}) {
		t.Errorf("filters = %+v", d.ProcFilters)
	}
	if got := s2.ProcFilter("u@b"); len(got.GPUs) != 0 || got.User != "" || got.Command != "" {
		t.Errorf("cleared filter = %+v", got)
	}

	for _, bad := range []ProcSort{{Column: "bogus", Order: SortAsc}, {Column: ProcColumnPID, Order: "up"}} {
		if err := s2.SetProcSort("u@a", bad); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("sort %+v accepted: %v", bad, err)
		}
	}
	if err := s2.SetProcSort("", DefaultProcSort()); err == nil {
		t.Error("empty host id accepted for sort")
	}
	if err := s2.SetProcFilter("u@a", ProcFilter{GPUs: []int{-1}}); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("negative gpu accepted: %v", err)
	}
	if err := s2.SetProcFilter("", ProcFilter{User: "x"}); err == nil {
		t.Error("empty host id accepted for filter")
	}
	if got := s2.ProcFilter("u@a"); !reflect.DeepEqual(got, want) {
		t.Errorf("failed setters must not change state: %+v", got)
	}

	if err := s2.UpdateHost("u@a", HostConn{Addr: "aa", User: "v"}); err != nil {
		t.Fatal(err)
	}
	if got := s2.ProcSort("v@aa"); got != (ProcSort{Column: ProcColumnMem, Order: SortDesc}) {
		t.Errorf("sort not migrated on rename: %+v", got)
	}
	if got := s2.ProcFilter("v@aa"); !reflect.DeepEqual(got, want) {
		t.Errorf("filter not migrated on rename: %+v", got)
	}
	d = s2.Snapshot()
	if _, ok := d.ProcSorts["u@a"]; ok {
		t.Errorf("old sort key kept: %+v", d.ProcSorts)
	}
	if _, ok := d.ProcFilters["u@a"]; ok {
		t.Errorf("old filter key kept: %+v", d.ProcFilters)
	}

	d.ProcFilters["v@aa"].GPUs[0] = 9
	if s2.ProcFilter("v@aa").GPUs[0] != 0 {
		t.Error("snapshot aliases filter gpus")
	}

	if err := s2.RemoveHost("v@aa"); err != nil {
		t.Fatal(err)
	}
	if d := s2.Snapshot(); len(d.ProcSorts) != 0 || len(d.ProcFilters) != 0 {
		t.Errorf("sort and filter not removed with host: %+v %+v", d.ProcSorts, d.ProcFilters)
	}
}

func TestLoadProcSortAndFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{
	  "version": 1,
	  "proc_sorts": {
	    "u@a": {"column": "command", "order": "desc"},
	    "u@b": {"column": "bogus", "order": "desc"},
	    "u@c": {"column": "pid", "order": "sideways"},
	    "u@d": {"column": "bogus", "order": "bogus"},
	    "u@e": {"column": "gpu", "order": "asc"},
	    "": {"column": "pid", "order": "desc"}
	  },
	  "proc_filters": {
	    "u@a": {"gpus": [3, 1, 3, -2], "user": " bob ", "command": ""},
	    "u@b": {"gpus": [], "user": "  ", "command": ""},
	    "u@c": {"gpus": null, "command": "train"},
	    "u@d": {}
	  }
	}`)
	d := mustOpen(t, dir).Snapshot()
	wantSorts := map[string]ProcSort{
		"u@a": {Column: ProcColumnCommand, Order: SortDesc},
		"u@b": {Column: ProcColumnGPU, Order: SortDesc},
		"u@c": {Column: ProcColumnPID, Order: SortAsc},
	}
	if !reflect.DeepEqual(d.ProcSorts, wantSorts) {
		t.Errorf("sorts = %+v", d.ProcSorts)
	}
	wantFilters := map[string]ProcFilter{
		"u@a": {GPUs: []int{1, 3}, User: "bob"},
		"u@c": {GPUs: []int{}, Command: "train"},
	}
	if !reflect.DeepEqual(d.ProcFilters, wantFilters) {
		t.Errorf("filters = %+v", d.ProcFilters)
	}

	// 舊設定檔沒有這兩個成員時為預設值, 亦即兩者皆為空.
	dir = t.TempDir()
	writeFile(t, dir, `{"version": 1, "collapsed_procs": ["u@a"]}`)
	s := mustOpen(t, dir)
	if d := s.Snapshot(); d.ProcSorts == nil || len(d.ProcSorts) != 0 || d.ProcFilters == nil || len(d.ProcFilters) != 0 {
		t.Errorf("legacy file: %+v %+v", d.ProcSorts, d.ProcFilters)
	}
	if s.ProcSort("u@a") != DefaultProcSort() {
		t.Errorf("legacy sort = %+v", s.ProcSort("u@a"))
	}
}

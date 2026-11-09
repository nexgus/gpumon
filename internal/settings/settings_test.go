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
	if d.ViewMode != ViewTabs || d.SharedRange != Range5Min || d.HistoryRange != Range5Min {
		t.Errorf("view = %v %v %v", d.ViewMode, d.SharedRange, d.HistoryRange)
	}
	if !reflect.DeepEqual(d.Curves, []Curve{CurveUtil, CurveMem}) {
		t.Errorf("curves = %v", d.Curves)
	}
	if len(AllCurves()) != MaxCurves || len(MonitorRanges()) != 9 || len(HistoryRanges()) != 10 {
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
	must(s.SetHostInterval("alice@10.0.0.5", 500))
	must(s.SetHostRange("alice@10.0.0.5", Range3Hours))
	must(s.SetWindow(Window{Width: 1000, Height: 700, HasPosition: true, X: -50, Y: 20}))
	must(s.SetAppearance(AppearanceDark))
	must(s.SetLanguage(LanguageTraditionalChinese))
	must(s.SetViewMode(ViewAll))
	must(s.SetSharedRange(Range24Hours))
	must(s.SetHistoryRange(RangeAll))
	must(s.SetCurves([]Curve{CurveTemp, CurveUtil, CurveClockMem}))
	must(s.SetGPUCollapsed("alice@10.0.0.5", 1, true))
	must(s.SetGPUCollapsed("alice@10.0.0.5", 0, true))
	must(s.SetGPUCollapsed("gpu02", 0, true))
	must(s.SetGPUCollapsed("gpu02", 0, false))

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
	h, _ := s2.Host("alice@10.0.0.5")
	if h.IntervalMS != 500 || h.Range != Range3Hours || h.Port != 2222 || h.KeyFile != "~/.ssh/k" {
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
	s.SetHostInterval("u@b", 300)
	s.SetGPUCollapsed("u@b", 3, true)
	if err := s.UpdateHost("u@b", HostConn{Addr: "bb", User: "v", Port: 22}); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got != "u@a,v@bb,u@c,u@d" {
		t.Fatalf("order after edit = %s", got)
	}
	h, ok := s.Host("v@bb")
	if !ok || h.IntervalMS != 300 {
		t.Errorf("per-host settings lost: %+v", h)
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
	if len(s.Snapshot().CollapsedGPUs) != 0 {
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
	for _, ms := range []int{199, 10001, 0, -5} {
		if err := s.SetHostInterval("u@a", ms); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("interval %d: %v", ms, err)
		}
	}
	for _, ms := range []int{200, 10000} {
		if err := s.SetHostInterval("u@a", ms); err != nil {
			t.Errorf("interval %d: %v", ms, err)
		}
	}
	if err := s.SetHostRange("u@a", RangeAll); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("monitor range must not accept all: %v", err)
	}
	if err := s.SetSharedRange("2m"); !errors.Is(err, ErrInvalidValue) {
		t.Error("bad shared range accepted")
	}
	if err := s.SetHistoryRange(RangeAll); err != nil {
		t.Error(err)
	}
	if err := s.SetAppearance("blue"); err == nil {
		t.Error("bad appearance accepted")
	}
	if err := s.SetLanguage("fr"); err == nil {
		t.Error("bad language accepted")
	}
	if err := s.SetViewMode("x"); err == nil {
		t.Error("bad view mode accepted")
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
	if s.Snapshot().Hosts[0].IntervalMS != 10000 {
		t.Error("failed setters must not change state")
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
	  "appearance": "neon", "language": "xx", "view_mode": "grid",
	  "shared_range": "all", "history_range": "7m",
	  "curves": ["util", "bogus", "util", "power"],
	  "hosts": [
	    {"addr": "a", "user": "u", "port": 99999, "interval_ms": 50, "range": "2h"},
	    {"addr": "b", "user": "u", "interval_ms": 99999, "range": "all"},
	    {"addr": "A", "user": "u"},
	    {"addr": "", "user": "u"},
	    {"addr": "c d"},
	    {"addr": "e"}
	  ],
	  "collapsed_gpus": {"u@a": [2, 1, 2, -4], "u@b": [], "x": [-1]}
	}`)
	s := mustOpen(t, dir)
	d := s.Snapshot()
	if d.Window.Width != DefaultWindowWidth || d.Window.Height != DefaultWindowHeight {
		t.Errorf("window = %+v", d.Window)
	}
	if d.Window.HasPosition {
		t.Errorf("out-of-range position should be dropped: %+v", d.Window)
	}
	if d.Appearance != AppearanceSystem || d.Language != LanguageEnglish || d.ViewMode != ViewTabs {
		t.Errorf("enums not defaulted: %v %v %v", d.Appearance, d.Language, d.ViewMode)
	}
	if d.SharedRange != Range5Min || d.HistoryRange != Range5Min {
		t.Errorf("ranges = %v %v", d.SharedRange, d.HistoryRange)
	}
	if !reflect.DeepEqual(d.Curves, []Curve{CurveUtil, CurvePower}) {
		t.Errorf("curves = %v", d.Curves)
	}
	if len(d.Hosts) != 3 {
		t.Fatalf("hosts = %+v", d.Hosts)
	}
	a, b, e := d.Hosts[0], d.Hosts[1], d.Hosts[2]
	if a.Port != 0 || a.IntervalMS != 200 || a.Range != Range5Min {
		t.Errorf("a = %+v", a)
	}
	if b.IntervalMS != 10000 || b.Range != Range5Min {
		t.Errorf("b = %+v", b)
	}
	if e.ID() != "e" || e.IntervalMS != DefaultIntervalMS {
		t.Errorf("e = %+v", e)
	}
	if !reflect.DeepEqual(d.CollapsedGPUs, map[string][]int{"u@a": {1, 2}}) {
		t.Errorf("collapsed = %v", d.CollapsedGPUs)
	}

	writeFile(t, dir, `{"window":{"width":1,"height":50}}`)
	d = mustOpen(t, dir).Snapshot()
	if d.Window.Width != 200 || d.Window.Height != 200 {
		t.Errorf("small window should clamp: %+v", d.Window)
	}
}

func TestLoadPartialAndWrongTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{"language":"zh-TW","appearance":5,"hosts":"nope","curves":[]}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := s.Snapshot()
	if d.Language != LanguageTraditionalChinese {
		t.Errorf("valid member lost: %v", d.Language)
	}
	if d.Appearance != AppearanceSystem || len(d.Hosts) != 0 {
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
	if d.Language != LanguageTraditionalChinese || len(d.Hosts) != 1 || d.Hosts[0].IntervalMS != 400 {
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
	d := s.Snapshot()
	d.Hosts[0].Addr = "zzz"
	d.Curves[0] = CurveFan
	d.CollapsedGPUs["a"][0] = 9
	d2 := s.Snapshot()
	if d2.Hosts[0].Addr != "a" || d2.Curves[0] != CurveUtil || d2.CollapsedGPUs["a"][0] != 0 {
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
				s.SetHostInterval("u@"+id, 200+i)
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

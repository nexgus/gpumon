package sshconn

import "testing"

func mustVersion(t *testing.T, s string) ProgramVersion {
	t.Helper()
	v, err := ParseProgramVersion(s)
	if err != nil {
		t.Fatalf("ParseProgramVersion(%q): %v", s, err)
	}
	return v
}

func TestProgramVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"0.1.1", "0.1.0", 1},
		{"0.1.0-alpha.1", "0.1.0-beta.0", -1},
		{"0.1.0-beta.5", "0.1.0-rc.1", -1},
		{"0.1.0-rc.9", "0.1.0", -1},
		{"0.1.0-alpha.0", "0.1.0", -1},
		{"0.1.0", "0.1.0-rc.1", 1},
		{"0.1.0-beta.2", "0.1.0-beta.10", -1},
		{"0.1.0-rc.3", "0.1.0-rc.3", 0},
		{"0.2.0-alpha.1", "0.1.9", 1},
		{"0.1.0-rc.1", "0.0.9", 1},
	}
	for _, c := range cases {
		a, b := mustVersion(t, c.a), mustVersion(t, c.b)
		if got := a.Compare(b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := b.Compare(a); got != -c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestParseProgramVersionInvalid(t *testing.T) {
	for _, s := range []string{
		"", "0.1", "0.1.0.0", "01.0.0", "0.01.0", "0.1.0-", "0.1.0-gamma.1", "0.1.0-beta",
		"0.1.0-beta.01", "0.1.0+3f2a9c1", "v0.1.0", " 0.1.0", "99999999999999999999.0.0",
	} {
		if _, err := ParseProgramVersion(s); err == nil {
			t.Errorf("ParseProgramVersion(%q) succeeded, want error", s)
		}
	}
	if v := mustVersion(t, "1.2.3-beta.4"); v.String() != "1.2.3-beta.4" || v.Stage() != "beta" {
		t.Errorf("round trip: %s %s", v, v.Stage())
	}
}

func TestParseVersionOutput(t *testing.T) {
	good := []struct {
		in    string
		ver   string
		proto int
	}{
		{"gpumon 0.1.0+3f2a9c1 (protocol 1)\n", "0.1.0", 1},
		{"gpumon 0.2.0-beta.3+3f2a9c1-dirty (protocol 2)\n", "0.2.0-beta.3", 2},
		{"gpumon 0.1.0+unknown (protocol 1)\n", "0.1.0", 1},
		{"gpumon 0.1.0+unknown-dirty (protocol 12)", "0.1.0", 12},
		{"gpumon 10.20.30-rc.0+abcdef0 (protocol 3)\n", "10.20.30-rc.0", 3},
	}
	for _, c := range good {
		info, err := ParseVersionOutput(c.in)
		if err != nil {
			t.Errorf("ParseVersionOutput(%q): %v", c.in, err)
			continue
		}
		if info.Version.String() != c.ver || info.Protocol != c.proto {
			t.Errorf("ParseVersionOutput(%q) = %s, %d", c.in, info.Version, info.Protocol)
		}
	}
	bad := []string{
		"",
		"\n",
		"gpumon 0.1.0 (protocol 1)\n",
		"gpumon 0.1.0+3F2A9C1 (protocol 1)\n",
		"gpumon 0.1.0+3f2a9c (protocol 1)\n",
		"gpumon 0.1.0+3f2a9c1 (protocol 0)\n",
		"gpumon 0.1.0+3f2a9c1 (protocol 01)\n",
		"gpumon 0.1.0+3f2a9c1 (protocol 1)\n\n",
		"gpumon 0.1.0+3f2a9c1 (protocol 1)\r\n",
		"gpumon 0.1.0+3f2a9c1 (protocol 1)\nextra\n",
		"warning\ngpumon 0.1.0+3f2a9c1 (protocol 1)\n",
		"gpumon  0.1.0+3f2a9c1 (protocol 1)\n",
		"gpumon 0.1.0+3f2a9c1  (protocol 1)\n",
		"gpumon 0.1.0+3f2a9c1 (protocol  1)\n",
		"GPUMON 0.1.0+3f2a9c1 (protocol 1)\n",
		"gpumon 0.1.0-beta+3f2a9c1 (protocol 1)\n",
		"gpumon 0.1.0+3f2a9c1-dirty-dirty (protocol 1)\n",
		"gpumon 0.1.0+3f2a9c1 (protocol 1) \n",
		"gpumon 0.1.0+3f2a9c1 (protocol 99999999999999999999)\n",
		"bash: /home/u/.gpumon/gpumon: No such file or directory\n",
	}
	for _, s := range bad {
		if _, err := ParseVersionOutput(s); err == nil {
			t.Errorf("ParseVersionOutput(%q) succeeded, want error", s)
		}
	}
}

func TestDecideUpload(t *testing.T) {
	gui := mustVersion(t, "0.2.0")
	higher := mustVersion(t, "0.3.0-alpha.1")
	equal := mustVersion(t, "0.2.0")
	lower := mustVersion(t, "0.2.0-rc.1")
	rows := []struct {
		name    string
		remote  *ProgramVersion
		running bool
		want    uploadAction
	}{
		{"unknown", nil, false, actionUpload},
		{"unknown, running is not checked", nil, true, actionUpload},
		{"equal", &equal, false, actionUseExisting},
		{"higher", &higher, false, actionUseExisting},
		{"lower, not running", &lower, false, actionUpload},
		{"lower, running", &lower, true, actionUseExisting},
	}
	for _, r := range rows {
		got := decideUpload(r.remote, gui)
		if got == actionCheckRunning {
			if r.remote == nil || r.remote.Compare(gui) >= 0 {
				t.Errorf("%s: running check requested unexpectedly", r.name)
			}
			got = decideAfterRunningCheck(r.running)
		} else if r.remote != nil && r.remote.Compare(gui) < 0 {
			t.Errorf("%s: running check was not requested", r.name)
		}
		if got != r.want {
			t.Errorf("%s: got %d, want %d", r.name, got, r.want)
		}
	}
}

func TestCompareProtocol(t *testing.T) {
	if compareProtocol(2, 2) != protocolEqual {
		t.Error("equal")
	}
	if compareProtocol(1, 2) != protocolOlder {
		t.Error("older")
	}
	if compareProtocol(3, 2) != protocolNewer {
		t.Error("newer")
	}
}

func TestIsProgramRunning(t *testing.T) {
	const p = "/home/alice/.gpumon/gpumon"
	out := "lrwxrwxrwx 1 alice alice 0 Nov  5 10:00 /proc/100/exe -> /usr/bin/bash\n" +
		"lrwxrwxrwx 1 alice alice 0 Nov  5 10:00 /proc/101/exe -> /home/alice/.gpumon/gpumon.tmp\n" +
		"lrwxrwxrwx 1 alice alice 0 Nov  5 10:00 /proc/102/exe -> /home/alice/.gpumon/gpumon2\n" +
		"lrwxrwxrwx 1 bob   bob   0 Nov  5 10:00 /proc/103/exe\n"
	if isProgramRunning(out, p) {
		t.Error("detected running without a matching line")
	}
	if !isProgramRunning(out+"lrwxrwxrwx 1 alice alice 0 Nov  5 10:00 /proc/104/exe -> "+p+"\n", p) {
		t.Error("exact path not detected")
	}
	if !isProgramRunning(out+"lrwxrwxrwx 1 alice alice 0 Nov  5 10:00 /proc/105/exe -> "+p+" (deleted)\n", p) {
		t.Error("deleted path not detected")
	}
	if isProgramRunning("lrwxrwxrwx 1 a a 0 Nov  5 10:00 /proc/106/exe -> "+p+" (deleted) x\n", p) {
		t.Error("suffix after (deleted) accepted")
	}
	if isProgramRunning("lrwxrwxrwx 1 a a 0 Nov  5 10:00 /proc/107/exe -> /other"+p+"\n", p) {
		t.Error("path with prefix accepted")
	}
	const spaced = "/home/a b/.gpumon/gpumon"
	if !isProgramRunning("lrwxrwxrwx 1 a a 0 Nov  5 10:00 /proc/108/exe -> "+spaced+"\n", spaced) {
		t.Error("path with space not detected")
	}
	if isProgramRunning("", p) {
		t.Error("empty output")
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/home/u/.gpumon/gpumon": "/home/u/.gpumon/gpumon",
		"/home/a b/x":            "'/home/a b/x'",
		"it's":                   `'it'\''s'`,
		"":                       "''",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
	if got := shCommand(`printf %s "$HOME"`); got != `sh -c 'printf %s "$HOME"'` {
		t.Errorf("shCommand = %s", got)
	}
	if got := shCommand("uname -sm"); got != "sh -c 'uname -sm'" {
		t.Errorf("shCommand = %s", got)
	}
}

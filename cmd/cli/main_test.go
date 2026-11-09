package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gpumon/internal/protocol"
)

func TestParseLine(t *testing.T) {
	ms500 := 500
	cases := []struct {
		line    string
		want    protocol.Command
		help    bool
		errLike string
	}{
		{line: `{"cmd":"set_interval","ms":500}`, want: protocol.Command{Cmd: "set_interval", Ms: &ms500}},
		{line: "{\"cmd\":\"ping\",\"extra\":1}\r\n", want: protocol.Command{Cmd: "ping"}},
		{line: `{"cmd":"set_fields","fields":["util","mem_used"]}`, want: protocol.Command{Cmd: "set_fields", Fields: []string{"util", "mem_used"}}},
		{line: `{"cmd":"set_fields"}`, want: protocol.Command{Cmd: "set_fields"}},
		{line: "/set_interval 500", want: protocol.Command{Cmd: "set_interval", Ms: &ms500}},
		{line: "/set_interval", want: protocol.Command{Cmd: "set_interval"}},
		{line: "/set_fields util mem_used", want: protocol.Command{Cmd: "set_fields", Fields: []string{"util", "mem_used"}}},
		{line: "/set_fields", want: protocol.Command{Cmd: "set_fields"}},
		{line: "/sample_now", want: protocol.Command{Cmd: "sample_now"}},
		{line: "/ping\n", want: protocol.Command{Cmd: "ping"}},
		{line: "/help", help: true},
		{line: "/help me", errLike: "takes no arguments"},
		{line: "/ping now", errLike: "takes no arguments"},
		{line: "/set_interval abc", errLike: "invalid ms"},
		{line: "/set_interval 1 2", errLike: "at most one"},
		{line: "/", errLike: "missing command"},
		{line: "/bogus", errLike: "unknown command: bogus"},
		{line: "ping", errLike: "invalid input"},
		{line: " {\"cmd\":\"ping\"}", errLike: "invalid input"},
		{line: `{"cmd":`, errLike: "invalid JSON"},
		{line: `{"ms":500}`, errLike: "missing \"cmd\""},
		{line: `{"cmd":"set_interval","ms":500.5}`, errLike: "invalid JSON"},
		{line: `{"cmd":"set_interval","ms":"500"}`, errLike: "invalid JSON"},
	}
	for _, c := range cases {
		in, err := parseLine(c.line)
		if c.errLike != "" {
			if err == nil || !strings.Contains(err.Error(), c.errLike) {
				t.Errorf("parseLine(%q) err = %v, want containing %q", c.line, err, c.errLike)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLine(%q) unexpected error %v", c.line, err)
			continue
		}
		if in.help != c.help {
			t.Errorf("parseLine(%q) help = %v", c.line, in.help)
		}
		got, _ := json.Marshal(in.cmd)
		want, _ := json.Marshal(c.want)
		if !c.help && !bytes.Equal(got, want) {
			t.Errorf("parseLine(%q) = %s, want %s", c.line, got, want)
		}
	}
	if _, err := parseLine("  \r\n"); !errors.Is(err, errBlank) {
		t.Errorf("blank line err = %v", err)
	}
}

func TestShorthandEqualsJSON(t *testing.T) {
	pairs := [][2]string{
		{"/set_interval 500", `{"cmd":"set_interval","ms":500}`},
		{"/set_interval", `{"cmd":"set_interval"}`},
		{"/set_fields util mem_used", `{"cmd":"set_fields","fields":["util","mem_used"]}`},
		{"/set_fields", `{"cmd":"set_fields"}`},
		{"/sample_now", `{"cmd":"sample_now"}`},
		{"/ping", `{"cmd":"ping"}`},
	}
	for _, p := range pairs {
		a, err1 := parseLine(p[0])
		b, err2 := parseLine(p[1])
		ja, _ := json.Marshal(a.cmd)
		jb, _ := json.Marshal(b.cmd)
		if err1 != nil || err2 != nil || !bytes.Equal(ja, jb) {
			t.Errorf("%s -> %s, %s -> %s (%v, %v)", p[0], ja, p[1], jb, err1, err2)
		}
	}
}

func TestValidate(t *testing.T) {
	if ms, err := validateInterval(nil); err != nil || ms != 1000 {
		t.Errorf("default interval = %d, %v", ms, err)
	}
	for _, v := range []int{200, 10000} {
		if ms, err := validateInterval(&v); err != nil || ms != v {
			t.Errorf("interval %d rejected: %v", v, err)
		}
	}
	for _, v := range []int{199, 10001, 0, -5} {
		if _, err := validateInterval(&v); err == nil {
			t.Errorf("interval %d accepted", v)
		}
	}
	fs, err := validateFields(nil)
	if err != nil || len(fs.list()) != len(protocol.SampleFields) {
		t.Errorf("all fields = %v, %v", fs.list(), err)
	}
	fs, err = validateFields([]string{"procs", "util"})
	if err != nil || strings.Join(fs.list(), ",") != "util,procs" {
		t.Errorf("fields = %v, %v", fs.list(), err)
	}
	if _, err := validateFields([]string{"util", "id"}); err == nil {
		t.Error("unknown field accepted")
	}
	fs, _ = validateFields([]string{})
	if len(fs.list()) != 0 {
		t.Errorf("empty fields = %v", fs.list())
	}
}

func TestParseArgs(t *testing.T) {
	ok := []struct {
		args            []string
		loop            bool
		interval, steps int
	}{
		{nil, false, 1000, 1},
		{[]string{"--steps", "3", "--interval", "500"}, false, 500, 3},
		{[]string{"--loop"}, true, 1000, 1},
		{[]string{"--loop", "--interval=200"}, true, 200, 1},
		{[]string{"--interval", "10000"}, false, 10000, 1},
	}
	for _, c := range ok {
		o, err := parseArgs(c.args)
		if err != nil || o.loop != c.loop || o.intervalMs != c.interval || o.steps != c.steps {
			t.Errorf("parseArgs(%v) = %+v, %v", c.args, o, err)
		}
	}
	bad := [][]string{
		{"--loop", "--steps", "2"},
		{"--loop", "--steps", "1"},
		{"--interval", "199"},
		{"--interval", "10001"},
		{"--interval", "abc"},
		{"--steps", "0"},
		{"--bogus"},
		{"extra"},
	}
	for _, args := range bad {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) accepted", args)
		}
	}
	if _, err := parseArgs([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h err = %v", err)
	}
}

func TestRunVersionAndUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d", code)
	}
	re := regexp.MustCompile(`^gpumon (0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?\+([0-9a-f]{7}|unknown)(-dirty)? \(protocol ([1-9][0-9]*)\)$`)
	if s := out.String(); !strings.HasSuffix(s, "\n") || !re.MatchString(strings.TrimSuffix(s, "\n")) || strings.Count(s, "\n") != 1 {
		t.Errorf("--version output %q does not match the format", s)
	}
	out.Reset()
	if code := run([]string{"--bogus"}, nil, &out, &errOut); code == 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("bad flag: code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// fakeCollector 以固定內容模擬取樣; delay 不為零時每次取樣先等待該時間.
type fakeCollector struct {
	mu     sync.Mutex
	delay  time.Duration
	calls  int
	procs  []bool
	closed bool
}

func (f *fakeCollector) Devices() []protocol.Device {
	return []protocol.Device{{ID: 0, Vendor: "nvidia", Name: "Fake"}}
}

func (f *fakeCollector) Sample(withProcs bool) []protocol.GPUSample {
	f.mu.Lock()
	f.calls++
	f.procs = append(f.procs, withProcs)
	d := f.delay
	f.mu.Unlock()
	time.Sleep(d)
	u, m := 5, uint64(100)
	s := protocol.GPUSample{ID: 0, Util: &u, MemUsed: &m, Temp: &u}
	if withProcs {
		s.Procs = []protocol.Process{}
	}
	return []protocol.GPUSample{s}
}

func (f *fakeCollector) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// syncBuffer 為可同時讀寫的 bytes.Buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// messages 將輸出逐行解碼為 map.
func messages(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func TestRunLoopSteps(t *testing.T) {
	col := &fakeCollector{}
	var out, errOut syncBuffer
	code := runLoop(loopConfig{intervalMs: 200, steps: 3, watchdog: time.Hour}, col, &out, &errOut, "h", "0.1.0")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut.String())
	}
	msgs := messages(t, out.String())
	if len(msgs) != 4 || msgs[0]["type"] != "devices" || msgs[0]["protocol"] != float64(1) || msgs[0]["hostname"] != "h" {
		t.Fatalf("output %s", out.String())
	}
	var prev float64
	for _, m := range msgs[1:] {
		ts := m["ts"].(float64)
		if m["type"] != "sample" || ts < prev {
			t.Errorf("bad sample %v", m)
		}
		prev = ts
	}
	if !col.closed {
		t.Error("collector not closed")
	}
}

func TestRunLoopCommands(t *testing.T) {
	col := &fakeCollector{}
	pr, pw := io.Pipe()
	var out, errOut syncBuffer
	done := make(chan int)
	go func() {
		done <- runLoop(loopConfig{intervalMs: 10000, stdin: pr, watchdog: time.Hour}, col, &out, &errOut, "h", "0.1.0")
	}()
	lines := []string{
		`{"cmd":"ping"}`,
		"/set_interval 500",
		"/set_interval 50",
		`{"cmd":"set_interval"}`,
		"/set_fields util bogus",
		"/set_fields util",
		"/sample_now",
		"",
		"hello",
		`{"cmd":"help"}`,
		"/help",
	}
	for _, l := range lines {
		io.WriteString(pw, l+"\n")
		time.Sleep(30 * time.Millisecond)
	}
	pw.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not exit on stdin EOF")
	}
	msgs := messages(t, out.String())
	var kinds []string
	for _, m := range msgs {
		k := m["type"].(string)
		switch k {
		case "ack":
			k += ":" + m["cmd"].(string)
		case "error":
			if _, ok := m["ts"]; !ok {
				t.Errorf("error without ts: %v", m)
			}
		}
		kinds = append(kinds, k)
	}
	want := "devices sample pong ack:set_interval error ack:set_interval error ack:set_fields sample error error"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("message kinds\n got %s\nwant %s\n%s", got, want, out.String())
	}
	if msgs[3]["ms"] != float64(500) || msgs[5]["ms"] != float64(1000) {
		t.Errorf("acks %v %v", msgs[3], msgs[5])
	}
	last := msgs[8]["gpus"].([]any)[0].(map[string]any)
	if len(last) != 2 || last["util"] != float64(5) {
		t.Errorf("filtered sample = %v", last)
	}
	if col.procs[len(col.procs)-1] {
		t.Error("procs collected although not selected")
	}
	if !strings.Contains(errOut.String(), "/set_interval") {
		t.Errorf("/help not printed to stderr: %q", errOut.String())
	}
}

func TestRunLoopWatchdog(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out, errOut syncBuffer
	done := make(chan int)
	go func() {
		done <- runLoop(loopConfig{intervalMs: 10000, stdin: pr, watchdog: 300 * time.Millisecond}, &fakeCollector{}, &out, &errOut, "h", "0.1.0")
	}()
	// 持續送入輸入時看門狗不觸發.
	for range 4 {
		time.Sleep(150 * time.Millisecond)
		io.WriteString(pw, "/ping\n")
	}
	select {
	case <-done:
		t.Fatal("watchdog fired although input kept arriving")
	default:
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not fire")
	}
	if !strings.Contains(errOut.String(), "no input") {
		t.Errorf("stderr %q", errOut.String())
	}
}

func TestRunLoopSignal(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	sigs := make(chan os.Signal, 1)
	done := make(chan int)
	var out, errOut syncBuffer
	go func() {
		done <- runLoop(loopConfig{intervalMs: 10000, stdin: pr, watchdog: time.Hour, signals: sigs}, &fakeCollector{}, &out, &errOut, "h", "0.1.0")
	}()
	time.Sleep(50 * time.Millisecond)
	sigs <- syscall.SIGHUP
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not exit on signal")
	}
}

// failWriter 於寫入指定行數後回傳 EPIPE.
type failWriter struct {
	n int
}

func (w *failWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, syscall.EPIPE
	}
	w.n--
	return len(p), nil
}

func TestRunLoopStdoutFailure(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	done := make(chan int)
	var errOut syncBuffer
	go func() {
		done <- runLoop(loopConfig{intervalMs: 200, stdin: pr, watchdog: time.Hour}, &fakeCollector{}, &failWriter{n: 2}, &errOut, "h", "0.1.0")
	}()
	select {
	case code := <-done:
		if code == 0 {
			t.Error("exit code 0 on stdout failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not exit on stdout failure")
	}
}

func TestRunLoopSampleTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for sampleTimeout")
	}
	col := &fakeCollector{delay: sampleTimeout + 500*time.Millisecond}
	pr, pw := io.Pipe()
	var out, errOut syncBuffer
	done := make(chan int)
	go func() {
		done <- runLoop(loopConfig{intervalMs: 10000, stdin: pr, watchdog: time.Hour}, col, &out, &errOut, "h", "0.1.0")
	}()
	// 取樣卡住期間主迴圈仍回應 ping.
	time.Sleep(100 * time.Millisecond)
	io.WriteString(pw, "/ping\n")
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(out.String(), `"pong"`) {
		t.Errorf("no pong while sampling is stuck: %s", out.String())
	}
	time.Sleep(sampleTimeout)
	if !strings.Contains(out.String(), "sampling timed out") {
		t.Errorf("no timeout error: %s", out.String())
	}
	pw.Close()
	<-done
	if strings.Contains(out.String(), `"type":"sample"`) {
		t.Errorf("late sample was emitted: %s", out.String())
	}
}

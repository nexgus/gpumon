package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpumon/internal/protocol"
	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

// testTiming 為測試用的縮短時間. 心跳與重試間隔為 60 ms.
func testTiming() Timing {
	return Timing{
		Heartbeat:        60 * time.Millisecond,
		Watchdog:         time.Hour,
		MaxRetries:       5,
		ConnectTimeout:   time.Second,
		ReconnectTimeout: 50 * time.Millisecond,
		DevicesTimeout:   300 * time.Millisecond,
		Flush:            time.Hour,
		ProcessRecord:    60 * time.Second,
	}
}

// eventLog 記錄 Manager 送出的所有通知.
type eventLog struct {
	mu     sync.Mutex
	events []event
}

type event struct {
	name string
	data any
}

func (l *eventLog) Notify(name string, data any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event{name, data})
}

func (l *eventLog) all(name string) []any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []any
	for _, e := range l.events {
		if e.name == name {
			out = append(out, e.data)
		}
	}
	return out
}

// states 回傳主機狀態通知的狀態序列, 相鄰重複者合併.
func (l *eventLog) states(id string) []State {
	var out []State
	for _, d := range l.all(EventHost) {
		st := d.(HostStatus)
		if st.ID != id {
			continue
		}
		if len(out) == 0 || out[len(out)-1] != st.State {
			out = append(out, st.State)
		}
	}
	return out
}

// fakeClock 為可控制的本機時鐘.
type fakeClock struct{ us atomic.Int64 }

func (c *fakeClock) now() time.Time   { return time.UnixMicro(c.us.Load()) }
func (c *fakeClock) set(us int64)     { c.us.Store(us) }
func (c *fakeClock) advance(us int64) { c.us.Add(us) }

// dialCall 為 fakeDialer 收到的一次連線要求.
type dialCall struct {
	at     time.Time
	target Target
}

// fakeDialer 以測試提供的函數決定每次連線的結果. n 為第幾次呼叫, 由 1 起算.
type fakeDialer struct {
	mu    sync.Mutex
	calls []dialCall
	fn    func(ctx context.Context, n int, t Target, a Auth) (Link, error)
}

func (d *fakeDialer) Dial(ctx context.Context, t Target, a Auth) (Link, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dialCall{at: time.Now(), target: t})
	n := len(d.calls)
	fn := d.fn
	d.mu.Unlock()
	return fn(ctx, n, t, a)
}

func (d *fakeDialer) set(fn func(ctx context.Context, n int, t Target, a Auth) (Link, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fn = fn
}

func (d *fakeDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func (d *fakeDialer) callTimes() []time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []time.Time
	for _, c := range d.calls {
		out = append(out, c.at)
	}
	return out
}

// fakeLink 模擬一個已啟動的遠端程式. 測試以 send 系列方法送出訊息.
type fakeLink struct {
	user  string
	proto int

	inR  *io.PipeReader
	inW  *io.PipeWriter
	outR *io.PipeReader
	outW *io.PipeWriter
	errR *io.PipeReader
	errW *io.PipeWriter

	cmds chan protocol.Command
	// pong 為真時自動回應 ping, ack 為真時自動回應 set_interval.
	pong, ack atomic.Bool

	wmu  sync.Mutex
	once sync.Once
	done chan struct{}
	err  error
}

func newFakeLink(user string) *fakeLink {
	l := &fakeLink{user: user, proto: protocol.Version, cmds: make(chan protocol.Command, 100), done: make(chan struct{})}
	l.inR, l.inW = io.Pipe()
	l.outR, l.outW = io.Pipe()
	l.errR, l.errW = io.Pipe()
	l.pong.Store(true)
	l.ack.Store(true)
	go func() {
		sc := bufio.NewScanner(l.inR)
		for sc.Scan() {
			var c protocol.Command
			if json.Unmarshal(sc.Bytes(), &c) != nil {
				continue
			}
			select {
			case l.cmds <- c:
			default:
			}
			switch {
			case c.Cmd == protocol.CmdPing && l.pong.Load():
				go l.send(map[string]string{"type": "pong"})
			case c.Cmd == protocol.CmdSetInterval && l.ack.Load() && c.Ms != nil:
				go l.send(map[string]any{"type": "ack", "cmd": "set_interval", "ms": *c.Ms})
			}
		}
	}()
	return l
}

func (l *fakeLink) User() string      { return l.user }
func (l *fakeLink) Protocol() int     { return l.proto }
func (l *fakeLink) Stdin() io.Writer  { return l.inW }
func (l *fakeLink) Stdout() io.Reader { return l.outR }
func (l *fakeLink) Stderr() io.Reader { return l.errR }

func (l *fakeLink) Wait() error {
	<-l.done
	return l.err
}

// die 模擬 exec 通道關閉.
func (l *fakeLink) die(err error) {
	l.once.Do(func() {
		l.err = err
		l.outW.Close()
		l.errW.Close()
		l.inR.Close()
		close(l.done)
	})
}

func (l *fakeLink) Close() error {
	l.die(nil)
	return nil
}

func (l *fakeLink) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *fakeLink) sendLine(s string) {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	io.WriteString(l.outW, s+"\n")
}

func (l *fakeLink) send(v any) {
	b, _ := json.Marshal(v)
	l.sendLine(string(b))
}

func ip(v int) *int { return &v }

func u64(v uint64) *uint64 { return &v }

// nvidiaDevices 回傳含指定 id 的 NVIDIA GPU 的 devices 訊息.
func nvidiaDevices(ids ...int) protocol.Devices {
	d := protocol.Devices{Type: "devices", Protocol: protocol.Version, Version: "0.1.0", Hostname: "fake", GPUs: []protocol.Device{}}
	for _, id := range ids {
		d.GPUs = append(d.GPUs, protocol.Device{ID: id, Vendor: "nvidia", Name: fmt.Sprintf("GPU %d", id), MemTotal: u64(1000)})
	}
	return d
}

// sampleMsg 回傳一筆 sample 訊息, 每張 GPU 的使用率為 util, 程序為 pids.
func sampleMsg(ts float64, util int, ids []int, pids ...int) protocol.Sample {
	s := protocol.Sample{Type: "sample", TS: protocol.Timestamp(ts)}
	for _, id := range ids {
		g := protocol.GPUSample{ID: id, Util: ip(util), MemUsed: u64(500), Procs: []protocol.Process{}}
		for _, pid := range pids {
			g.Procs = append(g.Procs, protocol.Process{PID: pid, User: "alice", Cmd: "python"})
		}
		s.GPUs = append(s.GPUs, g)
	}
	return s
}

// env 為一組測試用的 Manager 與其相依物件.
type env struct {
	t      *testing.T
	dir    string
	store  *settings.Store
	dialer *fakeDialer
	events *eventLog
	clock  *fakeClock
	m      *Manager
	users  map[string]string
}

func newEnv(t *testing.T, timing Timing) *env {
	t.Helper()
	dir := t.TempDir()
	store, err := settings.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dir: dir, store: store, dialer: &fakeDialer{}, events: &eventLog{}, clock: &fakeClock{}, users: map[string]string{}}
	e.clock.set(1_730_700_000_000_000)
	e.m = New(Options{
		DataDir: dir, Settings: store, Dialer: e.dialer, Notifier: e.events, Timing: timing,
		Now: e.clock.now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ResolveUser: func(addr string) (string, error) { return e.users[addr], nil },
	})
	t.Cleanup(e.m.Close)
	return e
}

func (e *env) addHost(user, addr string) string {
	e.t.Helper()
	st, err := e.m.AddHost(settings.HostConn{Addr: addr, User: user})
	if err != nil {
		e.t.Fatal(err)
	}
	return st.ID
}

// eventually 每 2 ms 檢查一次 cond, 逾時即失敗.
func (e *env) eventually(what string, timeout time.Duration, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (e *env) status(id string) HostStatus {
	e.t.Helper()
	st, err := e.m.Host(id)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) waitState(id string, want State) {
	e.t.Helper()
	e.eventually(fmt.Sprintf("state %s", want), 3*time.Second, func() bool { return e.status(id).State == want })
}

// waitSamples 等待主機的最新一筆取樣時刻為 t.
func (e *env) waitLiveTime(id string, t int64) {
	e.t.Helper()
	e.eventually(fmt.Sprintf("live time %d", t), 3*time.Second, func() bool {
		lv, _ := e.m.Live(id)
		return lv.HasSample && lv.Time == t
	})
}

// links 依序回傳每次成功連線的 fakeLink.
type links struct {
	mu  sync.Mutex
	all []*fakeLink
	ch  chan *fakeLink
}

func newLinks() *links { return &links{ch: make(chan *fakeLink, 100)} }

func (ls *links) add(l *fakeLink) {
	ls.mu.Lock()
	ls.all = append(ls.all, l)
	ls.mu.Unlock()
	ls.ch <- l
}

func (ls *links) next(t *testing.T) *fakeLink {
	t.Helper()
	select {
	case l := <-ls.ch:
		return l
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a connection")
		return nil
	}
}

// okDialer 每次連線皆成功, 新連線送出 devices 訊息後交給測試.
func okDialer(ls *links, user string, dev protocol.Devices) func(context.Context, int, Target, Auth) (Link, error) {
	return func(ctx context.Context, n int, t Target, a Auth) (Link, error) {
		l := newFakeLink(user)
		go func() {
			l.send(dev)
			ls.add(l)
		}()
		return l, nil
	}
}

func connectErr(msg string) error {
	return &sshconn.Error{Kind: sshconn.KindConnect, Message: msg}
}

func hostFiles(t *testing.T, dir, name string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "history", name, "*.gpurec"))
	return m
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

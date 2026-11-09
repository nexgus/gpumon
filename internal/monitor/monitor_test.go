package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gpumon/internal/history"
	"gpumon/internal/protocol"
	"gpumon/internal/recording"
	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

func TestStateTransitions(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))

	if st := e.status(id).State; st != StateDisconnected {
		t.Fatalf("initial state %s", st)
	}
	if err := e.m.Connect(id); err != nil {
		t.Fatal(err)
	}
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.die(nil)
	ls.next(t)
	e.waitState(id, StateConnected)
	if err := e.m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	if st := e.status(id).State; st != StateDisconnected {
		t.Fatalf("state after disconnect %s", st)
	}
	want := []State{StateConnecting, StateConnected, StateReconnecting, StateConnected, StateDisconnected}
	if got := e.events.states(id); !reflect.DeepEqual(got, want) {
		t.Fatalf("states %v, want %v", got, want)
	}
	for _, l := range ls.all {
		if !l.closed() {
			t.Fatal("link not closed after disconnect")
		}
	}
}

func TestFailureBeforeDevicesDoesNotRetry(t *testing.T) {
	cases := []struct {
		name string
		kind ErrorKind
		dial func(ctx context.Context, n int, tg Target, a Auth) (Link, error)
	}{
		{"connect", ErrConnect, func(context.Context, int, Target, Auth) (Link, error) {
			return nil, connectErr("Cannot connect to gpu01")
		}},
		{"auth", ErrAuth, func(context.Context, int, Target, Auth) (Link, error) {
			return nil, &sshconn.Error{Kind: sshconn.KindAuth, Message: "Authentication failed"}
		}},
		{"platform", ErrPlatform, func(context.Context, int, Target, Auth) (Link, error) {
			return nil, &sshconn.Error{Kind: sshconn.KindPlatform, Message: "not Linux x86_64"}
		}},
		{"first-message", ErrFirstMessage, func(context.Context, int, Target, Auth) (Link, error) {
			l := newFakeLink("alice")
			go l.send(sampleMsg(1, 1, []int{0}))
			return l, nil
		}},
		{"protocol-mismatch", ErrProtocolMismatch, func(context.Context, int, Target, Auth) (Link, error) {
			l := newFakeLink("alice")
			d := nvidiaDevices(0)
			d.Protocol = 2
			go l.send(d)
			return l, nil
		}},
		{"devices-timeout", ErrConnect, func(context.Context, int, Target, Auth) (Link, error) {
			return newFakeLink("alice"), nil
		}},
		{"exec", ErrExec, func(context.Context, int, Target, Auth) (Link, error) {
			l := newFakeLink("alice")
			go l.die(&sshconn.Error{Kind: sshconn.KindExec, Message: "could not be executed"})
			return l, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, testTiming())
			id := e.addHost("alice", "gpu01")
			e.dialer.set(tc.dial)
			if err := e.m.Connect(id); err != nil {
				t.Fatal(err)
			}
			e.eventually("error event", 3*time.Second, func() bool { return len(e.events.all(EventError)) == 1 })
			e.waitState(id, StateDisconnected)
			ce := e.events.all(EventError)[0].(ConnectError)
			if ce.Kind != tc.kind || ce.Host != id || ce.Name != "alice@gpu01" || ce.Message == "" {
				t.Fatalf("error event %+v", ce)
			}
			time.Sleep(200 * time.Millisecond)
			if n := e.dialer.count(); n != 1 {
				t.Fatalf("dial called %d times, want 1", n)
			}
			if got := e.events.states(id); slices.Contains(got, StateReconnecting) {
				t.Fatalf("states %v include reconnecting", got)
			}
		})
	}
}

func TestWatchdogDetectsSilence(t *testing.T) {
	tm := testTiming()
	tm.Watchdog = 200 * time.Millisecond
	e := newEnv(t, tm)
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	ok := okDialer(ls, "alice", nvidiaDevices(0))
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		if n == 1 {
			l, err := ok(ctx, n, tg, a)
			l.(*fakeLink).pong.Store(false)
			return l, err
		}
		return nil, connectErr("Cannot connect")
	})
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	start := time.Now()
	e.waitState(id, StateReconnecting)
	if el := time.Since(start); el < 180*time.Millisecond {
		t.Fatalf("disconnect detected after %v, before the watchdog timeout", el)
	}
	if r := e.status(id).Reason; !strings.Contains(r, "No response") {
		t.Fatalf("reason %q", r)
	}
	if !l.closed() {
		t.Fatal("silent link not closed")
	}
}

func TestMessagesKeepWatchdogAlive(t *testing.T) {
	tm := testTiming()
	tm.Watchdog = 200 * time.Millisecond
	e := newEnv(t, tm)
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	// 自動回應 ping 即可維持連線; 期間送出無法解析的行與不認得的 type 亦不中斷.
	l.sendLine("WARNING: something")
	l.sendLine(`{"type":"mystery"}`)
	time.Sleep(600 * time.Millisecond)
	if st := e.status(id).State; st != StateConnected {
		t.Fatalf("state %s", st)
	}
	l.send(sampleMsg(1000, 5, []int{0}))
	e.eventually("sample", time.Second, func() bool { lv, _ := e.m.Live(id); return lv.HasSample })
	var pings int
	for len(l.cmds) > 0 {
		if (<-l.cmds).Cmd == protocol.CmdPing {
			pings++
		}
	}
	if pings < 5 {
		t.Fatalf("%d pings in 600 ms with a 60 ms heartbeat", pings)
	}
}

func TestReconnectCadenceAndExhaustion(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	ok := okDialer(ls, "alice", nvidiaDevices(0))
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		if n == 1 {
			if tg.ConnectTimeout != time.Second {
				t.Errorf("initial connect timeout %v", tg.ConnectTimeout)
			}
			return ok(ctx, n, tg, a)
		}
		if tg.ConnectTimeout != 50*time.Millisecond {
			t.Errorf("reconnect timeout %v", tg.ConnectTimeout)
		}
		return nil, connectErr("Cannot connect to gpu01")
	})
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	killed := time.Now()
	l.die(nil)
	e.eventually("exhausted", 3*time.Second, func() bool {
		return e.dialer.count() == 6 && e.status(id).State == StateDisconnected
	})
	times := e.dialer.callTimes()[1:]
	if d := times[0].Sub(killed); d > 40*time.Millisecond {
		t.Fatalf("first retry %v after the disconnect, want immediately", d)
	}
	for i := 1; i < len(times); i++ {
		want := time.Duration(i) * 60 * time.Millisecond
		if d := times[i].Sub(times[0]); d < want-5*time.Millisecond || d > want+40*time.Millisecond {
			t.Fatalf("retry %d at %v after the first, want %v", i+1, d, want)
		}
	}
	time.Sleep(150 * time.Millisecond)
	if n := e.dialer.count(); n != 6 {
		t.Fatalf("dial called %d times after exhaustion", n)
	}
	st := e.status(id)
	if st.Reason != "The remote program exited" || st.Retries != 0 || st.RetryError == "" {
		t.Fatalf("status after exhaustion %+v", st)
	}
	var retries []int
	for _, d := range e.events.all(EventHost) {
		if hs := d.(HostStatus); hs.State == StateReconnecting && (len(retries) == 0 || retries[len(retries)-1] != hs.Retries) {
			retries = append(retries, hs.Retries)
		}
	}
	if want := []int{1, 2, 3, 4, 5}; !reflect.DeepEqual(retries, want) {
		t.Fatalf("retries %v, want %v", retries, want)
	}
	if len(e.events.all(EventError)) != 0 {
		t.Fatal("error event after exhausted retries")
	}
}

func TestReconnectSlowAttemptWaitsForNextHeartbeat(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	ok := okDialer(ls, "alice", nvidiaDevices(0))
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		switch n {
		case 1:
			return ok(ctx, n, tg, a)
		case 2:
			time.Sleep(90 * time.Millisecond)
		}
		return nil, connectErr("Cannot connect")
	})
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.die(nil)
	e.eventually("third attempt", 3*time.Second, func() bool { return e.dialer.count() >= 3 })
	times := e.dialer.callTimes()
	if d := times[2].Sub(times[1]); d < 115*time.Millisecond || d > 160*time.Millisecond {
		t.Fatalf("attempt 3 started %v after attempt 2, want the heartbeat at 120 ms", d)
	}
	e.m.Disconnect(id)
}

func TestReconnectSuccessResetsRetries(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	ok := okDialer(ls, "alice", nvidiaDevices(0))
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		if n == 1 || n == 4 {
			return ok(ctx, n, tg, a)
		}
		return nil, connectErr("Cannot connect")
	})
	e.m.Connect(id)
	ls.next(t).die(nil)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	if st := e.status(id); st.Retries != 0 || st.Reason != "" {
		t.Fatalf("status after reconnecting %+v", st)
	}
	l.die(errors.New("ssh: connection reset"))
	e.eventually("exhausted", 3*time.Second, func() bool {
		return e.dialer.count() == 9 && e.status(id).State == StateDisconnected
	})
	if r := e.status(id).Reason; r != "Connection lost: ssh: connection reset" {
		t.Fatalf("reason %q", r)
	}
}

func TestReconnectStopsOnCheckErrors(t *testing.T) {
	for _, kind := range []sshconn.Kind{sshconn.KindAuth, sshconn.KindPlatform, sshconn.KindUpload, sshconn.KindHostKey} {
		t.Run(kind.String(), func(t *testing.T) {
			e := newEnv(t, testTiming())
			id := e.addHost("alice", "gpu01")
			ls := newLinks()
			ok := okDialer(ls, "alice", nvidiaDevices(0))
			e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
				if n == 1 {
					return ok(ctx, n, tg, a)
				}
				return nil, &sshconn.Error{Kind: kind, Message: "failed"}
			})
			e.m.Connect(id)
			ls.next(t).die(nil)
			e.eventually("error event", 3*time.Second, func() bool { return len(e.events.all(EventError)) == 1 })
			e.waitState(id, StateDisconnected)
			time.Sleep(150 * time.Millisecond)
			if n := e.dialer.count(); n != 2 {
				t.Fatalf("dial called %d times", n)
			}
		})
	}
}

func TestDisconnectWhileReconnecting(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	ok := okDialer(ls, "alice", nvidiaDevices(0))
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		if n == 1 {
			return ok(ctx, n, tg, a)
		}
		return nil, connectErr("Cannot connect")
	})
	e.m.Connect(id)
	ls.next(t).die(nil)
	e.eventually("second attempt", 3*time.Second, func() bool { return e.dialer.count() >= 2 })
	if st := e.status(id).State; st != StateReconnecting {
		t.Fatalf("state %s", st)
	}
	e.m.Disconnect(id)
	if st := e.status(id); st.State != StateDisconnected || st.Reason != "" {
		t.Fatalf("status %+v", st)
	}
	n := e.dialer.count()
	time.Sleep(200 * time.Millisecond)
	if e.dialer.count() != n {
		t.Fatal("retried after disconnect")
	}
}

// passwordDialer 以密碼認證: 第一次連線向回呼函數要求密碼, 之後依 behave 決定.
type passwordEnv struct {
	*env
	ls       *links
	secret   []byte
	received [][]byte
}

func newPasswordEnv(t *testing.T, after func(n int) error) *passwordEnv {
	pe := &passwordEnv{env: newEnv(t, testTiming()), ls: newLinks()}
	ok := okDialer(pe.ls, "alice", nvidiaDevices(0))
	pe.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		pw, err := a.Password(ctx, sshconn.PasswordRequest{User: "alice", Host: "gpu01", Attempt: 1})
		if err != nil {
			if errors.Is(err, sshconn.ErrCanceled) || ctx.Err() != nil {
				return nil, &sshconn.Error{Kind: sshconn.KindCanceled, Message: "canceled", Err: err}
			}
			return nil, &sshconn.Error{Kind: sshconn.KindAuth, Message: "Authentication was aborted", Err: err}
		}
		pe.received = append(pe.received, append([]byte(nil), pw...))
		if string(pw) != "s3cret" {
			return nil, &sshconn.Error{Kind: sshconn.KindAuth, Message: "Authentication failed"}
		}
		if n > 1 && after != nil {
			if err := after(n); err != nil {
				return nil, err
			}
		}
		return ok(ctx, n, tg, a)
	})
	return pe
}

// connectWithPassword 連線並以輸入視窗回覆密碼.
func (pe *passwordEnv) connectWithPassword(id string) *fakeLink {
	pe.t.Helper()
	pe.m.Connect(id)
	pe.eventually("prompt", 3*time.Second, func() bool { return len(pe.events.all(EventPrompt)) == 1 })
	p := pe.events.all(EventPrompt)[0].(Prompt)
	if p.Kind != PromptPassword || p.Host != id || p.User != "alice" || p.Attempt != 1 || p.Name != "alice@gpu01" {
		pe.t.Fatalf("prompt %+v", p)
	}
	pe.secret = []byte("s3cret")
	if err := pe.m.AnswerPrompt(p.ID, pe.secret); err != nil {
		pe.t.Fatal(err)
	}
	l := pe.ls.next(pe.t)
	pe.waitState(id, StateConnected)
	return l
}

func (pe *passwordEnv) assertWiped() {
	pe.t.Helper()
	if !slices.Equal(pe.secret, make([]byte, len(pe.secret))) {
		pe.t.Fatalf("password not overwritten: %q", pe.secret)
	}
	for _, s := range pe.m.sessions {
		if s.password != nil || s.candidate != nil || s.keys != nil {
			pe.t.Fatal("password or key cache still referenced")
		}
	}
}

func TestPasswordRetainedForReconnectAndWipedOnDisconnect(t *testing.T) {
	pe := newPasswordEnv(t, nil)
	id := pe.addHost("alice", "gpu01")
	l := pe.connectWithPassword(id)
	if string(pe.secret) != "s3cret" {
		t.Fatal("password wiped while connected")
	}
	l.die(nil)
	pe.ls.next(t)
	pe.waitState(id, StateConnected)
	if n := len(pe.events.all(EventPrompt)); n != 1 {
		t.Fatalf("%d prompts, reconnecting must not ask again", n)
	}
	if len(pe.received) != 2 || string(pe.received[1]) != "s3cret" {
		t.Fatalf("reconnect received %q", pe.received)
	}
	pe.m.Disconnect(id)
	pe.assertWiped()
}

func TestPasswordWipedWhenRetriesExhausted(t *testing.T) {
	pe := newPasswordEnv(t, func(int) error { return connectErr("Cannot connect") })
	id := pe.addHost("alice", "gpu01")
	pe.connectWithPassword(id).die(nil)
	pe.eventually("exhausted", 3*time.Second, func() bool {
		return pe.dialer.count() == 6 && pe.status(id).State == StateDisconnected
	})
	pe.assertWiped()
}

func TestPasswordRejectedOnReconnectStopsAndWipes(t *testing.T) {
	pe := newPasswordEnv(t, nil)
	id := pe.addHost("alice", "gpu01")
	l := pe.connectWithPassword(id)
	// 遠端的密碼已變更: 暫留的密碼於重試時被拒.
	pe.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		if _, err := a.Password(ctx, sshconn.PasswordRequest{User: "alice", Host: "gpu01", Attempt: 1}); err != nil {
			t.Error("first attempt must use the saved password")
		}
		if _, err := a.Password(ctx, sshconn.PasswordRequest{User: "alice", Host: "gpu01", Attempt: 2}); err == nil {
			t.Error("the saved password must not be offered twice")
		}
		return nil, &sshconn.Error{Kind: sshconn.KindAuth, Message: "Authentication failed"}
	})
	l.die(nil)
	pe.eventually("error event", 3*time.Second, func() bool { return len(pe.events.all(EventError)) == 1 })
	pe.waitState(id, StateDisconnected)
	if n := len(pe.events.all(EventPrompt)); n != 1 {
		t.Fatalf("%d prompts", n)
	}
	pe.assertWiped()
}

func TestPasswordWipedOnCheckErrorAndClose(t *testing.T) {
	t.Run("check error", func(t *testing.T) {
		pe := newPasswordEnv(t, nil)
		id := pe.addHost("alice", "gpu01")
		ok := pe.dialer.fn
		pe.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
			l, err := ok(ctx, n, tg, a)
			if err != nil {
				return nil, err
			}
			l.Close()
			return nil, &sshconn.Error{Kind: sshconn.KindPlatform, Message: "not Linux"}
		})
		pe.m.Connect(id)
		pe.eventually("prompt", 3*time.Second, func() bool { return len(pe.events.all(EventPrompt)) == 1 })
		pe.secret = []byte("s3cret")
		pe.m.AnswerPrompt(pe.events.all(EventPrompt)[0].(Prompt).ID, pe.secret)
		pe.eventually("error event", 3*time.Second, func() bool { return len(pe.events.all(EventError)) == 1 })
		pe.waitState(id, StateDisconnected)
		pe.assertWiped()
	})
	t.Run("close", func(t *testing.T) {
		pe := newPasswordEnv(t, nil)
		id := pe.addHost("alice", "gpu01")
		pe.connectWithPassword(id)
		pe.m.Close()
		if st := pe.status(id).State; st != StateDisconnected {
			t.Fatalf("state %s after Close", st)
		}
		pe.assertWiped()
		if err := pe.m.Connect(id); CodeOf(err) != CodeClosed {
			t.Fatalf("connect after Close: %v", err)
		}
	})
}

func TestPromptCancelAndDisconnectDuringPrompt(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		pe := newPasswordEnv(t, nil)
		id := pe.addHost("alice", "gpu01")
		pe.m.Connect(id)
		pe.eventually("prompt", 3*time.Second, func() bool { return len(pe.events.all(EventPrompt)) == 1 })
		if err := pe.m.CancelPrompt(pe.events.all(EventPrompt)[0].(Prompt).ID); err != nil {
			t.Fatal(err)
		}
		pe.waitState(id, StateDisconnected)
		time.Sleep(50 * time.Millisecond)
		if len(pe.events.all(EventError)) != 0 {
			t.Fatal("error shown after the user canceled the prompt")
		}
	})
	t.Run("disconnect", func(t *testing.T) {
		pe := newPasswordEnv(t, nil)
		id := pe.addHost("alice", "gpu01")
		pe.m.Connect(id)
		pe.eventually("prompt", 3*time.Second, func() bool { return len(pe.events.all(EventPrompt)) == 1 })
		p := pe.events.all(EventPrompt)[0].(Prompt)
		pe.m.Disconnect(id)
		if st := pe.status(id).State; st != StateDisconnected {
			t.Fatalf("state %s", st)
		}
		closed := pe.events.all(EventPromptClosed)
		if len(closed) != 1 || closed[0].(PromptClosed).ID != p.ID {
			t.Fatalf("prompt-closed events %v", closed)
		}
		late := []byte("s3cret")
		if err := pe.m.AnswerPrompt(p.ID, late); CodeOf(err) != CodeNotFound {
			t.Fatalf("late answer: %v", err)
		}
		if !slices.Equal(late, make([]byte, len(late))) {
			t.Fatal("late answer not overwritten")
		}
		if len(pe.events.all(EventError)) != 0 {
			t.Fatal("error shown after the user disconnected")
		}
	})
}

func TestTimeBase(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.send(sampleMsg(1000.0, 1, []int{0}))
	e.waitLiveTime(id, t0)
	// 本機時鐘於連線期間被調整不影響時間軸.
	e.clock.advance(3_600_000_000)
	l.send(sampleMsg(1000.5, 1, []int{0}))
	e.waitLiveTime(id, t0+500_000)

	// 重新連線後重新記錄基準.
	t1 := t0 + 7_200_000_000
	e.clock.set(t1)
	l.die(nil)
	l2 := ls.next(t)
	e.waitState(id, StateConnected)
	l2.send(sampleMsg(5.0, 1, []int{0}))
	e.waitLiveTime(id, t1)
	l2.send(sampleMsg(5.25, 1, []int{0}))
	e.waitLiveTime(id, t1+250_000)
}

// readAll 讀取記錄檔的所有記錄.
func readAll(t *testing.T, path string) (*recording.File, []recording.Record) {
	t.Helper()
	f, err := recording.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	recs, err := f.ReadRange(math.MinInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	return f, recs
}

func kinds(recs []recording.Record) []recording.Kind {
	var out []recording.Kind
	for _, r := range recs {
		out = append(out, r.Kind)
	}
	return out
}

const (
	kS = recording.KindSample
	kE = recording.KindError
	kP = recording.KindProcesses
	kD = recording.KindDisconnect
)

func TestRecordingCreatedOnFirstSample(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0, 1)))
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.send(protocol.Error{Type: "error", TS: 999, Msg: "before first sample"})
	time.Sleep(30 * time.Millisecond)
	if files := hostFiles(t, e.dir, "alice@gpu01"); len(files) != 0 || e.status(id).Recording != "" {
		t.Fatalf("recording created before the first sample: %v", files)
	}
	l.send(sampleMsg(1000, 10, []int{0, 1}))
	l.send(protocol.Error{Type: "error", TS: 1000.2, Msg: "nvml timeout"})
	l.send(sampleMsg(1001, 20, []int{0, 1}))
	e.eventually("recording", time.Second, func() bool { lv, _ := e.m.Live(id); return lv.HasSample && lv.Time > e.clock.now().UnixMicro() })
	path := e.status(id).Recording
	if path == "" || !exists(path) || !slices.Contains(e.m.ActiveRecordings(), path) {
		t.Fatalf("recording %q not created", path)
	}
	e.m.Disconnect(id)
	f, recs := readAll(t, path)
	if h := f.Header(); h.Host != "alice@gpu01" || h.TS0 != 1000 || h.TWall0 != e.clock.now().UnixMicro() {
		t.Fatalf("header %+v", h)
	}
	if got, want := kinds(recs), []recording.Kind{kS, kP, kE, kS}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records %v, want %v", got, want)
	}
	if recs[2].Text != "nvml timeout" {
		t.Fatalf("error text %q", recs[2].Text)
	}
	if len(e.m.ActiveRecordings()) != 0 {
		t.Fatal("recording still active after disconnect")
	}
}

func TestRecordingNotCreatedWithoutGPUs(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices()))
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.send(protocol.Sample{Type: "sample", TS: 1000, GPUs: []protocol.GPUSample{}})
	e.waitLiveTime(id, e.clock.now().UnixMicro())
	e.m.Disconnect(id)
	if files := hostFiles(t, e.dir, "alice@gpu01"); len(files) != 0 {
		t.Fatalf("recording created for a host without GPUs: %v", files)
	}
}

func TestRecordingFlushesInBatches(t *testing.T) {
	tm := testTiming()
	tm.Flush = 150 * time.Millisecond
	e := newEnv(t, tm)
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.send(sampleMsg(1000, 1, []int{0}))
	e.waitLiveTime(id, e.clock.now().UnixMicro())
	f, err := recording.Open(e.status(id).Recording)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if n, _ := f.Count(); n != 0 {
		t.Fatalf("%d records written before the batch interval", n)
	}
	e.eventually("batch write", time.Second, func() bool { n, _ := f.Count(); return n == 2 })
}

func TestRecordingContinuesAfterReconnect(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.send(sampleMsg(1000, 1, []int{0}, 7))
	l.send(sampleMsg(1001, 2, []int{0}, 7))
	e.waitLiveTime(id, t0+1_000_000)
	path := e.status(id).Recording

	tDisc := t0 + 10_000_000
	e.clock.set(tDisc)
	l.die(errors.New("EOF"))
	l2 := ls.next(t)
	e.waitState(id, StateConnected)
	t1 := t0 + 20_000_000
	e.clock.set(t1)
	l2.send(sampleMsg(3, 3, []int{0}, 7))
	e.waitLiveTime(id, t1)
	if p := e.status(id).Recording; p != path {
		t.Fatalf("recording changed to %s", p)
	}
	e.m.Disconnect(id)
	if files := hostFiles(t, e.dir, "alice@gpu01"); len(files) != 1 {
		t.Fatalf("files %v", files)
	}
	_, recs := readAll(t, path)
	// 程序組成不變且未滿 60 秒, 重新連線後不另寫程序記錄.
	if got, want := kinds(recs), []recording.Kind{kS, kP, kS, kD, kS}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records %v, want %v", got, want)
	}
	d := recs[3]
	if d.Time != tDisc || d.Text != "Connection lost: EOF" {
		t.Fatalf("disconnect record %+v", d)
	}
	if recs[4].Time != t1 {
		t.Fatalf("first sample after reconnecting at %d, want %d", recs[4].Time, t1)
	}
}

func TestRecordingNewFileConditions(t *testing.T) {
	cases := []struct {
		name     string
		devices2 protocol.Devices
		clock2   int64
		user     bool
	}{
		{"gpu count", nvidiaDevices(0, 1), 20_000_000, false},
		{"gpu ids", nvidiaDevices(1), 20_000_000, false},
		{"earlier time", nvidiaDevices(0), -5_000_000, false},
		{"user reconnect", nvidiaDevices(0), 20_000_000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, testTiming())
			id := e.addHost("alice", "gpu01")
			ls := newLinks()
			devs := []protocol.Devices{nvidiaDevices(0), tc.devices2}
			e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
				return okDialer(ls, "alice", devs[min(n, 2)-1])(ctx, n, tg, a)
			})
			t0 := e.clock.now().UnixMicro()
			e.m.Connect(id)
			l := ls.next(t)
			e.waitState(id, StateConnected)
			l.send(sampleMsg(1000, 1, []int{0}))
			l.send(sampleMsg(1001, 1, []int{0}))
			e.waitLiveTime(id, t0+1_000_000)
			first := e.status(id).Recording
			if tc.user {
				e.m.Disconnect(id)
				e.m.Connect(id)
			} else {
				l.die(nil)
			}
			l2 := ls.next(t)
			e.waitState(id, StateConnected)
			t1 := t0 + tc.clock2
			e.clock.set(t1)
			var ids []int
			for _, g := range tc.devices2.GPUs {
				ids = append(ids, g.ID)
			}
			l2.send(sampleMsg(3, 1, ids))
			e.waitLiveTime(id, t1)
			second := e.status(id).Recording
			e.m.Disconnect(id)
			if second == first || second == "" {
				t.Fatalf("no new recording: %q", second)
			}
			_, recs := readAll(t, first)
			if got, want := kinds(recs), []recording.Kind{kS, kP, kS}; !reflect.DeepEqual(got, want) {
				t.Fatalf("first file records %v, want %v", got, want)
			}
			f, recs := readAll(t, second)
			if got, want := kinds(recs), []recording.Kind{kS, kP}; !reflect.DeepEqual(got, want) {
				t.Fatalf("second file records %v, want %v", got, want)
			}
			if f.Header().TWall0 != t1 {
				t.Fatalf("second header %+v", f.Header())
			}
		})
	}
}

func TestProcessRecordTiming(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0, 1)))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	both := []int{0, 1}
	send := func(ts float64, pids ...int) {
		l.send(sampleMsg(1000+ts, 1, both, pids...))
	}
	send(0, 10)
	send(1, 10)
	send(2, 10)
	send(3, 10, 11)
	send(4, 11, 10)
	send(63.5, 10, 11)
	send(64, 10, 11)
	e.waitLiveTime(id, t0+64_000_000)
	path := e.status(id).Recording
	e.m.Disconnect(id)
	_, recs := readAll(t, path)
	var procTimes []int64
	for _, r := range recs {
		if r.Kind == kP {
			procTimes = append(procTimes, (r.Time-t0)/1000)
		}
	}
	if want := []int64{0, 3000, 63500}; !reflect.DeepEqual(procTimes, want) {
		t.Fatalf("process records at %v ms, want %v", procTimes, want)
	}
}

func TestIntervalChangeAndAck(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	if err := e.m.SetInterval(id, 2000); err != nil {
		t.Fatal(err)
	}
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	if iv := e.dialer.calls[0].target.IntervalMS; iv != 2000 {
		t.Fatalf("--interval %d", iv)
	}
	l.send(sampleMsg(1000, 1, []int{0}))
	e.waitLiveTime(id, t0)

	l.ack.Store(false)
	if err := e.m.SetInterval(id, 500); err != nil {
		t.Fatal(err)
	}
	e.eventually("set_interval", time.Second, func() bool {
		for len(l.cmds) > 0 {
			if c := <-l.cmds; c.Cmd == protocol.CmdSetInterval && *c.Ms == 500 {
				return true
			}
		}
		return false
	})
	if h, _ := e.store.Host(id); h.IntervalMS != 500 {
		t.Fatalf("setting %d", h.IntervalMS)
	}
	l.send(sampleMsg(1001, 1, []int{0}))
	e.waitLiveTime(id, t0+1_000_000)
	l.send(map[string]any{"type": "ack", "cmd": "set_interval", "ms": 500})
	l.send(sampleMsg(1001.5, 1, []int{0}))
	e.waitLiveTime(id, t0+1_500_000)
	if lv, _ := e.m.Live(id); lv.IntervalMS != 500 {
		t.Fatalf("live interval %d", lv.IntervalMS)
	}
	if err := e.m.SetInterval(id, 150); CodeOf(err) != CodeInvalid {
		t.Fatalf("interval 150: %v", err)
	}
	if err := e.m.SetInterval(id, 10001); CodeOf(err) != CodeInvalid {
		t.Fatalf("interval 10001: %v", err)
	}
	path := e.status(id).Recording
	e.m.Disconnect(id)
	_, recs := readAll(t, path)
	var ivs []uint16
	for _, r := range recs {
		if r.Kind == kS {
			ivs = append(ivs, r.IntervalMS)
		}
	}
	if want := []uint16{2000, 2000, 500}; !reflect.DeepEqual(ivs, want) {
		t.Fatalf("intervals %v, want %v", ivs, want)
	}
	// 重新連線時以設定中的新值傳入 --interval.
	e.m.Connect(id)
	ls.next(t)
	e.waitState(id, StateConnected)
	if iv := e.dialer.calls[1].target.IntervalMS; iv != 500 {
		t.Fatalf("--interval after change %d", iv)
	}
}

func TestQueryIncludesUnsavedSamples(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0, 1)))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	for i := 0; i < 10; i++ {
		l.send(sampleMsg(1000+float64(i), i*10, []int{0, 1}))
		if i == 5 {
			l.send(protocol.Error{Type: "error", TS: 1005.5, Msg: "nvml timeout"})
		}
	}
	l.send(sampleMsg(1020, 99, []int{0, 1}))
	e.waitLiveTime(id, t0+20_000_000)
	ctx := context.Background()
	from, to := t0-60_000_000, t0+30_000_000
	for _, width := range []int{1000, 5} {
		before, err := e.m.Query(ctx, id, from, to, width, nil)
		if err != nil {
			t.Fatal(err)
		}
		if before.Samples != 11 {
			t.Fatalf("width %d: %d samples before saving", width, before.Samples)
		}
		if width == 1000 {
			if last := before.Times[len(before.Times)-1]; last != t0+20_000_000 {
				t.Fatalf("latest sample missing: %d", last)
			}
			if v := before.GPUs[1].Fields[history.FieldUtil].Avg[10]; v != 99 {
				t.Fatalf("latest util %v", v)
			}
			if len(before.Gaps) != 1 || before.Gaps[0].Reason != history.GapUnknown {
				t.Fatalf("gaps %+v", before.Gaps)
			}
		}
		e.m.recorderRefFor(id).flush()
		after, err := e.m.Query(ctx, id, from, to, width, nil)
		if err != nil {
			t.Fatal(err)
		}
		jb, _ := json.Marshal(before)
		ja, _ := json.Marshal(after)
		if string(jb) != string(ja) {
			t.Fatalf("width %d: query differs after saving\nbefore %s\nafter  %s", width, jb, ja)
		}
	}

	// 先前各次連線的記錄一併查詢, 兩次連線之間為未連線.
	e.m.Disconnect(id)
	e.clock.set(t0 + 40_000_000)
	e.m.Connect(id)
	l = ls.next(t)
	e.waitState(id, StateConnected)
	l.send(sampleMsg(1, 50, []int{0, 1}))
	e.waitLiveTime(id, t0+40_000_000)
	res, err := e.m.Query(ctx, id, from, t0+50_000_000, 1000, []history.Field{history.FieldUtil})
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 12 || len(res.GPUs) != 2 {
		t.Fatalf("%d samples across connections", res.Samples)
	}
	if g := res.Gaps[len(res.Gaps)-1]; g.Reason != history.GapNotConnected {
		t.Fatalf("gap between connections %+v", g)
	}
}

// recorderRefFor 供測試直接觸發 flush.
func (m *Manager) recorderRefFor(id string) *recorder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id].recorderRef()
}

func TestCloseSavesPendingSamples(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	l.send(sampleMsg(1000, 1, []int{0}))
	l.send(sampleMsg(1001, 1, []int{0}))
	e.waitLiveTime(id, t0+1_000_000)
	path := e.status(id).Recording
	if f, _ := recording.Open(path); f != nil {
		if n, _ := f.Count(); n != 0 {
			t.Fatalf("%d records saved before Close", n)
		}
		f.Close()
	}
	e.m.Close()
	_, recs := readAll(t, path)
	if got, want := kinds(recs), []recording.Kind{kS, kP, kS}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records after Close %v, want %v", got, want)
	}
	if !l.closed() {
		t.Fatal("link not closed by Close")
	}
}

func TestIntelUtilizationComputedLocally(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	dev := nvidiaDevices(0)
	dev.GPUs = append(dev.GPUs, protocol.Device{ID: 1, Vendor: "intel", Name: "Intel iGPU"})
	e.dialer.set(okDialer(ls, "alice", dev))
	t0 := e.clock.now().UnixMicro()
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	intel := func(ts float64, render uint64) protocol.Sample {
		return protocol.Sample{Type: "sample", TS: protocol.Timestamp(ts), GPUs: []protocol.GPUSample{
			{ID: 0, Util: ip(80), Procs: []protocol.Process{{PID: 5, Util: ip(80)}}},
			{ID: 1, Procs: []protocol.Process{{PID: 9, Engines: map[string]uint64{"render": render, "video": render / 4}}}},
		}}
	}
	l.send(intel(1000, 1_000_000_000))
	e.waitLiveTime(id, t0)
	if lv, _ := e.m.Live(id); lv.GPUs[1].Util != nil {
		t.Fatalf("utilization without a previous sample: %d", *lv.GPUs[1].Util)
	}
	l.send(intel(1002, 2_000_000_000))
	e.waitLiveTime(id, t0+2_000_000)
	lv, _ := e.m.Live(id)
	if g := lv.GPUs[1]; g.Util == nil || *g.Util != 50 || !g.UtilComputed {
		t.Fatalf("intel GPU %+v", g)
	}
	if g := lv.GPUs[0]; *g.Util != 80 || g.UtilComputed {
		t.Fatalf("nvidia GPU %+v", g)
	}
	if p := lv.Procs[1]; p.PID != 9 || p.Util == nil || *p.Util != 50 {
		t.Fatalf("intel process %+v", p)
	}
	path := e.status(id).Recording
	e.m.Disconnect(id)
	_, recs := readAll(t, path)
	var utils []uint8
	for _, r := range recs {
		if r.Kind == kS {
			utils = append(utils, r.GPUs[1].Util)
		}
	}
	if want := []uint8{recording.NoValue8, 50}; !reflect.DeepEqual(utils, want) {
		t.Fatalf("recorded intel utilization %v, want %v", utils, want)
	}
}

func TestHostManagement(t *testing.T) {
	e := newEnv(t, testTiming())
	e.users["cfg-host"] = "bob"

	st, err := e.m.AddHost(settings.HostConn{Addr: "cfg-host"})
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != "cfg-host" || st.Name != "bob@cfg-host" {
		t.Fatalf("blank user host %+v", st)
	}
	if _, err := e.m.AddHost(settings.HostConn{Addr: "nocfg"}); CodeOf(err) != CodeUserRequired {
		t.Fatalf("blank user without config: %v", err)
	}
	if _, err := e.m.AddHost(settings.HostConn{Addr: "CFG-HOST", User: "bob"}); CodeOf(err) != CodeDuplicate {
		t.Fatalf("resolved duplicate: %v", err)
	}
	if _, err := e.m.AddHost(settings.HostConn{Addr: "cfg-host", User: "Bob"}); err != nil {
		t.Fatalf("user names are case sensitive: %v", err)
	}
	id := e.addHost("alice", "gpu01")
	if _, err := e.m.UpdateHost(id, settings.HostConn{Addr: "cfg-host"}); CodeOf(err) != CodeDuplicate {
		t.Fatalf("edit into duplicate: %v", err)
	}
	if _, err := e.m.UpdateHost(id, settings.HostConn{Addr: "gpu09"}); CodeOf(err) != CodeUserRequired {
		t.Fatalf("edit with blank user: %v", err)
	}

	ls := newLinks()
	e.dialer.set(okDialer(ls, "alice", nvidiaDevices(0)))
	e.m.Connect(id)
	l := ls.next(t)
	e.waitState(id, StateConnected)
	if _, err := e.m.UpdateHost(id, settings.HostConn{Addr: "gpu02", User: "alice"}); CodeOf(err) != CodeBusy {
		t.Fatalf("edit while connected: %v", err)
	}
	if err := e.m.RemoveHost(id); CodeOf(err) != CodeBusy {
		t.Fatalf("remove while connected: %v", err)
	}
	if err := e.m.SetRange(id, settings.Range15Min); err != nil {
		t.Fatalf("range while connected: %v", err)
	}
	if n := e.m.ActiveCount(); n != 1 {
		t.Fatalf("active count %d", n)
	}
	l.send(sampleMsg(1000, 1, []int{0}))
	e.waitLiveTime(id, e.clock.now().UnixMicro())
	rec := e.status(id).Recording
	if _, err := history.List(e.dir, e.m.ActiveRecordings()); err != nil {
		t.Fatal(err)
	}
	if err := history.Delete(e.dir, rec, e.m.ActiveRecordings()); !errors.Is(err, history.ErrActiveFile) {
		t.Fatalf("delete active recording: %v", err)
	}
	e.m.Disconnect(id)
	if n := e.m.ActiveCount(); n != 0 {
		t.Fatalf("active count %d", n)
	}
	st, err = e.m.UpdateHost(id, settings.HostConn{Addr: "gpu02", User: "alice"})
	if err != nil || st.ID != "alice@gpu02" || st.Range != settings.Range15Min {
		t.Fatalf("edit: %+v %v", st, err)
	}
	if err := e.m.RemoveHost(st.ID); err != nil {
		t.Fatal(err)
	}
	if got := len(e.m.Hosts()); got != 2 {
		t.Fatalf("%d hosts", got)
	}
}

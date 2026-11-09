package monitor

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gpumon/internal/sshconn"
)

// uploadDialer 於連線過程中呼叫上傳確認的回呼函數, 確認後回傳 ok 的結果. need 決定
// 第 n 次連線是否需要上傳; lost 不為 nil 時, 等待確認期間於該 channel 收到值即模擬
// SSH 連線中斷.
func uploadDialer(ok func(context.Context, int, Target, Auth) (Link, error), need func(n int) *sshconn.UploadRequest,
	lost <-chan struct{}) func(context.Context, int, Target, Auth) (Link, error) {
	return func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		if req := need(n); req != nil {
			if a.ConfirmUpload == nil {
				return nil, errors.New("no upload confirmation callback")
			}
			cctx, cancel := context.WithCancel(ctx)
			defer cancel()
			if lost != nil {
				go func() {
					select {
					case <-lost:
						cancel()
					case <-cctx.Done():
					}
				}()
			}
			if err := a.ConfirmUpload(cctx, *req); err != nil {
				if ctx.Err() != nil {
					return nil, &sshconn.Error{Kind: sshconn.KindCanceled, Message: "canceled", Err: err}
				}
				return nil, connectErr("Lost the connection to gpu01 while waiting for the confirmation")
			}
		}
		return ok(ctx, n, tg, a)
	}
}

func freshUpload() *sshconn.UploadRequest {
	return &sshconn.UploadRequest{Host: "gpu01", User: "alice", Path: "/home/alice/.gpumon/gpumon", NewVersion: "0.2.0"}
}

func (e *env) waitNotices(n int) []UploadNotice {
	e.t.Helper()
	e.eventually("upload notices", 3*time.Second, func() bool { return len(e.events.all(EventUpload)) >= n })
	var out []UploadNotice
	for _, d := range e.events.all(EventUpload) {
		out = append(out, d.(UploadNotice))
	}
	return out
}

func (e *env) closedUploads() []string {
	var out []string
	for _, d := range e.events.all(EventUploadClosed) {
		out = append(out, d.(UploadClosed).ID)
	}
	return out
}

func TestUploadNoticeThenConnect(t *testing.T) {
	for _, update := range []bool{false, true} {
		name := "fresh"
		if update {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, testTiming())
			id := e.addHost("alice", "gpu01")
			ls := newLinks()
			req := freshUpload()
			if update {
				req.Update, req.RemoteVersion = true, "0.1.0"
			}
			e.dialer.set(uploadDialer(okDialer(ls, "alice", nvidiaDevices(0)), func(int) *sshconn.UploadRequest { return req }, nil))
			e.m.Connect(id)
			n := e.waitNotices(1)[0]
			want := UploadNotice{ID: n.ID, Host: id, Name: "alice@gpu01", Path: "~/.gpumon/gpumon", Update: update, NewVersion: "0.2.0"}
			if update {
				want.RemoteVersion = "0.1.0"
			}
			if n != want || n.ID == "" {
				t.Fatalf("notice %+v, want %+v", n, want)
			}
			time.Sleep(100 * time.Millisecond)
			if st := e.status(id).State; st != StateConnecting {
				t.Fatalf("state %s while waiting for the confirmation", st)
			}
			if err := e.m.ConfirmUpload("no-such-id"); CodeOf(err) != CodeNotFound {
				t.Fatalf("unknown notice: %v", err)
			}
			if err := e.m.ConfirmUpload(n.ID); err != nil {
				t.Fatal(err)
			}
			ls.next(t)
			e.waitState(id, StateConnected)
			if err := e.m.ConfirmUpload(n.ID); CodeOf(err) != CodeNotFound {
				t.Fatalf("second confirmation: %v", err)
			}
			if len(e.closedUploads()) != 0 || len(e.events.all(EventError)) != 0 {
				t.Fatal("unexpected upload-closed or error event")
			}
		})
	}
}

func TestUploadNoticeNotSentWithoutUpload(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	e.dialer.set(uploadDialer(okDialer(ls, "alice", nvidiaDevices(0)), func(int) *sshconn.UploadRequest { return nil }, nil))
	e.m.Connect(id)
	ls.next(t)
	e.waitState(id, StateConnected)
	if n := len(e.events.all(EventUpload)); n != 0 {
		t.Fatalf("%d upload notices", n)
	}
}

func TestUploadNoticeDisconnectAndClose(t *testing.T) {
	for _, how := range []string{"disconnect", "close"} {
		t.Run(how, func(t *testing.T) {
			pe := newPasswordEnv(t, nil)
			id := pe.addHost("alice", "gpu01")
			// 先輸入密碼, 再等待上傳確認.
			pe.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
				if _, err := a.Password(ctx, sshconn.PasswordRequest{User: "alice", Host: "gpu01", Attempt: 1}); err != nil {
					return nil, &sshconn.Error{Kind: sshconn.KindCanceled, Message: "canceled", Err: err}
				}
				if err := a.ConfirmUpload(ctx, *freshUpload()); err != nil {
					return nil, &sshconn.Error{Kind: sshconn.KindCanceled, Message: "canceled", Err: err}
				}
				t.Error("connection continued without a confirmation")
				return nil, connectErr("unexpected")
			})
			pe.m.Connect(id)
			pe.eventually("prompt", 3*time.Second, func() bool { return len(pe.events.all(EventPrompt)) == 1 })
			pe.secret = []byte("s3cret")
			if err := pe.m.AnswerPrompt(pe.events.all(EventPrompt)[0].(Prompt).ID, pe.secret); err != nil {
				t.Fatal(err)
			}
			n := pe.waitNotices(1)[0]
			if how == "disconnect" {
				pe.m.Disconnect(id)
			} else {
				pe.m.Close()
			}
			if st := pe.status(id).State; st != StateDisconnected {
				t.Fatalf("state %s", st)
			}
			if got := pe.closedUploads(); !slices.Equal(got, []string{n.ID}) {
				t.Fatalf("upload-closed events %v", got)
			}
			if err := pe.m.ConfirmUpload(n.ID); CodeOf(err) != CodeNotFound {
				t.Fatalf("late confirmation: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
			if len(pe.events.all(EventError)) != 0 {
				t.Fatal("error shown after the user disconnected")
			}
			if c := pe.dialer.count(); c != 1 {
				t.Fatalf("dial called %d times", c)
			}
			pe.assertWiped()
		})
	}
}

func TestUploadNoticeConnectionLost(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	lost := make(chan struct{}, 1)
	e.dialer.set(uploadDialer(okDialer(ls, "alice", nvidiaDevices(0)), func(int) *sshconn.UploadRequest { return freshUpload() }, lost))
	e.m.Connect(id)
	n := e.waitNotices(1)[0]
	lost <- struct{}{}
	e.eventually("error event", 3*time.Second, func() bool { return len(e.events.all(EventError)) == 1 })
	e.waitState(id, StateDisconnected)
	if got := e.closedUploads(); !slices.Equal(got, []string{n.ID}) {
		t.Fatalf("upload-closed events %v", got)
	}
	if ce := e.events.all(EventError)[0].(ConnectError); ce.Kind != ErrConnect || ce.Host != id {
		t.Fatalf("error event %+v", ce)
	}
	time.Sleep(150 * time.Millisecond)
	if n := e.dialer.count(); n != 1 {
		t.Fatalf("dial called %d times", n)
	}
	if slices.Contains(e.events.states(id), StateReconnecting) {
		t.Fatal("reconnected after a failure before devices")
	}
}

func TestUploadNoticeOnReconnect(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		e := newEnv(t, testTiming())
		id := e.addHost("alice", "gpu01")
		ls := newLinks()
		e.dialer.set(uploadDialer(okDialer(ls, "alice", nvidiaDevices(0)), func(n int) *sshconn.UploadRequest {
			if n == 2 {
				return &sshconn.UploadRequest{Update: true, RemoteVersion: "0.1.0", NewVersion: "0.2.0"}
			}
			return nil
		}, nil))
		e.m.Connect(id)
		ls.next(t).die(nil)
		n := e.waitNotices(1)[0]
		if !n.Update || n.Host != id || n.RemoteVersion != "0.1.0" {
			t.Fatalf("notice %+v", n)
		}
		// 等待時間超過重試的連線上限, 心跳間隔與等待 devices 的上限.
		time.Sleep(400 * time.Millisecond)
		st := e.status(id)
		if st.State != StateReconnecting || st.Retries != 1 || st.RetryError != "" {
			t.Fatalf("status while waiting %+v", st)
		}
		if c := e.dialer.count(); c != 2 {
			t.Fatalf("dial called %d times while waiting", c)
		}
		if err := e.m.ConfirmUpload(n.ID); err != nil {
			t.Fatal(err)
		}
		ls.next(t)
		e.waitState(id, StateConnected)
		if c := e.dialer.count(); c != 2 {
			t.Fatalf("dial called %d times", c)
		}
	})
	t.Run("connection lost", func(t *testing.T) {
		e := newEnv(t, testTiming())
		id := e.addHost("alice", "gpu01")
		ls := newLinks()
		lost := make(chan struct{}, 1)
		e.dialer.set(uploadDialer(okDialer(ls, "alice", nvidiaDevices(0)), func(n int) *sshconn.UploadRequest {
			if n == 2 {
				return freshUpload()
			}
			return nil
		}, lost))
		e.m.Connect(id)
		ls.next(t).die(nil)
		n := e.waitNotices(1)[0]
		time.Sleep(200 * time.Millisecond)
		lost <- struct{}{}
		// 該次重試計為失敗, 下一次重試不需上傳而成功.
		ls.next(t)
		e.waitState(id, StateConnected)
		if got := e.closedUploads(); !slices.Equal(got, []string{n.ID}) {
			t.Fatalf("upload-closed events %v", got)
		}
		if c := e.dialer.count(); c != 3 {
			t.Fatalf("dial called %d times", c)
		}
		var sawFailure bool
		for _, d := range e.events.all(EventHost) {
			if hs := d.(HostStatus); hs.State == StateReconnecting && hs.Retries == 1 && hs.RetryError != "" {
				sawFailure = true
			}
		}
		if !sawFailure || len(e.events.all(EventError)) != 0 {
			t.Fatal("the lost connection was not counted as a failed retry")
		}
	})
}

func TestUploadNoticesForSeveralHosts(t *testing.T) {
	e := newEnv(t, testTiming())
	a := e.addHost("alice", "gpu01")
	b := e.addHost("bob", "gpu02")
	ls := newLinks()
	e.dialer.set(uploadDialer(okDialer(ls, "x", nvidiaDevices(0)), func(int) *sshconn.UploadRequest { return freshUpload() }, nil))
	e.m.Connect(a)
	e.m.Connect(b)
	ns := e.waitNotices(2)
	byHost := map[string]UploadNotice{}
	for _, n := range ns {
		byHost[n.Host] = n
	}
	if len(byHost) != 2 || byHost[a].ID == byHost[b].ID || byHost[a].Name != "alice@gpu01" || byHost[b].Name != "bob@gpu02" {
		t.Fatalf("notices %+v", ns)
	}
	if err := e.m.ConfirmUpload(byHost[b].ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(b, StateConnected)
	time.Sleep(100 * time.Millisecond)
	if st := e.status(a).State; st != StateConnecting {
		t.Fatalf("host a state %s before its confirmation", st)
	}
	if err := e.m.ConfirmUpload(byHost[a].ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(a, StateConnected)
}

package monitor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"gpumon/internal/sshconn"
)

// canceledErr 模擬 sshconn 於使用者取消時回傳的錯誤.
func canceledErr(err error) error {
	return &sshconn.Error{Kind: sshconn.KindCanceled, Message: "canceled", Err: err}
}

func (e *env) waitEvents(name string, n int) []any {
	e.t.Helper()
	e.eventually(name, 3*time.Second, func() bool { return len(e.events.all(name)) >= n })
	return e.events.all(name)
}

func TestHostKeyPrompt(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	ok := okDialer(ls, "alice", nvidiaDevices(0))
	var auths []Auth
	req := sshconn.HostKeyRequest{
		Host: "gpu01", Address: "gpu01:22", Changed: true,
		Key: sshconn.HostKeyFingerprint{Type: "ssh-ed25519", Fingerprint: "SHA256:new"},
		Old: []sshconn.HostKeyFingerprint{{Type: "ssh-ed25519", Fingerprint: "SHA256:old1"}, {Type: "ssh-rsa", Fingerprint: "SHA256:old2"}},
	}
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		auths = append(auths, a)
		if n == 1 || n == 2 {
			if err := a.HostKey(ctx, req); err != nil {
				return nil, canceledErr(err)
			}
		}
		return ok(ctx, n, tg, a)
	})

	// 第一次連線不同意: 中止連線, 不顯示錯誤.
	e.m.Connect(id)
	p := e.waitEvents(EventHostKey, 1)[0].(HostKeyPrompt)
	want := HostKeyPrompt{
		ID: p.ID, Host: id, Name: "alice@gpu01", Changed: true,
		Key:     HostKeyInfo{KeyType: "ssh-ed25519", Fingerprint: "SHA256:new"},
		OldKeys: []HostKeyInfo{{"ssh-ed25519", "SHA256:old1"}, {"ssh-rsa", "SHA256:old2"}},
	}
	if !reflect.DeepEqual(p, want) || p.ID == "" {
		t.Fatalf("prompt %+v, want %+v", p, want)
	}
	if err := e.m.AnswerHostKey("no-such-id", true); CodeOf(err) != CodeNotFound {
		t.Fatalf("unknown prompt: %v", err)
	}
	if err := e.m.AnswerHostKey(p.ID, false); err != nil {
		t.Fatal(err)
	}
	e.waitState(id, StateDisconnected)
	if n := len(e.events.all(EventError)); n != 0 {
		t.Fatalf("%d error events after rejecting the host key", n)
	}

	// 第二次連線同意: 繼續連線.
	e.m.Connect(id)
	p = e.waitEvents(EventHostKey, 2)[1].(HostKeyPrompt)
	if err := e.m.AnswerHostKey(p.ID, true); err != nil {
		t.Fatal(err)
	}
	l := ls.next(t)
	e.waitState(id, StateConnected)

	// 自動重新連線不詢問主機金鑰與免密碼登入.
	l.die(errors.New("eof"))
	ls.next(t)
	e.waitState(id, StateConnected)
	if len(auths) != 3 || auths[2].HostKey != nil || auths[2].KeySetup != nil {
		t.Fatalf("reconnect auth = %+v", auths[len(auths)-1])
	}
	if auths[0].HostKey == nil || auths[0].KeySetup == nil {
		t.Fatal("first connection lacks the host key or passwordless login callbacks")
	}
}

func TestHostKeyPromptClosedOnDisconnect(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		err := a.HostKey(ctx, sshconn.HostKeyRequest{Key: sshconn.HostKeyFingerprint{Type: "ssh-ed25519", Fingerprint: "SHA256:x"}})
		return nil, canceledErr(err)
	})
	e.m.Connect(id)
	p := e.waitEvents(EventHostKey, 1)[0].(HostKeyPrompt)
	if p.OldKeys == nil || len(p.OldKeys) != 0 || p.Changed {
		t.Fatalf("prompt %+v", p)
	}
	if err := e.m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	closed := e.waitEvents(EventHostKeyClosed, 1)
	if closed[0].(HostKeyClosed).ID != p.ID {
		t.Fatalf("closed %+v", closed)
	}
	if err := e.m.AnswerHostKey(p.ID, true); CodeOf(err) != CodeNotFound {
		t.Fatalf("answer after close: %v", err)
	}
	if n := len(e.events.all(EventError)); n != 0 {
		t.Fatalf("%d error events", n)
	}
}

// keySetupDialer 於連線過程中呼叫免密碼登入的回呼函數 (提供時), 回答 "立即設定" 時
// 以 result 呼叫 KeySetupDone, 之後連線成功.
func keySetupDialer(ok func(context.Context, int, Target, Auth) (Link, error), result sshconn.KeySetupResult,
	asked *[]bool) func(context.Context, int, Target, Auth) (Link, error) {
	return func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		*asked = append(*asked, a.KeySetup != nil)
		if a.KeySetup != nil {
			choice, err := a.KeySetup(ctx, sshconn.KeySetupRequest{Host: "gpu01", User: "alice"})
			if err != nil {
				return nil, canceledErr(err)
			}
			if choice == sshconn.KeySetupNow {
				a.KeySetupDone(result)
			}
		}
		return ok(ctx, n, tg, a)
	}
}

func TestKeySetupNever(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	ls := newLinks()
	var asked []bool
	e.dialer.set(keySetupDialer(okDialer(ls, "alice", nvidiaDevices(0)), sshconn.KeySetupResult{}, &asked))

	e.m.Connect(id)
	p := e.waitEvents(EventKeySetup, 1)[0].(KeySetupPrompt)
	if p != (KeySetupPrompt{ID: p.ID, Host: id, Name: "alice@gpu01"}) || p.ID == "" {
		t.Fatalf("prompt %+v", p)
	}
	if err := e.m.AnswerKeySetup(p.ID, "maybe"); CodeOf(err) != CodeInvalid {
		t.Fatalf("invalid answer: %v", err)
	}
	if err := e.m.AnswerKeySetup(p.ID, KeySetupLater); err != nil {
		t.Fatal(err)
	}
	ls.next(t)
	e.waitState(id, StateConnected)
	if e.store.NoKeySetupPrompt(id) {
		t.Fatal("later recorded as never")
	}
	e.m.Disconnect(id)

	e.m.Connect(id)
	p = e.waitEvents(EventKeySetup, 2)[1].(KeySetupPrompt)
	if err := e.m.AnswerKeySetup(p.ID, KeySetupNever); err != nil {
		t.Fatal(err)
	}
	ls.next(t)
	e.waitState(id, StateConnected)
	if !e.store.NoKeySetupPrompt(id) {
		t.Fatal("never not recorded")
	}
	e.m.Disconnect(id)

	e.m.Connect(id)
	ls.next(t)
	e.waitState(id, StateConnected)
	if !reflect.DeepEqual(asked, []bool{true, true, false}) {
		t.Fatalf("asked = %v", asked)
	}
	if n := len(e.events.all(EventKeySetupResult)); n != 0 {
		t.Fatalf("%d result events", n)
	}
}

func TestKeySetupResults(t *testing.T) {
	cases := []struct {
		name   string
		result sshconn.KeySetupResult
		want   KeySetupResult
		never  bool
	}{
		{"success", sshconn.KeySetupResult{Outcome: sshconn.KeySetupSucceeded, KeyFile: "/k/id_ed25519", Generated: true},
			KeySetupResult{Outcome: KeySetupSucceeded, KeyFile: "/k/id_ed25519", Generated: true}, false},
		{"generate failed", sshconn.KeySetupResult{Outcome: sshconn.KeySetupGenerateFailed, KeyFile: "/k/id", Generated: true, Err: errors.New("disk full")},
			KeySetupResult{Outcome: KeySetupGenerateFailed, KeyFile: "/k/id", Generated: true, Reason: "disk full"}, false},
		{"append failed", sshconn.KeySetupResult{Outcome: sshconn.KeySetupAppendFailed, KeyFile: "/k/id", Err: errors.New("permission denied")},
			KeySetupResult{Outcome: KeySetupAppendFailed, KeyFile: "/k/id", Reason: "permission denied"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, testTiming())
			id := e.addHost("alice", "gpu01")
			ls := newLinks()
			var asked []bool
			e.dialer.set(keySetupDialer(okDialer(ls, "alice", nvidiaDevices(0)), tc.result, &asked))
			e.m.Connect(id)
			p := e.waitEvents(EventKeySetup, 1)[0].(KeySetupPrompt)
			if err := e.m.AnswerKeySetup(p.ID, KeySetupNow); err != nil {
				t.Fatal(err)
			}
			ls.next(t)
			e.waitState(id, StateConnected)
			got := e.waitEvents(EventKeySetupResult, 1)[0].(KeySetupResult)
			want := tc.want
			want.Host, want.Name = id, "alice@gpu01"
			if got != want {
				t.Fatalf("result %+v, want %+v", got, want)
			}
			if e.store.NoKeySetupPrompt(id) != tc.never {
				t.Fatalf("never recorded = %v", !tc.never)
			}
		})
	}
}

func TestKeySetupPromptClosedOnLoss(t *testing.T) {
	e := newEnv(t, testTiming())
	id := e.addHost("alice", "gpu01")
	lost := make(chan struct{})
	e.dialer.set(func(ctx context.Context, n int, tg Target, a Auth) (Link, error) {
		kctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-lost:
				cancel()
			case <-kctx.Done():
			}
		}()
		if _, err := a.KeySetup(kctx, sshconn.KeySetupRequest{}); err != nil && ctx.Err() == nil {
			return nil, connectErr("Lost the connection to gpu01 while waiting for the answer about passwordless login")
		}
		return nil, canceledErr(ctx.Err())
	})
	e.m.Connect(id)
	p := e.waitEvents(EventKeySetup, 1)[0].(KeySetupPrompt)
	close(lost)
	closed := e.waitEvents(EventKeySetupClosed, 1)
	if closed[0].(KeySetupClosed).ID != p.ID {
		t.Fatalf("closed %+v", closed)
	}
	errs := e.waitEvents(EventError, 1)
	if ce := errs[0].(ConnectError); ce.Kind != ErrConnect {
		t.Fatalf("error %+v", ce)
	}
	e.waitState(id, StateDisconnected)
}

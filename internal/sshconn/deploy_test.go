//go:build !windows

package sshconn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRemote 產生假的遠端程式: 一個 shell 腳本, --version 輸出指定的版本與協定
// 版本, --loop 輸出一行 devices 訊息後讀取 stdin 直到 EOF.
func fakeRemote(ver string, proto int) []byte {
	return []byte(fmt.Sprintf(`#!/bin/sh
case "$1" in
--version) echo "gpumon %s+unknown (protocol %d)"; exit 0;;
--loop) echo '{"type":"devices","protocol":%d,"version":"%s","hostname":"fake","gpus":[]}'; echo "args: $*" >&2
  while read line; do :; done; exit 0;;
esac
exit 2
`, ver, proto, proto, ver))
}

type deployFixture struct {
	srv *testServer
	c   *Client
	// procLs 為假的 `ls -l /proc/[0-9]*/exe` 輸出.
	procLs   string
	platform string
	delay    time.Duration
}

func newDeployFixture(t *testing.T) *deployFixture {
	t.Helper()
	srv := newTestServer(t)
	srv.setPassword("pw")
	f := &deployFixture{srv: srv, platform: "Linux x86_64"}
	srv.execHook = func(cmd string) (string, int, bool) {
		time.Sleep(f.delay)
		switch cmd {
		case "sh -c 'uname -sm'":
			return f.platform + "\n", 0, true
		case "sh -c 'ls -l /proc/[0-9]*/exe 2>/dev/null'":
			return f.procLs, 1, true
		}
		return "", 0, false
	}
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	f.c = c
	return f
}

func (f *deployFixture) remotePath() string {
	return filepath.Join(f.srv.home, RemoteDir, RemoteName)
}

func (f *deployFixture) countCommand(substr string) int {
	n := 0
	for _, c := range f.srv.commands() {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func TestDeployFlow(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	guiBin := fakeRemote("0.2.0", 1)
	opts := DeployOptions{Binary: guiBin, GUIVersion: "0.2.0", GUIProtocol: 1}

	// 全新部署.
	dep, err := f.c.Deploy(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !dep.Uploaded || dep.Remote != nil || dep.Protocol != 1 || dep.Home != f.srv.home ||
		dep.Path != f.srv.home+"/.gpumon/gpumon" {
		t.Errorf("fresh deploy = %+v", dep)
	}
	data, _ := os.ReadFile(f.remotePath())
	if string(data) != string(guiBin) {
		t.Error("uploaded content differs")
	}
	if st, _ := os.Stat(f.remotePath()); st.Mode().Perm() != 0o755 {
		t.Errorf("program mode = %v", st.Mode())
	}
	if st, _ := os.Stat(filepath.Dir(f.remotePath())); st.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v", st.Mode())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.remotePath()), remoteTmpName)); !os.IsNotExist(err) {
		t.Error("gpumon.tmp left behind")
	}
	for _, c := range f.srv.commands() {
		if !strings.HasPrefix(c, "sh -c ") {
			t.Errorf("command not sent through sh -c: %s", c)
		}
	}

	// 版號相同, 不上傳也不檢查是否正在執行.
	dep, err = f.c.Deploy(ctx, opts)
	if err != nil || dep.Uploaded || dep.Remote == nil || dep.Remote.Version.String() != "0.2.0" {
		t.Errorf("equal version: %+v %v", dep, err)
	}
	if n := f.countCommand("/proc"); n != 0 {
		t.Errorf("running check executed %d times for equal versions", n)
	}

	// 遠端較高, 不上傳, 協定版本取遠端的值.
	writeFile(t, f.remotePath(), string(fakeRemote("0.3.0-alpha.1", 1)), 0o755)
	dep, err = f.c.Deploy(ctx, DeployOptions{Binary: guiBin, GUIVersion: "0.2.0", GUIProtocol: 2})
	if err != nil || dep.Uploaded || dep.Protocol != 1 {
		t.Errorf("higher remote: %+v %v", dep, err)
	}

	// 遠端較低且正在執行, 不上傳.
	writeFile(t, f.remotePath(), string(fakeRemote("0.1.0", 1)), 0o755)
	f.procLs = "lrwxrwxrwx 1 u u 0 Nov  5 10:00 /proc/1/exe -> /bin/bash\n" +
		"lrwxrwxrwx 1 u u 0 Nov  5 10:00 /proc/2/exe -> " + f.remotePath() + " (deleted)\n"
	dep, err = f.c.Deploy(ctx, opts)
	if err != nil || dep.Uploaded || dep.Remote.Version.String() != "0.1.0" {
		t.Errorf("lower running: %+v %v", dep, err)
	}
	if n := f.countCommand("/proc"); n != 1 {
		t.Errorf("running check executed %d times", n)
	}

	// 遠端較低且未執行, 覆寫.
	f.procLs = "lrwxrwxrwx 1 u u 0 Nov  5 10:00 /proc/1/exe -> /bin/bash\n"
	dep, err = f.c.Deploy(ctx, opts)
	if err != nil || !dep.Uploaded || dep.Remote.Version.String() != "0.1.0" || dep.Protocol != 1 {
		t.Errorf("lower not running: %+v %v", dep, err)
	}
	data, _ = os.ReadFile(f.remotePath())
	if string(data) != string(guiBin) {
		t.Error("program was not overwritten")
	}

	// 無法取得遠端的程式版號 (輸出不合格式), 上傳.
	writeFile(t, f.remotePath(), "#!/bin/sh\necho gpumon 0.2.0\n", 0o755)
	dep, err = f.c.Deploy(ctx, opts)
	if err != nil || !dep.Uploaded || dep.Remote != nil {
		t.Errorf("unknown version: %+v %v", dep, err)
	}

	// 遠端協定版本較低, 可用.
	writeFile(t, f.remotePath(), string(fakeRemote("0.2.0", 1)), 0o755)
	dep, err = f.c.Deploy(ctx, DeployOptions{Binary: guiBin, GUIVersion: "0.2.0", GUIProtocol: 2})
	if err != nil || dep.Uploaded || dep.Protocol != 1 {
		t.Errorf("older protocol: %+v %v", dep, err)
	}

	// 遠端協定版本較高, 不可用.
	writeFile(t, f.remotePath(), string(fakeRemote("0.2.0", 2)), 0o755)
	_, err = f.c.Deploy(ctx, opts)
	expectKind(t, err, KindProtocolTooNew)
	if !strings.Contains(err.Error(), "update GPU Monitor") {
		t.Errorf("message = %v", err)
	}
}

func TestDeployPlatformMismatch(t *testing.T) {
	f := newDeployFixture(t)
	f.platform = "Linux aarch64"
	_, err := f.c.Deploy(context.Background(), DeployOptions{Binary: []byte("x"), GUIVersion: "0.1.0"})
	expectKind(t, err, KindPlatform)
	if !strings.Contains(err.Error(), "aarch64") {
		t.Errorf("message = %v", err)
	}
	if n := f.countCommand("HOME"); n != 0 {
		t.Error("continued after platform mismatch")
	}
}

func TestDeployUploadFailure(t *testing.T) {
	f := newDeployFixture(t)
	writeFile(t, filepath.Join(f.srv.home, RemoteDir), "not a directory", 0o600)
	_, err := f.c.Deploy(context.Background(), DeployOptions{Binary: []byte("x"), GUIVersion: "0.1.0"})
	expectKind(t, err, KindUpload)
}

func TestDeployCommandTimeout(t *testing.T) {
	f := newDeployFixture(t)
	f.delay = time.Second
	_, err := f.c.Deploy(context.Background(), DeployOptions{
		Binary: []byte("x"), GUIVersion: "0.1.0", CommandTimeout: 200 * time.Millisecond,
	})
	expectKind(t, err, KindConnect)
}

func TestDeployInvalidGUIVersion(t *testing.T) {
	f := newDeployFixture(t)
	_, err := f.c.Deploy(context.Background(), DeployOptions{Binary: []byte("x"), GUIVersion: "0.1"})
	expectKind(t, err, KindConfig)
}

func TestStart(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	dep, err := f.c.Deploy(ctx, DeployOptions{Binary: fakeRemote("0.2.0", 1), GUIVersion: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Start(ctx, dep, 50); KindOf(err) != KindConfig {
		t.Errorf("invalid interval: %v", err)
	}
	p, err := f.c.Start(ctx, dep, 500)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(p.Stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, `{"type":"devices","protocol":1`) {
		t.Fatalf("first line = %q, %v", line, err)
	}
	errLine, _ := bufio.NewReader(p.Stderr).ReadString('\n')
	if errLine != "args: --loop --interval 500\n" {
		t.Errorf("arguments = %q", errLine)
	}
	want := "sh -c 'exec " + dep.Path + " --loop --interval 500'"
	if cmds := f.srv.commands(); cmds[len(cmds)-1] != want {
		t.Errorf("command = %s, want %s", cmds[len(cmds)-1], want)
	}
	p.Stdin.Close()
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Wait = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("remote program did not exit after stdin EOF")
	}
}

func TestStartNotExecutable(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	dep, err := f.c.Deploy(ctx, DeployOptions{Binary: []byte("\x7fELF\x00\x00\x00\x00garbage"), GUIVersion: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.c.Start(ctx, dep, 1000)
	if err != nil {
		t.Fatal(err)
	}
	go bufio.NewReader(p.Stdout).ReadString(0)
	go bufio.NewReader(p.Stderr).ReadString(0)
	err = p.Wait()
	expectKind(t, err, KindExec)
	var e *Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, "could not be executed") {
		t.Errorf("message = %v", err)
	}
}

// confirmRecorder 記錄確認回呼函數的呼叫, 並於呼叫當下檢查遠端尚無任何寫入.
type confirmRecorder struct {
	t    *testing.T
	f    *deployFixture
	reqs []UploadRequest
	// before 為呼叫當下遠端程式的內容; 不存在時為 nil.
	before [][]byte
	// dirExisted 為呼叫當下 ~/.gpumon/ 是否存在.
	dirExisted []bool
	// tmpExisted 為呼叫當下 gpumon.tmp 是否存在.
	tmpExisted []bool
}

func (r *confirmRecorder) fn(ctx context.Context, req UploadRequest) error {
	r.reqs = append(r.reqs, req)
	data, err := os.ReadFile(r.f.remotePath())
	if err != nil {
		data = nil
	}
	r.before = append(r.before, data)
	_, err = os.Stat(filepath.Dir(r.f.remotePath()))
	r.dirExisted = append(r.dirExisted, err == nil)
	_, err = os.Stat(filepath.Join(filepath.Dir(r.f.remotePath()), remoteTmpName))
	r.tmpExisted = append(r.tmpExisted, err == nil)
	return nil
}

func TestDeployConfirmUpload(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	rec := &confirmRecorder{t: t, f: f}
	guiBin := fakeRemote("0.2.0", 1)
	opts := DeployOptions{Binary: guiBin, GUIVersion: "0.2.0", GUIProtocol: 1, ConfirmUpload: rec.fn}

	// 首次上傳: 呼叫回呼函數, 呼叫當下目錄尚未建立.
	dep, err := f.c.Deploy(ctx, opts)
	if err != nil || !dep.Uploaded {
		t.Fatalf("fresh deploy: %+v %v", dep, err)
	}
	if len(rec.reqs) != 1 {
		t.Fatalf("callback called %d times for a fresh deploy", len(rec.reqs))
	}
	want := UploadRequest{Host: f.srv.host, User: "tester", Path: f.remotePath(), Update: false, RemoteVersion: "", NewVersion: "0.2.0"}
	if rec.reqs[0] != want {
		t.Errorf("fresh request = %+v, want %+v", rec.reqs[0], want)
	}
	if rec.dirExisted[0] {
		t.Error("remote directory existed before the confirmation")
	}

	// 版號相同, 遠端較高, 遠端較低且正在執行: 皆不呼叫.
	if _, err := f.c.Deploy(ctx, opts); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.remotePath(), string(fakeRemote("0.3.0", 1)), 0o755)
	if _, err := f.c.Deploy(ctx, opts); err != nil {
		t.Fatal(err)
	}
	old := fakeRemote("0.1.0-rc.2", 1)
	writeFile(t, f.remotePath(), string(old), 0o755)
	f.procLs = "lrwxrwxrwx 1 u u 0 Nov  5 10:00 /proc/2/exe -> " + f.remotePath() + "\n"
	if dep, err := f.c.Deploy(ctx, opts); err != nil || dep.Uploaded {
		t.Fatalf("lower running: %+v %v", dep, err)
	}
	if len(rec.reqs) != 1 {
		t.Fatalf("callback called %d times without uploads", len(rec.reqs)-1)
	}

	// 更新: 呼叫回呼函數並附兩個版號, 呼叫當下遠端程式仍為舊的內容.
	f.procLs = ""
	dep, err = f.c.Deploy(ctx, opts)
	if err != nil || !dep.Uploaded {
		t.Fatalf("update: %+v %v", dep, err)
	}
	if len(rec.reqs) != 2 {
		t.Fatalf("callback called %d times", len(rec.reqs))
	}
	want = UploadRequest{Host: f.srv.host, User: "tester", Path: f.remotePath(), Update: true, RemoteVersion: "0.1.0-rc.2", NewVersion: "0.2.0"}
	if rec.reqs[1] != want {
		t.Errorf("update request = %+v, want %+v", rec.reqs[1], want)
	}
	if string(rec.before[1]) != string(old) || rec.tmpExisted[1] {
		t.Error("the remote program was written before the confirmation")
	}
	if data, _ := os.ReadFile(f.remotePath()); string(data) != string(guiBin) {
		t.Error("program was not updated after the confirmation")
	}
}

func TestDeployConfirmUploadDeclined(t *testing.T) {
	cases := []struct {
		name string
		fn   func(cancel context.CancelFunc) UploadConfirmFunc
		kind Kind
	}{
		{"canceled", func(context.CancelFunc) UploadConfirmFunc {
			return func(context.Context, UploadRequest) error { return ErrCanceled }
		}, KindCanceled},
		{"other error", func(context.CancelFunc) UploadConfirmFunc {
			return func(context.Context, UploadRequest) error { return errors.New("declined") }
		}, KindCanceled},
		{"context canceled", func(cancel context.CancelFunc) UploadConfirmFunc {
			return func(ctx context.Context, _ UploadRequest) error {
				go cancel()
				<-ctx.Done()
				return ctx.Err()
			}
		}, KindCanceled},
		{"confirmed after cancel", func(cancel context.CancelFunc) UploadConfirmFunc {
			return func(ctx context.Context, _ UploadRequest) error {
				cancel()
				return nil
			}
		}, KindCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeployFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := f.c.Deploy(ctx, DeployOptions{Binary: []byte("x"), GUIVersion: "0.1.0", ConfirmUpload: tc.fn(cancel)})
			expectKind(t, err, tc.kind)
			if _, err := os.Stat(filepath.Dir(f.remotePath())); !os.IsNotExist(err) {
				t.Error("remote directory created although the upload was not confirmed")
			}
		})
	}
}

func TestDeployConfirmWaitIsNotTimed(t *testing.T) {
	f := newDeployFixture(t)
	old := confirmKeepalive
	confirmKeepalive = 30 * time.Millisecond
	t.Cleanup(func() { confirmKeepalive = old })
	_, err := f.c.Deploy(context.Background(), DeployOptions{
		Binary: fakeRemote("0.2.0", 1), GUIVersion: "0.2.0",
		CommandTimeout: 150 * time.Millisecond, UploadTimeout: 300 * time.Millisecond,
		ConfirmUpload: func(ctx context.Context, _ UploadRequest) error {
			select {
			case <-time.After(700 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatalf("deploy after a long confirmation: %v", err)
	}
	if _, err := os.Stat(f.remotePath()); err != nil {
		t.Error("program not uploaded")
	}
}

func TestDeployConnectionLostWhileConfirming(t *testing.T) {
	waitLost := func(ctx context.Context, _ UploadRequest) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("connection loss not detected")
		}
	}
	t.Run("closed", func(t *testing.T) {
		f := newDeployFixture(t)
		_, err := f.c.Deploy(context.Background(), DeployOptions{
			Binary: []byte("x"), GUIVersion: "0.1.0",
			ConfirmUpload: func(ctx context.Context, req UploadRequest) error {
				f.c.Close()
				return waitLost(ctx, req)
			},
		})
		expectKind(t, err, KindConnect)
		if _, err := os.Stat(filepath.Dir(f.remotePath())); !os.IsNotExist(err) {
			t.Error("remote directory created")
		}
	})
	t.Run("no keepalive reply", func(t *testing.T) {
		f := newDeployFixture(t)
		f.srv.setIgnoreGlobal()
		old := confirmKeepalive
		confirmKeepalive = 30 * time.Millisecond
		t.Cleanup(func() { confirmKeepalive = old })
		start := time.Now()
		_, err := f.c.Deploy(context.Background(), DeployOptions{
			Binary: []byte("x"), GUIVersion: "0.1.0", CommandTimeout: 150 * time.Millisecond,
			ConfirmUpload: waitLost,
		})
		expectKind(t, err, KindConnect)
		if el := time.Since(start); el > 2*time.Second {
			t.Errorf("loss detected after %v", el)
		}
		if _, err := os.Stat(filepath.Dir(f.remotePath())); !os.IsNotExist(err) {
			t.Error("remote directory created")
		}
	})
}

//go:build !windows

package sshconn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"gpumon/internal/remotebin"
)

// TestLive 於實際的測試主機驗證部署流程. 只在設定環境變數
// GPUMON_LIVE_HOST=<user>@<host> 時執行; 使用本機的 ~/.ssh/config (唯讀) 與
// ssh-agent, known_hosts 指向暫存目錄內的複本. 結束時刪除遠端的 ~/.gpumon/.
func TestLive(t *testing.T) {
	target := os.Getenv("GPUMON_LIVE_HOST")
	if target == "" {
		t.Skip("GPUMON_LIVE_HOST is not set")
	}
	userName, host, ok := strings.Cut(target, "@")
	if !ok {
		t.Fatal("GPUMON_LIVE_HOST must be user@host")
	}
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	kh := filepath.Join(tmp, "known_hosts")
	if data, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts")); err == nil {
		writeFile(t, kh, string(data), 0o600)
	}
	cfg := Config{Host: host, User: userName, KnownHostsPath: kh}
	ctx := context.Background()

	dial := func() *Client {
		t.Helper()
		c, err := Dial(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := dial()
	defer c.Close()

	sc, err := sftp.NewClient(c.SSH())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	out, _, err := c.run(ctx, shCommand(`printf %s "$HOME"`), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	remoteHome := string(out)
	dir := path.Join(remoteHome, RemoteDir)
	if _, err := sc.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s already exists on the test host; refusing to touch it", dir)
	}
	t.Cleanup(func() {
		c2, err := Dial(ctx, cfg)
		if err != nil {
			t.Errorf("cleanup dial: %v", err)
			return
		}
		defer c2.Close()
		running, err := c2.programRunning(ctx, path.Join(dir, RemoteName), 10*time.Second)
		t.Logf("cleanup: gpumon running = %v (%v)", running, err)
		s2, err := sftp.NewClient(c2.SSH())
		if err != nil {
			t.Errorf("cleanup sftp: %v", err)
			return
		}
		defer s2.Close()
		for _, name := range []string{RemoteName, remoteTmpName} {
			s2.Remove(path.Join(dir, name))
		}
		if err := s2.RemoveDirectory(dir); err != nil {
			t.Errorf("cleanup: remove %s: %v", dir, err)
		}
		if _, err := s2.Stat(dir); errors.Is(err, os.ErrNotExist) {
			t.Logf("cleanup: %s removed", dir)
		} else {
			t.Errorf("cleanup: %s still exists (%v)", dir, err)
		}
	})
	size := func() int64 {
		st, err := sc.Stat(path.Join(dir, RemoteName))
		if err != nil {
			t.Fatal(err)
		}
		return st.Size()
	}

	// 全新部署, 啟動後讀取 devices 訊息, 關閉後確認遠端程式結束.
	bin := remotebin.Binary
	dep, err := c.Deploy(ctx, DeployOptions{Binary: bin, GUIVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := sc.Stat(dir)
	t.Logf("fresh: uploaded=%v remote=%v protocol=%d path=%s size=%d dirmode=%v",
		dep.Uploaded, dep.Remote, dep.Protocol, dep.Path, size(), st.Mode())
	if !dep.Uploaded || size() != int64(len(bin)) || st.Mode().Perm() != 0o700 {
		t.Fatal("fresh deploy did not upload as specified")
	}
	p, err := c.Start(ctx, dep, 1000)
	if err != nil {
		t.Fatal(err)
	}
	go bufio.NewReader(p.Stderr).WriteTo(discard{})
	line, err := bufio.NewReader(p.Stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, `{"type":"devices","protocol":1,`) {
		t.Fatalf("first line = %q, %v", line, err)
	}
	t.Logf("devices: %.200s", line)
	if running, _ := c.programRunning(ctx, dep.Path, 10*time.Second); !running {
		t.Error("running program not detected")
	}
	p.Close()
	t.Logf("wait after close: %v", p.Wait())
	time.Sleep(time.Second)
	running, err := c.programRunning(ctx, dep.Path, 10*time.Second)
	t.Logf("after close: running=%v err=%v", running, err)
	if running {
		t.Error("remote program still running after the channel was closed")
	}

	// 版號相同, 不上傳.
	dep, err = c.Deploy(ctx, DeployOptions{Binary: bin, GUIVersion: "0.1.0"})
	if err != nil || dep.Uploaded {
		t.Fatalf("equal version: %+v %v", dep, err)
	}
	t.Logf("equal: uploaded=%v remote=%s", dep.Uploaded, dep.Remote.Version)

	// 遠端較低且未執行, 覆寫. 內容於尾端附加位元組, 使其與原檔不同.
	bin2 := append(append([]byte(nil), bin...), []byte("gpumon-live-test-padding")...)
	dep, err = c.Deploy(ctx, DeployOptions{Binary: bin2, GUIVersion: "0.2.0"})
	if err != nil || !dep.Uploaded || size() != int64(len(bin2)) {
		t.Fatalf("lower not running: %+v %v", dep, err)
	}
	t.Logf("lower, not running: uploaded=%v size=%d", dep.Uploaded, size())

	// 遠端較低且正在執行, 不上傳.
	p, err = c.Start(ctx, dep, 1000)
	if err != nil {
		t.Fatal(err)
	}
	go bufio.NewReader(p.Stderr).WriteTo(discard{})
	if _, err := bufio.NewReader(p.Stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	c2 := dial()
	dep2, err := c2.Deploy(ctx, DeployOptions{Binary: bin, GUIVersion: "0.2.0"})
	c2.Close()
	if err != nil || dep2.Uploaded || size() != int64(len(bin2)) {
		t.Fatalf("lower running: %+v %v", dep2, err)
	}
	t.Logf("lower, running: uploaded=%v size=%d", dep2.Uploaded, size())
	p.Close()
	p.Wait()

	// 遠端的協定版本高於 GUI: 先以較高的 GUI 版號上傳一個回報協定版本 2 的腳本.
	script := []byte("#!/bin/sh\necho 'gpumon 0.1.0+unknown (protocol 2)'\n")
	time.Sleep(500 * time.Millisecond)
	if dep, err = c.Deploy(ctx, DeployOptions{Binary: script, GUIVersion: "0.3.0"}); err != nil || !dep.Uploaded {
		t.Fatalf("upload script: %+v %v", dep, err)
	}
	_, err = c.Deploy(ctx, DeployOptions{Binary: bin, GUIVersion: "0.1.0"})
	t.Logf("protocol too new: kind=%s msg=%v", KindOf(err), err)
	if KindOf(err) != KindProtocolTooNew {
		t.Error("expected KindProtocolTooNew")
	}

	// 遠端程式無法執行.
	garbage := []byte("\x7fELF\x00\x00\x00\x00not really an executable")
	if dep, err = c.Deploy(ctx, DeployOptions{Binary: garbage, GUIVersion: "0.4.0"}); err != nil || !dep.Uploaded {
		t.Fatalf("upload garbage: %+v %v", dep, err)
	}
	p, err = c.Start(ctx, dep, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	go bufio.NewReader(p.Stderr).WriteTo(&stderr)
	go bufio.NewReader(p.Stdout).WriteTo(discard{})
	err = p.Wait()
	t.Logf("not executable: kind=%s err=%v stderr=%q", KindOf(err), err, stderr.String())
	if KindOf(err) != KindExec {
		t.Error("expected KindExec")
	}

	// accept-new: 空的 known_hosts 自動寫入.
	empty := filepath.Join(tmp, "empty_known_hosts")
	cfgNew := cfg
	cfgNew.KnownHostsPath = empty
	c3, err := Dial(ctx, cfgNew)
	if err != nil {
		t.Fatal(err)
	}
	c3.Close()
	data, _ := os.ReadFile(empty)
	t.Logf("accept-new wrote: %.80s", data)

	// 不使用 ssh-agent, 只以金鑰檔登入.
	cfgNoAgent := cfg
	cfgNoAgent.Agent = NoAgent
	c4, err := Dial(ctx, cfgNoAgent)
	t.Logf("key file without agent: err=%v", err)
	if c4 != nil {
		c4.Close()
	}
}

// TestLiveConfirmUpload 於實際的測試主機驗證上傳前的確認: 首次上傳與更新皆呼叫
// 確認回呼函數, 回呼函數返回前遠端沒有任何寫入, 等待時間超過遠端指令的逾時上限
// 仍不逾時, 確認後上傳成功並收到 devices 訊息. 只在設定 GPUMON_LIVE_HOST 時執行.
// 遠端目錄改為家目錄下專用的暫時目錄, 不碰 ~/.gpumon/, 結束時刪除該目錄.
func TestLiveConfirmUpload(t *testing.T) {
	target := os.Getenv("GPUMON_LIVE_HOST")
	if target == "" {
		t.Skip("GPUMON_LIVE_HOST is not set")
	}
	userName, host, ok := strings.Cut(target, "@")
	if !ok {
		t.Fatal("GPUMON_LIVE_HOST must be user@host")
	}
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	kh := filepath.Join(tmp, "known_hosts")
	if data, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts")); err == nil {
		writeFile(t, kh, string(data), 0o600)
	}
	cfg := Config{Host: host, User: userName, KnownHostsPath: kh}
	ctx := context.Background()

	testDir := fmt.Sprintf(".gpumon-livetest-%d", time.Now().UnixNano())
	old, oldKeepalive := remoteDir, confirmKeepalive
	remoteDir = testDir
	// 縮短 keepalive 的間隔, 使等待確認期間實際送出 keepalive 並收到 OpenSSH 的回覆.
	confirmKeepalive = 200 * time.Millisecond
	t.Cleanup(func() { remoteDir, confirmKeepalive = old, oldKeepalive })

	c, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sc, err := sftp.NewClient(c.SSH())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	out, _, err := c.run(ctx, shCommand(`printf %s "$HOME"`), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dir := path.Join(string(out), testDir)
	if _, err := sc.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s already exists on the test host; refusing to touch it", dir)
	}
	t.Cleanup(func() {
		c2, err := Dial(ctx, cfg)
		if err != nil {
			t.Errorf("cleanup dial: %v", err)
			return
		}
		defer c2.Close()
		running, err := c2.programRunning(ctx, path.Join(dir, RemoteName), 10*time.Second)
		t.Logf("cleanup: test program running = %v (%v)", running, err)
		s2, err := sftp.NewClient(c2.SSH())
		if err != nil {
			t.Errorf("cleanup sftp: %v", err)
			return
		}
		defer s2.Close()
		for _, name := range []string{RemoteName, remoteTmpName} {
			s2.Remove(path.Join(dir, name))
		}
		s2.RemoveDirectory(dir)
		if _, err := s2.Stat(dir); errors.Is(err, os.ErrNotExist) {
			t.Logf("cleanup: %s removed", dir)
		} else {
			t.Errorf("cleanup: %s still exists (%v)", dir, err)
		}
	})

	var reqs []UploadRequest
	confirm := func(cctx context.Context, req UploadRequest) error {
		reqs = append(reqs, req)
		if len(reqs) == 1 {
			if _, err := sc.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s exists before the confirmation (%v)", dir, err)
			}
		} else {
			st, err := sc.Stat(path.Join(dir, RemoteName))
			if err != nil || st.Size() != int64(len(remotebin.Binary)) {
				t.Errorf("program changed before the confirmation (%v)", err)
			}
		}
		select {
		case <-time.After(2 * time.Second):
			return nil
		case <-cctx.Done():
			return cctx.Err()
		}
	}
	startAndRead := func(dep *Deployment) {
		t.Helper()
		p, err := c.Start(ctx, dep, 1000)
		if err != nil {
			t.Fatal(err)
		}
		go bufio.NewReader(p.Stderr).WriteTo(discard{})
		line, err := bufio.NewReader(p.Stdout).ReadString('\n')
		if err != nil || !strings.HasPrefix(line, `{"type":"devices",`) {
			t.Fatalf("first line = %q, %v", line, err)
		}
		t.Logf("devices: %.160s", line)
		p.Close()
		p.Wait()
		time.Sleep(time.Second)
		if running, _ := c.programRunning(ctx, dep.Path, 10*time.Second); running {
			t.Error("test program still running after the channel was closed")
		}
	}

	// 首次上傳. 遠端指令的逾時上限為 1 秒, 確認等待 2 秒.
	opts := DeployOptions{Binary: remotebin.Binary, GUIVersion: "0.1.0", CommandTimeout: time.Second, ConfirmUpload: confirm}
	dep, err := c.Deploy(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Update || reqs[0].RemoteVersion != "" || reqs[0].Path != path.Join(dir, RemoteName) ||
		reqs[0].User != userName || !dep.Uploaded {
		t.Fatalf("fresh: requests %+v, deployment %+v", reqs, dep)
	}
	t.Logf("fresh: request %+v", reqs[0])
	startAndRead(dep)

	// 版號相同, 不呼叫.
	if dep, err = c.Deploy(ctx, opts); err != nil || dep.Uploaded || len(reqs) != 1 {
		t.Fatalf("equal version: %+v %v, %d requests", dep, err, len(reqs))
	}

	// 更新.
	opts.GUIVersion = "0.2.0"
	dep, err = c.Deploy(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 || !reqs[1].Update || reqs[1].RemoteVersion == "" || reqs[1].NewVersion != "0.2.0" || !dep.Uploaded {
		t.Fatalf("update: requests %+v, deployment %+v", reqs, dep)
	}
	t.Logf("update: request %+v", reqs[1])
	startAndRead(dep)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

//go:build !windows

package sshconn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeema/knownhosts"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

// TestValidKnownHostsLine 確認逐行檢查的結果與 golang.org/x/crypto/ssh/knownhosts
// 讀取單行檔案的結果一致.
func TestValidKnownHostsLine(t *testing.T) {
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	pub := pubOf(t, k)
	good := knownhosts.Line([]string{"host.example.com"}, pub)
	fields := strings.Fields(good)
	blob := fields[2]
	lines := []string{
		"",
		"   ",
		"# comment",
		good,
		"  " + good + "\t",
		knownhosts.Line([]string{"[10.0.0.1]:2222", "alias"}, pub),
		"@cert-authority *.example.com " + fields[1] + " " + blob,
		"@revoked * " + fields[1] + " " + blob,
		"@revoked [broken " + fields[1] + " " + blob,
		xknownhosts.HashHostname("host.example.com") + " " + fields[1] + " " + blob,
		"!host,*.example.com " + fields[1] + " " + blob + " comment",
		"garbage",
		"host.example.com",
		"host.example.com ssh-ed25519",
		"host.example.com ssh-ed25519 !!!notbase64",
		"host.example.com ssh-rsa " + blob,
		"@unknown host.example.com " + fields[1] + " " + blob,
		"@cert-authority @revoked host " + fields[1] + " " + blob,
		"[broken " + fields[1] + " " + blob,
		"! " + fields[1] + " " + blob,
		"|2|c2FsdA==|aGFzaA== " + fields[1] + " " + blob,
		"|1|c2FsdA== " + fields[1] + " " + blob,
		"|1|!!|aGFzaA== " + fields[1] + " " + blob,
	}
	dir := t.TempDir()
	for i, line := range lines {
		path := filepath.Join(dir, "kh")
		writeFile(t, path, line+"\n", 0o600)
		_, err := xknownhosts.New(path)
		if got, want := validKnownHostsLine([]byte(line)), err == nil; got != want {
			t.Errorf("line %d %q: valid = %v, library accepts = %v (%v)", i, line, got, want, err)
		}
	}
	if validKnownHostsLine([]byte(good + " " + strings.Repeat("x", maxKnownHostsLine))) {
		t.Error("overlong line accepted")
	}
}

// badKnownHostsLines 為三行無法解析的記錄.
const badKnownHostsLines = "garbage line\n" +
	"broken.example.com ssh-ed25519 !!!notbase64\n" +
	"@unknown host ssh-ed25519 AAAA\n"

func TestOwnKnownHostsRepaired(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	good := knownhosts.Line([]string{srv.addr}, srv.hostSigner.PublicKey())
	writeFile(t, env.ownKnownHosts, "# keep\n"+badKnownHostsLines+good+"\n", 0o644)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	cfg.HostKey = nil
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	data, _ := os.ReadFile(env.ownKnownHosts)
	if string(data) != "# keep\n"+good+"\n" {
		t.Errorf("own known_hosts not repaired:\n%s", data)
	}
	if st, err := os.Stat(env.ownKnownHosts); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("mode: %v %v", st, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(env.ownKnownHosts), ".*")); len(matches) != 0 {
		t.Errorf("temporary files left: %v", matches)
	}
}

func TestOpenSSHKnownHostsBadLinesSkipped(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	content := badKnownHostsLines + knownhosts.Line([]string{srv.addr}, srv.hostSigner.PublicKey()) + "\n"
	writeFile(t, env.knownHosts, content, 0o600)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	cfg.HostKey = nil
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if data, _ := os.ReadFile(env.knownHosts); string(data) != content {
		t.Errorf("OpenSSH known_hosts modified:\n%s", data)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(env.ownKnownHosts), ".*")); len(matches) != 0 {
		t.Errorf("temporary files left: %v", matches)
	}

	// @revoked 的語意不變, 錯誤說明列出 OpenSSH 的 known_hosts 本身.
	writeFile(t, env.knownHosts, badKnownHostsLines+"@revoked "+
		knownhosts.Line([]string{"*"}, srv.hostSigner.PublicKey())+"\n", 0o600)
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindHostKey)
	if !strings.Contains(err.Error(), "revoked in "+env.knownHosts) {
		t.Errorf("error = %v", err)
	}
}

func TestKnownHostsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any file")
	}
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	good := knownhosts.Line([]string{srv.addr}, srv.hostSigner.PublicKey())
	writeFile(t, env.ownKnownHosts, good+"\n", 0o600)
	os.Chmod(env.ownKnownHosts, 0o000)
	writeFile(t, env.knownHosts, good+"\n", 0o600)
	os.Chmod(env.knownHosts, 0o000)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	asked := 0
	cfg.HostKey = func(_ context.Context, req HostKeyRequest) error {
		asked++
		if req.Changed {
			t.Errorf("request = %+v", req)
		}
		return nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 1 {
		t.Errorf("asked %d times; both files must be treated as empty", asked)
	}
	// 程式自己的檔案刪除後重建, 再寫入接受的主機金鑰.
	data, err := os.ReadFile(env.ownKnownHosts)
	if err != nil || string(data) != good+"\n" {
		t.Errorf("own known_hosts = %q %v", data, err)
	}
	if st, _ := os.Stat(env.knownHosts); st.Mode().Perm() != 0 {
		t.Errorf("OpenSSH known_hosts mode changed: %v", st.Mode())
	}
}

func TestKnownHostsRecreateFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write any directory")
	}
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	writeFile(t, env.ownKnownHosts, "x\n", 0o000)
	dir := filepath.Dir(env.ownKnownHosts)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	asked := 0
	cfg.HostKey = func(context.Context, HostKeyRequest) error {
		asked++
		return nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 1 {
		t.Errorf("asked %d times", asked)
	}
}

func TestConfigWarningNote(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	writeFile(t, env.config, "Host *\n  Port abc\n", 0o600)
	cfg := env.cfg(srv)
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)
	if !strings.HasSuffix(err.Error(), ". Parts of ~/.ssh/config were skipped because they could not be read or parsed") {
		t.Errorf("error = %v", err)
	}

	// 沒有略過之處時不附加.
	writeFile(t, env.config, "Host *\n  Port 22\n", 0o600)
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)
	if strings.Contains(err.Error(), "skipped") {
		t.Errorf("error = %v", err)
	}

	// 連線成功時不回報.
	srv.setPassword("pw")
	writeFile(t, env.config, "Host *\n  Port abc\n", 0o600)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

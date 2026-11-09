//go:build !windows

package sshconn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// genKey 產生 ed25519 金鑰並寫入 path; passphrase 不為空時以之加密. 回傳私鑰.
func genKey(t *testing.T, path, passphrase string, writePub bool) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(pem.EncodeToMemory(block)), 0o600)
	if writePub {
		pub, _ := ssh.NewPublicKey(priv.Public())
		writeFile(t, path+".pub", string(ssh.MarshalAuthorizedKey(pub)), 0o644)
	}
	return priv
}

func pubOf(t *testing.T, priv any) ssh.PublicKey {
	t.Helper()
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s.PublicKey()
}

func expectKind(t *testing.T, err error, kind Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", kind)
	}
	if got := KindOf(err); got != kind {
		t.Fatalf("expected %s error, got %s: %v", kind, got, err)
	}
}

func TestPasswordAuth(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("s3cret")
	env := newTestEnv(t)
	cfg := env.cfg(srv)

	var wiped [][]byte
	testHookWiped = func(b []byte) { wiped = append(wiped, b) }
	defer func() { testHookWiped = nil }()

	callerPW := []byte("s3cret")
	var reqs []PasswordRequest
	cfg.Password = func(ctx context.Context, req PasswordRequest) ([]byte, error) {
		reqs = append(reqs, req)
		return callerPW, nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(reqs) != 1 || reqs[0].User != "tester" || reqs[0].Attempt != 1 {
		t.Errorf("password requests = %+v", reqs)
	}
	if string(callerPW) != "s3cret" {
		t.Error("caller's password was modified; the caller owns it")
	}
	if len(wiped) == 0 {
		t.Fatal("password copy was not wiped")
	}
	for _, b := range wiped {
		if !allZero(b) {
			t.Errorf("password copy not zeroed: %q", b)
		}
	}
}

func TestPasswordAuthFailure(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("right")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	attempts := 0
	cfg.Password = func(ctx context.Context, req PasswordRequest) ([]byte, error) {
		attempts++
		if req.Attempt != attempts {
			t.Errorf("attempt = %d, want %d", req.Attempt, attempts)
		}
		return []byte("wrong"), nil
	}
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)
	if attempts != maxPasswordAttempts {
		t.Errorf("attempts = %d", attempts)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error message contains the password: %v", err)
	}
}

func TestPasswordCanceled(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("right")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.Password = func(ctx context.Context, req PasswordRequest) ([]byte, error) {
		return nil, ErrCanceled
	}
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindCanceled)
}

func TestNoAuthMethods(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	_, err := Dial(context.Background(), env.cfg(srv))
	expectKind(t, err, KindAuth)
}

func TestMissingUser(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.User = ""
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindConfig)
}

func TestDefaultKeySearch(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	other := genKey(t, filepath.Join(env.sshDir, "id_rsa"), "", false)
	_ = other
	priv := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "", false)
	srv.authorize(pubOf(t, priv))
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) {
		t.Error("password requested although a key is usable")
		return nil, ErrCanceled
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestExplicitKeyFile(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	keyPath := filepath.Join(env.home, "keys", "mykey")
	priv := genKey(t, keyPath, "", false)
	srv.authorize(pubOf(t, priv))
	cfg := env.cfg(srv)
	cfg.KeyFile = keyPath
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	cfg.KeyFile = filepath.Join(env.home, "keys", "missing")
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindConfig)
}

func TestConfigIdentityFile(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	priv := genKey(t, filepath.Join(env.home, "cfgkeys", "id_host"), "", false)
	srv.authorize(pubOf(t, priv))
	writeFile(t, env.config, "Host myalias\n  HostName 127.0.0.1\n  Port "+srv.port+
		"\n  User cfguser\n  IdentityFile ~/cfgkeys/id_%r\n", 0o600)
	// 檔名以 %r 展開, 因此需要 id_cfguser.
	os.Rename(filepath.Join(env.home, "cfgkeys", "id_host"), filepath.Join(env.home, "cfgkeys", "id_cfguser"))
	cfg := env.cfg(srv)
	cfg.Host, cfg.Port, cfg.User = "myalias", 0, ""
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestEncryptedKeyLazyPassphrase(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	priv := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "pass phrase", false)
	srv.authorize(pubOf(t, priv))

	var wiped [][]byte
	testHookWiped = func(b []byte) { wiped = append(wiped, b) }
	defer func() { testHookWiped = nil }()

	cache := &KeyCache{}
	cfg := env.cfg(srv)
	cfg.KeyCache = cache
	var given [][]byte
	asked := 0
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		asked++
		var p []byte
		if req.Attempt == 1 {
			p = []byte("wrong")
		} else {
			p = []byte("pass phrase")
		}
		given = append(given, p)
		return p, nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 2 {
		t.Errorf("passphrase asked %d times, want 2", asked)
	}
	for _, p := range given {
		if !allZero(p) {
			t.Errorf("passphrase not zeroed: %q", p)
		}
	}
	if len(wiped) < 2 {
		t.Errorf("wipe hook called %d times", len(wiped))
	}

	// 重新連線時沿用 KeyCache 內解密後的金鑰, 不再要求金鑰密碼.
	asked = 0
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 0 {
		t.Errorf("passphrase asked %d times with cache", asked)
	}
	cache.Clear()
	if len(cache.signers) != 0 {
		t.Error("cache not cleared")
	}
}

func TestEncryptedKeyNotAcceptedIsNotPrompted(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "secret", false)
	cfg := env.cfg(srv)
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		t.Error("passphrase requested for a key the server does not accept")
		return nil, ErrCanceled
	}
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestEncryptedKeyProvidedByAgent(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	priv := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "secret", true)
	srv.authorize(pubOf(t, priv))
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	cfg := env.cfg(srv)
	cfg.Agent = func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go agent.ServeAgent(keyring, b)
		return a, nil
	}
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		t.Error("passphrase requested although ssh-agent provides the key")
		return nil, ErrCanceled
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestLegacyEncryptedKeyWithoutPublicKey(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	agentKey := genKey(t, filepath.Join(env.home, "agentkey"), "", false)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1019 測試舊式 PEM 加密金鑰.
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey),
		[]byte("legacy"), x509.PEMCipherAES256)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.sshDir, "id_rsa"), string(pem.EncodeToMemory(block)), 0o600)

	keyring := agent.NewKeyring()
	keyring.Add(agent.AddedKey{PrivateKey: agentKey})
	agentDial := func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go agent.ServeAgent(keyring, b)
		return a, nil
	}

	// ssh-agent 的金鑰被接受時, 無法事先取得公鑰的金鑰不要求金鑰密碼.
	srv.authorize(pubOf(t, agentKey))
	cfg := env.cfg(srv)
	cfg.Agent = agentDial
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		t.Error("passphrase requested although another key was accepted")
		return nil, ErrCanceled
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	// 只有該金鑰被接受時, 其他金鑰皆失敗後才要求金鑰密碼.
	srv2 := newTestServer(t)
	srv2.authorize(pubOf(t, rsaKey))
	cfg = env.cfg(srv2)
	cfg.Agent = agentDial
	asked := 0
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		asked++
		return []byte("legacy"), nil
	}
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 1 {
		t.Errorf("passphrase asked %d times", asked)
	}
}

func TestHostKeyAcceptNew(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	// 刻意不以換行結尾, 確認附加時補上換行.
	writeFile(t, env.knownHosts, knownhosts.Line([]string{"other.example.com"}, pubOf(t, otherKey)), 0o600)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }

	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	data, _ := os.ReadFile(env.knownHosts)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantPrefix := "[127.0.0.1]:" + srv.port + " ssh-ed25519 "
	if len(lines) != 2 || !strings.HasPrefix(lines[1], wantPrefix) {
		t.Fatalf("known_hosts after first connect:\n%s", data)
	}

	// 已收錄且相符.
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	data2, _ := os.ReadFile(env.knownHosts)
	if string(data2) != string(data) {
		t.Errorf("known_hosts changed on second connect:\n%s", data2)
	}

	// 已收錄但不符.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	srv.setHostKey(signer)
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindHostKey)
	var e *Error
	errors.As(err, &e)
	if !strings.Contains(e.Message, "does not match") {
		t.Errorf("message = %q", e.Message)
	}
	data3, _ := os.ReadFile(env.knownHosts)
	if string(data3) != string(data) {
		t.Errorf("known_hosts changed after mismatch:\n%s", data3)
	}
}

func TestHostKeyFileCreated(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.KnownHostsPath = filepath.Join(env.home, "new", "known_hosts")
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	st, err := os.Stat(cfg.KnownHostsPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("known_hosts not created with 0600: %v %v", st, err)
	}
}

func TestConnectTimeout(t *testing.T) {
	srv := newTestServer(t)
	srv.setNoBanner()
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.ConnectTimeout = 300 * time.Millisecond
	start := time.Now()
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindConnect)
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("timeout took %s", d)
	}
}

func TestConnectRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()
	env := newTestEnv(t)
	cfg := Config{Host: "127.0.0.1", Port: addr.Port, User: "u", HomeDir: env.home, Agent: NoAgent}
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindConnect)
}

func TestTimeoutPausedDuringPrompt(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.ConnectTimeout = 300 * time.Millisecond
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) {
		time.Sleep(800 * time.Millisecond)
		return []byte("pw"), nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestContextCanceled(t *testing.T) {
	srv := newTestServer(t)
	srv.setNoBanner()
	env := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := Dial(ctx, env.cfg(srv))
	expectKind(t, err, KindCanceled)
}

func TestProxyJump(t *testing.T) {
	jump := newTestServer(t)
	jump.setPassword("jumppw")
	target := newTestServer(t)
	env := newTestEnv(t)
	priv := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "", false)
	target.authorize(pubOf(t, priv))
	writeFile(t, env.config, "Host target\n  HostName 127.0.0.1\n  Port "+target.port+
		"\n  User tuser\n  ProxyJump juser@127.0.0.1:"+jump.port+"\n", 0o600)
	cfg := env.cfg(target)
	cfg.Host, cfg.Port, cfg.User = "target", 0, ""
	var mu sync.Mutex
	var prompts []PasswordRequest
	cfg.Password = func(ctx context.Context, req PasswordRequest) ([]byte, error) {
		mu.Lock()
		prompts = append(prompts, req)
		mu.Unlock()
		return []byte("jumppw"), nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, code, err := c.run(context.Background(), "echo hi", time.Second)
	if err != nil || code != 0 || string(out) != "hi\n" {
		t.Errorf("run through jump: %q %d %v", out, code, err)
	}
	c.Close()
	if len(prompts) != 1 || prompts[0].User != "juser" || prompts[0].Host != "127.0.0.1" {
		t.Errorf("prompts = %+v", prompts)
	}
	data, _ := os.ReadFile(env.knownHosts)
	if strings.Count(string(data), "\n") != 2 {
		t.Errorf("known_hosts should contain the jump host and the target:\n%s", data)
	}
}

func TestProxyCommand(t *testing.T) {
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc not available")
	}
	target := newTestServer(t)
	target.setPassword("pw")
	env := newTestEnv(t)
	writeFile(t, env.config, "Host viacmd\n  HostName 127.0.0.1\n  Port "+target.port+
		"\n  ProxyCommand nc %h %p\n", 0o600)
	cfg := env.cfg(target)
	cfg.Host, cfg.Port = "viacmd", 0
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := c.run(context.Background(), "echo ok", time.Second)
	if err != nil || string(out) != "ok\n" {
		t.Errorf("run through proxy command: %q %v", out, err)
	}
	c.Close()

	writeFile(t, env.config, "Host viacmd\n  ProxyCommand false\n", 0o600)
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindConnect)
}

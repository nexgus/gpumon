//go:build !windows

package sshconn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	cfg = env.cfg(srv)
	cfg.KnownHostsPath = ""
	_, err = Dial(context.Background(), cfg)
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

	// 主機表單指定的金鑰檔不存在時略過, 不視為錯誤; 沒有其他登入方式即認證失敗.
	cfg.KeyFile = filepath.Join(env.home, "keys", "missing")
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)

	// ~/.ssh/config 的 IdentityFile 不再取用.
	writeFile(t, env.config, "Host *\n  IdentityFile ~/keys/mykey\n", 0o600)
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)

	// 主機表單指定金鑰檔時不列出預設名稱的金鑰檔.
	def := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "", false)
	srv.authorize(pubOf(t, def))
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)
	cfg.KeyFile = ""
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestKeyFileOrder(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	form := genKey(t, filepath.Join(env.home, "keys", "form"), "", false)
	rsa := genKey(t, filepath.Join(env.sshDir, "id_rsa"), "", false)
	ed := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "", false)
	agentKey := genKey(t, filepath.Join(env.home, "agentkey"), "", false)
	keyring := agent.NewKeyring()
	keyring.Add(agent.AddedKey{PrivateKey: agentKey})
	// 已在 ssh-agent 中的金鑰檔不重複嘗試.
	keyring.Add(agent.AddedKey{PrivateKey: rsa})
	// ~/.ssh/config 的 IdentityFile 不列入.
	writeFile(t, env.config, "Host *\n  IdentityFile ~/keys/form\n", 0o600)
	cfg := env.cfg(srv)
	cfg.Agent = func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go agent.ServeAgent(keyring, b)
		return a, nil
	}
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	fp := func(k ed25519.PrivateKey) string { return ssh.FingerprintSHA256(pubOf(t, k)) }

	// 主機表單未指定金鑰檔時依序列出預設名稱的金鑰檔.
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	want := []string{fp(agentKey), fp(rsa), fp(ed)}
	if got := srv.offeredKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("offered keys = %v, want %v", got, want)
	}

	// 主機表單指定金鑰檔時只列該檔案.
	srv2 := newTestServer(t)
	srv2.setPassword("pw")
	cfg = env.cfg(srv2)
	cfg.Agent = func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go agent.ServeAgent(keyring, b)
		return a, nil
	}
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	cfg.KeyFile = filepath.Join(env.home, "keys", "form")
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	want = []string{fp(agentKey), fp(rsa), fp(form)}
	if got := srv2.offeredKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("offered keys = %v, want %v", got, want)
	}
}

func TestIdentitiesOnly(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	agentKey := genKey(t, filepath.Join(env.home, "agentkey"), "", false)
	fileKey := genKey(t, filepath.Join(env.home, "keys", "file"), "", false)
	srv.authorize(pubOf(t, agentKey))
	keyring := agent.NewKeyring()
	keyring.Add(agent.AddedKey{PrivateKey: agentKey})
	keyring.Add(agent.AddedKey{PrivateKey: fileKey})
	// ~/.ssh/config 的 IdentitiesOnly 不再參考, 只依主機表單.
	writeFile(t, env.config, "Host *\n  IdentitiesOnly no\n", 0o600)
	cfg := env.cfg(srv)
	cfg.KeyFile = filepath.Join(env.home, "keys", "file")
	cfg.IdentitiesOnly = true
	cfg.Agent = func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go agent.ServeAgent(keyring, b)
		return a, nil
	}
	asked := 0
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) {
		asked++
		return []byte("pw"), nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 1 {
		t.Errorf("password asked %d times; the agent key not in the key files must be excluded", asked)
	}
	if got := srv.offeredKeys(); len(got) != 1 || got[0] != ssh.FingerprintSHA256(pubOf(t, fileKey)) {
		t.Errorf("offered keys = %v", got)
	}

	// IdentitiesOnly 為假時, ssh-agent 的金鑰皆可使用, 即使 ~/.ssh/config 設為 yes.
	writeFile(t, env.config, "Host *\n  IdentitiesOnly yes\n", 0o600)
	cfg.IdentitiesOnly = false
	asked = 0
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 0 {
		t.Errorf("password asked %d times", asked)
	}
}

// TestConfigOnlyHostName 確認連線時 ~/.ssh/config 只取 HostName, Port, User 與
// IdentityFile 一律以主機表單為準.
func TestConfigOnlyHostName(t *testing.T) {
	srv := newTestServer(t)
	env := newTestEnv(t)
	priv := genKey(t, filepath.Join(env.home, "cfgkeys", "id_host"), "", false)
	srv.authorize(pubOf(t, priv))
	writeFile(t, env.config, "Host myalias\n  HostName "+srv.host+"\n  Port 1\n  User cfguser\n  IdentityFile ~/cfgkeys/id_host\n", 0o600)
	cfg := env.cfg(srv)
	cfg.Host = "myalias"
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindAuth)
	if !strings.Contains(err.Error(), "tester@myalias") {
		t.Errorf("error = %v; the user name of the form must be used", err)
	}
	cfg.KeyFile = filepath.Join(env.home, "cfgkeys", "id_host")
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.SSH().User() != "tester" {
		t.Errorf("user = %q", c.SSH().User())
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

func TestLegacyEncryptedKey(t *testing.T) {
	env := newTestEnv(t)
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
	keyPath := filepath.Join(env.sshDir, "id_rsa")
	writeFile(t, keyPath, string(pem.EncodeToMemory(block)), 0o600)

	// 沒有 .pub 檔時無法取得公鑰, 該金鑰檔略過, 不要求金鑰密碼.
	srv := newTestServer(t)
	srv.setPassword("pw")
	srv.authorize(pubOf(t, rsaKey))
	cfg := env.cfg(srv)
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		t.Error("passphrase requested for a key without a public key")
		return nil, ErrCanceled
	}
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(srv.offeredKeys()) != 0 {
		t.Errorf("offered keys = %v", srv.offeredKeys())
	}

	// 有 .pub 檔時取其公鑰, 遠端主機要求簽章時才要求金鑰密碼.
	writeFile(t, keyPath+".pub", string(ssh.MarshalAuthorizedKey(pubOf(t, rsaKey))), 0o644)
	asked := 0
	cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
		asked++
		return []byte("legacy"), nil
	}
	cfg.Password = nil
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if asked != 1 {
		t.Errorf("passphrase asked %d times", asked)
	}
}

func TestPassphraseSkipFallsBackToPassword(t *testing.T) {
	cases := []struct {
		name  string
		reply func(attempt int) ([]byte, error)
		want  int
	}{
		{"canceled", func(int) ([]byte, error) { return nil, ErrCanceled }, 1},
		{"empty", func(int) ([]byte, error) { return []byte{}, nil }, 1},
		{"three wrong", func(int) ([]byte, error) { return []byte("wrong"), nil }, maxPassphraseAttempts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.setPassword("pw")
			env := newTestEnv(t)
			first := genKey(t, filepath.Join(env.sshDir, "id_rsa"), "secret", false)
			second := genKey(t, filepath.Join(env.sshDir, "id_ed25519"), "", false)
			srv.authorize(pubOf(t, first))
			srv.authorize(pubOf(t, second))
			cfg := env.cfg(srv)
			asked := 0
			cfg.Passphrase = func(ctx context.Context, req PassphraseRequest) ([]byte, error) {
				asked++
				return tc.reply(req.Attempt)
			}
			passwords := 0
			cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) {
				passwords++
				return []byte("pw"), nil
			}
			c, err := Dial(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			c.Close()
			if asked != tc.want {
				t.Errorf("passphrase asked %d times, want %d", asked, tc.want)
			}
			// 其後的金鑰不再嘗試, 改以密碼登入.
			if passwords != 1 {
				t.Errorf("password asked %d times", passwords)
			}
			if !c.login.encryptedRequested || c.login.eligible() {
				t.Errorf("login = %+v; passwordless login must not be offered", c.login)
			}
		})
	}
}

func TestHostKeyUnknown(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	// 刻意不以換行結尾, 確認附加時補上換行.
	writeFile(t, env.ownKnownHosts, knownhosts.Line([]string{"other.example.com"}, pubOf(t, otherKey)), 0o600)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	var reqs []HostKeyRequest
	cfg.HostKey = func(ctx context.Context, req HostKeyRequest) error {
		reqs = append(reqs, req)
		return ErrCanceled
	}

	// 不同意時中止連線, 不寫入.
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindCanceled)
	wantFP := ssh.FingerprintSHA256(srv.hostSigner.PublicKey())
	if len(reqs) != 1 || reqs[0].Changed || len(reqs[0].Old) != 0 ||
		reqs[0].Key != (HostKeyFingerprint{Type: ssh.KeyAlgoED25519, Fingerprint: wantFP}) ||
		reqs[0].Host != srv.host || reqs[0].Address != srv.addr {
		t.Fatalf("requests = %+v", reqs)
	}
	before, _ := os.ReadFile(env.ownKnownHosts)

	// 自動重新連線不詢問.
	cfg.HostKey = nil
	_, err = Dial(context.Background(), cfg)
	expectKind(t, err, KindHostKey)
	if !strings.Contains(err.Error(), "needs your confirmation") || !strings.Contains(err.Error(), wantFP) {
		t.Errorf("error = %v", err)
	}

	// 同意時寫入程式自己的 known_hosts, OpenSSH 的 known_hosts 不建立.
	cfg.HostKey = func(context.Context, HostKeyRequest) error { return nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	data, _ := os.ReadFile(env.ownKnownHosts)
	if !strings.HasPrefix(string(data), string(before)+"\n") {
		t.Fatalf("existing content changed:\n%s", data)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "[127.0.0.1]:"+srv.port+" ssh-ed25519 ") {
		t.Fatalf("known_hosts after accepting:\n%s", data)
	}
	if _, err := os.Stat(env.knownHosts); !os.IsNotExist(err) {
		t.Error("the OpenSSH known_hosts was created")
	}

	// 已記錄且相同時不詢問.
	cfg.HostKey = nil
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestHostKeyFromOpenSSHFile(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	writeFile(t, env.knownHosts, knownhosts.Line([]string{srv.addr}, srv.hostSigner.PublicKey())+"\n", 0o600)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	cfg.HostKey = nil
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if data, _ := os.ReadFile(env.ownKnownHosts); len(data) != 0 {
		t.Errorf("own known_hosts written:\n%s", data)
	}
}

func TestHostKeyChanged(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	_, old1, _ := ed25519.GenerateKey(rand.Reader)
	_, old2, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	ownBefore := knownhosts.Line([]string{srv.addr}, pubOf(t, old1)) + "\n" +
		knownhosts.Line([]string{"other.example.com"}, pubOf(t, other)) + "\n"
	writeFile(t, env.ownKnownHosts, ownBefore, 0o600)
	opensshBefore := knownhosts.Line([]string{srv.addr}, pubOf(t, old2)) + "\n"
	writeFile(t, env.knownHosts, opensshBefore, 0o600)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }

	cfg.HostKey = nil
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindHostKey)
	if !strings.Contains(err.Error(), "has changed") {
		t.Errorf("error = %v", err)
	}

	var reqs []HostKeyRequest
	cfg.HostKey = func(ctx context.Context, req HostKeyRequest) error {
		reqs = append(reqs, req)
		return nil
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	wantOld := []HostKeyFingerprint{
		{ssh.KeyAlgoED25519, ssh.FingerprintSHA256(pubOf(t, old1))},
		{ssh.KeyAlgoED25519, ssh.FingerprintSHA256(pubOf(t, old2))},
	}
	if len(reqs) != 1 || !reqs[0].Changed || !reflect.DeepEqual(reqs[0].Old, wantOld) {
		t.Fatalf("requests = %+v", reqs)
	}
	want := knownhosts.Line([]string{"other.example.com"}, pubOf(t, other)) + "\n" +
		knownhosts.Line([]string{srv.addr}, srv.hostSigner.PublicKey()) + "\n"
	if data, _ := os.ReadFile(env.ownKnownHosts); string(data) != want {
		t.Errorf("own known_hosts:\n%s\nwant:\n%s", data, want)
	}
	if st, _ := os.Stat(env.ownKnownHosts); st.Mode().Perm() != 0o600 {
		t.Errorf("own known_hosts mode = %v", st.Mode())
	}
	if data, _ := os.ReadFile(env.knownHosts); string(data) != opensshBefore {
		t.Errorf("the OpenSSH known_hosts changed:\n%s", data)
	}

	// OpenSSH 的檔案仍為舊金鑰, 程式自己的檔案相同即通過, 不再詢問.
	cfg.HostKey = nil
	c, err = Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestHostKeyWriteFailureStillConnects(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	// 程式自己的 known_hosts 所在位置為一般檔案, 無法建立目錄與寫入.
	writeFile(t, filepath.Join(env.home, "blocker"), "", 0o600)
	cfg.KnownHostsPath = filepath.Join(env.home, "blocker", "known_hosts")
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestHostKeyFileCreated(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	cfg.HostKey = func(context.Context, HostKeyRequest) error {
		st, err := os.Stat(cfg.KnownHostsPath)
		if err != nil || st.Mode().Perm() != 0o600 || st.Size() != 0 {
			t.Errorf("known_hosts not created empty with 0600: %v %v", st, err)
		}
		return ErrCanceled
	}
	_, err := Dial(context.Background(), cfg)
	expectKind(t, err, KindCanceled)
	st, err := os.Stat(filepath.Dir(cfg.KnownHostsPath))
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("directory not created with 0700: %v %v", st, err)
	}
}

func TestTimeoutPausedDuringHostKeyPrompt(t *testing.T) {
	srv := newTestServer(t)
	srv.setPassword("pw")
	env := newTestEnv(t)
	cfg := env.cfg(srv)
	cfg.ConnectTimeout = 300 * time.Millisecond
	cfg.HostKey = func(context.Context, HostKeyRequest) error {
		time.Sleep(800 * time.Millisecond)
		return nil
	}
	cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
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
	cfg := Config{Host: "127.0.0.1", Port: addr.Port, User: "u", HomeDir: env.home, KnownHostsPath: env.ownKnownHosts, Agent: NoAgent}
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

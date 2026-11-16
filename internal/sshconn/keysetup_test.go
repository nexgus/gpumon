//go:build !windows

package sshconn

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// keySetupFixture 以密碼登入測試伺服器, 並記錄免密碼登入的詢問與結果.
type keySetupFixture struct {
	srv     *testServer
	env     *testEnv
	cfg     Config
	asked   int
	answer  KeySetupChoice
	results []KeySetupResult
}

func newKeySetupFixture(t *testing.T) *keySetupFixture {
	t.Helper()
	f := &keySetupFixture{srv: newTestServer(t), env: newTestEnv(t), answer: KeySetupNow}
	f.srv.setPassword("pw")
	f.cfg = f.env.cfg(f.srv)
	f.cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }
	f.cfg.KeySetup = func(ctx context.Context, req KeySetupRequest) (KeySetupChoice, error) {
		f.asked++
		if req.Host != f.srv.host || req.User != "tester" {
			t.Errorf("request = %+v", req)
		}
		return f.answer, nil
	}
	f.cfg.KeySetupDone = func(r KeySetupResult) { f.results = append(f.results, r) }
	return f
}

// run 登入並進行免密碼登入的詢問.
func (f *keySetupFixture) run(t *testing.T) error {
	t.Helper()
	c, err := Dial(context.Background(), f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.OfferKeySetup(context.Background(), time.Second)
}

func (f *keySetupFixture) authorizedKeys() string {
	data, _ := os.ReadFile(filepath.Join(f.srv.home, ".ssh", "authorized_keys"))
	return string(data)
}

func TestKeySetupGeneratesKey(t *testing.T) {
	f := newKeySetupFixture(t)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(f.env.sshDir, "id_ed25519")
	if f.asked != 1 || len(f.results) != 1 {
		t.Fatalf("asked %d, results %+v", f.asked, f.results)
	}
	r := f.results[0]
	if r.Outcome != KeySetupSucceeded || !r.Generated || r.KeyFile != keyPath || r.Err != nil {
		t.Fatalf("result = %+v", r)
	}
	st, err := os.Stat(keyPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	data, _ := os.ReadFile(keyPath)
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("generated key: %v", err)
	}
	pubLine, _ := os.ReadFile(keyPath + ".pub")
	if strings.TrimSpace(f.authorizedKeys()) != strings.TrimSpace(string(pubLine)) {
		t.Errorf("authorized_keys = %q, .pub = %q", f.authorizedKeys(), pubLine)
	}
	for path, mode := range map[string]os.FileMode{
		filepath.Join(f.srv.home, ".ssh"):                    0o700,
		filepath.Join(f.srv.home, ".ssh", "authorized_keys"): 0o600,
	} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v %v", path, st, err)
		}
	}

	// 之後以金鑰登入, 不再要求密碼, 也不再詢問.
	f.cfg.Password = func(context.Context, PasswordRequest) ([]byte, error) {
		t.Error("password requested after the key was set up")
		return nil, ErrCanceled
	}
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if f.asked != 1 {
		t.Errorf("asked %d times after key login", f.asked)
	}
}

func TestKeySetupGeneratesAtConfiguredPath(t *testing.T) {
	f := newKeySetupFixture(t)
	// ~/.ssh/config 的 IdentityFile 不再參考, 主機表單未指定金鑰檔時產生於 ~/.ssh/id_ed25519.
	writeFile(t, f.env.config, "Host *\n  IdentityFile ~/cfg/first\n  IdentityFile ~/cfg/second\n", 0o600)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.env.sshDir, "id_ed25519")
	if len(f.results) != 1 || f.results[0].KeyFile != want || f.results[0].Outcome != KeySetupSucceeded {
		t.Fatalf("results = %+v", f.results)
	}
	if st, err := os.Stat(filepath.Dir(want)); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("key directory: %v %v", st, err)
	}

	// 主機表單指定金鑰檔時產生於該處, 所在目錄不存在時以 0700 建立.
	f2 := newKeySetupFixture(t)
	writeFile(t, f2.env.config, "Host *\n  IdentityFile ~/cfg/first\n", 0o600)
	f2.cfg.KeyFile = filepath.Join(f2.env.home, "form", "key")
	if err := f2.run(t); err != nil {
		t.Fatal(err)
	}
	if len(f2.results) != 1 || f2.results[0].KeyFile != f2.cfg.KeyFile {
		t.Fatalf("results = %+v", f2.results)
	}
}

func TestKeySetupUsesExistingKey(t *testing.T) {
	f := newKeySetupFixture(t)
	// 第一個金鑰檔有密碼保護, 未獲遠端接受, 不要求金鑰密碼.
	// 預設名稱的金鑰檔依 id_rsa, id_ecdsa, id_ed25519 的順序列出, 不存在的 id_ecdsa 略過.
	genKey(t, filepath.Join(f.env.sshDir, "id_rsa"), "secret", true)
	genKey(t, filepath.Join(f.env.sshDir, "id_ed25519"), "", true)
	f.cfg.Passphrase = func(context.Context, PassphraseRequest) ([]byte, error) {
		t.Error("passphrase requested")
		return nil, ErrCanceled
	}
	// 既有內容不以換行結尾, 附加前先補換行.
	writeFile(t, filepath.Join(f.srv.home, ".ssh", "authorized_keys"), "ssh-ed25519 AAAA existing", 0o640)
	os.Chmod(filepath.Join(f.srv.home, ".ssh"), 0o755)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	keyA := filepath.Join(f.env.sshDir, "id_rsa")
	if len(f.results) != 1 || f.results[0].Generated || f.results[0].KeyFile != keyA {
		t.Fatalf("results = %+v", f.results)
	}
	pub, _ := os.ReadFile(keyA + ".pub")
	want := "ssh-ed25519 AAAA existing\n" + strings.TrimSpace(string(pub)) + "\n"
	if got := f.authorizedKeys(); got != want {
		t.Errorf("authorized_keys = %q, want %q", got, want)
	}
	// 既存者不更動權限.
	if st, _ := os.Stat(filepath.Join(f.srv.home, ".ssh", "authorized_keys")); st.Mode().Perm() != 0o640 {
		t.Errorf("authorized_keys mode = %v", st.Mode())
	}
	if st, _ := os.Stat(filepath.Join(f.srv.home, ".ssh")); st.Mode().Perm() != 0o755 {
		t.Errorf(".ssh mode = %v", st.Mode())
	}
}

func TestKeySetupGenerateFailed(t *testing.T) {
	f := newKeySetupFixture(t)
	// 主機表單的金鑰檔存在但不是金鑰, 不得覆寫.
	f.cfg.KeyFile = filepath.Join(f.env.home, "notakey")
	writeFile(t, f.cfg.KeyFile, "hello", 0o600)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if len(f.results) != 1 || f.results[0].Outcome != KeySetupGenerateFailed || f.results[0].Err == nil ||
		f.results[0].KeyFile != f.cfg.KeyFile {
		t.Fatalf("results = %+v", f.results)
	}
	if data, _ := os.ReadFile(f.cfg.KeyFile); string(data) != "hello" {
		t.Error("existing file overwritten")
	}
	if f.authorizedKeys() != "" {
		t.Error("public key placed although generation failed")
	}
}

func TestKeySetupAppendFailed(t *testing.T) {
	f := newKeySetupFixture(t)
	writeFile(t, filepath.Join(f.srv.home, ".ssh"), "not a directory", 0o600)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if len(f.results) != 1 || f.results[0].Outcome != KeySetupAppendFailed || f.results[0].Err == nil {
		t.Fatalf("results = %+v", f.results)
	}
	if !strings.Contains(f.results[0].Err.Error(), "authorized_keys") {
		t.Errorf("reason = %v", f.results[0].Err)
	}
}

func TestKeySetupLaterAndNever(t *testing.T) {
	for _, choice := range []KeySetupChoice{KeySetupLater, KeySetupNever} {
		f := newKeySetupFixture(t)
		f.answer = choice
		if err := f.run(t); err != nil {
			t.Fatal(err)
		}
		if f.asked != 1 || len(f.results) != 0 || f.authorizedKeys() != "" {
			t.Errorf("choice %d: asked %d, results %+v", choice, f.asked, f.results)
		}
		if _, err := os.Stat(filepath.Join(f.env.sshDir, "id_ed25519")); !os.IsNotExist(err) {
			t.Errorf("choice %d: key generated", choice)
		}
	}
}

func TestKeySetupNotOffered(t *testing.T) {
	t.Run("key login", func(t *testing.T) {
		f := newKeySetupFixture(t)
		priv := genKey(t, filepath.Join(f.env.sshDir, "id_ed25519"), "", false)
		f.srv.authorize(pubOf(t, priv))
		if err := f.run(t); err != nil {
			t.Fatal(err)
		}
		if f.asked != 0 {
			t.Error("asked after key login")
		}
	})
	t.Run("agent keys without key files", func(t *testing.T) {
		f := newKeySetupFixture(t)
		keyring := agent.NewKeyring()
		keyring.Add(agent.AddedKey{PrivateKey: genKey(t, filepath.Join(f.env.home, "elsewhere"), "", false)})
		f.cfg.Agent = func(context.Context) (net.Conn, error) {
			a, b := net.Pipe()
			go agent.ServeAgent(keyring, b)
			return a, nil
		}
		if err := f.run(t); err != nil {
			t.Fatal(err)
		}
		if f.asked != 0 {
			t.Error("asked although ssh-agent has keys and there is no key file")
		}
	})
	t.Run("agent keys with key files", func(t *testing.T) {
		f := newKeySetupFixture(t)
		genKey(t, filepath.Join(f.env.sshDir, "id_ed25519"), "", true)
		keyring := agent.NewKeyring()
		keyring.Add(agent.AddedKey{PrivateKey: genKey(t, filepath.Join(f.env.home, "elsewhere"), "", false)})
		f.cfg.Agent = func(context.Context) (net.Conn, error) {
			a, b := net.Pipe()
			go agent.ServeAgent(keyring, b)
			return a, nil
		}
		if err := f.run(t); err != nil {
			t.Fatal(err)
		}
		if f.asked != 1 {
			t.Errorf("asked %d times", f.asked)
		}
	})
	t.Run("encrypted key requested", func(t *testing.T) {
		f := newKeySetupFixture(t)
		priv := genKey(t, filepath.Join(f.env.sshDir, "id_ed25519"), "secret", false)
		f.srv.authorize(pubOf(t, priv))
		f.cfg.Passphrase = func(context.Context, PassphraseRequest) ([]byte, error) { return nil, ErrCanceled }
		if err := f.run(t); err != nil {
			t.Fatal(err)
		}
		if f.asked != 0 {
			t.Error("asked although the remote accepted a passphrase-protected key")
		}
	})
	t.Run("no callback", func(t *testing.T) {
		f := newKeySetupFixture(t)
		f.cfg.KeySetup = nil
		if err := f.run(t); err != nil {
			t.Fatal(err)
		}
	})
}

func TestKeySetupCanceledAndLost(t *testing.T) {
	f := newKeySetupFixture(t)
	f.cfg.KeySetup = func(context.Context, KeySetupRequest) (KeySetupChoice, error) { return 0, ErrCanceled }
	expectKind(t, f.run(t), KindCanceled)

	old := confirmKeepalive
	confirmKeepalive = 30 * time.Millisecond
	t.Cleanup(func() { confirmKeepalive = old })
	f = newKeySetupFixture(t)
	f.srv.setIgnoreGlobal()
	f.cfg.KeySetup = func(ctx context.Context, _ KeySetupRequest) (KeySetupChoice, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(3 * time.Second):
			return 0, errors.New("connection loss not detected")
		}
	}
	c, err := Dial(context.Background(), f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = c.OfferKeySetup(context.Background(), 150*time.Millisecond)
	expectKind(t, err, KindConnect)
	if !strings.Contains(err.Error(), "passwordless login") {
		t.Errorf("error = %v", err)
	}
}

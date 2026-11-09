package sshconn

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// PasswordRequest 為要求登入密碼時提供給回呼函數的資訊.
type PasswordRequest struct {
	// User 與 Host 為要登入的帳號與主機; 經由跳板主機連線時, 可能是跳板主機.
	User string
	Host string
	// Attempt 為本次連線中第幾次要求密碼, 由 1 起算; 大於 1 表示前一次的密碼
	// 遭拒.
	Attempt int
}

// PassphraseRequest 為要求金鑰密碼時提供給回呼函數的資訊.
type PassphraseRequest struct {
	// KeyFile 為金鑰檔的路徑.
	KeyFile string
	// Attempt 為同一個金鑰檔第幾次要求金鑰密碼, 由 1 起算; 大於 1 表示前一次的
	// 金鑰密碼錯誤.
	Attempt int
}

// PasswordFunc 回傳登入密碼. 回傳的位元組陣列仍歸呼叫端所有, 本套件只複製使用,
// 認證結束後即以零覆寫該副本; 何時銷毀原陣列由呼叫端決定. 回傳 ErrCanceled 或
// 其他錯誤時, 連線隨即中止.
type PasswordFunc func(ctx context.Context, req PasswordRequest) ([]byte, error)

// PassphraseFunc 回傳金鑰密碼. 回傳的位元組陣列交由本套件處置, 解密完成後立即
// 以零覆寫. 回傳空陣列表示略過該金鑰; 回傳 ErrCanceled 或其他錯誤時, 連線隨即
// 中止.
type PassphraseFunc func(ctx context.Context, req PassphraseRequest) ([]byte, error)

// maxPasswordAttempts 與 maxPassphraseAttempts 與 OpenSSH 的
// NumberOfPasswordPrompts 預設值相同.
const (
	maxPasswordAttempts   = 3
	maxPassphraseAttempts = 3
)

// KeyCache 保存解密後的金鑰, 供同一台主機自動重新連線時沿用, 不必再次要求金鑰
// 密碼. 呼叫端於主機轉為未連線狀態或程式結束時呼叫 Clear, 解除對金鑰的引用.
// 零值可直接使用, 可同時供多個 goroutine 使用.
type KeyCache struct {
	mu      sync.Mutex
	signers map[string]ssh.Signer
}

// Clear 解除對所有解密後金鑰的引用.
func (c *KeyCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.signers)
	c.signers = nil
}

func (c *KeyCache) get(key string) ssh.Signer {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.signers[key]
}

func (c *KeyCache) put(key string, s ssh.Signer) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.signers == nil {
		c.signers = make(map[string]ssh.Signer)
	}
	c.signers[key] = s
}

// zero 以零覆寫 b.
func zero(b []byte) {
	clear(b)
}

// authSession 保存一次連線 (一個主機) 認證所需的狀態. 結束後須呼叫 wipe.
type authSession struct {
	ctx        context.Context
	user, host string
	keyFiles   []string
	// explicitKey 為真時, keyFiles 由使用者明確指定, 檔案不存在即為錯誤.
	explicitKey bool
	agentSigner []ssh.Signer
	password    PasswordFunc
	passphrase  PassphraseFunc
	cache       *KeyCache
	clock       *budget

	mu sync.Mutex
	// buffers 為本套件為認證而建立的密碼副本, wipe 時以零覆寫.
	buffers [][]byte
	// pending 為無法事先取得公鑰的有密碼保護金鑰, 留待其他金鑰皆不被接受後才要求
	// 金鑰密碼.
	pending          []string
	pendingTried     bool
	passwordAttempts int
	// abortErr 為回呼函數回傳的錯誤; 設定後所有認證方式立即失敗.
	abortErr error
	// configErr 為金鑰檔設定的錯誤, 例如明確指定的金鑰檔不存在.
	configErr error
}

func (a *authSession) setAbort(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.abortErr == nil {
		a.abortErr = err
	}
}

func (a *authSession) aborted() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.abortErr
}

// wipe 以零覆寫本次認證建立的所有密碼副本.
func (a *authSession) wipe() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, b := range a.buffers {
		zero(b)
		if testHookWiped != nil {
			testHookWiped(b)
		}
	}
	a.buffers = nil
}

// testHookWiped 供測試確認密碼副本已被覆寫.
var testHookWiped func([]byte)

// methods 回傳依 OpenSSH 預設順序排列的認證方式: 公鑰 (ssh-agent 的金鑰在前,
// 金鑰檔在後), keyboard-interactive, 密碼.
func (a *authSession) methods() []ssh.AuthMethod {
	m := []ssh.AuthMethod{ssh.PublicKeysCallback(a.signers)}
	if a.password != nil {
		m = append(m,
			ssh.RetryableAuthMethod(ssh.KeyboardInteractive(a.keyboardInteractive), maxPasswordAttempts),
			ssh.RetryableAuthMethod(ssh.PasswordCallback(a.passwordString), maxPasswordAttempts),
		)
	}
	return m
}

// authCallback 於公鑰認證失敗後, 為無法事先取得公鑰的有密碼保護金鑰再進行一次
// 公鑰認證, 使這類金鑰只在其他金鑰皆不被接受時才要求金鑰密碼.
func (a *authSession) authCallback(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
	if err := a.aborted(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	run := !a.pendingTried && len(a.pending) > 0 && contains(ctx.TriedMethods, "publickey") &&
		contains(ctx.AllowedMethods, "publickey")
	if run {
		a.pendingTried = true
	}
	a.mu.Unlock()
	if !run {
		return nil, nil
	}
	return ssh.PublicKeysCallback(a.pendingSigners), nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// signers 回傳第一輪公鑰認證使用的金鑰: ssh-agent 的金鑰, 無密碼保護的金鑰,
// 以及可事先取得公鑰的有密碼保護金鑰 (簽章時才要求金鑰密碼).
func (a *authSession) signers() ([]ssh.Signer, error) {
	if err := a.aborted(); err != nil {
		return nil, err
	}
	out := append([]ssh.Signer(nil), a.agentSigner...)
	agentKeys := make(map[string]bool)
	for _, s := range a.agentSigner {
		agentKeys[string(s.PublicKey().Marshal())] = true
	}
	for _, path := range a.keyFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			if a.explicitKey {
				a.mu.Lock()
				a.configErr = newError(KindConfig, err, "Cannot read the key file %s", path)
				a.mu.Unlock()
				a.setAbort(a.configErr)
				return nil, a.configErr
			}
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err == nil {
			if !agentKeys[string(signer.PublicKey().Marshal())] {
				out = append(out, signer)
			}
			continue
		}
		var missing *ssh.PassphraseMissingError
		if !errors.As(err, &missing) {
			if a.explicitKey {
				a.mu.Lock()
				a.configErr = newError(KindConfig, err, "The key file %s is not a supported private key", path)
				a.mu.Unlock()
				a.setAbort(a.configErr)
				return nil, a.configErr
			}
			continue
		}
		pub := missing.PublicKey
		if pub == nil {
			pub = readPublicKeyFile(path + ".pub")
		}
		if pub != nil && agentKeys[string(pub.Marshal())] {
			// ssh-agent 已提供此金鑰, 不要求金鑰密碼.
			continue
		}
		if pub == nil {
			if cached := a.cache.get(cacheKey(path, nil)); cached != nil {
				out = append(out, cached)
				continue
			}
			a.mu.Lock()
			a.pending = append(a.pending, path)
			a.mu.Unlock()
			continue
		}
		if cached := a.cache.get(cacheKey(path, pub)); cached != nil {
			out = append(out, cached)
			continue
		}
		out = append(out, &lazySigner{auth: a, path: path, data: data, pub: pub})
	}
	return out, nil
}

// pendingSigners 為無法事先取得公鑰的有密碼保護金鑰要求金鑰密碼並解密.
func (a *authSession) pendingSigners() ([]ssh.Signer, error) {
	var out []ssh.Signer
	a.mu.Lock()
	pending := append([]string(nil), a.pending...)
	a.mu.Unlock()
	for _, path := range pending {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s, err := a.decrypt(path, data, nil)
		if err != nil {
			if a.aborted() != nil {
				return nil, err
			}
			continue
		}
		if s != nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// cacheKey 為 KeyCache 的鍵. 無法事先取得公鑰的金鑰以 pub 為 nil 的鍵存放.
func cacheKey(path string, pub ssh.PublicKey) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if pub == nil {
		return abs + "\x00"
	}
	return abs + "\x00" + ssh.FingerprintSHA256(pub)
}

func readPublicKeyFile(path string) ssh.PublicKey {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil
	}
	return pub
}

// errKeySkipped 表示使用者略過某個金鑰.
var errKeySkipped = errors.New("key skipped")

// decrypt 要求金鑰密碼並解密金鑰, 至多嘗試 maxPassphraseAttempts 次. 金鑰密碼
// 於每次解密後立即以零覆寫. 成功時把解密後的金鑰存入 KeyCache. 使用者略過時
// 回傳 errKeySkipped.
func (a *authSession) decrypt(path string, data []byte, want ssh.PublicKey) (ssh.Signer, error) {
	if a.passphrase == nil {
		return nil, errKeySkipped
	}
	for attempt := 1; attempt <= maxPassphraseAttempts; attempt++ {
		a.clock.pause()
		pass, err := a.passphrase(a.ctx, PassphraseRequest{KeyFile: path, Attempt: attempt})
		a.clock.resume()
		if err != nil {
			zero(pass)
			a.setAbort(err)
			return nil, err
		}
		if len(pass) == 0 {
			return nil, errKeySkipped
		}
		key, err := ssh.ParseRawPrivateKeyWithPassphrase(data, pass)
		zero(pass)
		if testHookWiped != nil {
			testHookWiped(pass)
		}
		if err != nil {
			if errors.Is(err, x509.IncorrectPasswordError) || isDecryptionFailure(err) {
				continue
			}
			return nil, err
		}
		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			return nil, err
		}
		if want != nil && !bytes.Equal(signer.PublicKey().Marshal(), want.Marshal()) {
			return nil, fmt.Errorf("key file %s does not match its public key", path)
		}
		if want == nil {
			a.cache.put(cacheKey(path, nil), signer)
		} else {
			a.cache.put(cacheKey(path, want), signer)
		}
		return signer, nil
	}
	return nil, fmt.Errorf("incorrect passphrase for %s", path)
}

// isDecryptionFailure 判斷舊式 PEM 金鑰以錯誤的金鑰密碼解密時的錯誤. 此類金鑰以
// 錯誤的金鑰密碼解密時, 可能通過填充檢查而在解析 ASN.1 結構時才失敗.
func isDecryptionFailure(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "decryption password incorrect") ||
		strings.Contains(msg, "structure error") || strings.Contains(msg, "asn1")
}

// lazySigner 代表一個有密碼保護且已知公鑰的金鑰. 伺服器表示接受此公鑰後, 簽章時
// 才要求金鑰密碼並解密, 與 OpenSSH 的行為相同.
type lazySigner struct {
	auth *authSession
	path string
	data []byte
	pub  ssh.PublicKey

	once   sync.Once
	signer ssh.Signer
	err    error
}

func (s *lazySigner) PublicKey() ssh.PublicKey { return s.pub }

func (s *lazySigner) load() (ssh.AlgorithmSigner, error) {
	s.once.Do(func() {
		s.signer, s.err = s.auth.decrypt(s.path, s.data, s.pub)
	})
	if s.err != nil {
		return nil, s.err
	}
	as, ok := s.signer.(ssh.AlgorithmSigner)
	if !ok {
		return nil, fmt.Errorf("key type %s does not support algorithm selection", s.pub.Type())
	}
	return as, nil
}

func (s *lazySigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	as, err := s.load()
	if err != nil {
		return nil, err
	}
	return as.Sign(rand, data)
}

func (s *lazySigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	as, err := s.load()
	if err != nil {
		return nil, err
	}
	return as.SignWithAlgorithm(rand, data, algorithm)
}

// Algorithms 回傳此金鑰可用的簽章演算法, 使 RSA 金鑰可使用 rsa-sha2-256 與
// rsa-sha2-512.
func (s *lazySigner) Algorithms() []string {
	if s.pub.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{s.pub.Type()}
}

// obtainPassword 向回呼函數要求密碼, 並複製為本套件所有的副本. 副本於 wipe 時以零
// 覆寫. 超過嘗試次數或已中止時回傳錯誤.
func (a *authSession) obtainPassword() (string, error) {
	if err := a.aborted(); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.passwordAttempts >= maxPasswordAttempts {
		a.mu.Unlock()
		return "", errors.New("too many password attempts")
	}
	a.passwordAttempts++
	attempt := a.passwordAttempts
	a.mu.Unlock()

	a.clock.pause()
	pw, err := a.password(a.ctx, PasswordRequest{User: a.user, Host: a.host, Attempt: attempt})
	a.clock.resume()
	if err != nil {
		a.setAbort(err)
		return "", err
	}
	buf := make([]byte, len(pw))
	copy(buf, pw)
	a.mu.Lock()
	a.buffers = append(a.buffers, buf)
	a.mu.Unlock()
	if len(buf) == 0 {
		return "", nil
	}
	// SSH 函式庫的密碼認證只接受字串. 此字串與 buf 共用記憶體而不另行複製, wipe
	// 以零覆寫 buf 即同時覆寫此字串. SSH 函式庫於送出認證封包時即完成序列化,
	// 認證結束後不再引用此字串 (讀 golang.org/x/crypto/ssh 原始碼所得, 推斷,
	// 未實測其內部副本).
	return unsafe.String(&buf[0], len(buf)), nil
}

func (a *authSession) passwordString() (string, error) {
	return a.obtainPassword()
}

// keyboardInteractive 以登入密碼回答 keyboard-interactive 認證的單一不回顯問題,
// 與 OpenSSH 以密碼回答此類提示的行為相同. 沒有問題的提示直接回覆空答案; 其他
// 形式的提示 (多個問題, 回顯的問題) 無法回答, 該認證方式失敗.
func (a *authSession) keyboardInteractive(name, instruction string, questions []string, echos []bool) ([]string, error) {
	if len(questions) == 0 {
		return nil, nil
	}
	if len(questions) != 1 || echos[0] {
		return nil, errors.New("unsupported keyboard-interactive prompt")
	}
	pw, err := a.obtainPassword()
	if err != nil {
		return nil, err
	}
	return []string{pw}, nil
}

// agentSigners 取得 ssh-agent 提供的金鑰. 無法連上 ssh-agent 時回傳 nil.
func agentSigners(ctx context.Context, dial AgentDialer) ([]ssh.Signer, io.Closer) {
	if dial == nil {
		dial = defaultAgentDialer
	}
	conn, err := dial(ctx)
	if err != nil || conn == nil {
		return nil, nil
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		conn.Close()
		return nil, nil
	}
	return signers, conn
}

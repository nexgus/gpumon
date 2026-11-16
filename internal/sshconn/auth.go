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
	// User 與 Host 為要登入的帳號與主機, Host 即 Config.Host.
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
// 以零覆寫. 回傳空陣列, ErrCanceled 或其他錯誤時略過該金鑰: 本套件回覆 SSH 函式庫
// 該金鑰無法簽章, 其後的金鑰不再嘗試, 改以密碼登入. 唯 ctx 已結束時連線隨即中止.
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

// localKey 為一個存在且能取得公鑰的金鑰檔.
type localKey struct {
	path string
	data []byte
	pub  ssh.PublicKey
	// signer 為沒有密碼保護的金鑰; 有密碼保護者為 nil, 簽章時才解密.
	signer ssh.Signer
}

// loadKeyFiles 依序讀取 paths, 回傳存在且能取得公鑰的金鑰檔. 沒有密碼保護的金鑰
// 直接解析; 有密碼保護者取金鑰檔內的公鑰, 舊式 PEM 格式沒有內含公鑰時取同名加
// .pub 的檔案. 不存在, 無法解析或無法取得公鑰的檔案略過. 公鑰相同者只取第一個.
func loadKeyFiles(paths []string) []localKey {
	var out []localKey
	seen := map[string]bool{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		k := localKey{path: path, data: data}
		signer, err := ssh.ParsePrivateKey(data)
		if err == nil {
			k.signer, k.pub = signer, signer.PublicKey()
		} else {
			var missing *ssh.PassphraseMissingError
			if !errors.As(err, &missing) {
				continue
			}
			k.pub = missing.PublicKey
			if k.pub == nil {
				k.pub = readPublicKeyFile(path + ".pub")
			}
			if k.pub == nil {
				continue
			}
		}
		blob := string(k.pub.Marshal())
		if seen[blob] {
			continue
		}
		seen[blob] = true
		out = append(out, k)
	}
	return out
}

// onlyFileKeys 回傳 agent 中公鑰與 files 之一相同的金鑰, 順序不變. 用於
// 主機表單的 IdentitiesOnly 為真的主機.
func onlyFileKeys(agent []ssh.Signer, files []localKey) []ssh.Signer {
	want := map[string]bool{}
	for _, f := range files {
		want[string(f.pub.Marshal())] = true
	}
	var out []ssh.Signer
	for _, s := range agent {
		if want[string(s.PublicKey().Marshal())] {
			out = append(out, s)
		}
	}
	return out
}

// authSession 保存一次連線認證所需的狀態. 結束後須呼叫 wipe.
type authSession struct {
	ctx         context.Context
	user, host  string
	files       []localKey
	agentSigner []ssh.Signer
	password    PasswordFunc
	passphrase  PassphraseFunc
	cache       *KeyCache
	clock       *budget

	mu sync.Mutex
	// buffers 為本套件為認證而建立的密碼副本, wipe 時以零覆寫.
	buffers          [][]byte
	passwordAttempts int
	// signRequested 為遠端主機曾要求以有密碼保護的金鑰簽章.
	signRequested bool
	// abortErr 為回呼函數回傳的錯誤; 設定後所有認證方式立即失敗.
	abortErr error
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

// passwordUsed 回報本次認證是否曾要求登入密碼.
func (a *authSession) passwordUsed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.passwordAttempts > 0
}

// encryptedRequested 回報遠端主機是否曾要求以有密碼保護的金鑰簽章.
func (a *authSession) encryptedRequested() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.signRequested
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

// methods 回傳依規格排列的認證方式: 公鑰 (ssh-agent 的金鑰在前, 金鑰檔在後),
// 密碼. 不支援 keyboard-interactive.
func (a *authSession) methods() []ssh.AuthMethod {
	m := []ssh.AuthMethod{ssh.PublicKeysCallback(a.signers)}
	if a.password != nil {
		m = append(m, ssh.RetryableAuthMethod(ssh.PasswordCallback(a.passwordString), maxPasswordAttempts))
	}
	return m
}

// signers 回傳公鑰認證使用的金鑰: ssh-agent 的金鑰, 再依序為金鑰檔. 已在
// ssh-agent 中的金鑰檔不重複列出. 有密碼保護的金鑰於遠端主機要求簽章時才要求
// 金鑰密碼; KeyCache 已有其解密後的金鑰時直接沿用.
func (a *authSession) signers() ([]ssh.Signer, error) {
	if err := a.aborted(); err != nil {
		return nil, err
	}
	out := append([]ssh.Signer(nil), a.agentSigner...)
	agentKeys := make(map[string]bool)
	for _, s := range a.agentSigner {
		agentKeys[string(s.PublicKey().Marshal())] = true
	}
	for _, k := range a.files {
		if agentKeys[string(k.pub.Marshal())] {
			continue
		}
		switch {
		case k.signer != nil:
			out = append(out, k.signer)
		case a.cache.get(cacheKey(k.path, k.pub)) != nil:
			out = append(out, a.cache.get(cacheKey(k.path, k.pub)))
		default:
			out = append(out, &lazySigner{auth: a, path: k.path, data: k.data, pub: k.pub})
		}
	}
	return out, nil
}

// cacheKey 為 KeyCache 的鍵.
func cacheKey(path string, pub ssh.PublicKey) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
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

// errKeySkipped 表示略過某個金鑰: 使用者取消, 留空送出, 或金鑰密碼連續錯誤.
var errKeySkipped = errors.New("key skipped")

// decrypt 要求金鑰密碼並解密金鑰, 至多要求 maxPassphraseAttempts 次. 金鑰密碼
// 於每次解密後立即以零覆寫. 成功時把解密後的金鑰存入 KeyCache. 使用者取消, 留空
// 送出或第 maxPassphraseAttempts 次仍錯誤時回傳 errKeySkipped; ctx 已結束時另
// 中止整個認證.
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
			if a.ctx.Err() != nil {
				a.setAbort(a.ctx.Err())
				return nil, a.ctx.Err()
			}
			return nil, errKeySkipped
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
		if !bytes.Equal(signer.PublicKey().Marshal(), want.Marshal()) {
			return nil, fmt.Errorf("key file %s does not match its public key", path)
		}
		a.cache.put(cacheKey(path, want), signer)
		return signer, nil
	}
	return nil, errKeySkipped
}

// isDecryptionFailure 判斷舊式 PEM 金鑰以錯誤的金鑰密碼解密時的錯誤. 此類金鑰以
// 錯誤的金鑰密碼解密時, 可能通過填充檢查而在解析 ASN.1 結構時才失敗.
func isDecryptionFailure(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "decryption password incorrect") ||
		strings.Contains(msg, "structure error") || strings.Contains(msg, "asn1")
}

// lazySigner 代表一個有密碼保護且已知公鑰的金鑰. 伺服器表示接受此公鑰後, 簽章時
// 才要求金鑰密碼並解密, 與 OpenSSH 的行為相同. 無法解密時簽章失敗, SSH 函式庫隨即
// 結束公鑰認證, 改以密碼登入.
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
		s.auth.mu.Lock()
		s.auth.signRequested = true
		s.auth.mu.Unlock()
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

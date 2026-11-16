// Package sshconn 實作本機 GUI 連到遠端主機所需的 SSH 層: 建立 SSH 連線
// (~/.ssh/config, 金鑰, ssh-agent, 密碼, 主機金鑰的比對與確認, 逾時), 免密碼登入
// 的設定, 以及遠端程式的部署與啟動 (規格第 4 章連線流程的步驟 1 至 4).
//
// 本套件不含畫面, 自動重新連線, 心跳與訊息解析. 需要向使用者詢問的登入密碼,
// 金鑰密碼, 主機金鑰的確認與免密碼登入的設定一律經由 Config 的回呼函數取得, 上傳
// 遠端程式之前的確認經由 DeployOptions 的回呼函數取得; 本套件不自行保留登入密碼,
// 也不寫入磁碟或日誌.
//
// 典型用法為依序呼叫 Dial, Client.Deploy 與 Client.Start; Connect 一次完成三者,
// 並於 Dial 之後依 Config 詢問是否設定免密碼登入.
package sshconn

import (
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// 逾時的預設值.
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultCommandTimeout = 10 * time.Second
	DefaultUploadTimeout  = 60 * time.Second
)

// defaultKeyNames 為主機表單與 ~/.ssh/config 皆未指定金鑰檔時依序列出的預設私鑰
// 檔名, 順序與 OpenSSH 相同 (以 `ssh -G` 查證). OpenSSH 另有的 id_ecdsa_sk,
// id_ed25519_sk 等需硬體金鑰或 golang.org/x/crypto/ssh 不支援的種類, 不在此列.
var defaultKeyNames = []string{"id_rsa", "id_ecdsa", "id_ed25519"}

// defaultNewKeyName 為本機沒有金鑰檔, 且主機表單與 ~/.ssh/config 皆未指定金鑰檔
// 時, 設定免密碼登入所產生的金鑰檔名稱.
const defaultNewKeyName = "id_ed25519"

// AgentDialer 建立至 ssh-agent 的連線.
type AgentDialer func(ctx context.Context) (net.Conn, error)

// NoAgent 為不使用 ssh-agent 的 AgentDialer.
var NoAgent AgentDialer = func(context.Context) (net.Conn, error) {
	return nil, errors.New("ssh-agent disabled")
}

// Config 為建立 SSH 連線的設定. 除 KnownHostsPath 外, 欄位的零值即採用預設值.
type Config struct {
	// Host 為主機表單的位址, 可為主機名稱, IP 位址或 ~/.ssh/config 的 Host 名稱.
	Host string
	// Port 為主機表單的連接埠; 0 表示取 ~/.ssh/config 的設定, 無設定時為 22.
	Port int
	// User 為主機表單的使用者名稱; 空字串表示取 ~/.ssh/config 的設定, 無設定時
	// 回傳 KindConfig 錯誤.
	User string
	// KeyFile 為主機表單的金鑰檔, 排在 ~/.ssh/config 的 IdentityFile 之前; 兩者
	// 皆無時依 OpenSSH 的順序列出 KeyDir 下的預設私鑰. 檔案不存在或無法取得公鑰
	// 時略過, 不視為錯誤.
	KeyFile string

	// Password 於所有金鑰皆不可用時要求登入密碼; nil 表示不使用密碼認證.
	Password PasswordFunc
	// Passphrase 於遠端主機接受有密碼保護的金鑰並要求簽章時要求金鑰密碼; nil 表示
	// 略過這類金鑰.
	Passphrase PassphraseFunc
	// HostKey 於遠端主機的主機金鑰需要使用者確認時呼叫; nil 表示不詢問, 需要確認
	// 時回傳 KindHostKey 錯誤, 供自動重新連線使用.
	HostKey HostKeyFunc
	// KeySetup 於以密碼登入成功且符合規格 6.3 的條件時, 詢問是否設定免密碼登入;
	// nil 表示不詢問. 只有 Connect 會詢問, Dial 不詢問.
	KeySetup KeySetupFunc
	// KeySetupDone 於使用者選擇立即設定並處理完畢後, 以結果呼叫; 可為 nil.
	KeySetupDone func(KeySetupResult)
	// KeyCache 保存解密後的金鑰供重新連線沿用; nil 表示不保存.
	KeyCache *KeyCache
	// Agent 建立至 ssh-agent 的連線; nil 表示使用預設位置 (macOS 為
	// SSH_AUTH_SOCK, Windows 為內建 OpenSSH 的 named pipe), NoAgent 表示不使用.
	Agent AgentDialer

	// ConnectTimeout 為建立 SSH 連線 (TCP 連線, 金鑰交換與認證) 的逾時上限, 不含
	// 等待回呼函數 (使用者輸入密碼或確認主機金鑰) 的時間.
	ConnectTimeout time.Duration

	// HomeDir 為本機的家目錄, 用於展開 ~; 預設為 os.UserHomeDir.
	HomeDir string
	// SSHConfigPath 預設為 <HomeDir>/.ssh/config.
	SSHConfigPath string
	// KnownHostsPath 為程式自己的 known_hosts, 必填. 本套件只寫入此檔, 檔案不存在
	// 時建立空檔案, 權限為 0600.
	KnownHostsPath string
	// OpenSSHKnownHostsPath 為 OpenSSH 的 known_hosts, 預設為
	// <HomeDir>/.ssh/known_hosts. 存在時讀取, 僅供比對, 永不寫入.
	OpenSSHKnownHostsPath string
	// KeyDir 為預設私鑰的目錄, 預設為 <HomeDir>/.ssh.
	KeyDir string
}

func (c Config) withDefaults() (Config, error) {
	if c.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return c, newError(KindConfig, err, "Cannot determine the local home directory")
		}
		c.HomeDir = home
	}
	if c.SSHConfigPath == "" {
		c.SSHConfigPath = filepath.Join(c.HomeDir, ".ssh", "config")
	}
	if c.OpenSSHKnownHostsPath == "" {
		c.OpenSSHKnownHostsPath = filepath.Join(c.HomeDir, ".ssh", "known_hosts")
	}
	if c.KeyDir == "" {
		c.KeyDir = filepath.Join(c.HomeDir, ".ssh")
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = DefaultConnectTimeout
	}
	return c, nil
}

// Client 為一條已認證的 SSH 連線.
type Client struct {
	ssh *ssh.Client
	// Host 為連線目標的顯示名稱, 即 Config.Host.
	Host string

	// login 為本次登入的經過, 供判斷是否詢問免密碼登入.
	login        loginInfo
	keySetup     KeySetupFunc
	keySetupDone func(KeySetupResult)

	mu     sync.Mutex
	closed bool
}

// SSH 回傳底層的 *ssh.Client.
func (c *Client) SSH() *ssh.Client { return c.ssh }

// Close 關閉連線; 經由此連線啟動的遠端程式隨之結束.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.ssh.Close()
}

// Wait 等待連線結束, 回傳結束的原因.
func (c *Client) Wait() error { return c.ssh.Wait() }

// Dial 建立至 cfg.Host 的 SSH 連線並完成認證. 失敗時回傳 *Error.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		return nil, newError(KindConfig, nil, "No host address is specified")
	}
	if cfg.KnownHostsPath == "" {
		return nil, newError(KindConfig, nil, "No known_hosts file is specified")
	}
	checker, err := newHostKeyChecker(cfg.KnownHostsPath, cfg.OpenSSHKnownHostsPath)
	if err != nil {
		return nil, err
	}

	alias := cfg.Host
	hc, err := loadHostConfig(cfg.SSHConfigPath, alias, cfg.HomeDir)
	if err != nil {
		return nil, newError(KindConfig, err, "Cannot read the SSH configuration file %s", cfg.SSHConfigPath)
	}
	hostname := alias
	if hc.HostName != "" {
		hostname = expandTokens(hc.HostName, map[byte]string{'h': alias})
	}
	port := cfg.Port
	if port == 0 {
		port = hc.Port
	}
	if port == 0 {
		port = 22
	}
	userName := cfg.User
	if userName == "" {
		userName = hc.User
	}
	if userName == "" {
		return nil, newError(KindConfig, nil, "No user name is specified for %s", alias)
	}
	tokens := map[byte]string{
		'h': hostname, 'p': strconv.Itoa(port), 'r': userName, 'n': alias,
		'd': cfg.HomeDir, 'u': localUserName(),
	}
	paths, newKeyPath := keyFilePaths(cfg, hc, tokens)
	files := loadKeyFiles(paths)

	agentKeys, agentConn := agentSigners(ctx, cfg.Agent)
	if agentConn != nil {
		defer agentConn.Close()
	}
	agentCount := len(agentKeys)
	if hc.IdentitiesOnly {
		agentKeys = onlyFileKeys(agentKeys, files)
	}

	addr := net.JoinHostPort(hostname, strconv.Itoa(port))
	clock := newBudget(cfg.ConnectTimeout)
	defer clock.stop()

	dialCtx, cancel := context.WithCancel(ctx)
	clock.onFire(cancel)
	var nd net.Dialer
	conn, err := nd.DialContext(dialCtx, "tcp", addr)
	cancel()
	if err != nil {
		return nil, classifyDialError(ctx, clock, err, alias)
	}
	clock.onFire(func() { conn.Close() })
	stopWatch := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopWatch()

	auth := &authSession{
		ctx: ctx, user: userName, host: alias, files: files, agentSigner: agentKeys,
		password: cfg.Password, passphrase: cfg.Passphrase, cache: cfg.KeyCache, clock: clock,
	}
	defer auth.wipe()

	var hostKeyErr error
	sshCfg := &ssh.ClientConfig{
		User: userName,
		Auth: auth.methods(),
		HostKeyCallback: func(h string, remote net.Addr, key ssh.PublicKey) error {
			hostKeyErr = checker.check(ctx, h, remote, key, alias, cfg.HostKey, clock)
			return hostKeyErr
		},
		HostKeyAlgorithms: checker.algorithms(addr),
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	clock.stop()
	if err != nil {
		conn.Close()
		if hostKeyErr != nil {
			var e *Error
			switch {
			case errors.As(hostKeyErr, &e):
				return nil, e
			case ctx.Err() != nil, errors.Is(hostKeyErr, ErrCanceled), errors.Is(hostKeyErr, context.Canceled):
				return nil, newError(KindCanceled, hostKeyErr, "Connecting to %s was canceled", alias)
			}
			return nil, newError(KindHostKey, hostKeyErr, "Cannot verify the host key of %s", alias)
		}
		if ctx.Err() != nil {
			return nil, newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", alias)
		}
		if abort := auth.aborted(); abort != nil {
			var e *Error
			switch {
			case errors.As(abort, &e):
				return nil, e
			case errors.Is(abort, ErrCanceled), errors.Is(abort, context.Canceled):
				return nil, newError(KindCanceled, abort, "Connecting to %s was canceled", alias)
			}
			return nil, newError(KindAuth, abort, "Authentication for %s@%s was aborted", userName, alias)
		}
		if clock.expired() {
			return nil, newError(KindConnect, err, "Connecting to %s timed out", alias)
		}
		if isAuthFailure(err) {
			return nil, newError(KindAuth, err, "Authentication failed for %s@%s", userName, alias)
		}
		return nil, newError(KindConnect, err, "Cannot connect to %s", alias)
	}
	client := &Client{
		ssh: ssh.NewClient(c, chans, reqs), Host: cfg.Host,
		keySetup: cfg.KeySetup, keySetupDone: cfg.KeySetupDone,
		login: loginInfo{
			byPassword:         auth.passwordUsed(),
			encryptedRequested: auth.encryptedRequested(),
			files:              files,
			agentKeys:          agentCount,
			newKeyPath:         newKeyPath,
		},
	}
	return client, nil
}

// keyFilePaths 回傳依規格 6.3 "金鑰檔的來源" 列出的金鑰檔路徑, 以及本機沒有金鑰檔
// 時產生金鑰檔的位置. 主機表單的金鑰檔在前, ~/.ssh/config 的 IdentityFile 依出現
// 順序在後, 重複的路徑只列一次; 兩者皆無時列出預設私鑰. IdentityFile 為 none 者
// 不列出, 但仍算作有設定, 與 OpenSSH 相同.
func keyFilePaths(cfg Config, hc hostConfig, tokens map[byte]string) (paths []string, newKey string) {
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if cfg.KeyFile != "" {
		p := resolveKeyPath(cfg.KeyFile, tokens, cfg.HomeDir)
		add(p)
		newKey = p
	}
	for _, f := range hc.IdentityFiles {
		if strings.EqualFold(f, "none") {
			continue
		}
		p := resolveKeyPath(f, tokens, cfg.HomeDir)
		add(p)
		if newKey == "" {
			newKey = p
		}
	}
	if cfg.KeyFile == "" && len(hc.IdentityFiles) == 0 {
		for _, name := range defaultKeyNames {
			add(filepath.Join(cfg.KeyDir, name))
		}
	}
	if newKey == "" {
		newKey = filepath.Join(cfg.KeyDir, defaultNewKeyName)
	}
	return paths, newKey
}

func classifyDialError(ctx context.Context, clock *budget, err error, display string) error {
	if ctx.Err() != nil {
		return newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", display)
	}
	if clock.expired() {
		return newError(KindConnect, err, "Connecting to %s timed out", display)
	}
	return newError(KindConnect, err, "Cannot connect to %s", display)
}

func resolveKeyPath(p string, tokens map[byte]string, home string) string {
	p = expandTilde(expandTokens(p, tokens), home)
	if !filepath.IsAbs(p) {
		p = filepath.Join(home, p)
	}
	return p
}

// isAuthFailure 判斷 golang.org/x/crypto/ssh 回傳的錯誤是否為認證失敗. 該函式庫
// 未提供可比對的錯誤型別, 只能比對訊息.
func isAuthFailure(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "too many authentication") ||
		strings.Contains(msg, "Too many authentication failures") ||
		strings.Contains(msg, "no supported methods remain")
}

// localUserName 回傳本機的使用者名稱, 供 %u 代碼與產生金鑰檔的註解使用.
// Windows 的 DOMAIN\user 只取 user.
func localUserName() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	name := u.Username
	if i := strings.LastIndex(name, `\`); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// budget 為可暫停的逾時計時器. 等待回呼函數 (使用者輸入) 期間暫停計時, 逾時時
// 執行以 onFire 登記的函數. 所有方法皆可於 nil 上呼叫.
type budget struct {
	mu        sync.Mutex
	remaining time.Duration
	started   time.Time
	timer     *time.Timer
	paused    bool
	stopped   bool
	fired     bool
	fns       []func()
}

func newBudget(d time.Duration) *budget {
	b := &budget{remaining: d, started: time.Now()}
	b.timer = time.AfterFunc(d, b.fire)
	return b
}

func (b *budget) fire() {
	b.mu.Lock()
	if b.stopped || b.paused {
		b.mu.Unlock()
		return
	}
	b.fired = true
	fns := append([]func(){}, b.fns...)
	b.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

func (b *budget) onFire(f func()) {
	if b == nil {
		return
	}
	b.mu.Lock()
	fired := b.fired
	if !fired {
		b.fns = append(b.fns, f)
	}
	b.mu.Unlock()
	if fired {
		f()
	}
}

func (b *budget) pause() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.paused || b.stopped || b.fired {
		return
	}
	b.timer.Stop()
	b.remaining -= time.Since(b.started)
	b.paused = true
}

func (b *budget) resume() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.paused || b.stopped {
		return
	}
	b.paused = false
	b.started = time.Now()
	if b.remaining < 0 {
		b.remaining = 0
	}
	b.timer = time.AfterFunc(b.remaining, b.fire)
}

func (b *budget) stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	b.timer.Stop()
}

func (b *budget) expired() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fired
}

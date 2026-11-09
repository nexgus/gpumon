// Package sshconn 實作本機 GUI 連到遠端主機所需的 SSH 層: 建立 SSH 連線
// (~/.ssh/config, 金鑰, ssh-agent, 密碼, host key 檢查, 逾時), 以及遠端程式的
// 部署與啟動 (規格第 4 章連線流程的步驟 2 至 4).
//
// 本套件不含畫面, 自動重新連線, 心跳與訊息解析. 需要向使用者詢問的登入密碼與
// 金鑰密碼一律經由 Config 的回呼函數取得, 上傳遠端程式之前的確認經由
// DeployOptions 的回呼函數取得; 本套件不自行保留登入密碼, 也不寫入磁碟或日誌.
//
// 典型用法為依序呼叫 Dial, Client.Deploy 與 Client.Start; Connect 一次完成三者.
package sshconn

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// maxJumpDepth 限制 ProxyJump 的巢狀層數, 避免設定互相參照時無窮遞迴.
const maxJumpDepth = 8

// defaultKeyNames 為未指定金鑰檔時依序嘗試的預設私鑰檔名, 順序與 OpenSSH 相同
// (以 `ssh -G` 查證). OpenSSH 另有的 id_ecdsa_sk, id_ed25519_sk 等需硬體金鑰或
// golang.org/x/crypto/ssh 不支援的種類, 不在此列.
var defaultKeyNames = []string{"id_rsa", "id_ecdsa", "id_ed25519"}

// AgentDialer 建立至 ssh-agent 的連線.
type AgentDialer func(ctx context.Context) (net.Conn, error)

// NoAgent 為不使用 ssh-agent 的 AgentDialer.
var NoAgent AgentDialer = func(context.Context) (net.Conn, error) {
	return nil, errors.New("ssh-agent disabled")
}

// Config 為建立 SSH 連線的設定. 欄位的零值即採用預設值.
type Config struct {
	// Host 為主機表單的位址, 可為主機名稱, IP 位址或 ~/.ssh/config 的 Host 名稱.
	Host string
	// Port 為主機表單的連接埠; 0 表示取 ~/.ssh/config 的設定, 無設定時為 22.
	Port int
	// User 為主機表單的使用者名稱; 空字串表示取 ~/.ssh/config 的設定, 無設定時
	// 回傳 KindConfig 錯誤.
	User string
	// KeyFile 為主機表單的金鑰檔; 空字串表示取 ~/.ssh/config 的 IdentityFile,
	// 無設定時依 OpenSSH 的順序嘗試 KeyDir 下的預設私鑰.
	KeyFile string

	// Password 於所有金鑰皆不可用時要求登入密碼; nil 表示不使用密碼認證.
	Password PasswordFunc
	// Passphrase 於有密碼保護的金鑰需要解密時要求金鑰密碼; nil 表示略過這類金鑰.
	Passphrase PassphraseFunc
	// KeyCache 保存解密後的金鑰供重新連線沿用; nil 表示不保存.
	KeyCache *KeyCache
	// Agent 建立至 ssh-agent 的連線; nil 表示使用預設位置 (macOS 為
	// SSH_AUTH_SOCK, Windows 為內建 OpenSSH 的 named pipe), NoAgent 表示不使用.
	Agent AgentDialer

	// ConnectTimeout 為建立一條 SSH 連線 (TCP 連線, 金鑰交換與認證) 的逾時上限,
	// 不含等待回呼函數 (使用者輸入密碼) 的時間. 經由跳板主機連線時, 每一段各自
	// 適用此上限.
	ConnectTimeout time.Duration

	// HomeDir 為本機的家目錄, 用於展開 ~; 預設為 os.UserHomeDir.
	HomeDir string
	// SSHConfigPath 預設為 <HomeDir>/.ssh/config.
	SSHConfigPath string
	// KnownHostsPath 預設為 <HomeDir>/.ssh/known_hosts.
	KnownHostsPath string
	// KeyDir 為預設私鑰的搜尋目錄, 預設為 <HomeDir>/.ssh.
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
	if c.KnownHostsPath == "" {
		c.KnownHostsPath = filepath.Join(c.HomeDir, ".ssh", "known_hosts")
	}
	if c.KeyDir == "" {
		c.KeyDir = filepath.Join(c.HomeDir, ".ssh")
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = DefaultConnectTimeout
	}
	return c, nil
}

// Client 為一條已認證的 SSH 連線. 經由跳板主機時, 一併持有各段跳板連線.
type Client struct {
	ssh *ssh.Client
	// Host 為連線目標的顯示名稱, 即 Config.Host.
	Host string

	mu      sync.Mutex
	closers []io.Closer
	closed  bool
}

// SSH 回傳底層的 *ssh.Client.
func (c *Client) SSH() *ssh.Client { return c.ssh }

// Close 關閉連線與所有跳板連線; 經由此連線啟動的遠端程式隨之結束.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var first error
	for i := len(c.closers) - 1; i >= 0; i-- {
		if err := c.closers[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Wait 等待連線結束, 回傳結束的原因.
func (c *Client) Wait() error { return c.ssh.Wait() }

func (c *Client) addCloser(cl io.Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closers = append(c.closers, cl)
}

// endpoint 為要連線的一台主機.
type endpoint struct {
	alias    string
	port     int
	user     string
	keyFile  string
	explicit bool
}

type dialer struct {
	cfg     Config
	checker *hostKeyChecker
	agent   []ssh.Signer
	client  *Client
}

// Dial 建立至 cfg.Host 的 SSH 連線並完成認證. 失敗時回傳 *Error.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		return nil, newError(KindConfig, nil, "No host address is specified")
	}
	checker, err := newHostKeyChecker(cfg.KnownHostsPath)
	if err != nil {
		return nil, newError(KindConfig, err, "Cannot read %s", cfg.KnownHostsPath)
	}
	d := &dialer{cfg: cfg, checker: checker, client: &Client{Host: cfg.Host}}
	signers, agentConn := agentSigners(ctx, cfg.Agent)
	if agentConn != nil {
		defer agentConn.Close()
	}
	d.agent = signers

	sc, err := d.dialHost(ctx, endpoint{
		alias: cfg.Host, port: cfg.Port, user: cfg.User, keyFile: cfg.KeyFile, explicit: true,
	}, 0, nil)
	if err != nil {
		d.client.Close()
		return nil, err
	}
	d.client.ssh = sc
	return d.client, nil
}

// dialHost 建立至 ep 的連線. via 不為 nil 時經由該連線轉接, 並忽略 ep 的
// ProxyJump 與 ProxyCommand 設定, 對應 OpenSSH 處理 ProxyJump 清單中第二段以後
// 的方式.
func (d *dialer) dialHost(ctx context.Context, ep endpoint, depth int, via *ssh.Client) (*ssh.Client, error) {
	hc, err := loadHostConfig(d.cfg.SSHConfigPath, ep.alias, d.cfg.HomeDir)
	if err != nil {
		return nil, newError(KindConfig, err, "Cannot read the SSH configuration file %s", d.cfg.SSHConfigPath)
	}
	hostname := ep.alias
	if hc.HostName != "" {
		hostname = expandTokens(hc.HostName, map[byte]string{'h': ep.alias})
	}
	port := ep.port
	if port == 0 {
		port = hc.Port
	}
	if port == 0 {
		port = 22
	}
	userName := ep.user
	if userName == "" {
		userName = hc.User
	}
	if userName == "" {
		if depth == 0 {
			return nil, newError(KindConfig, nil, "No user name is specified for %s", ep.alias)
		}
		userName = localUserName()
	}
	localUser := localUserName()
	tokens := map[byte]string{
		'h': hostname, 'p': strconv.Itoa(port), 'r': userName, 'n': ep.alias,
		'd': d.cfg.HomeDir, 'u': localUser,
	}
	var keyFiles []string
	explicit := false
	switch {
	case ep.keyFile != "":
		keyFiles = []string{d.resolveKeyPath(ep.keyFile, tokens)}
		explicit = ep.explicit
	case len(hc.IdentityFiles) > 0:
		for _, f := range hc.IdentityFiles {
			if strings.EqualFold(f, "none") {
				continue
			}
			keyFiles = append(keyFiles, d.resolveKeyPath(f, tokens))
		}
	default:
		for _, name := range defaultKeyNames {
			keyFiles = append(keyFiles, filepath.Join(d.cfg.KeyDir, name))
		}
	}
	addr := net.JoinHostPort(hostname, strconv.Itoa(port))
	display := ep.alias
	if depth > 0 {
		display = "jump host " + ep.alias
	}

	clock := newBudget(d.cfg.ConnectTimeout)
	defer clock.stop()

	var conn net.Conn
	var proxy *proxyCommandConn
	switch {
	case via == nil && hc.ProxyCommand != "":
		proxy, err = startProxyCommand(expandTokens(hc.ProxyCommand, tokens))
		if err != nil {
			return nil, newError(KindConnect, err, "Cannot start the ProxyCommand for %s", display)
		}
		conn = proxy
	case via == nil && hc.ProxyJump != "":
		if depth >= maxJumpDepth {
			return nil, newError(KindConfig, nil, "Too many nested ProxyJump hosts for %s", display)
		}
		var jump *ssh.Client
		for _, hop := range strings.Split(hc.ProxyJump, ",") {
			hep, err := parseJumpHost(strings.TrimSpace(hop))
			if err != nil {
				return nil, newError(KindConfig, err, "Invalid ProxyJump setting for %s", display)
			}
			jump, err = d.dialHost(ctx, hep, depth+1, jump)
			if err != nil {
				return nil, err
			}
		}
		via = jump
	}
	if conn == nil {
		dialCtx, cancel := context.WithCancel(ctx)
		clock.onFire(cancel)
		if via != nil {
			conn, err = via.DialContext(dialCtx, "tcp", addr)
		} else {
			var nd net.Dialer
			conn, err = nd.DialContext(dialCtx, "tcp", addr)
		}
		cancel()
		if err != nil {
			return nil, d.classifyDialError(ctx, clock, err, display)
		}
	}
	clock.onFire(func() { conn.Close() })
	stopWatch := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopWatch()

	auth := &authSession{
		ctx: ctx, user: userName, host: ep.alias, keyFiles: keyFiles, explicitKey: explicit,
		agentSigner: d.agent, password: d.cfg.Password, passphrase: d.cfg.Passphrase,
		cache: d.cfg.KeyCache, clock: clock,
	}
	defer auth.wipe()

	var mismatch *hostKeyMismatchError
	hostKeyCB := d.checker.callback()
	sshCfg := &ssh.ClientConfig{
		User:         userName,
		Auth:         auth.methods(),
		AuthCallback: auth.authCallback,
		HostKeyCallback: func(h string, remote net.Addr, key ssh.PublicKey) error {
			err := hostKeyCB(h, remote, key)
			errors.As(err, &mismatch)
			return err
		},
		HostKeyAlgorithms: d.checker.algorithms(addr),
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	clock.stop()
	if err != nil {
		conn.Close()
		if proxy != nil {
			if tail := proxy.stderrTail(); tail != "" {
				err = fmt.Errorf("%w (proxy command: %s)", err, tail)
			}
		}
		switch {
		case mismatch != nil:
			return nil, newError(KindHostKey, mismatch,
				"The host key of %s does not match the key recorded in %s. The host may have been reinstalled, "+
					"or the connection may be intercepted. Remove the old key from that file only if you trust the host",
				display, d.cfg.KnownHostsPath)
		case ctx.Err() != nil:
			return nil, newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", display)
		}
		if abort := auth.aborted(); abort != nil {
			var e *Error
			switch {
			case errors.As(abort, &e):
				return nil, e
			case errors.Is(abort, ErrCanceled), errors.Is(abort, context.Canceled):
				return nil, newError(KindCanceled, abort, "Connecting to %s was canceled", display)
			}
			return nil, newError(KindAuth, abort, "Authentication for %s@%s was aborted", userName, display)
		}
		if clock.expired() {
			return nil, newError(KindConnect, err, "Connecting to %s timed out", display)
		}
		if isAuthFailure(err) {
			return nil, newError(KindAuth, err, "Authentication failed for %s@%s", userName, display)
		}
		return nil, newError(KindConnect, err, "Cannot connect to %s", display)
	}
	client := ssh.NewClient(c, chans, reqs)
	d.client.addCloser(client)
	if proxy != nil {
		d.client.addCloser(proxy)
	}
	return client, nil
}

func (d *dialer) classifyDialError(ctx context.Context, clock *budget, err error, display string) error {
	if ctx.Err() != nil {
		return newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", display)
	}
	if clock.expired() {
		return newError(KindConnect, err, "Connecting to %s timed out", display)
	}
	return newError(KindConnect, err, "Cannot connect to %s", display)
}

func (d *dialer) resolveKeyPath(p string, tokens map[byte]string) string {
	p = expandTilde(expandTokens(p, tokens), d.cfg.HomeDir)
	if !filepath.IsAbs(p) {
		p = filepath.Join(d.cfg.HomeDir, p)
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

// parseJumpHost 解析 ProxyJump 清單中的一段, 格式為 [user@]host[:port] 或
// ssh://[user@]host[:port].
func parseJumpHost(s string) (endpoint, error) {
	s = strings.TrimPrefix(s, "ssh://")
	if s == "" {
		return endpoint{}, errors.New("empty jump host")
	}
	var ep endpoint
	if i := strings.LastIndex(s, "@"); i >= 0 {
		ep.user, s = s[:i], s[i+1:]
	}
	host := s
	if strings.HasPrefix(s, "[") || strings.Count(s, ":") == 1 {
		h, p, err := net.SplitHostPort(s)
		if err != nil {
			return endpoint{}, err
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return endpoint{}, fmt.Errorf("invalid port %q", p)
		}
		host, ep.port = h, n
	}
	if host == "" {
		return endpoint{}, errors.New("empty jump host")
	}
	ep.alias = host
	return ep, nil
}

// localUserName 回傳本機的使用者名稱, 供未設定使用者名稱的跳板主機使用, 與
// OpenSSH 相同. Windows 的 DOMAIN\user 只取 user.
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

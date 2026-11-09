package monitor

import (
	"context"
	"io"
	"sync"
	"time"

	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

// Target 為連線一台主機所需的設定.
type Target struct {
	// Conn 為主機表單的四個欄位.
	Conn settings.HostConn
	// IntervalMS 為以 --interval 傳給遠端程式的取樣間隔.
	IntervalMS int
	// ConnectTimeout 為建立 SSH 連線的逾時上限, 不含等待使用者輸入密碼的時間.
	ConnectTimeout time.Duration
}

// Auth 為連線過程中向使用者取得回應的回呼函數 (登入密碼, 金鑰密碼, 上傳遠端程式
// 之前的確認), 以及保存解密後金鑰的快取.
type Auth struct {
	Password      sshconn.PasswordFunc
	Passphrase    sshconn.PassphraseFunc
	KeyCache      *sshconn.KeyCache
	ConfirmUpload sshconn.UploadConfirmFunc
}

// Link 為一條已啟動遠端程式的連線, 即連線流程步驟 1 至 4 的結果.
type Link interface {
	// User 回傳實際登入的使用者名稱.
	User() string
	// Protocol 回傳步驟 3 所定的協定版本, devices 訊息的 protocol 必須等於此值.
	Protocol() int
	// Stdin 送出指令給遠端程式.
	Stdin() io.Writer
	// Stdout 與 Stderr 為遠端程式的輸出, 遠端程式結束後讀到 EOF.
	Stdout() io.Reader
	Stderr() io.Reader
	// Wait 等待 exec 通道關閉或 SSH 連線結束, 回傳結束的原因. 遠端程式未輸出
	// 任何內容即結束時, 回傳 sshconn.KindExec 的錯誤.
	Wait() error
	// Close 關閉 exec 通道與 SSH 連線, 遠端程式隨之結束.
	Close() error
}

// Dialer 建立至遠端主機的連線並啟動遠端程式. 失敗時回傳 *sshconn.Error.
type Dialer interface {
	Dial(ctx context.Context, t Target, auth Auth) (Link, error)
}

// SSHDialer 以 internal/sshconn 實作 Dialer. 欄位的零值即採用 sshconn 的預設值.
type SSHDialer struct {
	// KnownHostsPath, SSHConfigPath 與 HomeDir 對應 sshconn.Config 的同名欄位.
	KnownHostsPath string
	SSHConfigPath  string
	HomeDir        string
	// Agent 對應 sshconn.Config.Agent.
	Agent sshconn.AgentDialer
	// Deploy 為部署遠端程式的設定.
	Deploy sshconn.DeployOptions
}

// Dial 依序進行 SSH 連線, 部署與啟動遠端程式.
func (d SSHDialer) Dial(ctx context.Context, t Target, auth Auth) (Link, error) {
	cfg := sshconn.Config{
		Host:           t.Conn.Addr,
		Port:           t.Conn.Port,
		User:           t.Conn.User,
		KeyFile:        t.Conn.KeyFile,
		Password:       auth.Password,
		Passphrase:     auth.Passphrase,
		KeyCache:       auth.KeyCache,
		Agent:          d.Agent,
		ConnectTimeout: t.ConnectTimeout,
		HomeDir:        d.HomeDir,
		SSHConfigPath:  d.SSHConfigPath,
		KnownHostsPath: d.KnownHostsPath,
	}
	opts := d.Deploy
	if auth.ConfirmUpload != nil {
		opts.ConfirmUpload = auth.ConfirmUpload
	}
	c, _, p, err := sshconn.Connect(ctx, cfg, opts, t.IntervalMS)
	if err != nil {
		return nil, err
	}
	return newSSHLink(c, p), nil
}

type sshLink struct {
	c *sshconn.Client
	p *sshconn.Process

	once sync.Once
	done chan struct{}
	err  error
}

func newSSHLink(c *sshconn.Client, p *sshconn.Process) *sshLink {
	l := &sshLink{c: c, p: p, done: make(chan struct{})}
	// 遠端程式結束與 SSH 連線結束兩者以先發生者為準.
	go func() { l.finish(p.Wait()) }()
	go func() {
		err := c.Wait()
		// SSH 連線結束時 exec 通道必隨之關閉; 稍候讓遠端程式的結束原因優先回報.
		time.Sleep(200 * time.Millisecond)
		l.finish(err)
	}()
	return l
}

func (l *sshLink) finish(err error) {
	l.once.Do(func() {
		l.err = err
		close(l.done)
	})
}

func (l *sshLink) User() string      { return l.c.SSH().User() }
func (l *sshLink) Protocol() int     { return l.p.Protocol }
func (l *sshLink) Stdin() io.Writer  { return l.p.Stdin }
func (l *sshLink) Stdout() io.Reader { return l.p.Stdout }
func (l *sshLink) Stderr() io.Reader { return l.p.Stderr }

func (l *sshLink) Wait() error {
	<-l.done
	return l.err
}

func (l *sshLink) Close() error {
	l.p.Close()
	return l.c.Close()
}

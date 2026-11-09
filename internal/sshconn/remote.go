package sshconn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"gpumon/internal/protocol"
	"gpumon/internal/remotebin"
	"gpumon/internal/version"
)

// RemoteDir 與 RemoteName 為遠端程式於遠端家目錄下的位置, 不可變更.
const (
	RemoteDir     = ".gpumon"
	RemoteName    = "gpumon"
	remoteTmpName = "gpumon.tmp"
)

// remoteDir 為部署實際使用的目錄名稱, 恆為 RemoteDir. 只有實機測試改為其他名稱,
// 以免動到測試主機上既有的遠端程式.
var remoteDir = RemoteDir

// confirmKeepalive 為等待上傳確認期間送出 SSH keepalive 的間隔, 與心跳相同.
// 測試可縮短.
var confirmKeepalive = 5 * time.Second

// requiredPlatform 為 `uname -sm` 須得到的輸出.
const requiredPlatform = "Linux x86_64"

// maxCommandOutput 限制遠端指令輸出的讀取量, 避免異常輸出耗盡記憶體.
const maxCommandOutput = 16 << 20

// DeployOptions 為部署遠端程式的設定. 欄位的零值即採用預設值.
type DeployOptions struct {
	// Binary 為 GUI 內嵌的遠端程式內容, 預設為 remotebin.Binary.
	Binary []byte
	// GUIVersion 為 GUI 的程式版號 x.y.z[-stage.N], 預設為 version.VERSION.
	GUIVersion string
	// GUIProtocol 為 GUI 所實作的最新協定版本, 預設為 protocol.Version.
	GUIProtocol int
	// CommandTimeout 為每一個遠端指令 (含啟動遠端程式) 的逾時上限.
	CommandTimeout time.Duration
	// UploadTimeout 為 SFTP 上傳 (建立目錄, 寫入, 設定權限, 更名) 的逾時上限.
	UploadTimeout time.Duration
	// ConfirmUpload 於需要上傳遠端程式時, 在上傳的第一個步驟之前呼叫, 取得使用者
	// 的確認; nil 表示不需確認, 直接上傳.
	ConfirmUpload UploadConfirmFunc
}

// UploadRequest 為上傳遠端程式之前提供給確認回呼函數的資訊.
type UploadRequest struct {
	// Host 為連線目標的顯示名稱, 即 Config.Host; User 為實際登入的使用者名稱.
	Host string
	User string
	// Path 為遠端程式的完整路徑 <家目錄>/.gpumon/gpumon.
	Path string
	// Update 為真表示更新, 即遠端的程式版號低於 GUI 且未在執行; 為假表示首次
	// 上傳, 即無法取得遠端的程式版號.
	Update bool
	// RemoteVersion 為遠端現有的程式版號, 首次上傳時為空字串.
	RemoteVersion string
	// NewVersion 為即將上傳的程式版號, 即 GUI 的程式版號.
	NewVersion string
}

// UploadConfirmFunc 等待使用者確認上傳, 回傳 nil 表示確認. 回傳錯誤時不上傳, 部署
// 隨即中止. ctx 於呼叫端的 context 結束或 SSH 連線中斷時結束, 回呼函數應隨之
// 返回. 等待回呼函數的時間不計入任何逾時.
type UploadConfirmFunc func(ctx context.Context, req UploadRequest) error

func (o DeployOptions) withDefaults() DeployOptions {
	if o.Binary == nil {
		o.Binary = remotebin.Binary
	}
	if o.GUIVersion == "" {
		o.GUIVersion = version.VERSION
	}
	if o.GUIProtocol == 0 {
		o.GUIProtocol = protocol.Version
	}
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = DefaultCommandTimeout
	}
	if o.UploadTimeout <= 0 {
		o.UploadTimeout = DefaultUploadTimeout
	}
	return o
}

// Deployment 為部署的結果, 即連線流程步驟 3 所定的內容.
type Deployment struct {
	// Home 為遠端帳號家目錄的絕對路徑.
	Home string
	// Path 為遠端程式的完整路徑 <Home>/.gpumon/gpumon.
	Path string
	// Remote 為上傳前自遠端程式 --version 取得的資訊; 無法取得時為 nil.
	Remote *RemoteInfo
	// Uploaded 表示本次是否上傳了遠端程式.
	Uploaded bool
	// Protocol 為與遠端程式溝通所用的協定版本, 即步驟 5 的 devices 訊息的
	// protocol 必須等於的值.
	Protocol int

	commandTimeout time.Duration
}

// Deploy 進行連線流程的步驟 2 與 3: 檢查平台, 取得遠端的程式版號與協定版本,
// 依規格的對照表決定是否上傳並上傳, 再比對協定版本. 失敗時回傳 *Error, 呼叫端
// 應關閉連線.
func (c *Client) Deploy(ctx context.Context, opts DeployOptions) (*Deployment, error) {
	opts = opts.withDefaults()
	guiVer, err := ParseProgramVersion(opts.GUIVersion)
	if err != nil {
		return nil, newError(KindConfig, err, "Invalid GUI program version")
	}

	out, code, err := c.run(ctx, shCommand("uname -sm"), opts.CommandTimeout)
	if err != nil {
		return nil, err
	}
	platform := strings.TrimSuffix(string(out), "\n")
	if code != 0 {
		return nil, newError(KindPlatform, fmt.Errorf("uname exited with status %d", code),
			"Cannot determine the platform of %s", c.Host)
	}
	if platform != requiredPlatform {
		return nil, newError(KindPlatform, nil,
			"%s is %q. GPU Monitor supports only Linux x86_64 hosts", c.Host, platform)
	}

	out, code, err = c.run(ctx, shCommand(`printf %s "$HOME"`), opts.CommandTimeout)
	if err != nil {
		return nil, err
	}
	home := string(out)
	if code != 0 || !strings.HasPrefix(home, "/") || strings.ContainsAny(home, "\n\x00") {
		return nil, newError(KindPlatform, nil, "Cannot determine the home directory on %s", c.Host)
	}
	dep := &Deployment{
		Home:           home,
		Path:           path.Join(home, remoteDir, RemoteName),
		commandTimeout: opts.CommandTimeout,
	}

	out, code, err = c.run(ctx, shCommand(shellQuote(dep.Path)+" --version"), opts.CommandTimeout)
	if err != nil {
		return nil, err
	}
	var remoteVer *ProgramVersion
	if code == 0 {
		if info, perr := ParseVersionOutput(string(out)); perr == nil {
			dep.Remote = &info
			remoteVer = &info.Version
		}
	}

	action := decideUpload(remoteVer, guiVer)
	if action == actionCheckRunning {
		running, err := c.programRunning(ctx, dep.Path, opts.CommandTimeout)
		if err != nil {
			return nil, err
		}
		action = decideAfterRunningCheck(running)
	}
	if action == actionUpload {
		if opts.ConfirmUpload != nil {
			req := UploadRequest{
				Host: c.Host, User: c.ssh.User(), Path: dep.Path,
				Update: dep.Remote != nil, NewVersion: guiVer.String(),
			}
			if dep.Remote != nil {
				req.RemoteVersion = dep.Remote.Version.String()
			}
			if err := c.confirmUpload(ctx, opts.ConfirmUpload, req, opts.CommandTimeout); err != nil {
				return nil, err
			}
		}
		if err := c.upload(ctx, home, opts.Binary, opts.UploadTimeout); err != nil {
			return nil, err
		}
		dep.Uploaded = true
		dep.Protocol = opts.GUIProtocol
	} else {
		dep.Protocol = dep.Remote.Protocol
	}

	if compareProtocol(dep.Protocol, opts.GUIProtocol) == protocolNewer {
		return nil, newError(KindProtocolTooNew, nil,
			"The GPU Monitor program on %s (version %s, protocol %d) is newer than this GPU Monitor "+
				"(protocol %d). Please update GPU Monitor on this computer",
			c.Host, dep.Remote.Version, dep.Protocol, opts.GUIProtocol)
	}
	return dep, nil
}

// connectionLost 為等待上傳確認期間 SSH 連線中斷的原因.
type connectionLost struct{ err error }

func (e *connectionLost) Error() string { return "connection lost: " + e.err.Error() }

// confirmUpload 呼叫確認回呼函數並等待其返回. 等待期間以 keepalive 監看 SSH 連線,
// 連線中斷 (含無回應超過 replyTimeout) 時結束回呼函數的 ctx.
func (c *Client) confirmUpload(ctx context.Context, fn UploadConfirmFunc, req UploadRequest, replyTimeout time.Duration) error {
	cctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		err := c.ssh.Wait()
		if err == nil {
			err = errors.New("the SSH connection was closed")
		}
		cancel(&connectionLost{err})
	}()
	go c.keepalive(cctx, cancel, confirmKeepalive, replyTimeout)

	err := fn(cctx, req)
	var lost *connectionLost
	switch {
	case ctx.Err() != nil:
		return newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", c.Host)
	case errors.As(context.Cause(cctx), &lost):
		return newError(KindConnect, lost.err,
			"Lost the connection to %s while waiting for the confirmation to upload the remote program", c.Host)
	case err == nil:
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return newError(KindCanceled, err, "Uploading the remote program to %s was canceled", c.Host)
}

// keepalive 每隔 interval 送出一次 keepalive@openssh.com 請求, 直到 ctx 結束.
// 送出失敗或 replyTimeout 內未收到回覆時以 connectionLost 呼叫 lost. 遠端不認得
// 此請求時回覆失敗, 亦視為有回應.
func (c *Client) keepalive(ctx context.Context, lost context.CancelCauseFunc, interval, replyTimeout time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		reply := make(chan error, 1)
		go func() {
			_, _, err := c.ssh.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		timer := time.NewTimer(replyTimeout)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case err := <-reply:
			timer.Stop()
			if err != nil {
				lost(&connectionLost{err})
				return
			}
		case <-timer.C:
			lost(&connectionLost{fmt.Errorf("no response for %v", replyTimeout)})
			return
		}
	}
}

// programRunning 以 `ls -l /proc/[0-9]*/exe` 判斷遠端程式是否正在執行. 此指令
// 讀不到其他帳號的程序時結束碼不為 0, 屬正常情形, 故不檢查結束碼.
func (c *Client) programRunning(ctx context.Context, fullPath string, timeout time.Duration) (bool, error) {
	out, _, err := c.run(ctx, shCommand("ls -l /proc/[0-9]*/exe 2>/dev/null"), timeout)
	if err != nil {
		return false, err
	}
	return isProgramRunning(string(out), fullPath), nil
}

// upload 依規格的四個步驟以 SFTP 上傳遠端程式.
func (c *Client) upload(ctx context.Context, home string, bin []byte, timeout time.Duration) error {
	dir := path.Join(home, remoteDir)
	tmp := path.Join(dir, remoteTmpName)
	final := path.Join(dir, RemoteName)

	var mu sync.Mutex
	var sc *sftp.Client
	closed := false
	abort := func() {
		mu.Lock()
		defer mu.Unlock()
		closed = true
		if sc != nil {
			sc.Close()
		}
	}
	done := make(chan error, 1)
	go func() {
		client, err := sftp.NewClient(c.ssh, sftp.UseConcurrentWrites(true))
		if err != nil {
			done <- uploadError(c.Host, "start SFTP", err)
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			client.Close()
			done <- errors.New("aborted")
			return
		}
		sc = client
		mu.Unlock()
		defer client.Close()
		done <- uploadSteps(client, c.Host, dir, tmp, final, bin)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		abort()
		return newError(KindConnect, nil, "%s did not respond in time while uploading the remote program", c.Host)
	case <-ctx.Done():
		abort()
		return newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", c.Host)
	}
}

func uploadSteps(sc *sftp.Client, host, dir, tmp, final string, bin []byte) error {
	st, err := sc.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := sc.Mkdir(dir); err != nil {
			return uploadError(host, "create "+dir, err)
		}
		if err := sc.Chmod(dir, 0o700); err != nil {
			return uploadError(host, "set permissions of "+dir, err)
		}
	case err != nil:
		return uploadError(host, "check "+dir, err)
	case !st.IsDir():
		return uploadError(host, "use "+dir, errors.New("not a directory"))
	}

	f, err := sc.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return uploadError(host, "create "+tmp, err)
	}
	if _, err := f.ReadFrom(bytes.NewReader(bin)); err != nil {
		f.Close()
		return uploadError(host, "write "+tmp, err)
	}
	if err := f.Close(); err != nil {
		return uploadError(host, "write "+tmp, err)
	}
	if err := sc.Chmod(tmp, 0o755); err != nil {
		return uploadError(host, "set permissions of "+tmp, err)
	}
	if err := sc.PosixRename(tmp, final); err != nil {
		return uploadError(host, "rename "+tmp+" to "+final, err)
	}
	return nil
}

func uploadError(host, step string, err error) error {
	return newError(KindUpload, err, "Cannot upload the remote program to %s (failed to %s)", host, step)
}

// Process 為以 exec 通道執行中的遠端程式. 呼叫端須持續讀取 Stdout 與 Stderr
// 直到 EOF, 否則通道的流量視窗填滿後遠端程式會停住.
type Process struct {
	// Stdin 送出指令 (JSON Lines); 關閉即對遠端程式送出 EOF.
	Stdin io.WriteCloser
	// Stdout 為遠端程式輸出的訊息 (JSON Lines), 遠端程式結束後讀到 EOF.
	Stdout io.Reader
	// Stderr 為遠端程式的錯誤日誌, 遠端程式結束後讀到 EOF.
	Stderr io.Reader
	// Protocol 為與遠端程式溝通所用的協定版本, 同 Deployment.Protocol.
	Protocol int

	host    string
	path    string
	session *ssh.Session
	stdoutR *io.PipeReader
	stderrR *io.PipeReader
	written *countingWriter
	done    chan struct{}
	waitErr error
}

// countingWriter 記錄寫入的位元組數.
type countingWriter struct {
	mu sync.Mutex
	w  io.Writer
	n  int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.mu.Lock()
	c.n += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *countingWriter) count() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Start 進行連線流程的步驟 4: 開 exec 通道, 以完整路徑執行遠端程式的 --loop
// 模式, 並以 --interval 傳入取樣間隔 (毫秒). 遠端程式能否執行要到其結束時才
// 得知, 見 Process.Wait.
func (c *Client) Start(ctx context.Context, dep *Deployment, intervalMs int) (*Process, error) {
	if !protocol.ValidInterval(intervalMs) {
		return nil, newError(KindConfig, nil, "Invalid sampling interval %d ms", intervalMs)
	}
	timeout := dep.commandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	cmd := shCommand("exec " + shellQuote(dep.Path) + " --loop --interval " + strconv.Itoa(intervalMs))

	done := make(chan startResult, 1)
	go func() {
		sess, err := c.ssh.NewSession()
		if err != nil {
			done <- startResult{err: err}
			return
		}
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()
		p := &Process{
			Protocol: dep.Protocol, host: c.Host, path: dep.Path, session: sess,
			Stdout: stdoutR, Stderr: stderrR, stdoutR: stdoutR, stderrR: stderrR,
			written: &countingWriter{w: stdoutW}, done: make(chan struct{}),
		}
		sess.Stdout = p.written
		sess.Stderr = stderrW
		if p.Stdin, err = sess.StdinPipe(); err == nil {
			err = sess.Start(cmd)
		}
		if err != nil {
			sess.Close()
			stdoutW.Close()
			stderrW.Close()
			done <- startResult{err: newError(KindExec, err, "Cannot start the remote program on %s", c.Host)}
			return
		}
		go func() {
			p.waitErr = sess.Wait()
			stdoutW.Close()
			stderrW.Close()
			close(p.done)
		}()
		done <- startResult{p: p}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			var e *Error
			if errors.As(r.err, &e) {
				return nil, e
			}
			return nil, newError(KindConnect, r.err, "Lost the connection to %s", c.Host)
		}
		return r.p, nil
	case <-timer.C:
		go discardLateStart(done)
		return nil, newError(KindConnect, nil, "%s did not respond in time while starting the remote program", c.Host)
	case <-ctx.Done():
		go discardLateStart(done)
		return nil, newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", c.Host)
	}
}

type startResult struct {
	p   *Process
	err error
}

// discardLateStart 關閉逾時或取消之後才啟動的遠端程式.
func discardLateStart(ch <-chan startResult) {
	if r := <-ch; r.p != nil {
		r.p.Close()
	}
}

// Wait 等待遠端程式結束. 遠端程式未於 stdout 輸出任何內容即以非零碼結束時
// (例如檔案不存在, 家目錄掛載為 noexec, 缺少 glibc), 回傳 KindExec 的 *Error;
// 其餘情形回傳 golang.org/x/crypto/ssh 的錯誤, 正常結束時為 nil.
func (p *Process) Wait() error {
	<-p.done
	err := p.waitErr
	var exit *ssh.ExitError
	if errors.As(err, &exit) && p.written.count() == 0 {
		return newError(KindExec, err, "The remote program %s on %s could not be executed", p.path, p.host)
	}
	return err
}

// Close 關閉 exec 通道, 遠端程式隨之結束. 尚未讀取的 Stdout 與 Stderr 內容捨棄.
func (p *Process) Close() error {
	err := p.session.Close()
	p.stdoutR.CloseWithError(io.ErrClosedPipe)
	p.stderrR.CloseWithError(io.ErrClosedPipe)
	return err
}

// run 以 exec 通道執行 cmd, 回傳 stdout 與結束碼. 連線錯誤, 逾時與取消以 *Error
// 回傳; 指令以非零碼結束不視為錯誤. 遠端未回報結束碼時結束碼為 -1.
func (c *Client) run(ctx context.Context, cmd string, timeout time.Duration) ([]byte, int, error) {
	type result struct {
		out  []byte
		code int
		err  error
	}
	var mu sync.Mutex
	var sess *ssh.Session
	abandoned := false
	done := make(chan result, 1)
	go func() {
		s, err := c.ssh.NewSession()
		if err != nil {
			done <- result{err: err}
			return
		}
		mu.Lock()
		if abandoned {
			mu.Unlock()
			s.Close()
			return
		}
		sess = s
		mu.Unlock()
		defer s.Close()
		out := &limitedBuffer{max: maxCommandOutput}
		s.Stdout = out
		err = s.Run(cmd)
		var exit *ssh.ExitError
		var missing *ssh.ExitMissingError
		switch {
		case err == nil:
			done <- result{out: out.Bytes()}
		case errors.As(err, &exit):
			done <- result{out: out.Bytes(), code: exit.ExitStatus()}
		case errors.As(err, &missing):
			done <- result{out: out.Bytes(), code: -1}
		default:
			done <- result{err: err}
		}
	}()
	abandon := func() {
		mu.Lock()
		defer mu.Unlock()
		abandoned = true
		if sess != nil {
			go sess.Close()
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, 0, newError(KindConnect, r.err, "Lost the connection to %s", c.Host)
		}
		return r.out, r.code, nil
	case <-timer.C:
		abandon()
		return nil, 0, newError(KindConnect, nil, "%s did not respond in time", c.Host)
	case <-ctx.Done():
		abandon()
		return nil, 0, newError(KindCanceled, ctx.Err(), "Connecting to %s was canceled", c.Host)
	}
}

// limitedBuffer 只保留前 max 個位元組, 超出的部分捨棄.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - b.Len(); room < len(p) {
		if room < 0 {
			room = 0
		}
		p = p[:room]
	}
	b.Buffer.Write(p)
	return n, nil
}

// shCommand 把 inner 包成 `sh -c '<inner>'`, 使遠端指令不受登入 shell 的種類
// 影響.
func shCommand(inner string) string {
	return "sh -c " + shellQuote(inner)
}

// shellQuote 以單引號引用 s, 供 POSIX shell 解析. 只含安全字元時原樣回傳.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("/._-+,:@%=", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Connect 依序進行 Dial, Deploy 與 Start, 即連線流程的步驟 1 至 4. 任一步驟
// 失敗時關閉連線並回傳 *Error.
func Connect(ctx context.Context, cfg Config, opts DeployOptions, intervalMs int) (*Client, *Deployment, *Process, error) {
	c, err := Dial(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	dep, err := c.Deploy(ctx, opts)
	if err != nil {
		c.Close()
		return nil, nil, nil, err
	}
	p, err := c.Start(ctx, dep, intervalMs)
	if err != nil {
		c.Close()
		return nil, nil, nil, err
	}
	return c, dep, p, nil
}

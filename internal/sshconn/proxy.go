package sshconn

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// proxyCommandConn 以 ProxyCommand 指令的 stdin 與 stdout 作為連線. 關閉時終止
// 該指令. ProxyCommand 的支援為推斷, 只於 macOS 實測, Windows 未實測.
type proxyCommandConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer

	closeOnce sync.Once
}

// startProxyCommand 以本機的 shell 執行 command. macOS 與 Linux 以 $SHELL (未設定
// 時為 /bin/sh) 的 -c 執行, 並加上 exec 使該 shell 由指令取代, 與 OpenSSH 相同;
// Windows 以 cmd.exe /c 執行.
func startProxyCommand(command string) (*proxyCommandConn, error) {
	cmd := proxyShellCommand(command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := &tailBuffer{max: 1024}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &proxyCommandConn{cmd: cmd, stdin: stdin, stdout: stdout, stderr: tail}, nil
}

func (c *proxyCommandConn) Read(b []byte) (int, error)  { return c.stdout.Read(b) }
func (c *proxyCommandConn) Write(b []byte) (int, error) { return c.stdin.Write(b) }

func (c *proxyCommandConn) Close() error {
	c.closeOnce.Do(func() {
		c.stdin.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		go c.cmd.Wait()
	})
	return nil
}

// stderrTail 回傳 ProxyCommand 指令最後輸出至 stderr 的文字, 供錯誤說明使用.
func (c *proxyCommandConn) stderrTail() string { return c.stderr.String() }

// LocalAddr 與 RemoteAddr 回傳零值位址. known_hosts 的檢查會解析遠端位址, 零值
// 位址不對應任何收錄的主機, 檢查只依主機名稱進行.
func (c *proxyCommandConn) LocalAddr() net.Addr                { return &net.TCPAddr{IP: net.IPv4zero} }
func (c *proxyCommandConn) RemoteAddr() net.Addr               { return &net.TCPAddr{IP: net.IPv4zero} }
func (c *proxyCommandConn) SetDeadline(t time.Time) error      { return nil }
func (c *proxyCommandConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *proxyCommandConn) SetWriteDeadline(t time.Time) error { return nil }

// tailBuffer 只保留最後 max 個位元組.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}

func shellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

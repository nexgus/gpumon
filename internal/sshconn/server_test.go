//go:build !windows

package sshconn

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// testServer 為測試用的本機 SSH 伺服器. 支援密碼與公鑰認證, exec (交由本機的
// /bin/sh 執行, 或由 execHook 攔截), sftp 子系統與 direct-tcpip 轉接.
type testServer struct {
	t          *testing.T
	ln         net.Listener
	addr       string
	host, port string
	hostSigner ssh.Signer
	home       string

	mu         sync.Mutex
	password   string
	authorized map[string]bool
	// execHook 攔截 exec 指令; handled 為假時交由本機的 /bin/sh 執行.
	execHook func(cmd string) (out string, code int, handled bool)
	execLog  []string
	conns    int
	// noBanner 為真時接受 TCP 連線但不回應, 用於測試逾時.
	noBanner bool
	// ignoreGlobal 為真時不回覆連線層級的請求 (例如 keepalive), 模擬無回應的遠端.
	ignoreGlobal bool
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	s := &testServer{
		t: t, ln: ln, addr: ln.Addr().String(), host: host, port: port,
		hostSigner: signer, home: t.TempDir(), authorized: map[string]bool{},
	}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *testServer) portNum() int {
	n, _ := strconv.Atoi(s.port)
	return n
}

func (s *testServer) setHostKey(signer ssh.Signer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hostSigner = signer
}

func (s *testServer) setPassword(pw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = pw
}

func (s *testServer) setIgnoreGlobal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ignoreGlobal = true
}

func (s *testServer) setNoBanner() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noBanner = true
}

func (s *testServer) authorize(pub ssh.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorized[string(pub.Marshal())] = true
}

func (s *testServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.execLog...)
}

func (s *testServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		noBanner := s.noBanner
		s.mu.Unlock()
		if noBanner {
			go func() { io.Copy(io.Discard, c) }()
			continue
		}
		go s.handle(c)
	}
}

func (s *testServer) handle(c net.Conn) {
	s.mu.Lock()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.password != "" && string(pw) == s.password {
				return nil, nil
			}
			return nil, errors.New("wrong password")
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.authorized[string(key.Marshal())] {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(s.hostSigner)
	s.mu.Unlock()
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer conn.Close()
	go func() {
		for req := range reqs {
			s.mu.Lock()
			ignore := s.ignoreGlobal
			s.mu.Unlock()
			if req.WantReply && !ignore {
				req.Reply(false, nil)
			}
		}
	}()
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go s.session(ch, creqs)
		case "direct-tcpip":
			var p struct {
				Host     string
				Port     uint32
				OrigHost string
				OrigPort uint32
			}
			if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
				nc.Reject(ssh.ConnectionFailed, "bad payload")
				continue
			}
			target, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
			if err != nil {
				nc.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, creqs, err := nc.Accept()
			if err != nil {
				target.Close()
				continue
			}
			go ssh.DiscardRequests(creqs)
			go func() {
				io.Copy(ch, target)
				ch.CloseWrite()
			}()
			go func() {
				io.Copy(target, ch)
				target.Close()
			}()
		default:
			nc.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func sendExit(ch ssh.Channel, code int) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	ch.SendRequest("exit-status", false, b)
	ch.Close()
}

func (s *testServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "exec":
			var p struct{ Command string }
			ssh.Unmarshal(req.Payload, &p)
			req.Reply(true, nil)
			s.mu.Lock()
			s.execLog = append(s.execLog, p.Command)
			hook := s.execHook
			s.mu.Unlock()
			if hook != nil {
				if out, code, ok := hook(p.Command); ok {
					io.WriteString(ch, out)
					sendExit(ch, code)
					return
				}
			}
			go s.runLocal(ch, p.Command)
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(req.Payload, &p)
			if p.Name != "sftp" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			go func() {
				srv, err := sftp.NewServer(ch)
				if err != nil {
					ch.Close()
					return
				}
				srv.Serve()
				srv.Close()
				ch.Close()
			}()
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func (s *testServer) runLocal(ch ssh.Channel, command string) {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "HOME="+s.home)
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	// 不直接把 ch 設為 Stdin, 否則程序結束後 Wait 仍等待 ch 的讀取而無法返回.
	stdin, _ := cmd.StdinPipe()
	err := cmd.Start()
	if err == nil {
		go func() {
			io.Copy(stdin, ch)
			stdin.Close()
		}()
		err = cmd.Wait()
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		} else {
			code = exitErr.ExitCode()
		}
	} else if err != nil {
		code = 127
	}
	sendExit(ch, code)
}

// testEnv 為測試用的本機目錄配置, 所有路徑皆位於暫存目錄.
type testEnv struct {
	home       string
	sshDir     string
	config     string
	knownHosts string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &testEnv{
		home: home, sshDir: sshDir,
		config:     filepath.Join(sshDir, "config"),
		knownHosts: filepath.Join(sshDir, "known_hosts"),
	}
}

func (e *testEnv) cfg(s *testServer) Config {
	return Config{
		Host: s.host, Port: s.portNum(), User: "tester",
		HomeDir: e.home, SSHConfigPath: e.config, KnownHostsPath: e.knownHosts, KeyDir: e.sshDir,
		Agent: NoAgent,
	}
}

func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func allZero(b []byte) bool {
	return bytes.Count(b, []byte{0}) == len(b)
}

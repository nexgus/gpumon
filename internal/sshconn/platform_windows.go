//go:build windows

package sshconn

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio"
)

// createNoWindow 為 Windows 的 CREATE_NO_WINDOW, 使 ProxyCommand 不跳出主控台視窗.
const createNoWindow = 0x08000000

// openSSHAgentPipe 為 Windows 內建 OpenSSH 的 ssh-agent 所用的 named pipe.
const openSSHAgentPipe = `\\.\pipe\openssh-ssh-agent`

// proxyShellCommand 以 cmd.exe 執行 ProxyCommand. 未於 Windows 實測.
func proxyShellCommand(command string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /c ` + command,
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd
}

// defaultAgentDialer 連線至 Windows 內建 OpenSSH 的 ssh-agent. SSH_AUTH_SOCK 設為
// named pipe 路徑時改用該路徑. 不支援 Pageant. 未於 Windows 實測.
func defaultAgentDialer(ctx context.Context) (net.Conn, error) {
	pipe := openSSHAgentPipe
	if s := os.Getenv("SSH_AUTH_SOCK"); strings.HasPrefix(s, `\\.\pipe\`) {
		pipe = s
	}
	return winio.DialPipeContext(ctx, pipe)
}

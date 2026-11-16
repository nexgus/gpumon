//go:build windows

package sshconn

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
)

// openSSHAgentPipe 為 Windows 內建 OpenSSH 的 ssh-agent 所用的 named pipe.
const openSSHAgentPipe = `\\.\pipe\openssh-ssh-agent`

// defaultAgentDialer 連線至 Windows 內建 OpenSSH 的 ssh-agent. SSH_AUTH_SOCK 設為
// named pipe 路徑時改用該路徑. 不支援 Pageant. 未於 Windows 實測.
func defaultAgentDialer(ctx context.Context) (net.Conn, error) {
	pipe := openSSHAgentPipe
	if s := os.Getenv("SSH_AUTH_SOCK"); strings.HasPrefix(s, `\\.\pipe\`) {
		pipe = s
	}
	return winio.DialPipeContext(ctx, pipe)
}

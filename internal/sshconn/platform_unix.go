//go:build !windows

package sshconn

import (
	"context"
	"errors"
	"net"
	"os"
)

// defaultAgentDialer 連線至 SSH_AUTH_SOCK 所指的 ssh-agent.
func defaultAgentDialer(ctx context.Context) (net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", sock)
}

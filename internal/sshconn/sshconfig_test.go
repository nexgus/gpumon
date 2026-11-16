//go:build !windows

package sshconn

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadHostConfig(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, env.config, `
# 註解
Host gpu01 gpu02
  HostName 10.0.0.1
  User alice
  Port 2222
  IdentityFile ~/.ssh/id_gpu

Host gpu*  !gpu03
  User wildcard-user
  IdentityFile=~/.ssh/id_second
  ServerAliveInterval 30

Host gpu03
  HostName %h.example.com

Match host gpu01
  User matched

Host "quoted name"
  HostName quoted.example.com

Host *
  User fallback
  Port 22
`, 0o600)

	got, err := loadHostConfig(env.config, "gpu01", env.home)
	if err != nil {
		t.Fatal(err)
	}
	want := hostConfig{
		HostName: "10.0.0.1", User: "alice", Port: 2222,
		IdentityFiles: []string{"~/.ssh/id_gpu", "~/.ssh/id_second"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gpu01 = %+v, want %+v", got, want)
	}

	got, _ = loadHostConfig(env.config, "GPU02", env.home)
	if got.HostName != "10.0.0.1" {
		t.Errorf("host matching should be case-insensitive: %+v", got)
	}

	got, _ = loadHostConfig(env.config, "gpu03", env.home)
	if got.User != "fallback" || got.HostName != "%h.example.com" || got.Port != 22 {
		t.Errorf("negated pattern: %+v", got)
	}

	got, _ = loadHostConfig(env.config, "quoted name", env.home)
	if got.HostName != "quoted.example.com" {
		t.Errorf("quoted host pattern: %+v", got)
	}

	got, _ = loadHostConfig(env.config, "other", env.home)
	if got.User != "fallback" || got.HostName != "" {
		t.Errorf("other = %+v", got)
	}

	got, err = loadHostConfig(filepath.Join(env.home, "missing"), "gpu01", env.home)
	if err != nil || !reflect.DeepEqual(got, hostConfig{}) {
		t.Errorf("missing file = %+v, %v", got, err)
	}
}

func TestLoadHostConfigInclude(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, env.config, `
Include conf.d/*.conf
Include ~/extra/abs.conf

Host jump
  Include jump-only.conf

Host target
  User target-user
`, 0o600)
	writeFile(t, filepath.Join(env.sshDir, "conf.d", "10-a.conf"), `
Host target
  HostName 192.0.2.10
`, 0o600)
	writeFile(t, filepath.Join(env.sshDir, "conf.d", "20-b.conf"), `
Host target
  HostName 192.0.2.99
  Port 2200
  ProxyJump bastion
  ProxyCommand nc %h %p
  IdentitiesOnly yes
`, 0o600)
	writeFile(t, filepath.Join(env.home, "extra", "abs.conf"), `
Host target
  IdentityFile ~/.ssh/id_included
`, 0o600)
	writeFile(t, filepath.Join(env.sshDir, "jump-only.conf"), `
User jump-user
Host target
  User should-not-apply
`, 0o600)

	got, err := loadHostConfig(env.config, "target", env.home)
	if err != nil {
		t.Fatal(err)
	}
	want := hostConfig{
		HostName: "192.0.2.10", User: "target-user", Port: 2200,
		IdentityFiles:  []string{"~/.ssh/id_included"},
		IdentitiesOnly: true, identitiesOnlySet: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("target = %+v, want %+v", got, want)
	}

	got, _ = loadHostConfig(env.config, "jump", env.home)
	if got.User != "jump-user" {
		t.Errorf("include inside Host block: %+v", got)
	}
}

func TestLoadHostConfigIdentitiesOnlyFirstWins(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, env.config, `
Host a
  IdentitiesOnly no
Host *
  IdentitiesOnly yes
`, 0o600)
	a, _ := loadHostConfig(env.config, "a", env.home)
	if a.IdentitiesOnly {
		t.Errorf("a = %+v", a)
	}
	b, _ := loadHostConfig(env.config, "b", env.home)
	if !b.IdentitiesOnly {
		t.Errorf("b = %+v", b)
	}
}

func TestLoadHostConfigErrors(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, env.config, "Host x\n  Port abc\n", 0o600)
	if _, err := loadHostConfig(env.config, "x", env.home); err == nil {
		t.Error("invalid port accepted")
	}
	writeFile(t, env.config, "Host x\n  IdentitiesOnly maybe\n", 0o600)
	if _, err := loadHostConfig(env.config, "x", env.home); err == nil {
		t.Error("invalid IdentitiesOnly accepted")
	}
	writeFile(t, env.config, "Include self\n", 0o600)
	writeFile(t, filepath.Join(env.sshDir, "self"), "Include self\n", 0o600)
	if _, err := loadHostConfig(env.config, "x", env.home); err == nil {
		t.Error("recursive Include accepted")
	}
}

func TestExpandTokens(t *testing.T) {
	got := expandTokens("%%h=%h p=%p r=%r x=%x %", map[byte]string{'h': "host", 'p': "22", 'r': "u"})
	if got != "%h=host p=22 r=u x=%x %" {
		t.Errorf("expandTokens = %q", got)
	}
}

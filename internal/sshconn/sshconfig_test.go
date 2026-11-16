//go:build !windows

package sshconn

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	got, warnings := loadHostConfig(env.config, "gpu01", env.home)
	if len(warnings) != 0 {
		t.Fatal(warnings)
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

	got, warnings = loadHostConfig(filepath.Join(env.home, "missing"), "gpu01", env.home)
	if warnings == nil || len(warnings) != 0 || !reflect.DeepEqual(got, hostConfig{}) {
		t.Errorf("missing file = %+v, %#v", got, warnings)
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

	got, warnings := loadHostConfig(env.config, "target", env.home)
	if len(warnings) != 0 {
		t.Fatal(warnings)
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

func TestLoadHostConfigTolerant(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, env.config, `Host x
  Port abc
  Port
  IdentitiesOnly maybe
  HostName "unterminated
  User "alice"
  Port 2200
  Include missing-dir/*.conf
  Include "bad
  IdentitiesOnly yes
Host "broken
  User wrong
Host x
  HostName real.example.com
`, 0o600)
	got, warnings := loadHostConfig(env.config, "x", env.home)
	want := hostConfig{HostName: "real.example.com", User: "alice", Port: 2200, IdentitiesOnly: true, identitiesOnlySet: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("config = %+v, want %+v", got, want)
	}
	wantWarnings := []string{
		`Skipped line 2 of ~/.ssh/config: invalid port "abc"`,
		`Skipped line 3 of ~/.ssh/config: missing argument for Port`,
		`Skipped line 4 of ~/.ssh/config: invalid IdentitiesOnly value "maybe"`,
		`Skipped line 5 of ~/.ssh/config: unterminated quote`,
		`Skipped line 9 of ~/.ssh/config: unterminated quote`,
		`Skipped line 11 of ~/.ssh/config: unterminated quote`,
	}
	if !reflect.DeepEqual(warnings, wantWarnings) {
		t.Errorf("warnings = %q, want %q", warnings, wantWarnings)
	}

	// Include 巢狀過深時略過該 Include, 其餘照常使用.
	writeFile(t, env.config, "Include self\nHost x\n  User bob\n", 0o600)
	writeFile(t, filepath.Join(env.sshDir, "self"), "Include self\n", 0o600)
	got, warnings = loadHostConfig(env.config, "x", env.home)
	if got.User != "bob" || len(warnings) != 1 || !strings.Contains(warnings[0], "nested more than 16 levels") ||
		!strings.Contains(warnings[0], "line 1 of ~/.ssh/self") {
		t.Errorf("recursive Include: %+v %q", got, warnings)
	}

	if os.Geteuid() != 0 {
		// Include 的檔案讀不到時略過該檔案.
		unreadable := filepath.Join(env.sshDir, "secret.conf")
		writeFile(t, unreadable, "Host x\n  User secret\n", 0o000)
		writeFile(t, env.config, "Include secret.conf\nHost x\n  Port 2022\n", 0o600)
		got, warnings = loadHostConfig(env.config, "x", env.home)
		if got.Port != 2022 || got.User != "" || len(warnings) != 1 ||
			!strings.HasPrefix(warnings[0], "Skipped the Include on line 1 of ~/.ssh/config for ~/.ssh/secret.conf") {
			t.Errorf("unreadable Include: %+v %q", got, warnings)
		}

		// 主檔案讀不到時視為沒有設定.
		writeFile(t, env.config, "Host x\n  Port 2022\n", 0o600)
		if err := os.Chmod(env.config, 0o000); err != nil {
			t.Fatal(err)
		}
		got, warnings = loadHostConfig(env.config, "x", env.home)
		if !reflect.DeepEqual(got, hostConfig{}) || len(warnings) != 1 ||
			!strings.HasPrefix(warnings[0], "Ignored ~/.ssh/config because it cannot be read") {
			t.Errorf("unreadable config: %+v %q", got, warnings)
		}
	}
}

func TestLookupConfig(t *testing.T) {
	env := newTestEnv(t)
	writeFile(t, env.config, `Host gpu
  HostName %h.example
  User alice
  Port 2222
  IdentityFile none
  IdentityFile ~/.ssh/id_%r_%h_%p
  IdentityFile ~/.ssh/second
  IdentitiesOnly yes
  Port oops
Host plain
  IdentityFile keys/rel
`, 0o600)
	d := LookupConfig(env.config, env.home, "gpu")
	want := ConfigDefaults{
		User: "alice", Port: 2222,
		KeyFile:        filepath.Join(env.sshDir, "id_alice_gpu.example_2222"),
		IdentitiesOnly: true, IdentitiesOnlySet: true,
		Warnings: []string{`Skipped line 9 of ~/.ssh/config: invalid port "oops"`},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("gpu = %+v, want %+v", d, want)
	}
	d = LookupConfig(env.config, env.home, "plain")
	if d.KeyFile != filepath.Join(env.home, "keys", "rel") || d.Port != 0 || d.User != "" || d.IdentitiesOnlySet ||
		d.Warnings == nil || len(d.Warnings) != 0 {
		t.Errorf("plain = %+v", d)
	}
	d = LookupConfig(filepath.Join(env.home, "missing"), env.home, "gpu")
	if d.KeyFile != "" || d.Port != 0 || d.Warnings == nil || len(d.Warnings) != 0 {
		t.Errorf("missing = %+v", d)
	}
}

func TestExpandTokens(t *testing.T) {
	got := expandTokens("%%h=%h p=%p r=%r x=%x %", map[byte]string{'h': "host", 'p': "22", 'r': "u"})
	if got != "%h=host p=22 r=u x=%x %" {
		t.Errorf("expandTokens = %q", got)
	}
}

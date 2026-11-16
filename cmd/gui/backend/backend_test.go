package backend

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gpumon/internal/history"
	"gpumon/internal/recording"
	"gpumon/internal/settings"
	"gpumon/internal/sshconn"
)

func TestSSHConfigDefaults(t *testing.T) {
	calls := 0
	lookup := func(addr string) (sshconn.ConfigDefaults, error) {
		calls++
		if addr != "gpu" {
			t.Errorf("addr = %q", addr)
		}
		return sshconn.ConfigDefaults{User: "u", Port: 2222, KeyFile: "/k", IdentitiesOnly: true, IdentitiesOnlySet: true,
			Warnings: []string{"w"}}, nil
	}
	got, err := sshConfigDefaults("  gpu ", lookup)
	want := SSHConfigDefaults{Port: 2222, KeyFile: "/k", IdentitiesOnly: true, IdentitiesOnlySet: true, Warnings: []string{"w"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v %v", got, err)
	}

	got, err = sshConfigDefaults(" ", lookup)
	if err != nil || calls != 1 {
		t.Errorf("blank address: %v, %d lookups", err, calls)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"port":0,"key_file":"","identities_only":false,"identities_only_set":false,"warnings":[]}` {
		t.Errorf("blank address JSON = %s", b)
	}

	got, _ = sshConfigDefaults("gpu", func(string) (sshconn.ConfigDefaults, error) {
		return sshconn.ConfigDefaults{}, nil
	})
	if got.Warnings == nil {
		t.Error("warnings must be an empty array")
	}

	if _, err := sshConfigDefaults("gpu", func(string) (sshconn.ConfigDefaults, error) {
		return sshconn.ConfigDefaults{}, errors.New("no home")
	}); err == nil {
		t.Error("internal error not returned")
	}
}

func TestSSHConfigDefaultsReadsFile(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".ssh", "config")
	os.MkdirAll(filepath.Dir(config), 0o700)
	os.WriteFile(config, []byte("Host gpu\n  Port 2200\n  Port x\n  IdentityFile ~/.ssh/id_gpu\n"), 0o600)
	got, err := sshConfigDefaults("gpu", func(addr string) (sshconn.ConfigDefaults, error) {
		return sshconn.LookupConfig(config, home, addr), nil
	})
	if err != nil || got.Port != 2200 || got.KeyFile != filepath.Join(home, ".ssh", "id_gpu") || len(got.Warnings) != 1 {
		t.Errorf("got %+v %v", got, err)
	}
}

func TestUpgradeLegacyHostsMovesRecordings(t *testing.T) {
	dataDir := t.TempDir()
	devices := json.RawMessage(`{"type":"devices","protocol":1,"gpus":[{"id":0,"vendor":"nvidia","name":"GPU"}]}`)
	for _, host := range []string{"bob@srv", "alice@plain"} {
		w, err := recording.Create(dataDir, recording.Header{Host: host, TWall0: 1730700000123456, TS0: 1, Devices: devices})
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	settingsJSON := `{"hosts": [
	  {"addr": "srv", "user": "", "port": 0, "key_file": ""},
	  {"addr": "plain", "user": "", "port": 0, "key_file": ""}
	], "collapsed_procs": ["srv", "plain"]}`
	if err := os.WriteFile(filepath.Join(dataDir, settings.FileName), []byte(settingsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := settings.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]sshconn.ConfigDefaults{
		"srv":   {User: "bob", Port: 2222},
		"plain": {User: "alice"},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	upgradeLegacyHosts(dataDir, store, func(addr string) (sshconn.ConfigDefaults, error) { return cfg[addr], nil }, log)

	var ids []string
	for _, h := range store.Hosts() {
		ids = append(ids, h.ID())
	}
	if want := []string{"bob@srv:2222", "alice@plain"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
	if want := []string{"bob@srv:2222", "alice@plain"}; !reflect.DeepEqual(store.Snapshot().CollapsedProcs, want) {
		t.Errorf("collapsed procs = %v", store.Snapshot().CollapsedProcs)
	}
	list, err := history.List(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var groups []string
	for _, g := range list.Hosts {
		groups = append(groups, g.Host)
		if len(g.Files) != 1 {
			t.Errorf("%s: %d files", g.Host, len(g.Files))
		}
	}
	if want := []string{"alice@plain", "bob@srv:2222"}; !reflect.DeepEqual(groups, want) {
		t.Errorf("recording groups = %v", groups)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "history", "bob@srv+2222")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "history", "bob@srv")); !os.IsNotExist(err) {
		t.Errorf("old directory kept: %v", err)
	}
}

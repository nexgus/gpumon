package settings

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestHostID(t *testing.T) {
	cases := []struct {
		c    HostConn
		id   string
		same HostConn
	}{
		{HostConn{Addr: "gpu", User: "u"}, "u@gpu", HostConn{Addr: "GPU", User: "u", Port: 22}},
		{HostConn{Addr: "gpu", User: "u", Port: 22}, "u@gpu", HostConn{Addr: "gpu", User: "u"}},
		{HostConn{Addr: "gpu", User: "u", Port: 2222}, "u@gpu:2222", HostConn{Addr: "Gpu", User: "u", Port: 2222}},
		{HostConn{Addr: "fe80::1", User: "u"}, "u@fe80::1", HostConn{Addr: "FE80::1", User: "u"}},
		{HostConn{Addr: "fe80::1", User: "u", Port: 2222}, "u@[fe80::1]:2222", HostConn{Addr: "fe80::1", User: "u", Port: 2222}},
		{HostConn{Addr: "gpu"}, "gpu", HostConn{Addr: "gpu", Port: 22}},
		{HostConn{Addr: "gpu", Port: 2222}, "gpu:2222", HostConn{Addr: "gpu", Port: 2222}},
	}
	for _, tc := range cases {
		if got := tc.c.ID(); got != tc.id {
			t.Errorf("%+v ID = %q, want %q", tc.c, got, tc.id)
		}
		if tc.c.Key() != tc.same.Key() {
			t.Errorf("%+v and %+v must be the same host", tc.c, tc.same)
		}
	}
	for _, pair := range [][2]HostConn{
		{{Addr: "gpu", User: "u"}, {Addr: "gpu", User: "U"}},
		{{Addr: "gpu", User: "u"}, {Addr: "gpu", User: "u", Port: 2222}},
		{{Addr: "gpu", User: "u", Port: 2200}, {Addr: "gpu", User: "u", Port: 2222}},
	} {
		if pair[0].Key() == pair[1].Key() {
			t.Errorf("%+v and %+v must be different hosts", pair[0], pair[1])
		}
	}
}

func TestUpgradeLegacyHosts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{
	  "hosts": [
	    {"addr": "cfg-user", "user": "", "port": 0, "key_file": ""},
	    {"addr": "cfg-port", "user": "bob", "port": 0, "key_file": "/form/key"},
	    {"addr": "form-port", "user": "carol", "port": 2200, "key_file": ""},
	    {"addr": "plain", "user": "dave", "port": 0, "key_file": ""},
	    {"addr": "nocfg", "user": "", "port": 0, "key_file": ""},
	    {"addr": "dup", "user": "erin", "port": 0, "key_file": ""},
	    {"addr": "dup", "user": "erin", "port": 2022, "key_file": "", "identities_only": false},
	    {"addr": "new", "user": "fay", "port": 0, "key_file": "", "identities_only": false}
	  ],
	  "collapsed_gpus": {"cfg-user": [1], "bob@cfg-port": [0, 2], "erin@dup": [3]},
	  "collapsed_procs": ["cfg-user", "carol@form-port", "nocfg"],
	  "collapsed_maxima": ["bob@cfg-port"],
	  "proc_sorts": {"cfg-user": {"column": "pid", "order": "desc"}},
	  "proc_filters": {"carol@form-port": {"gpus": [1], "user": "x", "command": ""}},
	  "no_key_setup_prompt": ["cfg-user", "bob@cfg-port", "dave@plain"]
	}`)
	s := mustOpen(t, dir)
	if !s.HasLegacyHosts() {
		t.Fatal("legacy hosts not detected")
	}
	cfg := map[string]LegacyDefaults{
		"cfg-user":  {User: "alice", KeyFile: "/cfg/alice", IdentitiesOnly: true},
		"cfg-port":  {User: "ignored", Port: 2222, KeyFile: "/cfg/ignored", IdentitiesOnly: true},
		"form-port": {Port: 2333},
		"plain":     {},
		"dup":       {Port: 2022},
		"new":       {User: "never", Port: 1},
	}
	var lookedUp []string
	var before []HostUpgrade
	ups, err := s.UpgradeLegacyHosts(func(addr string) LegacyDefaults {
		lookedUp = append(lookedUp, addr)
		return cfg[addr]
	}, func(u []HostUpgrade) {
		before = append(before, u...)
		// 寫回設定檔之前呼叫.
		raw, _ := os.ReadFile(s.Path())
		var top map[string]json.RawMessage
		json.Unmarshal(raw, &top)
		if string(top["hosts"]) == "" || !json.Valid(top["hosts"]) {
			t.Error("settings file missing")
		}
		var hosts []map[string]any
		json.Unmarshal(top["hosts"], &hosts)
		if _, ok := hosts[0]["identities_only"]; ok {
			t.Error("beforeSave called after saving")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cfg-user", "cfg-port", "form-port", "plain", "nocfg", "dup"}; !reflect.DeepEqual(lookedUp, want) {
		t.Errorf("looked up %v, want %v", lookedUp, want)
	}
	wantHosts := []HostConn{
		{Addr: "cfg-user", User: "alice", KeyFile: "/cfg/alice", IdentitiesOnly: true},
		{Addr: "cfg-port", User: "bob", Port: 2222, KeyFile: "/form/key", IdentitiesOnly: true},
		{Addr: "form-port", User: "carol", Port: 2200},
		{Addr: "plain", User: "dave"},
		{Addr: "nocfg"},
		// 填入後與下一台主機重複, 維持原本的欄位.
		{Addr: "dup", User: "erin"},
		{Addr: "dup", User: "erin", Port: 2022},
		{Addr: "new", User: "fay"},
	}
	var got []HostConn
	for _, h := range s.Hosts() {
		got = append(got, h.HostConn)
		if h.legacy {
			t.Errorf("%s still legacy", h.ID())
		}
	}
	if !reflect.DeepEqual(got, wantHosts) {
		t.Errorf("hosts =\n%+v\nwant\n%+v", got, wantHosts)
	}
	wantUps := []HostUpgrade{
		{OldID: "cfg-user", NewID: "alice@cfg-user", OldRecordingHost: "alice@cfg-user", Host: wantHosts[0]},
		{OldID: "bob@cfg-port", NewID: "bob@cfg-port:2222", OldRecordingHost: "bob@cfg-port", Host: wantHosts[1]},
		{OldID: "carol@form-port", NewID: "carol@form-port:2200", OldRecordingHost: "carol@form-port", Host: wantHosts[2]},
	}
	if !reflect.DeepEqual(ups, wantUps) || !reflect.DeepEqual(before, wantUps) {
		t.Errorf("upgrades =\n%+v\nbefore save\n%+v\nwant\n%+v", ups, before, wantUps)
	}

	d := s.Snapshot()
	wantGPUs := map[string][]int{"alice@cfg-user": {1}, "bob@cfg-port:2222": {0, 2}, "erin@dup": {3}}
	if !reflect.DeepEqual(d.CollapsedGPUs, wantGPUs) {
		t.Errorf("collapsed gpus = %v", d.CollapsedGPUs)
	}
	if want := []string{"alice@cfg-user", "carol@form-port:2200", "nocfg"}; !reflect.DeepEqual(d.CollapsedProcs, want) {
		t.Errorf("collapsed procs = %v", d.CollapsedProcs)
	}
	if want := []string{"bob@cfg-port:2222"}; !reflect.DeepEqual(d.CollapsedMaxima, want) {
		t.Errorf("collapsed maxima = %v", d.CollapsedMaxima)
	}
	if _, ok := d.ProcSorts["alice@cfg-user"]; !ok || len(d.ProcSorts) != 1 {
		t.Errorf("proc sorts = %v", d.ProcSorts)
	}
	if _, ok := d.ProcFilters["carol@form-port:2200"]; !ok || len(d.ProcFilters) != 1 {
		t.Errorf("proc filters = %v", d.ProcFilters)
	}
	if want := []string{"alice@cfg-user", "bob@cfg-port:2222", "dave@plain"}; !reflect.DeepEqual(d.NoKeySetupPrompt, want) {
		t.Errorf("no key setup prompt = %v", d.NoKeySetupPrompt)
	}

	// 寫回之後即為新版主機, 不再升級.
	s2 := mustOpen(t, dir)
	if s2.HasLegacyHosts() {
		t.Error("hosts still legacy after reopening")
	}
	if !reflect.DeepEqual(s2.Snapshot(), d) {
		t.Errorf("saved settings differ:\n%+v\n%+v", s2.Snapshot(), d)
	}
	ups, err = s2.UpgradeLegacyHosts(func(string) LegacyDefaults {
		t.Error("lookup called without legacy hosts")
		return LegacyDefaults{}
	}, nil)
	if ups != nil || err != nil {
		t.Errorf("second upgrade = %v %v", ups, err)
	}
}

// TestUpgradeLegacyHostsChained 確認一台主機的新識別等於另一台主機的舊識別時,
// 各自的狀態不互相混入.
func TestUpgradeLegacyHostsChained(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{
	  "hosts": [
	    {"addr": "srv", "user": "", "port": 2022},
	    {"addr": "srv", "user": "bob", "port": 0}
	  ],
	  "collapsed_gpus": {"srv": [1], "bob@srv": [2]}
	}`)
	s := mustOpen(t, dir)
	_, err := s.UpgradeLegacyHosts(func(addr string) LegacyDefaults {
		return LegacyDefaults{User: "bob"}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, h := range s.Hosts() {
		ids = append(ids, h.ID())
	}
	if want := []string{"bob@srv:2022", "bob@srv"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
	if want := map[string][]int{"bob@srv:2022": {1}, "bob@srv": {2}}; !reflect.DeepEqual(s.Snapshot().CollapsedGPUs, want) {
		t.Errorf("collapsed gpus = %v", s.Snapshot().CollapsedGPUs)
	}
}

// TestUpgradeLegacyHostsSaveFails 確認寫回失敗時記憶體中的內容仍為升級後的內容.
func TestUpgradeLegacyHostsSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write any directory")
	}
	dir := t.TempDir()
	writeFile(t, dir, `{"hosts": [{"addr": "a", "user": ""}]}`)
	s := mustOpen(t, dir)
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	_, err := s.UpgradeLegacyHosts(func(string) LegacyDefaults { return LegacyDefaults{User: "u"} }, nil)
	if err == nil {
		t.Fatal("save error not reported")
	}
	if hs := s.Hosts(); len(hs) != 1 || hs[0].ID() != "u@a" || hs[0].legacy {
		t.Errorf("hosts = %+v", hs)
	}
}

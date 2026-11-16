package recording

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeRecording 建立 host 的記錄檔, 寫入一筆取樣與一筆錯誤後關閉, 回傳路徑.
func writeRecording(t *testing.T, dataDir, host string, tw int64) string {
	t.Helper()
	w, err := Create(dataDir, Header{Host: host, TWall0: tw, TS0: 1730700000.123, Devices: devicesJSON(2)})
	if err != nil {
		t.Fatal(err)
	}
	w.AddSample(1730700000.623, 500, []GPUSample{{ID: 0, Util: i64(50)}, {ID: 1, Temp: i64(60)}})
	w.AddError(1730700000.7, "boom")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.Path()
}

func TestRehost(t *testing.T) {
	dataDir := t.TempDir()
	a := writeRecording(t, dataDir, "bob@s v", t0)
	b := writeRecording(t, dataDir, "bob@s v", t0+3_600_000_000)
	// 目錄名稱相同而 host 成員不同的檔案不動.
	other := writeRecording(t, dataDir, "bob@s_v", t0+7_200_000_000)
	// 新目錄已有同名檔案時加 -2.
	clash := writeRecording(t, dataDir, "bob@s v:2222", t0)
	origA := readAll(t, open(t, a))

	if err := Rehost(dataDir, "bob@s v", "bob@s v:2222"); err != nil {
		t.Fatal(err)
	}
	newDir := HostDir(dataDir, "bob@s v:2222")
	if filepath.Base(newDir) != "bob@s_v+2222" || filepath.Base(clash) != filepath.Base(a) {
		t.Fatalf("new dir %s, %s", newDir, clash)
	}
	movedA := filepath.Join(newDir, trimExt(filepath.Base(a))+"-2.gpurec")
	movedB := filepath.Join(newDir, filepath.Base(b))
	for _, p := range []string{a, b} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s not removed: %v", p, err)
		}
	}
	fa := open(t, movedA)
	if h := fa.Header(); h.Host != "bob@s v:2222" || h.TWall0 != t0 || h.TS0 != 1730700000.123 {
		t.Errorf("header = %+v", h)
	}
	if !reflect.DeepEqual(readAll(t, fa), origA) {
		t.Errorf("records changed")
	}
	if h := open(t, movedB).Header(); h.Host != "bob@s v:2222" {
		t.Errorf("header = %+v", h)
	}
	if h := open(t, clash).Header(); h.Host != "bob@s v:2222" {
		t.Errorf("existing file changed: %+v", h)
	}
	if h := open(t, other).Header(); h.Host != "bob@s_v" {
		t.Errorf("other host's file changed: %+v", h)
	}
	if st, err := os.Stat(movedA); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v %v", st, err)
	}
	entries, _ := os.ReadDir(newDir)
	if len(entries) != 3 {
		t.Errorf("entries in new dir = %v", entries)
	}

	// 原目錄只剩他人的檔案, 保留; 清空時刪除.
	if _, err := os.Stat(filepath.Dir(other)); err != nil {
		t.Errorf("old dir with other files removed: %v", err)
	}
	if err := Rehost(dataDir, "bob@s_v", "bob@s_v:2022"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(other)); !os.IsNotExist(err) {
		t.Errorf("empty old dir kept: %v", err)
	}

	// 沒有舊目錄時不做事.
	if err := Rehost(dataDir, "nobody@x", "nobody@x:2"); err != nil {
		t.Error(err)
	}
}

func trimExt(name string) string { return name[:len(name)-len(filepath.Ext(name))] }

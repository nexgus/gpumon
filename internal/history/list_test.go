package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gpumon/internal/recording"
)

func mustExist(t *testing.T, p string, want bool) {
	t.Helper()
	_, err := os.Stat(p)
	if (err == nil) != want {
		t.Fatalf("exists(%s) = %v, want %v", p, err == nil, want)
	}
}

func TestListGroupsAndTotals(t *testing.T) {
	dir := t.TempDir()
	if l, err := List(dir, nil); err != nil || len(l.Hosts) != 0 || len(l.Skipped) != 0 {
		t.Fatalf("empty = %+v, %v", l, err)
	}
	b1 := newRec(t, dir, "bob@10.0.0.2", 5*sec, 0)
	add(b1, 0, 1000, 1, 0)
	add(b1, 60, 1000, 1, 0)
	flush(t, b1)
	a1 := newRec(t, dir, "alice@10.0.0.1", 100*sec, 0)
	add(a1, 0, 1000, 1, 0)
	add(a1, 30, 1000, 1, 0)
	flush(t, a1)
	a2 := newRec(t, dir, "alice@10.0.0.1", 10*sec, 0, 1)
	add(a2, 0, 1000, 1, 0, 1)
	flush(t, a2)
	empty := newRec(t, dir, "alice@10.0.0.1", 500*sec, 0) // 只有說明區
	_ = empty

	// 損毀與非記錄檔: 檔頭不符, 版本不認得, 截斷, 副檔名不符.
	hd := filepath.Join(dir, "history", "alice@10.0.0.1")
	os.WriteFile(filepath.Join(hd, "bad1.gpurec"), []byte("not a recording at all"), 0o644)
	os.WriteFile(filepath.Join(hd, "bad2.gpurec"), []byte{}, 0o644)
	raw, _ := os.ReadFile(a1.Path())
	raw[8] = 9
	os.WriteFile(filepath.Join(hd, "bad3.gpurec"), raw, 0o644)
	os.WriteFile(filepath.Join(hd, "bad4.gpurec"), raw[:20], 0o644)
	os.WriteFile(filepath.Join(hd, "note.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "history", "stray.gpurec"), []byte("x"), 0o644)

	l, err := List(dir, []string{a1.Path()})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Hosts) != 2 || l.Hosts[0].Host != "alice@10.0.0.1" || l.Hosts[1].Host != "bob@10.0.0.2" {
		t.Fatalf("hosts = %+v", l.Hosts)
	}
	alice := l.Hosts[0]
	if len(alice.Files) != 3 {
		t.Fatalf("alice files = %+v", alice.Files)
	}
	f0, f1, f2 := alice.Files[0], alice.Files[1], alice.Files[2]
	if f0.Path != a2.Path() || f0.Start != twall+10*sec || f0.End != twall+10*sec || !f0.HasData || f0.NumGPUs != 2 || f0.Active {
		t.Fatalf("f0 = %+v", f0)
	}
	if f1.Path != a1.Path() || f1.Start != twall+100*sec || f1.End != twall+130*sec || !f1.Active {
		t.Fatalf("f1 = %+v", f1)
	}
	if f2.HasData || f2.Start != twall+500*sec || f2.End != f2.Start {
		t.Fatalf("f2 = %+v", f2)
	}
	var sum int64
	for _, f := range alice.Files {
		st, _ := os.Stat(f.Path)
		if f.Size != st.Size() || f.Name != filepath.Base(f.Path) {
			t.Fatalf("size/name = %+v", f)
		}
		sum += f.Size
	}
	if alice.TotalSize != sum {
		t.Fatalf("total = %d, want %d", alice.TotalSize, sum)
	}
	bob := l.Hosts[1]
	if len(bob.Files) != 1 || bob.Files[0].End != twall+65*sec || bob.TotalSize != bob.Files[0].Size {
		t.Fatalf("bob = %+v", bob)
	}
	if len(l.Skipped) != 4 {
		t.Fatalf("skipped = %+v", l.Skipped)
	}
	for _, s := range l.Skipped {
		if s.Error == "" || filepath.Base(s.Path) == "note.txt" {
			t.Fatalf("skipped = %+v", s)
		}
	}
	if _, err := json.Marshal(l); err != nil {
		t.Fatal(err)
	}
	_ = recording.KindSample
}

func TestDeleteRestrictions(t *testing.T) {
	dir := t.TempDir()
	w := newRec(t, dir, "a@b", 0, 0)
	add(w, 0, 1000, 1, 0)
	flush(t, w)
	w2 := newRec(t, dir, "a@b", sec, 0)
	add(w2, 0, 1000, 1, 0)
	flush(t, w2)

	outside := filepath.Join(t.TempDir(), "x.gpurec")
	os.WriteFile(outside, []byte("x"), 0o644)
	hd := filepath.Dir(w.Path())
	txt := filepath.Join(hd, "note.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	stray := filepath.Join(dir, "history", "stray.gpurec")
	os.WriteFile(stray, []byte("x"), 0o644)
	deep := filepath.Join(hd, "sub")
	os.MkdirAll(deep, 0o755)
	deepFile := filepath.Join(deep, "y.gpurec")
	os.WriteFile(deepFile, []byte("x"), 0o644)
	dirNamed := filepath.Join(hd, "dir.gpurec")
	os.MkdirAll(dirNamed, 0o755)
	link := filepath.Join(hd, "link.gpurec")
	linkOK := os.Symlink(outside, link) == nil
	hostLink := filepath.Join(dir, "history", "linkhost")
	hostLinkOK := os.Symlink(filepath.Dir(outside), hostLink) == nil

	bad := []string{
		outside, txt, stray, deepFile, dirNamed, dir, hd,
		filepath.Join(hd, "..", "..", "..", filepath.Base(filepath.Dir(outside)), "x.gpurec"),
		filepath.Join(hd, ".gpurec"),
		filepath.Join(hd, "missing.gpurec"),
	}
	if linkOK {
		bad = append(bad, link)
	}
	if hostLinkOK {
		bad = append(bad, filepath.Join(hostLink, "x.gpurec"))
	}
	for _, p := range bad {
		if err := Delete(dir, p, nil); err == nil {
			t.Fatalf("Delete(%s) should fail", p)
		}
	}
	for _, p := range []string{outside, txt, stray, deepFile, dirNamed, w.Path()} {
		mustExist(t, p, true)
	}
	if err := Delete(dir, w.Path(), nil); err != nil {
		t.Fatal(err)
	}
	// 已被刪除的第二次必須失敗, 且不影響其他檔案.
	if err := Delete(dir, w.Path(), nil); err == nil {
		t.Fatal("second delete should fail")
	}
	mustExist(t, w.Path(), false)

	// 連線中的檔案不可刪除, 以不同寫法指向同一檔案亦同.
	rel := filepath.Join(hd, ".", filepath.Base(w2.Path()))
	for _, active := range [][]string{{w2.Path()}, {rel}} {
		err := Delete(dir, w2.Path(), active)
		if !errors.Is(err, ErrActiveFile) {
			t.Fatalf("err = %v", err)
		}
		mustExist(t, w2.Path(), true)
	}
	err := Delete(dir, outside, nil)
	if !errors.Is(err, ErrOutsideHistory) {
		t.Fatalf("err = %v", err)
	}
	if err := Delete(dir, w2.Path(), []string{"/nonexistent"}); err != nil {
		t.Fatal(err)
	}
	mustExist(t, w2.Path(), false)
	mustExist(t, txt, true)
	_ = fmt.Sprint
}

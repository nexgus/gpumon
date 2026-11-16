package version

import (
	"bytes"
	"regexp"
	"testing"
)

func TestContentHash(t *testing.T) {
	// SHA-256("abc") 的前 7 個字元.
	if got := ContentHash([]byte("abc")); got != "ba7816b" {
		t.Errorf("ContentHash(abc) = %q", got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{7}$`).MatchString(ContentHash(nil)) {
		t.Error("ContentHash(nil) is not 7 lowercase hex characters")
	}
}

func TestRemoteFull(t *testing.T) {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(VERSION) + `\+[0-9a-f]{7}$`)
	if got := RemoteFull(); !re.MatchString(got) {
		t.Errorf("RemoteFull() = %q", got)
	}
}

func TestContentHashReader(t *testing.T) {
	content := []byte("some remote program content")
	got, err := ContentHashReader(bytes.NewReader(content))
	if err != nil || got != ContentHash(content) {
		t.Errorf("ContentHashReader = %q, %v; want %q", got, err, ContentHash(content))
	}
}

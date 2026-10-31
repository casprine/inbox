package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

func k(b byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune('a'+b)), 32)))
}

func TestParseKeyring(t *testing.T) {
	t.Run("list: first seals, all open", func(t *testing.T) {
		kr, err := ParseKeyring("new:"+k(1)+", old:"+k(0), "")
		if err != nil || kr.CurrentID() != "new" || !kr.Has("old") {
			t.Fatalf("%v %v", kr, err)
		}
		old, _ := ParseKeyring("old:"+k(0), "")
		sealed, _ := old.Seal([]byte("x"), nil)
		if pt, err := kr.Open(sealed, nil); err != nil || string(pt) != "x" {
			t.Fatalf("data sealed under the old key must open: %q %v", pt, err)
		}
		fresh, _ := kr.Seal([]byte("y"), nil)
		if id, _ := SealedKeyID(fresh); id != "new" {
			t.Fatalf("sealed with %q", id)
		}
	})
	t.Run("legacy single key is k1", func(t *testing.T) {
		kr, err := ParseKeyring("", k(0))
		if err != nil || kr.CurrentID() != "k1" {
			t.Fatalf("%v %v", kr, err)
		}
	})
	t.Run("list plus legacy keeps k1 readable but not current", func(t *testing.T) {
		kr, err := ParseKeyring("k2:"+k(1), k(0))
		if err != nil || kr.CurrentID() != "k2" || !kr.Has("k1") {
			t.Fatalf("%v %v", kr, err)
		}
	})
	t.Run("an explicit k1 in the list wins over the legacy variable", func(t *testing.T) {
		kr, err := ParseKeyring("k1:"+k(2), k(0))
		if err != nil || kr.CurrentID() != "k1" {
			t.Fatal(err)
		}
		sealed, _ := kr.Seal([]byte("z"), nil)
		only, _ := ParseKeyring("k1:"+k(2), "")
		if _, err := only.Open(sealed, nil); err != nil {
			t.Fatal("legacy key replaced the listed one")
		}
	})
	for name, tc := range map[string][2]string{
		"empty":         {"", ""},
		"no colon":      {k(0), ""},
		"bad id":        {"a b:" + k(0), ""},
		"empty id":      {":" + k(0), ""},
		"long id":       {strings.Repeat("a", 65) + ":" + k(0), ""},
		"duplicate":     {"a:" + k(0) + ",a:" + k(1), ""},
		"not base64":    {"a:!!!", ""},
		"short key":     {"a:" + base64.StdEncoding.EncodeToString([]byte("short")), ""},
		"bad legacy":    {"", "nope"},
		"bad with good": {"a:" + k(0), "nope"},
	} {
		if _, err := ParseKeyring(tc[0], tc[1]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Errors never contain key material.
	_, err := ParseKeyring("bad id:"+k(0), "")
	if err == nil || strings.Contains(err.Error(), k(0)) {
		t.Fatalf("error leaks the key: %v", err)
	}
}

func TestSealedKeyID(t *testing.T) {
	kr, _ := ParseKeyring("abc:"+k(0), "")
	s, _ := kr.Seal([]byte("x"), nil)
	if id, ok := SealedKeyID(s); !ok || id != "abc" {
		t.Fatal(id)
	}
	if _, ok := SealedKeyID(nil); ok {
		t.Fatal("empty")
	}
	if _, ok := SealedKeyID([]byte{9, 'a'}); ok {
		t.Fatal("truncated")
	}
}

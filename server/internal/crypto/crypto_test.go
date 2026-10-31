package crypto

import (
	"bytes"
	"testing"
)

func ring(t *testing.T, cur string, ids ...string) *Keyring {
	t.Helper()
	m := map[string][]byte{}
	for _, id := range ids {
		m[id] = bytes.Repeat([]byte(id[:1]), 32)
	}
	k, err := NewKeyring(cur, m)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpen(t *testing.T) {
	k := ring(t, "k1", "k1")
	s, err := k.Seal([]byte("hello"), []byte("ctx"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := k.Open(s, []byte("ctx"))
	if err != nil || string(pt) != "hello" {
		t.Fatalf("got %q %v", pt, err)
	}
	if _, err := k.Open(s, []byte("other")); err == nil {
		t.Fatal("aad mismatch should fail")
	}
	s[len(s)-1] ^= 1
	if _, err := k.Open(s, []byte("ctx")); err == nil {
		t.Fatal("tamper should fail")
	}
	if _, err := k.Open([]byte{9, 1}, nil); err == nil {
		t.Fatal("short should fail")
	}
}

func TestRotation(t *testing.T) {
	old := ring(t, "a1", "a1")
	s, _ := old.Seal([]byte("x"), nil)
	rot := ring(t, "b2", "a1", "b2")
	if pt, err := rot.Open(s, nil); err != nil || string(pt) != "x" {
		t.Fatalf("old key open: %q %v", pt, err)
	}
	if _, err := ring(t, "b2", "b2").Open(s, nil); err == nil {
		t.Fatal("unknown key id should fail")
	}
}

func TestKeyringValidation(t *testing.T) {
	if _, err := NewKeyring("a", map[string][]byte{"a": []byte("short")}); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := NewKeyring("z", map[string][]byte{}); err == nil {
		t.Fatal("missing current accepted")
	}
}

func TestTokens(t *testing.T) {
	a, _ := NewToken()
	b, _ := NewToken()
	if a == b || len(a) != 43 {
		t.Fatalf("bad tokens %q %q", a, b)
	}
	h := HashToken(a)
	if len(h) != 64 || !TokenMatches(a, h) || TokenMatches(b, h) {
		t.Fatal("hash mismatch logic")
	}
}

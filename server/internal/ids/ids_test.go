package ids

import "testing"

func TestNewV7(t *testing.T) {
	a, b := NewV7(), NewV7()
	if a.Version() != 7 {
		t.Fatalf("version = %d", a.Version())
	}
	if a == b {
		t.Fatal("duplicate")
	}
	prev := NewV7().String()
	for i := 0; i < 100; i++ {
		cur := NewV7().String()
		if cur < prev {
			t.Fatalf("not ordered: %s < %s", cur, prev)
		}
		prev = cur
	}
}

package gmail

import (
	"reflect"
	"testing"
)

// D-015: both scopes are requested up front, in one consent.
func TestDefaultScopesRequestModifyAndSettings(t *testing.T) {
	want := []string{"https://www.googleapis.com/auth/gmail.modify", "https://www.googleapis.com/auth/gmail.settings.basic"}
	if got := (Config{}).scopes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default scopes %v, want %v", got, want)
	}
	if got := (Config{Scopes: []string{"x"}}).scopes(); len(got) != 1 {
		t.Fatalf("explicit scopes must win: %v", got)
	}
}

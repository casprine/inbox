package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestScrubURLHidesQueryAndKeepsCause(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://10.255.255.1/ping/SECRETKEY?q=rfc822msgid:<a@b.com>&token=abc", nil)
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Fatal("expected an error")
	}
	got := ScrubURL(err)
	for _, leak := range []string{"SECRETKEY", "rfc822msgid", "a@b.com", "token=abc"} {
		if strings.Contains(got.Error(), leak) {
			t.Fatalf("scrubbed error still contains %q: %v", leak, got)
		}
	}
	if !strings.Contains(got.Error(), "10.255.255.1") || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("host or cause lost: %v", got)
	}
	if plain := errors.New("x"); ScrubURL(plain) != plain {
		t.Fatal("non-URL errors pass through")
	}
}

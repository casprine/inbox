package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStoreIntegration(t *testing.T) {
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	ctx := context.Background()
	s := New(Config{Endpoint: ep, Bucket: "inbox-mail", AccessKey: "minio", SecretKey: "minio12345"})
	pfx := fmt.Sprintf("test-blob/%d/", time.Now().UnixNano())
	t.Cleanup(func() {
		l, _ := s.List(ctx, pfx, "")
		var ks []string
		for _, o := range l {
			ks = append(ks, o.Key)
		}
		_ = s.DeleteMany(ctx, ks)
	})

	body := "hello blob"
	if err := s.Put(ctx, pfx+"a", strings.NewReader(body), int64(len(body)), map[string]string{"env-from": "x@y.z"}); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, pfx+"a")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != body {
		t.Fatalf("got %q", got)
	}
	hi, err := s.Head(ctx, pfx+"a")
	if err != nil || hi.Size != int64(len(body)) || hi.Metadata["env-from"] != "x@y.z" {
		t.Fatalf("head %+v %v", hi, err)
	}
	if _, err := s.Get(ctx, pfx+"missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	if _, err := s.Head(ctx, pfx+"missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head missing: %v", err)
	}

	for _, k := range []string{"b", "c", "d"} {
		if err := s.Put(ctx, pfx+k, strings.NewReader(k), 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.List(ctx, pfx, "")
	if err != nil || len(l) != 4 {
		t.Fatalf("list %d %v", len(l), err)
	}
	l, err = s.List(ctx, pfx, pfx+"b")
	if err != nil || len(l) != 2 || l[0].Key != pfx+"c" {
		t.Fatalf("list after %+v %v", l, err)
	}

	u, err := s.PresignGet(ctx, pfx+"a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(pb) != body {
		t.Fatalf("presigned: %d %q", resp.StatusCode, pb)
	}

	if err := s.Delete(ctx, pfx+"a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, pfx+"a"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if err := s.DeleteMany(ctx, []string{pfx + "b", pfx + "c", pfx + "d"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(ctx, pfx, ""); len(l) != 0 {
		t.Fatalf("left over %d", len(l))
	}
}

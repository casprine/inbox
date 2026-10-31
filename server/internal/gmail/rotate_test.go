package gmail

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/ids"
)

func key(c string) string { return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(c, 32))) }

func TestRotateKeys(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	oldKeys, _ := crypto.ParseKeyring("k1:"+key("a"), "")
	enc, _ := oldKeys.Seal([]byte("refresh-1"), []byte(tokenAAD))
	if _, err := e.st.UpsertGmailAccount(ctx, ids.NewV7(), "sam@gmail.com", enc, "k1"); err != nil {
		t.Fatal(err)
	}

	// Nothing to do when already current.
	if res, err := RotateKeys(ctx, e.st, oldKeys); err != nil || res.Current != 1 || res.Resealed != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// New current key in front; the old one stays for reading.
	both, _ := crypto.ParseKeyring("k2:"+key("b")+",k1:"+key("a"), "")
	e.svc.Keys = both
	if tok, err := e.svc.AccessToken(ctx); err != nil || tok == "" {
		t.Fatalf("the old token must still work before rotation: %q %v", tok, err)
	}
	res, err := RotateKeys(ctx, e.st, both)
	if err != nil || res.Resealed != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	a, _ := e.st.GetGmailAccount(ctx)
	if id, _ := crypto.SealedKeyID(a.RefreshTokenEnc); id != "k2" || a.KeyID != "k2" {
		t.Fatalf("sealed under %q / column %q", id, a.KeyID)
	}
	if res, err := RotateKeys(ctx, e.st, both); err != nil || res.Current != 1 || res.Resealed != 0 {
		t.Fatalf("second run %+v %v", res, err)
	}

	// The old key can now be dropped: Gmail keeps working under k2 alone.
	onlyNew, _ := crypto.ParseKeyring("k2:"+key("b"), "")
	e.svc.Keys = onlyNew
	e.svc.mu.Lock()
	e.svc.access = nil
	e.svc.mu.Unlock()
	if tok, err := e.svc.AccessToken(ctx); err != nil || tok == "" {
		t.Fatalf("after dropping k1: %q %v", tok, err)
	}

	// Rotating with a keyring that lacks the key in use fails and changes nothing.
	other, _ := crypto.ParseKeyring("k9:"+key("z"), "")
	before, _ := e.st.GetGmailAccount(ctx)
	if _, err := RotateKeys(ctx, e.st, other); err == nil || !strings.Contains(err.Error(), `"k2"`) {
		t.Fatalf("want an error naming the missing key, got %v", err)
	}
	after, _ := e.st.GetGmailAccount(ctx)
	if string(before.RefreshTokenEnc) != string(after.RefreshTokenEnc) {
		t.Fatal("failed rotation changed the token")
	}

	// No account: nothing to rotate.
	e2 := setup(t)
	if res, err := RotateKeys(ctx, e2.st, onlyNew); err != nil || res != (RotateResult{}) {
		t.Fatalf("%+v %v", res, err)
	}
}

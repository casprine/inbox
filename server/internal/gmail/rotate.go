package gmail

import (
	"context"
	"errors"
	"fmt"

	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/store"
)

// RotateResult says what RotateKeys did.
type RotateResult struct {
	Resealed int // re-sealed under the current key
	Current  int // already under the current key
}

// RotateKeys re-seals the stored Gmail refresh token under the keyring's current key. Run it after
// adding a new first key to TOKEN_ENC_KEYS and before removing the old one. It is idempotent, and it
// fails without changing anything when a token is sealed with a key the keyring does not have.
func RotateKeys(ctx context.Context, st *store.Store, keys *crypto.Keyring) (RotateResult, error) {
	var res RotateResult
	err := st.WithTx(ctx, func(q *store.Queries) error {
		a, err := q.GetGmailAccount(ctx)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		id, ok := crypto.SealedKeyID(a.RefreshTokenEnc)
		if !ok {
			return errors.New("gmail: stored token is malformed")
		}
		if id == keys.CurrentID() && a.KeyID == id {
			res.Current++
			return nil
		}
		if !keys.Has(id) {
			return fmt.Errorf("gmail: the stored token is sealed with key %q, which is not in the keyring; add it back to TOKEN_ENC_KEYS", id)
		}
		plain, err := keys.Open(a.RefreshTokenEnc, []byte(tokenAAD))
		if err != nil {
			return fmt.Errorf("gmail: cannot open the stored token: %w", err)
		}
		enc, err := keys.Seal(plain, []byte(tokenAAD))
		if err != nil {
			return err
		}
		if err := q.UpdateGmailAccountSeal(ctx, a.ID, enc, keys.CurrentID()); err != nil {
			return err
		}
		res.Resealed++
		return nil
	})
	return res, err
}

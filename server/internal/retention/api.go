package retention

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/store"
)

const (
	settingDeleteToken = "data_delete_token"
	deleteTokenTTL     = 5 * time.Minute
	tokenAlphabet      = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I: it is typed by a person
)

// BlobStore is what the data purge needs from object storage.
type BlobStore interface {
	BlobDeleter
	List(ctx context.Context, prefix, after string) ([]blob.Info, error)
}

// API serves /v1/prefs and /v1/data.
type API struct {
	Store *store.Store
	Blob  BlobStore // nil: DELETE /data answers 503 blob_unavailable
	Log   *slog.Logger
	Now   func() time.Time
}

func (a *API) Routes(r chi.Router) {
	r.Get("/prefs", a.getPrefs)
	r.Put("/prefs", a.putPrefs)
	r.Post("/data:confirm", a.confirm)
	r.Delete("/data", a.purge)
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *API) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *API) internal(w http.ResponseWriter, err error) {
	a.log().Error("retention api", "err", err)
	httpapi.WriteProblem(w, http.StatusInternalServerError, "internal", "")
}

func (a *API) getPrefs(w http.ResponseWriter, r *http.Request) {
	p, err := Load(r.Context(), a.Store)
	if err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, p)
}

// putPrefs applies a partial update: omitted fields keep their value, retention_days: null keeps mail forever.
func (a *API) putPrefs(w http.ResponseWriter, r *http.Request) {
	var patch Patch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&patch); err != nil {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	cur, err := Load(r.Context(), a.Store)
	if err != nil {
		a.internal(w, err)
		return
	}
	next, err := patch.Apply(cur)
	var fe FieldError
	if errors.As(err, &fe) {
		httpapi.WriteProblemExt(w, http.StatusUnprocessableEntity, "invalid_prefs", fe.Message, map[string]any{"field": fe.Field})
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	if err := Save(r.Context(), a.Store, next); err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, next)
}

type deleteToken struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires_at"`
}

func newToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = tokenAlphabet[int(b[i])%len(tokenAlphabet)]
	}
	return "DELETE-" + string(b), nil
}

// confirm issues the token DELETE /data needs. Asking again replaces the previous token.
func (a *API) confirm(w http.ResponseWriter, r *http.Request) {
	tok, err := newToken()
	if err != nil {
		a.internal(w, err)
		return
	}
	t := deleteToken{Token: tok, Expires: a.now().Add(deleteTokenTTL)}
	if err := a.Store.SetSetting(r.Context(), settingDeleteToken, t); err != nil {
		a.internal(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"token": t.Token, "expires_at": t.Expires, "expires_in": int(deleteTokenTTL / time.Second)})
}

// consume checks and burns the token.
func (a *API) consume(ctx context.Context, given string) (bool, error) {
	raw, err := a.Store.GetSetting(ctx, settingDeleteToken)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var t deleteToken
	if json.Unmarshal(raw, &t) != nil || t.Token == "" {
		return false, nil
	}
	ok := subtle.ConstantTimeCompare([]byte(t.Token), []byte(given)) == 1 && a.now().Before(t.Expires)
	if ok {
		if err := a.Store.DeleteSetting(ctx, settingDeleteToken); err != nil { // single use
			return false, err
		}
	}
	return ok, nil
}

var purgePrefixes = []string{"raw/", "att/", "gmail/"}

// purge deletes every stored copy of your mail (rows, search index, raw messages, attachments).
// Businesses, rules, devices, the Gmail connection and everything in Gmail itself are untouched.
func (a *API) purge(w http.ResponseWriter, r *http.Request) {
	if a.Blob == nil {
		httpapi.WriteProblem(w, http.StatusServiceUnavailable, "blob_unavailable", "object storage is not configured")
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpapi.WriteProblem(w, http.StatusBadRequest, "invalid_body", `send {"confirm": "<token from POST /data:confirm>"}`)
		return
	}
	ok, err := a.consume(r.Context(), in.Confirm)
	if err != nil {
		a.internal(w, err)
		return
	}
	if !ok {
		httpapi.WriteProblem(w, http.StatusForbidden, "confirmation_invalid", "The confirmation is wrong or expired; ask for a new one")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	p := &Pruner{Store: a.Store, Blob: a.Blob, Log: a.Log, Now: a.Now}
	messages, blobs, err := p.deleteMessages(ctx, nil)
	if err != nil {
		a.internal(w, err)
		return
	}
	// Sweep objects no row owns (failed rows without raw, interrupted ingests, earlier orphans).
	for _, prefix := range purgePrefixes {
		infos, err := a.Blob.List(ctx, prefix, "")
		if err != nil {
			a.internal(w, err)
			return
		}
		keys := make([]string, len(infos))
		for i, in := range infos {
			keys[i] = in.Key
		}
		if err := a.Blob.DeleteMany(ctx, keys); err != nil {
			a.internal(w, err)
			return
		}
		blobs += len(keys)
	}
	if err := a.Store.WithTx(ctx, func(q *store.Queries) error { return q.ResetAfterPurge(ctx) }); err != nil {
		a.internal(w, err)
		return
	}
	a.log().Info("data purged", "messages", messages, "blobs", blobs)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"deleted_messages": messages, "deleted_blobs": blobs})
}

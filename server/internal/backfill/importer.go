package backfill

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/ingest/parse"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/store"
)

// Importer implements mail.GmailFetcher: it re-imports a store_failed message from Gmail.
type Importer struct {
	Gmail  *gmail.Service
	Store  *store.Store
	Blob   *blob.Store
	Ingest *ingest.Worker
}

func (i *Importer) RetryFromGmail(ctx context.Context, failed store.Message) (uuid.UUID, error) {
	hdr, err := i.Store.GetOrigMessageID(ctx, failed.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return uuid.Nil, err
	}
	if hdr == "" {
		return uuid.Nil, mail.ErrNotInGmail // the Worker never told us the Message-ID
	}
	gid, thread, found, err := i.Gmail.FindByRFC822(ctx, hdr)
	switch {
	case errors.Is(err, gmail.ErrNoAccount), errors.Is(err, gmail.ErrNeedsReauth), errors.Is(err, gmail.ErrNotConfigured):
		return uuid.Nil, mail.ErrNeedsGmail
	case err != nil:
		return uuid.Nil, err
	case !found:
		return uuid.Nil, mail.ErrNotInGmail
	}
	raw, err := i.Gmail.GetRaw(ctx, gid)
	if err != nil {
		return uuid.Nil, err
	}
	p, err := parse.Parse(raw.Raw)
	if err != nil {
		return uuid.Nil, mail.ErrNotInGmail
	}
	key := "gmail/" + gid + ".eml"
	if err := i.Blob.Put(ctx, key, bytes.NewReader(raw.Raw), int64(len(raw.Raw)), nil); err != nil {
		return uuid.Nil, err
	}
	// The failed row is removed first so the real message can take its place; it is restored on error.
	if _, err := i.Store.DeleteFailedMessage(ctx, failed.ID); err != nil {
		return uuid.Nil, err
	}
	envTo := ""
	if failed.EnvTo != nil {
		envTo = *failed.EnvTo
	}
	when := failed.ReceivedAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	res, err := i.Ingest.Ingest(ctx, p, ingest.RawOpts{Key: key, EnvTo: envTo, ReceivedAt: when, Size: len(raw.Raw),
		Source: "edge", GmailID: gid, GmailThread: thread})
	if err == nil && !res.Inserted {
		err = errors.New("message already stored")
	}
	if err != nil {
		_, _ = i.Store.InsertMessage(context.WithoutCancel(ctx), failed)
		_ = i.Blob.Delete(context.WithoutCancel(ctx), key)
		return uuid.Nil, err
	}
	return res.MessageID, nil
}

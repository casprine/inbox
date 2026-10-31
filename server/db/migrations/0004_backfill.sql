-- +goose Up
ALTER TABLE backfills
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN last_error text;
-- the original RFC 5322 Message-ID of a store_failed row, so Retry can fetch it from Gmail
ALTER TABLE messages ADD COLUMN orig_message_id text;

-- +goose Down
ALTER TABLE messages DROP COLUMN IF EXISTS orig_message_id;
ALTER TABLE backfills DROP COLUMN IF EXISTS created_at, DROP COLUMN IF EXISTS last_error;

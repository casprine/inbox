-- +goose Up
ALTER TABLE gmail_sync
  ADD COLUMN last_attempt_at timestamptz,
  ADD COLUMN last_synced_at  timestamptz;
CREATE INDEX messages_gmail_thread ON messages (gmail_thread_id) WHERE gmail_thread_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS messages_gmail_thread;
ALTER TABLE gmail_sync DROP COLUMN IF EXISTS last_attempt_at, DROP COLUMN IF EXISTS last_synced_at;

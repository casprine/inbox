-- +goose Up
CREATE TABLE gmail_oauth_states (
  state      text PRIMARY KEY,
  device_id  uuid,
  verifier   text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
-- single-owner server: at most one connected Gmail account
CREATE UNIQUE INDEX gmail_accounts_single ON gmail_accounts ((true));

-- +goose Down
DROP INDEX IF EXISTS gmail_accounts_single;
DROP TABLE IF EXISTS gmail_oauth_states;

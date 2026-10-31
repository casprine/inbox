-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA public;

CREATE TYPE route_state   AS ENUM ('pending','routed','unrouted');
CREATE TYPE msg_state     AS ENUM ('stored','failed');
CREATE TYPE msg_source    AS ENUM ('edge','backfill','gmail_thread');
CREATE TYPE msg_dir       AS ENUM ('in','out');
CREATE TYPE gmail_state   AS ENUM ('pending','synced','not_found','failed','paused');
CREATE TYPE routing_state AS ENUM ('ok','to_email','none','unknown');

CREATE TABLE businesses (
  id                 uuid PRIMARY KEY,
  name               text NOT NULL,
  domain             text NOT NULL UNIQUE,
  cf_zone_id         text NOT NULL,
  sort_order         int  NOT NULL DEFAULT 0,
  hidden             boolean NOT NULL DEFAULT false,
  enabled            boolean NOT NULL DEFAULT true,
  routing            routing_state NOT NULL DEFAULT 'unknown',
  routing_checked_at timestamptz,
  last_message_at    timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE business_addresses (
  business_id uuid NOT NULL REFERENCES businesses ON DELETE CASCADE,
  local_part  text NOT NULL,
  msg_count   int  NOT NULL DEFAULT 0,
  last_at     timestamptz,
  PRIMARY KEY (business_id, local_part)
);

CREATE TABLE rules (
  id                   uuid PRIMARY KEY,
  name                 text NOT NULL,
  position             int NOT NULL,
  enabled              boolean NOT NULL DEFAULT true,
  business_id          uuid REFERENCES businesses ON DELETE CASCADE,
  match_to_local       text,
  match_from           text,
  match_subject        text,
  gmail_label          text,
  gmail_archive        boolean NOT NULL DEFAULT false,
  gmail_mark_read      boolean NOT NULL DEFAULT false,
  label_only           boolean NOT NULL DEFAULT true,
  imported_from_filter text,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
  id               uuid PRIMARY KEY,
  seq              bigint GENERATED ALWAYS AS IDENTITY,
  business_id      uuid REFERENCES businesses ON DELETE SET NULL,
  direction        msg_dir NOT NULL DEFAULT 'in',
  source           msg_source NOT NULL,
  state            msg_state NOT NULL DEFAULT 'stored',
  failure_reason   text,
  dismissed_at     timestamptz,
  message_id_hdr   text NOT NULL UNIQUE,
  env_to           text,
  env_to_all       text[] NOT NULL DEFAULT '{}',
  to_local         text,
  to_tag           text,
  from_addr        text,
  from_name        text,
  subject          text,
  snippet          text,
  in_reply_to      text,
  thread_key       text NOT NULL,
  received_at      timestamptz NOT NULL,
  size_bytes       int,
  attachment_count int NOT NULL DEFAULT 0,
  raw_key          text,
  auth_results     jsonb,
  route            route_state NOT NULL DEFAULT 'pending',
  rule_id          uuid REFERENCES rules ON DELETE SET NULL,
  is_read          boolean NOT NULL DEFAULT false,
  read_at          timestamptz,
  archived_at      timestamptz,
  gmail_id         text,
  gmail_thread_id  text,
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_list     ON messages (business_id, received_at DESC, id DESC) WHERE archived_at IS NULL AND state = 'stored';
CREATE INDEX messages_unread   ON messages (business_id) WHERE NOT is_read AND archived_at IS NULL AND state = 'stored';
CREATE INDEX messages_addr     ON messages (business_id, to_local, received_at DESC);
CREATE INDEX messages_thread   ON messages (thread_key, received_at);
CREATE INDEX messages_unrouted ON messages (received_at DESC) WHERE route = 'unrouted' AND state = 'stored';
CREATE INDEX messages_failed   ON messages (received_at DESC) WHERE state = 'failed' AND dismissed_at IS NULL;
CREATE INDEX messages_seq      ON messages (seq);
CREATE INDEX messages_raw_key  ON messages (raw_key) WHERE raw_key IS NOT NULL;

CREATE TABLE message_search (
  message_id uuid PRIMARY KEY REFERENCES messages ON DELETE CASCADE,
  body_text  text,
  fts        tsvector NOT NULL
);
CREATE INDEX message_search_fts ON message_search USING gin (fts);
CREATE INDEX messages_from_trgm ON messages USING gin (from_addr gin_trgm_ops, from_name gin_trgm_ops);

CREATE TABLE attachments (
  id         uuid PRIMARY KEY,
  message_id uuid NOT NULL REFERENCES messages ON DELETE CASCADE,
  filename   text,
  mime       text,
  size_bytes int,
  content_id text,
  inline     boolean NOT NULL DEFAULT false,
  r2_key     text NOT NULL
);
CREATE INDEX attachments_message ON attachments (message_id);

CREATE TABLE gmail_accounts (
  id                uuid PRIMARY KEY,
  email             text NOT NULL,
  refresh_token_enc bytea NOT NULL,
  key_id            text NOT NULL,
  status            text NOT NULL CHECK (status IN ('connected','needs_reauth','revoked')),
  connected_at      timestamptz NOT NULL,
  last_write_at     timestamptz
);

CREATE TABLE gmail_labels (
  account_id     uuid NOT NULL REFERENCES gmail_accounts ON DELETE CASCADE,
  name           text NOT NULL,
  gmail_label_id text NOT NULL,
  PRIMARY KEY (account_id, name)
);

CREATE TABLE gmail_sync (
  message_id      uuid PRIMARY KEY REFERENCES messages ON DELETE CASCADE,
  want_label      text,
  want_archived   boolean NOT NULL DEFAULT false,
  want_read       boolean NOT NULL DEFAULT false,
  applied         jsonb NOT NULL DEFAULT '{}',
  state           gmail_state NOT NULL DEFAULT 'pending',
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz,
  last_error      text,
  labels_only     boolean NOT NULL DEFAULT false,
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gmail_sync_due ON gmail_sync (next_attempt_at) WHERE state IN ('pending','paused');

CREATE TABLE ingest_log (
  id         bigserial PRIMARY KEY,
  ingest_key text NOT NULL,
  message_id uuid REFERENCES messages ON DELETE CASCADE,
  at         timestamptz NOT NULL DEFAULT now(),
  level      text NOT NULL,
  line       text NOT NULL
);
CREATE INDEX ingest_log_key ON ingest_log (ingest_key, at);

CREATE TABLE backfills (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses ON DELETE CASCADE,
  since        date NOT NULL,
  until        date,
  status       text NOT NULL CHECK (status IN ('queued','running','paused','done','failed','cancelled')),
  page_token   text,
  estimated    int,
  scanned      int NOT NULL DEFAULT 0,
  imported     int NOT NULL DEFAULT 0,
  sorted       int NOT NULL DEFAULT 0,
  unrouted     int NOT NULL DEFAULT 0,
  skipped      int NOT NULL DEFAULT 0,
  paused_until timestamptz,
  started_at   timestamptz,
  finished_at  timestamptz
);

CREATE TABLE devices (
  id           uuid PRIMARY KEY,
  name         text,
  apple_sub    text NOT NULL,
  token_hash   bytea NOT NULL UNIQUE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz,
  revoked_at   timestamptz
);

CREATE TABLE consumers (
  id           uuid PRIMARY KEY,
  kind         text NOT NULL,
  token_hash   bytea NOT NULL UNIQUE,
  cursor_seq   bigint NOT NULL DEFAULT 0,
  last_seen_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idempotency_keys (
  key       text NOT NULL,
  device_id uuid NOT NULL REFERENCES devices ON DELETE CASCADE,
  response  jsonb,
  at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, key)
);

CREATE TABLE settings (
  key   text PRIMARY KEY,
  value jsonb NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS settings, idempotency_keys, consumers, devices, backfills, ingest_log,
  gmail_sync, gmail_labels, gmail_accounts, attachments, message_search, messages, rules,
  business_addresses, businesses CASCADE;
DROP TYPE IF EXISTS routing_state, gmail_state, msg_dir, msg_source, msg_state, route_state;

-- +goose Up
-- Sign-in is a server password now (D-012): devices no longer carry an Apple subject, and the
-- first-claim owner / bootstrap code settings are gone. Existing device tokens keep working.
ALTER TABLE devices DROP COLUMN apple_sub;
DELETE FROM settings WHERE key IN ('owner_apple_sub', 'bootstrap_code_hash');

-- +goose Down
ALTER TABLE devices ADD COLUMN apple_sub text NOT NULL DEFAULT '';

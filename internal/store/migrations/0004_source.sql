-- +goose Up
-- Module API (feature 028): submissions created by another module (the hr
-- module) carry their source, the source's reference and an idempotency key;
-- module calls only see submissions of their own source.
ALTER TABLE signing_submissions
  ADD COLUMN source text NOT NULL DEFAULT '' CHECK (source IN ('', 'hr')),
  ADD COLUMN source_ref text NOT NULL DEFAULT '' CHECK (char_length(source_ref) <= 64),
  ADD COLUMN idempotency_key text NOT NULL DEFAULT '' CHECK (char_length(idempotency_key) <= 128);
CREATE UNIQUE INDEX signing_submissions_idem ON signing_submissions (tenant_id, source, idempotency_key)
  WHERE idempotency_key <> '';

-- +goose Down
DROP INDEX IF EXISTS signing_submissions_idem;
ALTER TABLE signing_submissions DROP COLUMN IF EXISTS idempotency_key, DROP COLUMN IF EXISTS source_ref,
  DROP COLUMN IF EXISTS source;

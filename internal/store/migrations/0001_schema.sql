-- +goose Up
-- Signing module schema (specs/027-signing-v4/data-model.md). Every table is
-- tenant-owned (RLS in 0003). Ids are app-generated UUIDv7. Documents, images
-- and audit trails live in the object store; the database holds only their
-- keys and hashes. Private keys are stored only sealed (module KEK) or
-- PIN-encrypted, never in clear.

CREATE TABLE signing_template_folders (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  parent_id   uuid REFERENCES signing_template_folders(id) ON DELETE RESTRICT,
  name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120 AND position('/' in name) = 0),
  path        text NOT NULL,
  sort_order  int  NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text NOT NULL DEFAULT '',
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX signing_folders_name ON signing_template_folders (tenant_id, parent_id, lower(name)) NULLS NOT DISTINCT;

CREATE TABLE signing_templates (
  id                  uuid PRIMARY KEY,
  tenant_id           uuid NOT NULL,
  folder_id           uuid REFERENCES signing_template_folders(id) ON DELETE RESTRICT,
  name                text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
  description         text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
  tags                text[] NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','archived')),
  pdf_key             text NOT NULL,
  pdf_sha256          text NOT NULL,
  pdf_size            bigint NOT NULL,
  pdf_pages           int NOT NULL,
  file_name           text NOT NULL DEFAULT '',
  pdf_signed          boolean NOT NULL DEFAULT false,
  parties             jsonb NOT NULL DEFAULT '[]',
  fields              jsonb NOT NULL DEFAULT '[]',
  default_expiry_days int CHECK (default_expiry_days IS NULL OR default_expiry_days BETWEEN 1 AND 365),
  default_reminder    jsonb,
  version             int NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          text NOT NULL DEFAULT '',
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX signing_templates_name ON signing_templates (tenant_id, folder_id, lower(name)) NULLS NOT DISTINCT;
CREATE INDEX signing_templates_list ON signing_templates (tenant_id, status, updated_at DESC);
CREATE INDEX signing_templates_tags ON signing_templates USING gin (tags);

CREATE TABLE signing_submissions (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL,
  template_id        uuid NOT NULL REFERENCES signing_templates(id) ON DELETE RESTRICT,
  name               text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
  pdf_key            text NOT NULL,
  pdf_sha256         text NOT NULL,
  fields             jsonb NOT NULL DEFAULT '[]',
  parties            jsonb NOT NULL DEFAULT '[]',
  mode               text NOT NULL CHECK (mode IN ('sequential','parallel')),
  status             text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','in_progress','completed','expired','cancelled')),
  expires_at         timestamptz,
  reminder           jsonb,
  current_version    int NOT NULL DEFAULT 0,
  final_version      int,
  audit_trail_key    text NOT NULL DEFAULT '',
  audit_trail_sha256 text NOT NULL DEFAULT '',
  sent_at            timestamptz,
  completed_at       timestamptz,
  cancelled_at       timestamptz,
  cancel_reason      text NOT NULL DEFAULT '' CHECK (char_length(cancel_reason) <= 500),
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         text NOT NULL DEFAULT '',
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signing_submissions_list ON signing_submissions (tenant_id, status, created_at DESC);
CREATE INDEX signing_submissions_sender ON signing_submissions (tenant_id, created_by);
CREATE INDEX signing_submissions_template ON signing_submissions (template_id);
CREATE INDEX signing_submissions_expiry ON signing_submissions (expires_at) WHERE status = 'in_progress' AND expires_at IS NOT NULL;

CREATE TABLE signing_signers (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  submission_id    uuid NOT NULL REFERENCES signing_submissions(id) ON DELETE CASCADE,
  user_id          text NOT NULL,
  name             text NOT NULL DEFAULT '',
  email            text NOT NULL DEFAULT '',
  party            text NOT NULL,
  position         int NOT NULL,
  status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','invited','opened','signed','declined')),
  "values"         jsonb NOT NULL DEFAULT '{}',
  method           text NOT NULL DEFAULT '' CHECK (method IN ('','local_certificate','qes')),
  certificate_id   uuid,
  cert_subject     text NOT NULL DEFAULT '',
  cert_serial      text NOT NULL DEFAULT '',
  cert_issuer      text NOT NULL DEFAULT '',
  ip               text NOT NULL DEFAULT '',
  user_agent       text NOT NULL DEFAULT '',
  decline_reason   text NOT NULL DEFAULT '' CHECK (char_length(decline_reason) <= 500),
  invited_at       timestamptz,
  opened_at        timestamptz,
  signed_at        timestamptz,
  declined_at      timestamptz,
  reminders_sent   int NOT NULL DEFAULT 0,
  next_reminder_at timestamptz,
  mail_error       text NOT NULL DEFAULT ''
);
CREATE INDEX signing_signers_inbox ON signing_signers (tenant_id, user_id, status);
CREATE INDEX signing_signers_submission ON signing_signers (submission_id, position);
CREATE INDEX signing_signers_reminder ON signing_signers (next_reminder_at) WHERE status IN ('invited','opened');

CREATE TABLE signing_document_versions (
  submission_id uuid NOT NULL REFERENCES signing_submissions(id) ON DELETE CASCADE,
  version       int NOT NULL,
  tenant_id     uuid NOT NULL,
  object_key    text NOT NULL,
  sha256        text NOT NULL,
  size          bigint NOT NULL,
  signer_id     uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (submission_id, version)
);

CREATE TABLE signing_certificates (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  kind              text NOT NULL CHECK (kind IN ('ca','system','signer','admin')),
  issuer_id         uuid REFERENCES signing_certificates(id) ON DELETE RESTRICT,
  owner_user_id     text,
  subject_cn        text NOT NULL,
  email             text NOT NULL DEFAULT '',
  serial            text NOT NULL,
  not_before        timestamptz NOT NULL,
  not_after         timestamptz NOT NULL,
  cert_der          bytea NOT NULL,
  key_protection    text NOT NULL CHECK (key_protection IN ('pin','sealed')),
  key_blob          bytea NOT NULL,
  status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked','expired','needs_reissue')),
  revoked_at        timestamptz,
  revocation_reason text NOT NULL DEFAULT '',
  failed_pin_count  int NOT NULL DEFAULT 0,
  locked_until      timestamptz,
  crl_der           bytea,
  crl_next_update   timestamptz,
  superseded_by     uuid,
  created_at        timestamptz NOT NULL DEFAULT now(),
  created_by        text NOT NULL DEFAULT '',
  CHECK ((kind = 'ca') = (issuer_id IS NULL)),
  CHECK (key_protection <> 'pin' OR kind = 'signer')
);
CREATE UNIQUE INDEX signing_certificates_serial ON signing_certificates (tenant_id, serial);
CREATE UNIQUE INDEX signing_certificates_active_signer ON signing_certificates (tenant_id, owner_user_id)
  WHERE kind = 'signer' AND status = 'active';
CREATE INDEX signing_certificates_list ON signing_certificates (tenant_id, kind, status, created_at DESC);

CREATE TABLE signing_qes_preparations (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  signer_id        uuid NOT NULL REFERENCES signing_signers(id) ON DELETE CASCADE,
  chain_der        bytea[] NOT NULL,
  signed_attrs     bytea NOT NULL,
  digest           bytea NOT NULL,
  prepared_key     text NOT NULL,
  based_on_version int NOT NULL,
  "values"         jsonb NOT NULL DEFAULT '{}',
  expires_at       timestamptz NOT NULL,
  used_at          timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signing_qes_expiry ON signing_qes_preparations (expires_at);

CREATE TABLE signing_events (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL,
  submission_id  uuid REFERENCES signing_submissions(id) ON DELETE CASCADE,
  signer_id      uuid,
  certificate_id uuid,
  actor_user_id  text,
  type           text NOT NULL,
  ip             text NOT NULL DEFAULT '',
  meta           jsonb NOT NULL DEFAULT '{}',
  at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signing_events_submission ON signing_events (tenant_id, submission_id, at);
CREATE INDEX signing_events_certificate ON signing_events (tenant_id, certificate_id, at) WHERE certificate_id IS NOT NULL;

CREATE TABLE signing_jobs (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  kind            text NOT NULL CHECK (kind IN ('audit_trail')),
  submission_id   uuid NOT NULL REFERENCES signing_submissions(id) ON DELETE CASCADE,
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text NOT NULL DEFAULT '',
  done_at         timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX signing_jobs_once ON signing_jobs (submission_id, kind);
CREATE INDEX signing_jobs_due ON signing_jobs (next_attempt_at) WHERE done_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS signing_jobs;
DROP TABLE IF EXISTS signing_events;
DROP TABLE IF EXISTS signing_qes_preparations;
DROP TABLE IF EXISTS signing_certificates;
DROP TABLE IF EXISTS signing_document_versions;
DROP TABLE IF EXISTS signing_signers;
DROP TABLE IF EXISTS signing_submissions;
DROP TABLE IF EXISTS signing_templates;
DROP TABLE IF EXISTS signing_template_folders;

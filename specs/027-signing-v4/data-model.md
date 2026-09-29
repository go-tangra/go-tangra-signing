# Data Model: Document Signing Module for v4

**Feature**: 027-signing-v4 | **Plan**: [plan.md](./plan.md)

Database `signing`, runtime role `signing_app` (LOGIN NOBYPASSRLS). Every table has
`tenant_id uuid NOT NULL` with `FORCE ROW LEVEL SECURITY` and the policy
`tenant_id = current_setting('app.tenant_id')::uuid` (system scope sets
`app.system = on` for the platform task types, D12). Ids are UUIDv7. Times are
`timestamptz`. Objects live in the bucket `signing` under `tenants/<tenant>/…`.

## template_folders

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| parent_id | uuid null | FK template_folders ON DELETE RESTRICT |
| name | text | 1..120, no `/` |
| path | text | materialized `/HR/Contracts`, maintained on rename/move |
| sort_order | int | |
| created_at/by, updated_at/by | | |

Unique `(tenant_id, parent_id, name)` (NULLS NOT DISTINCT). Delete only when no child
folder and no template (FR-040). Depth ≤ 8.

## templates

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| folder_id | uuid null | FK template_folders ON DELETE RESTRICT |
| name | text | 1..200, unique per tenant+folder |
| description | text | ≤ 2000 |
| tags | text[] | each 1..40, ≤ 20; GIN index |
| status | text | `draft` \| `active` \| `archived` |
| pdf_key, pdf_sha256, pdf_size, pdf_pages, file_name | | object of the uploaded PDF (immutable; re-upload = new key) |
| pdf_signed | bool | PDF already carries signatures (edge case) |
| parties | jsonb | `[{"key":"p1","name":"Employee"}]` 1..50 |
| fields | jsonb | `[]Field` ≤ 500 (below) |
| default_expiry_days | int null | 1..365 |
| default_reminder | jsonb null | `{"interval_days":3,"max":5}` |
| created_at/by, updated_at/by, version | | optimistic concurrency for the builder |

**Field**: `{id, name, type, party, page, x, y, w, h (0..1 fractions), required,
font, font_size, options[], default, conditions {mode: all|any, rules:[{field, op,
value}], effect: visible|required}, formula}`. Types: `text, number, signature,
initials, date, checkbox, select, radio, image, file, cells, stamp`.
Validation: unique names, party exists, page ≤ pdf_pages, geometry in [0,1],
rules valid and acyclic (D8).

State: draft → active ⇄ archived; delete refused while a submission in
`draft|in_progress` references it (FR-004).

## submissions

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| template_id | uuid | FK templates ON DELETE RESTRICT |
| name | text | copied from template, editable |
| pdf_key, pdf_sha256 | | the template PDF version it was created from (FR-008) |
| fields | jsonb | frozen copy of template fields + prefills (`prefill`) |
| parties | jsonb | frozen |
| mode | text | `sequential` \| `parallel` |
| status | text | `draft` \| `in_progress` \| `completed` \| `expired` \| `cancelled` |
| expires_at | timestamptz null | |
| reminder | jsonb null | `{"interval_days":3,"max":5}` |
| current_version | int | latest document version number (0 = original) |
| final_version | int null | set on completion |
| audit_trail_key, audit_trail_sha256 | null | set by the audit job |
| sent_at, completed_at, cancelled_at | | |
| cancel_reason | text | ≤ 500 |
| created_at, created_by (sender), updated_at | | |

Indexes: `(tenant_id, status, created_at desc)`, `(tenant_id, created_by)`,
`(status, expires_at) WHERE status='in_progress'`.

State machine: draft →send→ in_progress → completed (last signer) | cancelled (cancel,
decline) | expired (expiry task). Terminal states are final. Delete allowed in any state
(removes objects).

## signers

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| submission_id | uuid | FK ON DELETE CASCADE |
| user_id | uuid | platform user (decision 2) |
| name, email | text | from auth Contacts at creation/replace (D1) |
| party | text | template party key |
| position | int | order (sequential) |
| status | text | `pending` \| `invited` \| `opened` \| `signed` \| `declined` |
| values | jsonb | submitted values (after signing); image refs as object keys |
| method | text null | `local_certificate` \| `qes` |
| certificate_id | uuid null | local certificate used |
| cert_subject, cert_serial, cert_issuer | text null | recorded for QES and local |
| ip, user_agent | text null | at signing |
| decline_reason | text null | |
| invited_at, opened_at, signed_at, declined_at | | |
| reminders_sent | int | |
| next_reminder_at | timestamptz null | guard for idempotent reminders (D12) |
| mail_error | text null | last e-mail failure (reason code only) |

Indexes: `(tenant_id, user_id, status)` ("To sign", "Signed by me"),
`(submission_id, position)`, `(next_reminder_at) WHERE status IN ('invited','opened')`.

State: pending →invite→ invited →open→ opened → signed | declined. Replace is allowed
only while pending/invited/opened (FR-015).

## document_versions

| column | type | notes |
|---|---|---|
| submission_id, version | uuid, int | PK; version 0 = original |
| tenant_id | uuid | |
| object_key, sha256, size | | `tenants/<t>/submissions/<s>/v<n>.pdf` |
| signer_id | uuid null | who produced it |
| created_at | | |

## certificates

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| kind | text | `ca` \| `system` \| `signer` \| `admin` |
| issuer_id | uuid null | FK certificates (null for CA) |
| owner_user_id | uuid null | signer/admin owner |
| subject_cn, email, serial (hex, unique per tenant) | text | |
| not_before, not_after | | |
| cert_der | bytea | |
| key_protection | text | `pin` \| `sealed` |
| key_blob | bytea | PIN envelope (D5) or sealed blob (AD `cert:<id>`) |
| status | text | `active` \| `revoked` \| `expired` \| `needs_reissue` |
| revoked_at, revocation_reason | | RFC 5280 reason codes |
| failed_pin_count | int | |
| locked_until | timestamptz null | |
| crl_der, crl_next_update | null | CA only |
| superseded_by | uuid null | CA renewal chain (FR-036) |
| created_at/by | | |

Constraints: at most one `active` signer certificate per `(tenant_id, owner_user_id)`
(partial unique index); `kind='ca'` has `issuer_id IS NULL`; `key_protection='pin'` only
for `kind='signer'`.

## qes_preparations

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| signer_id | uuid | FK signers ON DELETE CASCADE |
| chain_der | bytea[] | chosen card chain, leaf first |
| signed_attrs | bytea | DER to be signed |
| digest | bytea | SHA-256 given to BISS |
| prepared_key | text | object key of the prepared PDF |
| based_on_version | int | document version the preparation is built on |
| values | jsonb | the signer's evaluated values |
| expires_at | timestamptz | now + 10 min |
| used_at | timestamptz null | one use only |

Complete refuses if expired, used, or `based_on_version` ≠ `submissions.current_version`.

## events (signing history, FR-044)

| column | type | notes |
|---|---|---|
| id, tenant_id | uuid | |
| submission_id | uuid null | |
| signer_id, certificate_id | uuid null | |
| actor_user_id | uuid null | null = system |
| type | text | closed vocabulary (contracts/audit-events.md) |
| ip | text null | |
| meta | jsonb | no field values (reason codes, method, version) |
| at | timestamptz | |

Index `(tenant_id, submission_id, at)`. Retained with the submission (deleted with it).

## signing_jobs

`id, tenant_id, kind ('audit_trail'), submission_id, attempts, next_attempt_at,
last_error, done_at` — drained by the in-process worker (D9), max 10 attempts with
backoff.

## audit_events (hypertable)

Framework audit schema (actor, tenant, action, object, outcome, correlation id); closed
vocabulary in contracts/audit-events.md; never field values, PINs or keys.

## Objects (bucket `signing`)

| key | content |
|---|---|
| `tenants/<t>/templates/<tpl>/<uuid>.pdf` | uploaded template PDF |
| `tenants/<t>/submissions/<s>/v<n>.pdf` | document versions |
| `tenants/<t>/submissions/<s>/audit-trail.pdf` | audit trail |
| `tenants/<t>/submissions/<s>/signer/<sg>/<field>.<png\|jpg\|bin>` | signature/image/file field uploads |
| `tenants/<t>/qes/<prep>.pdf` | prepared QES document (10 min) |
| `tenants/<t>/uploads/<uuid>.pdf` | transient verify/admin-sign uploads (1 h) |

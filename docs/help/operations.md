---
title: Operate signing
summary: Configuration keys and defaults, the key-encryption key, object store, scheduler tasks, service policies, qualified-signature setup and fixes for common start-up and delivery problems.
kind: reference
order: 90
tags: [signing, operations, configuration, policy, scheduler, troubleshooting]
audience: operator
---
The module runs as `signingsvc` (image `ghcr.io/go-tangra/go-tangra-signing`).

```sh
signingsvc -config /app/config.yaml             # serve; applies migrations
signingsvc bootstrap -config /app/config.yaml   # apply migrations and exit
signingsvc version
```

It needs PostgreSQL/TimescaleDB (database `signing`, application role
without `BYPASSRLS`), an S3-compatible bucket, the platform Valkey (event
bus and rate limits), a 32-byte key-encryption key, and on the mesh: auth,
the gateway, notification (mail), scheduler (expiry and reminders) and
warden (timestamp-authority credentials).

## Configuration

YAML; unknown keys are refused. Besides the framework keys (`service_name`,
`trust_domain`, `env`, `identity`, `authz`, `server`, `admin`, `discovery`,
`limits`):

| Key | Default | Notes |
|---|---|---|
| `db.dsn` | — | required; production needs `sslmode=verify-full` or `verify-ca` |
| `db.migrate_dsn` | `db.dsn` | migration role |
| `db.max_conns` | 16 | 0–256 |
| `valkey.addresses` | — | required; Valkey user `signing`, TLS |
| `valkey.username`, `valkey.password`, `valkey.ca_file` | — | |
| `valkey.allow_plaintext` | false | refused in production |
| `events.enabled` | true | publish signing events (live UI updates, other modules) |
| `kek.source` | `file` | `file` (with `kek.path`) or `env` (with `kek.env`) |
| `object_store.endpoint` | — | required, `host:port` without scheme |
| `object_store.bucket`, `object_store.region` | `signing`, `us-east-1` | |
| `object_store.use_ssl` | true | false is refused in production |
| `object_store.access_key`, `object_store.secret_key` | — | required |
| `auth.service`, `notification.service`, `warden.service` | `auth`, `notification`, `warden` | discovery names |
| `task_scheduler.enabled`, `task_scheduler.service` | false, `scheduler` | register the expiry and reminder task types |
| `links.portal_base_url` | — | required `https://` origin; links in e-mails point here |
| `gateway.service` | `gateway` | mesh name of the gateway; the only peer allowed to fetch the help pages |
| `gateway.issuer` | — | required `https://` origin of the platform token issuer |
| `platform_tenant_id` | `00000000-0000-0000-0000-000000000001` | the platform tenant; its owners and admins act as platform administrators |
| `mesh_enroll.*` | — | `enabled`, `enroll_url`, `lcm_grpc`, `tenant_id`, `token_file`, `state_file`, `insecure` (refused in production) |
| `limits_signing.max_pdf_bytes` | 50 MiB | 1–200 MiB |
| `limits_signing.max_pdf_pages` | 500 | 1–5000 |
| `limits_signing.max_fields` | 500 | 1–5000 |
| `limits_signing.max_signers` | 50 | 1–100 |
| `limits_signing.max_image_bytes` | 1 MiB | 16 KiB–10 MiB |
| `limits_signing.max_field_upload_bytes` | 5 MiB | 16 KiB–50 MiB |
| `limits_signing.parse_timeout_seconds` | 20 | 1–300 |
| `limits_signing.max_page_size` | 200 | 1–200, largest list page |
| `limits_signing.max_backup_bytes` | 2 GiB | 1 MiB–64 GiB |
| `limits_signing.signings_per_minute` | 10 | per person |
| `limits_signing.qes_preparation_minutes` | 10 | 1–60, lifetime of a prepared card signature |
| `signing.ca_validity_years`, `signing.ca_renew_before_years` | 10, 2 | the tenant CA is renewed this long before it expires |
| `signing.cert_validity_years` | 2 | 1–5, personal certificates |
| `signing.crl_validity_days` | 7 | 1–90 |
| `signing.pin_min`, `signing.pin_max` | 6, 32 | |
| `signing.pin_iterations` | 600000 | PBKDF2 iterations for PIN-encrypted keys |
| `signing.lock_attempts`, `signing.lock_minutes` | 5, 15 | wrong-PIN lockout |
| `signing.audit_job_interval_ms` | 2000 | how often the audit-trail worker polls |
| `signing.audit_job_max_attempts` | 10 | validated (1–100) but not used: failed audit-trail jobs back off up to one hour and keep retrying |
| `verify.use_system_roots` | true | trust the image's CA store when verifying |
| `verify.extra_roots_file` | — | PEM bundle of more trusted roots, for example qualified trust service providers |
| `qes.origin_cert_file`, `qes.origin_key_file` | — | both or neither: the certificate and key that prove the portal's origin to BISS |

## Key-encryption key

The KEK seals the tenant CA, system and administrator keys and stored field
values. Back it up with the database. Without the same key, sealed keys
cannot be used: restored backups need their certificates re-issued.

## Scheduler tasks

Expiry and reminders run as scheduler tasks. Set `task_scheduler.enabled:
true` (otherwise the module warns at start that submissions never expire and
no reminders are sent), then a platform administrator creates one task of
each type in the scheduler, once:

| Task type | Suggested schedule | Does |
|---|---|---|
| `signing:expire-submissions` | `*/15 * * * *` | expires overdue submissions |
| `signing:send-reminders` | `0 * * * *` | sends due reminders; also removes expired card preparations and objects of deleted submissions, and republishes due CRLs |

Both are safe to run repeatedly.

## Service policies

Signing's own policy:

```yaml
rules:
  - id: gateway-forwards
    from: ["spiffe://<trust domain>/svc/gateway"]
    to: ["signing"]
    operations: ["*"]
    effect: allow
  - id: scheduler-execute
    from: ["spiffe://<trust domain>/svc/scheduler"]
    to: ["signing"]
    operations: ["/scheduler.v1.TaskExecutor/ExecuteTask"]
    effect: allow
  - id: hr-module-api
    from: ["spiffe://<trust domain>/svc/hr"]
    to: ["signing"]
    operations:
      - "/signing.v1.ModuleSubmissions/ListTemplates"
      - "/signing.v1.ModuleSubmissions/CreateAndSend"
      - "/signing.v1.ModuleSubmissions/GetSubmission"
      - "/signing.v1.ModuleSubmissions/Cancel"
      - "/signing.v1.ModuleSubmissions/Delete"
      - "/signing.v1.ModuleSubmissions/FinalDocument"
    effect: allow
```

The gateway rule also covers the help pages (`GET /docs/v1/bundle`). Other
modules must admit `spiffe://<trust domain>/svc/signing`:

| Module | Operations |
|---|---|
| auth | `/auth.v1.Profiles/Lookup`, `/auth.v1.Profiles/ListMembers`, `/auth.v1.Profiles/Contacts`, plus token verification and registration |
| notification | `/notification.v1.Notifier/Send` (system templates `signing.*`) |
| scheduler | registering task types; scheduler needs `discovery.static.signing` |
| warden | `/warden.v1.Secrets/Get`, `/warden.v1.Secrets/GetPassword` (TSA credentials, always on behalf of the signed-in user) |

## Qualified signatures

Signing with a card talks to B-Trust BISS on the user's computer. The portal
edge must allow browser connections to it:

```yaml
edge:
  connect_sources: ["https://localhost:53952", "https://localhost:53953", "https://localhost:53954", "https://localhost:53955"]
```

Add the qualified providers' roots with `verify.extra_roots_file` so card
signatures verify as trusted.

## Mail

Invitations, reminders and results go out through the notification module
as the system templates `signing.invitation`, `signing.next_signer`,
`signing.certificate_setup`, `signing.reminder`, `signing.completed`,
`signing.declined`, `signing.cancelled`, `signing.expired` and
`signing.certificate_locked`. They carry names, links and reasons, never
field values. A failed mail never fails the signing step: it shows as an
**E-mail problem** on the signer.

## Troubleshooting

**The module refuses to start with `config: ...`.** The message names the
key, for example `links.portal_base_url must be an https origin`,
`object_store.endpoint is required (host:port, no scheme)`,
`kek.path is required for kek.source file`.

**The module refuses to start with `help docs: ...`.** The help pages built
into the binary are invalid. CI catches this with `tangra-docs lint`;
rebuild from a commit that passed CI.

**Signers show *E-mail problem*.** Notification is unreachable, its policy
does not admit signing, or the tenant has no working email channel and the
platform email is not configured. Check notification's delivery log.

**Submissions never expire and no reminders arrive.** `task_scheduler` is
disabled, or the two scheduler tasks were not created.

**Card signing says BISS was not found although it runs.** The portal edge
does not list the BISS addresses in `edge.connect_sources`.

**The audit trail stays *being generated*.** The worker retries failed jobs
with growing delays (up to one hour); the log shows `signing job failed`
with the reason, often the object store.

**Roles are missing in auth.** Look for `auth registration: ...` in the log.

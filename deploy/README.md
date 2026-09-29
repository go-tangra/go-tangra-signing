# Signing service — deployment notes

The **signing** module (feature 027, go-tangra-signing v4) manages PDF
templates, submissions signed by platform users with personal PIN-protected
certificates or qualified cards (B-Trust BISS), a per-tenant signing CA,
verification and audit trails.

Module id `signing`; browser API under `/api/signing/v1` (gateway-proxied,
platform token); the only gRPC service it serves is
`scheduler.v1.TaskExecutor` for the scheduler.

## Server operations

| Item | Value |
|---|---|
| Binary | `signingsvc -config deploy/container.yaml` (applies migrations, then serves); `signingsvc bootstrap -config …` migrates and exits; `signingsvc version` |
| Listeners | gRPC `server.grpc_addr` (:9915), HTTP `server.http_addr` (:9916) — mesh, mTLS; admin `admin.addr` (127.0.0.1:9860, `/healthz`, `/readyz`, `/metrics`) |
| Store | TimescaleDB, database `signing`, app role `signing_app` (LOGIN, NOBYPASSRLS, created by init-db — migrations only grant to it); `db.migrate_dsn` for the migration role |
| Objects | S3-compatible bucket `signing` (RustFS/MinIO); keys `tenants/<tenant>/…` |
| Event bus | Valkey user `signing` (Streams `platform:events:<tenant>`; rate-limit counters `signing:rl:*`) |
| Secrets | the module KEK (`kek`, 32 bytes base64) seals CA/system/administrator keys and field values — back it up with the database; losing it means re-issuing those certificates. Optional BISS origin certificate/key (`qes`) |
| Mesh identity | enrols with lcm through the gateway edge (`mesh_enroll`), keeps its SVID in `/state` |
| Gateway | registers its manifest (routes, permissions, abilities, nav) on a lease; registers the module roles administrator / operator / sender / viewer and the built-in grants with auth |
| Image | `ghcr.io/go-tangra/go-tangra-signing:<semver>` (fonts embedded; no `latest`) |

### Database

```sql
CREATE DATABASE signing;
CREATE ROLE signing_app LOGIN PASSWORD '…' NOBYPASSRLS;
-- in database signing, as the owner:
CREATE EXTENSION IF NOT EXISTS timescaledb;
```

Every table carries `tenant_id` under FORCE row-level security;
`signing_audit_events` is a hypertable.

### Background work

- **Audit trails**: an in-process worker drains `signing_jobs` (kicked on
  every completed submission, polled every `signing.audit_job_interval_ms`),
  renders the audit-trail PDF, certifies it with the tenant system
  certificate and only then sends the completion e-mails and
  `signing.submission.completed`.
- **Expiry, reminders, housekeeping**: scheduler task types
  `signing:expire-submissions` (suggested `*/15 * * * *`) and
  `signing:send-reminders` (suggested `0 * * * *`, also sweeps expired card
  preparations, objects of deleted submissions, expired signed-document
  downloads and republishes due CRLs). Both are **platform-scoped**: a
  platform administrator creates one task of each in the scheduler UI after
  the first deploy. With `task_scheduler.enabled=false` nothing expires and no
  reminders are sent (the service warns at start).

## Mesh policies

`deploy/policy.yaml` (this repository): the gateway forwards everything; only
`svc/scheduler` may call `/scheduler.v1.TaskExecutor/ExecuteTask`.

Rules the callees need for signing's outbound calls:

| Callee | Rule | Operations |
|---|---|---|
| auth | `signing-profiles` from `svc/signing` | `/auth.v1.Profiles/Lookup`, `/auth.v1.Profiles/ListMembers`, `/auth.v1.Profiles/Contacts`; plus the usual `services-verify` / `services-register` |
| notification | `modules-send` gains `svc/signing` | `/notification.v1.Notifier/Send` (system templates `signing.*`) |
| scheduler | `modules-register` gains `svc/signing` | `RegisterTaskTypes`, `UnregisterTaskTypes`; scheduler `discovery.static.signing: ["signing:9915"]` |
| warden | on-behalf secret read gains `svc/signing` | `/warden.v1.Secrets/Get`, `/warden.v1.Secrets/GetPassword` (TSA credentials for administrator signing, always on behalf of the signed-in user) |

## Gateway and portal

```
-allow spiffe://example.org/svc/signing=/api/signing;signing
```

The UI remote is served by the module at `/ui/` and relayed by the gateway
under `/m/signing/`. Qualified signing needs the portal edge to allow browser
connections to BISS:

```yaml
edge:
  connect_sources: ["https://localhost:53952", "https://localhost:53953", "https://localhost:53954", "https://localhost:53955"]
```

(framework ≥ v4.2.4 `edge.Config.ConnectSources`; connect-src only).

## Notification templates

System templates (notification ≥ the release that adds them): `signing.invitation`,
`signing.next_signer`, `signing.certificate_setup`, `signing.reminder`,
`signing.completed`, `signing.declined`, `signing.cancelled`,
`signing.expired`, `signing.certificate_locked`. Variables are names, links
and reasons only — never field values. The tenant needs an e-mail channel;
failures are shown on the signer (`mail_error`).

## Verification trust

`verify.use_system_roots` adds the image's CA store; `verify.extra_roots_file`
adds a PEM bundle (e.g. the Bulgarian qualified trust service roots). Tenant
CAs (current and previous) are always trusted for the tenant's documents.

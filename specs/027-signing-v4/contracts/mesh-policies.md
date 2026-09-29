# Contract: Mesh policies and stack wiring

Trust domain placeholder `example.org` (dev) / `infra.verax.net` (prod, prod-init).

## signing `deploy/policy.yaml` (callee rules)

```yaml
- id: gateway-proxy          # browser API relayed by the gateway (like every module)
  from: ["spiffe://example.org/svc/gateway"]
  to: ["signing"]
  operations: ["*"]          # HTTP relay + manifest; same shape as scheduler
  effect: allow
- id: scheduler-execute
  from: ["spiffe://example.org/svc/scheduler"]
  to: ["signing"]
  operations: ["/scheduler.v1.TaskExecutor/ExecuteTask"]
  effect: allow
```

No other caller: signing exposes no gRPC API to modules in this feature (events are
the integration surface).

## Rules added in other modules

| module | rule | from | operations |
|---|---|---|---|
| auth | signing-profiles | svc/signing | Profiles/Lookup, Profiles/ListMembers, Profiles/Contacts (+ existing Authorization/Check, module-role registration rules gain svc/signing) |
| notification | modules-send | svc/signing | /notification.v1.Notifier/Send |
| scheduler | modules-register | svc/signing | RegisterTaskTypes, UnregisterTaskTypes |
| warden | on-behalf-read | svc/signing | secret read on behalf of user (same ops as ipam 024) |
| gateway | allow-list | `svc/signing=/api/signing;signing` (gateway-bootstrap `-allow`) | |

## Stack

- Ports: gRPC 9915, HTTP 9916, admin 127.0.0.1:9860; no host port.
- Database `signing`, role `signing_app` LOGIN NOBYPASSRLS, extensions timescaledb,
  citext; `migrate_dsn` as postgres.
- Valkey ACL user `signing` (streams + rate limits).
- Object store: bucket `signing` on RustFS, credentials as paperless (config
  `object_store`), bucket created at start.
- KEK: `./keys/signing.kek` (dev `deploy/kek.dev`), mounted read-only.
- Enrolment token job `signing-token`, volume `signing-state`.
- Consumers: scheduler `discovery.static.signing`; signing `discovery.static`: gateway,
  auth, notification, scheduler, warden.
- Portal: `edge.connect_sources` with the four BISS origins (production overlay).

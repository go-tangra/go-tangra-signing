# go-tangra-signing (v4)

Document signing module of the go-tangra v4 platform (feature 027), the v4
successor of the v3 DocuSeal-style signing service: PDF templates with a
field builder, submissions signed by platform users in order or in parallel,
personal PIN-protected signing certificates issued by a per-tenant signing
CA, qualified signatures with a smart card through B-Trust BISS, and an
audit trail for every completed document.

- **Templates** — upload a PDF (bounded parsing), place fields in the
  builder (text, number, date, checkbox, select, radio, cells, image, file,
  signature, initials, stamp) per party, detect placeholders, folders and
  tags, conditions (show/require) and number formulas.
- **Submissions** — one active member per party, sequential or parallel, a
  frozen copy of the fields and the PDF, prefills, expiry and reminders
  (scheduler task types), cancel/resend/replace/delete by the sender, live
  "To sign" inbox, e-mails through the notification module.
- **Signing** — the signer sees only their own fields; values are stamped as
  an incremental update and a PAdES signature (signer certificate + tenant
  CA) is applied with the visible appearance on the first signature field, so
  every earlier signature stays valid. Parallel signers are serialised by a
  row lock. A wrong PIN signs nothing and counts towards a lockout.
- **Qualified signatures** — prepare/complete through BISS on the signer's
  computer; the card signature is verified against the prepared digest and the
  stored chain before it is spliced in.
- **Certificates** — per-tenant CA (sealed with the module KEK, renewed before
  expiry), personal certificates (PIN-encrypted keys), administrator
  certificates, revocation and CRLs, administrator document signing with an
  optional RFC 3161 time-stamp (credentials in Warden).
- **Verification, audit trail, backup** — verify any PDF against the tenant
  CAs and configured qualified roots; an audit-trail PDF certified with the
  tenant system certificate for every completed submission; tenant export and
  import with key handling that never exposes keys.

## Layout

```
cmd/signingsvc            binary (serve | bootstrap | version)
api/openapi               browser API contract (OpenAPI 3.1)
internal/                 config, authz, audit, sealed, blob, store, repo (+repodb, memstore),
                          pdf/{limits,detect,render,incr,overlay,sign,verify,audittrail,fonts},
                          pincrypto, pki, certs, contacts, templates, submissions, signing,
                          qes, rules, fieldvalues, documents, jobs, tasks, backup, mail,
                          warden, events, stream, metrics, httpapi, app
pkg/signingmanifest       gateway manifest, permissions, module roles, abilities, nav
ui/                       federated UI remote (Vue 3, @go-tangra/ui, pdf.js)
tests/integration         whole-service suites on TimescaleDB (isolation, leaks, concurrency)
deploy/                   policy.yaml, container.yaml (stack-shaped example), README.md
specs/027-signing-v4      specification, plan, research, data model, contracts, tasks
```

## Development

```bash
unset GOROOT; export GOWORK=off
make test                 # unit + contract tests (race)
make cover                # ≥ 80 % overall; 100 % authz, pincrypto, rules, qes, pdf/limits
make lint vuln            # vet, staticcheck, gosec; govulncheck
make fuzz                 # every fuzz target for FUZZTIME
sg docker -c 'make test-integration'   # TimescaleDB via testcontainers
(cd ui && npm ci && npm run lint && npx vitest run)
make build                # bin/signingsvc (no UI); make build-ui embeds the remote
```

Permissions: `signing:sign` (every tenant member; relationship checks in
code), `signing:read`, `templates:manage`, `submissions:create`,
`submissions:manage`, `certificates:manage`, `backup:manage`; module roles
Signing administrator / operator / sender / viewer. Deployment: see
[deploy/README.md](deploy/README.md). Security: see [SECURITY.md](SECURITY.md).

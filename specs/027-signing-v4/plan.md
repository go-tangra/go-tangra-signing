# Implementation Plan: Document Signing Module for v4

**Branch**: `027-signing-v4` (spec, v3 repo) → code on orphan branch `v4` of
go-tangra-signing (worktree `go-tangra-signing-v4`) | **Date**: 2026-09-29 |
**Spec**: [spec.md](./spec.md)

## Summary

A v4 signing module with v3 parity — PDF templates with a visual field builder and
auto-detection, multi-signer sequential/parallel submissions, PAdES signatures with a
per-tenant signing CA and PIN-protected personal certificates, qualified signatures
through the local B-Trust BISS app, administrator document signing and verification,
events and backup — rebuilt on v4 foundations (SPIFFE mesh, RLS, enforced
permissions, sealed keys, object store) with the v3 defects fixed and the unfinished
v3 features completed (folders/tags, signer decline, expiry and reminders as scheduler
task types, audit-trail PDF, conditions/formulas).

The v3 PDF signer (`pkg/pdf/sign|verify|revocation`) is ported and hardened rather than
rewritten (research D2). Signing is one transactional pipeline that never marks a
signer signed without an applied signature and always builds on the latest document
version (D3). Signers are platform users; their e-mail comes from a new
policy-restricted auth RPC `Profiles.Contacts` (D1). BISS needs a new edge
`connect-src` allowance (D6).

## Technical Context

**Language/Version**: Go 1.26 (toolchain 1.26.8); UI TypeScript + Vue 3.

**Primary Dependencies**: go-tangra framework v4 (mesh, edge, policy, observe), auth
SDK (verifier, permission checks, module roles, **Profiles.Contacts**), portal SDK
(gateway client), scheduler SDK (`taskexec`, `schedulerclient`), notification SDK
(`notifyclient.SendKey`), warden client (on-behalf secret read, as ipam 024), `pgx/v5`,
`goose/v3`, `kin-openapi`, `valkey-go`, `minio-go/v7`; PDF: `digitorus/pdf`,
`digitorus/pkcs7`, `digitorus/timestamp`, `pdfcpu/pdfcpu`, `signintech/gopdf`,
`menta2k/go-pdfplumber`, `menta2k/go-transliteration`; stdlib `crypto/pbkdf2`,
`crypto/ecdsa`, `crypto/x509`. UI: `@go-tangra/ui` 4.2.3, `pdfjs-dist` 4.x.

**Storage**: TimescaleDB database `signing` (role `signing_app`, NOBYPASSRLS), tables
per [data-model.md](./data-model.md); RustFS bucket `signing`; Valkey (events, rate
limits).

**Testing**: Go `testing`; `memstore` fake repository and `blob` fake; golden PDFs
(`tests/testdata`) signed and verified round-trip, validated also with `pdfcpu validate`
and an independent CMS check; QES simulated signer (ECDSA test card chain); bufconn
tests for the task executor; fuzz: PDF upload limits, field/rule JSON, formula parser,
QES signature parsing, PIN envelope; contract test OpenAPI ↔ routes; integration suite
(testcontainers TimescaleDB + Valkey + MinIO, `//go:build integration`): RLS isolation,
parallel signing serialisation, idempotent expiry/reminders, backup round-trip; UI
vitest (rules vectors shared with Go, signature pad, builder store) + lint; coverage
≥ 80 %, 100 % on `internal/authz`, `internal/pincrypto`, `internal/rules`,
`internal/qes`, `internal/pdf/limits`.

**Target Platform**: Linux container (alpine + DejaVu fonts embedded in the binary)
in `go-tangra/deploy/stack` and go-tangra-docker; behind the gateway; mesh identity by
enrolment with lcm.

**Project Type**: Web service (Go, OpenAPI HTTP + gRPC task executor) +
Module-Federation UI; small changes in framework, portal, auth, notification,
scheduler (policy only), warden (policy only), stack repos.

**Performance Goals**: local signing of a 50-page, 20 MB PDF < 5 s (SC-003; PBKDF2
600k ≈ 0.3 s); template list/submission list < 1 s at 10k rows; builder renders pages
lazily.

**Constraints**: RLS on every row; no key/PIN/value in logs, events, audit or e-mails;
PDF ≤ 50 MiB, ≤ 500 pages, ≤ 500 fields, ≤ 50 signers; signature image ≤ 1 MiB; QES
preparation 10 min; PIN 6–32, lock after 5 failures for 15 min; signing rate 10/min
per user.

**Scale/Scope**: nine user stories; ports gRPC 9915, HTTP 9916, admin 9860.

## Constitution Check

*GATE: passed before Phase 0 and re-checked after Phase 1 (one justified deviation, see
Complexity Tracking).*

- **I. Secure by Default** — refuses to start without db, valkey, object store, KEK,
  gateway issuer; production refuses plaintext DB/Valkey/object store and insecure
  enrolment; CA/admin/system keys always sealed; opt-outs are named config flags in
  `Warnings()`.
- **II. Zero Trust Service Communication** — SPIFFE mTLS with per-module policies:
  only the gateway relays the browser API, only the scheduler executes task types;
  outbound calls to auth/notification/warden/scheduler are admitted by their policies
  (contracts/mesh-policies.md).
- **III. Boundary Validation & Defense in Depth** — OpenAPI validation, permission
  re-check in the module, participant/sender checks, RLS, bounded PDF parsing (D10),
  server-side rule evaluation (D8), QES verification before embedding (D6), 404
  masking.
- **IV. Test-First** — tests precede implementation in every phase; negative tests
  for cross-tenant access, out-of-turn/closed signing, wrong/locked PIN, forged QES,
  tampered formulas, malicious PDFs; fuzz on every parser; 100 % on security packages.
- **V. Observability & Auditability** — closed audit vocabulary + signing history
  (contracts/audit-events.md); OTel metrics (signings by method/outcome, PIN failures,
  QES prepares/completes, mail failures, job backlog); correlation ids.
- **VI. Supply Chain** — **deviation**: seven new Go dependencies for PDF handling
  and pdf.js for the UI (no v4 module handles PDFs). All were already in v3, pinned,
  `govulncheck` gated; `expr-lang/expr` is dropped for an in-repo rule evaluator.
- **VII. Simplicity & Explicit Configuration** — one binary, one database, one bucket;
  typed YAML with `KnownFields`; scheduled work lives in the scheduler, not in hidden
  loops (the only in-process worker drains audit-trail jobs).

## Project Structure

### Documentation (this feature)

```text
specs/027-signing-v4/
├── spec.md, checklists/requirements.md
├── plan.md, research.md, data-model.md, quickstart.md, tasks.md
└── contracts/{signing-api.md, cross-module.md, audit-events.md, mesh-policies.md}
```

### Source Code

```text
go-tangra-signing (branch v4, worktree go-tangra-signing-v4)
├── cmd/signingsvc/{main.go,version.go}           # + bootstrap (migrate) subcommand
├── api/openapi/{signing.yaml,embed.go}
├── internal/
│   ├── app/          # wiring, permissions/roles registration, gateway lease, workers, scheduler
│   ├── config/       # typed config (db, valkey, object_store, kek, limits, signing, verify, mail links)
│   ├── authz/        # permissions, participant/sender relations, platform admin
│   ├── audit/        # closed vocabulary + writer
│   ├── sealed/       # module KEK envelope (copy of paperless)
│   ├── blob/         # object store (copy of paperless) + fake
│   ├── pincrypto/    # PIN envelope, KDF, lockout policy (pure)
│   ├── pki/          # tenant CA lifecycle, signer/system/admin certs, CRL, transliteration
│   ├── pdf/
│   │   ├── limits/   # safe open: size, pages, time, encryption, signed detection
│   │   ├── sign/     # ported v3 signer (PAdES, external signing split, revocation embed)
│   │   ├── verify/   # ported v3 verifier + tenant trust pool + change detection
│   │   ├── overlay/  # values/signature images/stamps via pdfcpu (DejaVu)
│   │   ├── detect/   # placeholder detection (pdfplumber)
│   │   └── audittrail/ # gopdf audit-trail document
│   ├── rules/        # conditions + formula parser/evaluator (+ testdata/vectors.json)
│   ├── templates/    # folders, templates, fields validation, clone
│   ├── submissions/  # create/send/cancel/replace/resend/delete, inbox
│   ├── signing/      # sign pipeline (D3), decline, open, participant views
│   ├── qes/          # prepare/complete, CMS verification (pure core)
│   ├── certs/        # my certificate, admin certificates, revoke, admin document signing, verify
│   ├── mail/         # notification SendKey wrapper, link building, failure recording
│   ├── contacts/     # auth Profiles client (ListMembers, Lookup, Contacts)
│   ├── warden/       # on-behalf secret read (TSA)
│   ├── tasks/        # scheduler task types (expire, reminders+sweeps)
│   ├── jobs/         # audit-trail job worker
│   ├── events/       # stream publisher; stream/ (copied hub)
│   ├── backup/       # export/import (records + objects)
│   ├── metrics/
│   ├── store/        # pool, scopes, goose migrations, ids
│   ├── repo/         # contract; repodb/ (pgx), memstore/ (fake)
│   └── httpapi/      # OpenAPI-validated browser API, multipart, streaming downloads
├── pkg/signingmanifest/                          # gateway manifest, permissions, roles, abilities, nav
├── ui/                                           # federated remote `signing`
│   └── src/{views/{templates,builder,submissions,inbox,sign,certificate,admin,verify},
│            components/{PdfPages,FieldOverlay,FieldPalette,FieldProps,SignaturePad,BissButton},
│            rules/, api/, stores/, remote/}
├── deploy/{policy.yaml,container.yaml,kek.dev,README.md}
├── tests/{contract,integration,qes,testdata}
├── Dockerfile, Makefile, scripts/{coverage-gate.sh,vulncheck.sh}, .github/workflows/ci.yaml
└── README.md, SECURITY.md

go-tangra (framework, branch 027-connect-sources)     transport/edge ConnectSources (+tests) → v4.2.4
go-tangra-portal-v4 (branch 027-connect-sources)      edge.connect_sources config → portal patch
go-tangra-auth (branch 027-profiles-contacts)         Profiles.Contacts (sdk + server + policy) → sdk + minor
go-tangra-notification-v4 (branch 027-signing-mail)   signing.* system templates, policy → minor
go-tangra-scheduler-v4 (branch 027-signing)           policy modules-register + discovery → patch
go-tangra-warden-v4 (branch 027-signing)              policy on-behalf read for svc/signing → patch
go-tangra/deploy/stack (branch 027-signing)           compose signing + token, configs, init-db, valkey user, bucket, allow
go-tangra-docker (branch v4)                          compose example, configs/signing.yaml, policies, init-db, .env, prod-init
```

**Structure Decision**: mirror go-tangra-scheduler-v4 (app/httpapi/store/audit/
manifest/UI) and go-tangra-paperless-v4 (blob, sealed, multipart upload, streamed
download). Cross-repo SDK dependencies (auth `Contacts`) use a TEMP local `replace`
committed last on the branch, dropped at release (as 026).

## Rollout

1. Framework `ConnectSources` (v4.2.4) → portal patch with `edge.connect_sources`.
2. auth `Profiles.Contacts` (sdk tag + minor), notification `signing.*` templates
   (minor), scheduler/warden policy patches.
3. go-tangra-signing: `v3` branch keeps v3; `v4` → `main`; release `v4.0.0`, image.
4. Stack/prod: DB + role, Valkey user, RustFS bucket, KEK file, gateway allow-list,
   policies of consumers, portal connect sources, deploy; platform administrator
   creates the two scheduler platform tasks (quickstart).
5. v4 starts empty (no v3 data migration).

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|------|------------|--------------------------------------|
| Seven new PDF/CMS dependencies + pdf.js | PAdES signing, overlays, detection, audit PDF, rendering have no v4 precedent; all proven in v3 | writing a PDF writer/parser in-repo is high-risk; upstream-only pdfsign lacks the external (BISS) split |
| New auth RPC `Profiles.Contacts` | signers are platform users and need their e-mail for mail and certificate subject | widening `Lookup` leaks e-mail to every allowed module; typed e-mails contradict decision 2 |
| Global `connect-src` allowance for BISS | the edge CSP is global (F13); BISS runs on the user's machine | per-route CSP would need an edge redesign; allowance is connect-only |
| In-repo rule evaluator (Go + TS) | server-authoritative conditions/formulas on untrusted input | `expr-lang` executes a far larger language than the spec needs |
| Separate PIN-failure transaction | the counter must survive the rolled-back signing | counting in the signing tx loses failures and defeats the lockout |

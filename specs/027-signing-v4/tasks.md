# Tasks: Document Signing Module for v4

**Feature**: 027-signing-v4 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

Tests are MANDATORY and precede implementation in every phase (Constitution IV).
`[P]` = parallelizable (different files, no dependency on an unfinished task);
`[US#]` = user story. Paths are relative to `go-tangra-signing-v4/` unless a repository
is named. Module `github.com/go-tangra/go-tangra-signing/v4`. Mirror
go-tangra-scheduler-v4 (app, httpapi, store, audit, manifest, UI, task executor) and
go-tangra-paperless-v4 (blob, sealed, multipart upload, streamed download). v3 sources
are in `../go-tangra-signing` (branch `master`). Release tasks (push, tags, images,
deploy) are marked **(release)** and are not done in this change.

Story order: US1 → US3 → US2 (US2 needs a certificate) → US7 → US4 → US5 → US8 → US6
→ US9. Cross-module work (Phase 3) can run in parallel with Phases 4–6.

## Phase 1: Setup

- [x] T001 Orphan branch `v4` of go-tangra-signing as worktree `go-tangra-signing-v4`; copy `specs/027-signing-v4/`; `go.mod` (module …/v4, go 1.26.3, toolchain 1.26.8), `.gitignore`, `.dockerignore`, `deploy/kek.dev` (dev KEK, 32 bytes base64).
- [x] T002 [P] `Makefile` (lint, vuln, test, test-integration, cover, fuzz, ui-build, build, build-ui, image), `scripts/coverage-gate.sh` (≥ 80 %, 100 % authz/pincrypto/rules/qes/pdf/limits), `scripts/vulncheck.sh`, `Dockerfile` (ui → build -tags ui → alpine, `signingsvc`, fonts embedded), `.github/workflows/ci.yaml` (go vet/test, UI lint/unit, image `ghcr.io/go-tangra/go-tangra-signing`, semver tags, no latest).
- [x] T003 [P] UI scaffold `ui/` from go-tangra-scheduler-v4/ui (package `go-tangra-signing-ui`, `@go-tangra/ui` ^4.2.3, `pdfjs-dist` ^4, vite base `/m/signing/`, remote `signing`, `layer(utilities)`, `breakpointSpecificity`, icon safelist test from scheduler), `ui/embed.go`, `ui/embed_stub.go`.
- [x] T004 [P] Test fixtures `tests/testdata/`: `contract.pdf` (dotted placeholders, 3 pages, Cyrillic text), `signed-external.pdf` (already signed), `encrypted.pdf`, `huge-pages.pdf` generator, `not-a-pdf.pdf`, test CA + "card" chain for QES (`tests/qes/`).

## Phase 2: Foundational

### Tests (write first, must fail)
- [x] T005 [P] `internal/config/config_test.go` — defaults, unknown keys refused, required db/valkey/object_store/kek/gateway, production guards (sslmode, plaintext valkey, object store use_ssl, insecure enroll), limits bounds, `links.portal_base_url`, warnings.
- [x] T006 [P] `internal/authz/authz_test.go` — permissions (`signing:read`, `templates:manage`, `submissions:create`, `submissions:manage`, `certificates:manage`, `backup:manage`), Require for user/system/service actors, platform admin, participant and sender relations, not-found masking. 100 %.
- [x] T007 [P] `internal/audit/audit_test.go` — vocabulary (contracts/audit-events.md), refusal of unknown actions, redaction of values/pin/key/signature/reason keys, writer flush/close/drop.
- [x] T008 [P] `internal/sealed/sealed_test.go` + `internal/blob/blob_test.go` (copied from paperless with their tests; fake store).
- [x] T009 [P] `internal/pdf/limits/limits_test.go` + `limits_fuzz_test.go` — MIME sniff, size, page count, encrypted refused, signed detection, parse timeout, panic recovery → `invalid_pdf`. 100 %.
- [x] T010 [P] `pkg/signingmanifest/manifest_test.go` — routes from OpenAPI (every route has a known permission or `member`), permissions, roles (administrator/operator/sender/viewer), grants (owner/admin → administrator, auditor → viewer, member → none), abilities, nav.
- [ ] T011 [P] `tests/contract/openapi_test.go` — document valid; every declared route mounted; no undeclared route; binary routes carry `x-freya-max-body-bytes`; error envelope.

### Implementation
- [x] T012 `internal/config/config.go` — framework config inline + db, valkey, object_store, kek, gateway, mesh_enroll, discovery, task_scheduler, notification, warden, links (`portal_base_url`), limits (pdf bytes/pages/fields/signers, image bytes, backup bytes, rate), signing (ca/cert validity, pin min/max, lock attempts/duration), verify (`extra_roots_file`), events.
- [x] T013 [P] `internal/authz/authz.go` — permissions, Checker (auth `Authorization/Check`), Require, RequireParticipant/Sender helpers, platform admin.
- [x] T014 [P] `internal/audit/audit.go` — closed vocabulary, redaction, async writer.
- [x] T015 [P] Copy `internal/sealed/` and `internal/blob/` from go-tangra-paperless-v4 (bucket/key helpers `tenants/<t>/…`).
- [x] T016 [P] `internal/pdf/limits/limits.go` — `Open(ctx, r, Limits) (*Doc, error)` with sniff, size, pdfcpu relaxed validation under context timeout, page/field counts, encryption and signature detection, recover guard.
- [x] T017 `internal/store/` — migrations `0001_schema.sql` (all tables of data-model.md with checks and indexes), `0002_audit.sql` (hypertable), `0003_rls.sql` (FORCE RLS + system scope + grants); `store.go` (pool, Migrate under advisory lock, Tx with Scope/SystemScope), `ids.go` (UUIDv7).
- [x] T018 `internal/repo/repo.go` — storage contract (folders, templates, submissions, signers, versions, certificates, qes, events, jobs, task queries, backup, audit) + sentinel errors; `internal/memstore/` fake (+ tests).
- [x] T019 `internal/repo/repodb/` — pgx implementation + `repodb_integration_test.go` (`//go:build integration`): migrations, RLS isolation between two tenants, system scope, unique active signer certificate, `FOR UPDATE` serialisation of two concurrent signers.
- [x] T020 [P] `internal/metrics/metrics.go` — signings{method,outcome}, pin_failures, qes{phase,outcome}, mail_failures, jobs_backlog; nil-safe.
- [x] T021 [P] `internal/stream/` (copy scheduler hub) + `internal/events/events.go` (types of contracts/cross-module.md; ids only) + tests.
- [x] T022 `api/openapi/signing.yaml` (contracts/signing-api.md) + `embed.go`.
- [x] T023 `internal/httpapi/` — server (OpenAPI validation, authenticate, authorize, binary routes, streamed downloads with `Content-Disposition` and `nosniff`, multipart helper with per-part limits, 501 until wired), errors, JSON helpers.
- [x] T024 `pkg/signingmanifest/manifest.go` — module `signing`, prefix `/api/signing`, permissions, roles, grants, abilities (`SigningTemplate`, `SigningSubmission`, `SigningCertificate`, `SigningDocument`, `SigningBackup`), nav (Templates, Submissions, To sign, Certificates, Verify).
- [x] T025 `internal/app/` — Build (runtime + mesh enrol, store, blob EnsureBucket, sealed KEK, auth peers, stream, metrics, services, HTTP, gRPC), Run (verifier, gateway lease, auth permission/role registration loop, workers); `cmd/signingsvc/{main.go,version.go}` (+ `bootstrap` migrate subcommand); `deploy/{policy.yaml,container.yaml,README.md}` (contracts/mesh-policies.md).

## Phase 3: Cross-module prerequisites (parallel with Phases 4–6)

- [ ] T026 [P] go-tangra (framework, branch `027-connect-sources`): tests first in `transport/edge/headers_test.go` + `server_test.go` (empty = unchanged header byte-for-byte; valid origins spliced into `connect-src`; invalid origin/path/http refused by `NewServer`), then `edge.Config.ConnectSources` + `connectSources()`; CHANGELOG 4.2.4.
- [ ] T027 [P] go-tangra-portal-v4 (branch `027-connect-sources`): config `edge.connect_sources` (+ validation test), passed through `edgeConfig`; TEMP replace on the framework.
- [x] T028 [P] go-tangra-auth (branch `027-profiles-contacts`): proto `Profiles.Contacts` (contracts/cross-module.md) + generated; tests first (tenant grant mismatch refused, inactive/foreign/unknown omitted, ≤ 100 ids, phone never returned, audit count only, policy denies other services); server implementation; `deploy/policy.yaml` rule `signing-profiles` + svc/signing in Authorization/Check and module-role registration rules.
- [ ] T029 [P] go-tangra-notification-v4 (branch `027-signing-mail`): tests first (templates render with required variables, missing variable refused, key prefix enforcement for svc/signing); nine `signing.*` system templates; policy `modules-send` gains svc/signing.
- [ ] T030 [P] go-tangra-scheduler-v4 (branch `027-signing`): policy `modules-register` + `discovery.static.signing`; go-tangra-warden-v4 (branch `027-signing`): policy on-behalf read for svc/signing (+ policy tests where present).
- [ ] T031 [P] go-tangra/deploy/stack (branch `027-signing`): compose `signing-token`, `signing` (depends lcm, gateway, timescaledb, valkey, rustfs), `configs/signing.yaml`, init-db (database, role, extensions), Valkey ACL user, RustFS bucket, gateway `-allow svc/signing=/api/signing;signing`, portal `edge.connect_sources` (BISS origins), consumer policies.

## Phase 4: User Story 1 — Templates and builder (P1) 🎯 MVP part 1

**Goal**: upload a PDF, place and save fields, auto-detect, folders/tags, clone, archive, delete.
**Independent test**: all field types for two parties survive save/reopen; auto-detect proposes fields on `contract.pdf`.

### Tests (write first, must fail)
- [x] T032 [P] [US1] `internal/templates/fields_test.go` + fuzz — field validation (types, unique names, party exists, page ≤ pages, geometry in [0,1], options for select/radio, font size bounds, ≤ 500 fields), parties 1..50; conditions/formula stored structurally valid (full rule validation arrives in US6).
- [x] T033 [P] [US1] `internal/templates/templates_test.go` — create (limits → `invalid_pdf`, `payload_too_large`), get/list with folder/tag/status/q filters, update, version conflict, clone copies object and fields, archive/activate, delete refused while in use, tenant scoping, audit rows.
- [x] T034 [P] [US1] `internal/templates/folders_test.go` — create/rename/move (cycle refused, depth ≤ 8), path maintenance, unique names, delete non-empty refused.
- [x] T035 [P] [US1] `internal/pdf/detect/detect_test.go` — placeholders found on `contract.pdf` with font/size; bounded time; empty result on image-only PDF.
- [x] T036 [P] [US1] `internal/httpapi/templates_test.go` — upload multipart (MIME, size), PDF streaming download with tenant check (foreign id → 404), detect-fields requires `templates:manage`.

### Implementation
- [x] T037 [US1] `internal/templates/{fields.go,templates.go,folders.go}` — services per data-model.md (object key `tenants/<t>/templates/<id>/<uuid>.pdf`, sha256, pages, pdf_signed).
- [x] T038 [P] [US1] `internal/pdf/detect/detect.go` — port v3 `internal/service/detect.go` on go-pdfplumber, returning proposed fields.
- [x] T039 [US1] `internal/httpapi/templates.go` — folder, template, fields, clone, pdf, detect-fields handlers.
- [x] T040 [P] [US1] UI tests `ui/tests/unit/{builder.spec.ts,templates.spec.ts}` — palette drag creates fields in page fractions, move/resize, party assignment, properties, save/reload round-trip, folder tree and filters.
- [x] T041 [US1] UI `ui/src/components/{PdfPages.vue (pdfjs, lazy pages, isEvalSupported false, local worker),FieldOverlay.vue,FieldPalette.vue,FieldProps.vue}`, `ui/src/views/templates/` (folders tree, list, upload drawer), `ui/src/views/builder/` (builder, auto-detect, parties editor, activate).

## Phase 5: User Story 3 — Personal certificate with a PIN (P1)

**Goal**: a signer sets up, views, re-PINs, renews and revokes their certificate from the tenant CA.
**Independent test**: setup creates a certificate chained to the tenant CA with the transliterated name and e-mail; the key is unusable without the PIN.

### Tests (write first, must fail)
- [x] T042 [P] [US3] `internal/pincrypto/pincrypto_test.go` + fuzz — envelope v1 round-trip, wrong PIN → `ErrPIN`, AD binding (moved blob refused), PIN length rules, tampered envelope, lockout policy (5 failures → lock 15 min, reset on success, lock expiry). 100 %.
- [x] T043 [P] [US3] `internal/pki/pki_test.go` — lazy tenant CA (single under concurrency), sealed CA key, signer cert profile (KU, EKU incl. documentSigning, subject transliteration bg→Latin, e-mail, 2 y), system cert, admin cert, CRL generation, CA renewal when < 2 y left (old CA kept, new issuer).
- [x] T044 [P] [US3] `internal/contacts/contacts_test.go` — Contacts/Lookup/ListMembers client over bufconn (TEMP replace on the auth SDK), missing e-mail handling.
- [x] T045 [P] [US3] `internal/certs/me_test.go` — setup (needs e-mail from Contacts, `certificate_exists`), view, PIN change (old PIN required), renew (old superseded), self-revoke, never returns key material, audit rows.

### Implementation
- [x] T046 [P] [US3] `internal/pincrypto/pincrypto.go` — PBKDF2-SHA256 600k (stdlib), AES-256-GCM envelope, lockout policy.
- [x] T047 [US3] `internal/pki/{ca.go,issue.go,crl.go,translit.go}` — port v3 `pkg/security/cert` onto sealed keys and the profiles of research D4.
- [x] T048 [P] [US3] `internal/contacts/contacts.go` — auth Profiles client (dial `auth`).
- [x] T049 [US3] `internal/certs/me.go` + `internal/httpapi/me.go` — `/me/certificate*` routes.
- [x] T050 [US3] UI `ui/src/views/certificate/` — My signing certificate (setup with PIN twice, details, change PIN, renew, revoke), shared `PinDialog.vue`.

## Phase 6: User Story 2 — Submissions and local signing (P1) 🎯 MVP part 2

**Goal**: create/send submissions to platform users; signers sign with PIN; sequential/parallel; completion.
**Independent test**: two-signer sequential flow produces one PDF with both values and two valid PAdES signatures; wrong PIN, wrong user, wrong turn and closed submissions are refused.

### Tests (write first, must fail)
- [x] T051 [P] [US2] `internal/pdf/sign/sign_test.go` — port v3 signer tests; PAdES B-B attributes (signing-certificate-v2), SHA-256, incremental update keeps earlier signatures valid (sign twice, verify both), certification (DocMDP) only first, visible appearance on the first signature field, external-signing split round-trip.
- [x] T052 [P] [US2] `internal/pdf/overlay/overlay_test.go` — text/number/date/checkbox/select/radio/cells/image/stamp values placed at fractions on the right page; Cyrillic text; signature image placement; stamps for extra signature fields; already-signed input keeps signatures (incremental).
- [x] T053 [P] [US2] `internal/submissions/submissions_test.go` — create (template active + same tenant, signers are active members via Contacts, parties covered, positions, prefill validated, expiry/reminder defaults from template), frozen fields, send (sequential invites first, parallel all), inbox queries, sender vs `submissions:manage`, tenant scoping.
- [x] T054 [P] [US2] `internal/signing/signing_test.go` — every FR-011 refusal (not the signer, draft/cancelled/expired/completed, not your turn, already signed/declined, missing required), wrong PIN → nothing signed + counter incremented in its own tx + `attempts_left`, locked → 423, expired/revoked certificate refused, success creates version n+1 and marks signed, next signer invited, last signer completes (final_version, job queued, event published), object cleanup on failure, rate limit.
- [x] T055 [P] [US2] `internal/mail/mail_test.go` — template keys and variables per event, portal links, failures recorded as `mail_error` + history event, no field values in variables.
- [ ] T056 [P] [US2] `tests/integration/signing_integration_test.go` (`//go:build integration`) — two parallel signers signing concurrently: both signatures present and valid, versions 1 and 2, no lost update.

### Implementation
- [x] T057 [US2] `internal/pdf/sign/` — port v3 `pkg/pdf/sign` + `revocation` with the fixes of research D2.
- [x] T058 [P] [US2] `internal/pdf/overlay/overlay.go` — port v3 overlay/stamp code (`pdf_generator.go`, `local_signing.go` stamp rendering, embedded fonts).
- [x] T059 [US2] `internal/submissions/submissions.go` + `internal/httpapi/submissions.go` — create/get/list/send, inbox, users picker (`/users`), document/version downloads with participant/sender/read checks.
- [x] T060 [US2] `internal/signing/signing.go` + `internal/httpapi/signing.go` — session view (own fields only), open, sign pipeline (research D3), completion.
- [x] T061 [P] [US2] `internal/mail/mail.go` — notification client (`SendKey`), link builder, failure recording.
- [x] T062 [P] [US2] UI tests `ui/tests/unit/{sign.spec.ts,submissions.spec.ts,signaturepad.spec.ts}` — signing page shows own fields only, required validation, PIN errors (attempts left, locked), not-your-turn state, submission create drawer (signers per party, order, prefill), progress view.
- [x] T063 [US2] UI `ui/src/views/submissions/` (list, create drawer with user picker per party, detail with signer states), `ui/src/views/inbox/` (To sign / Signed by me, live via `signing.inbox`), `ui/src/views/sign/` (PDF with own field inputs, `SignaturePad.vue` draw/type, PIN dialog, certificate setup redirect), downloads.

## Phase 7: User Story 7 — Admin certificates, document signing, verification (P2)

### Tests (write first, must fail)
- [x] T064 [P] [US7] `internal/pdf/verify/verify_test.go` — port v3 tests; trust pool = tenant CAs (current + previous) + roots; revoked via tenant records; modified-after-signature detection; foreign QES chain reported with `unknown` revocation when no embedded data.
- [x] T065 [P] [US7] `internal/certs/admin_test.go` — list/filter, revoke (CRL regenerated, later signing refused), admin certificate creation (sealed), admin document signing (certification, reason/location/contact, TSA via fake Warden on behalf of the caller, TSA failure → `tsa_failed`), permission `certificates:manage`, tenant scoping.
- [x] T066 [P] [US7] `internal/warden/warden_test.go` — on-behalf secret read client (pattern of ipam 024), errors mapped, secret never logged.

### Implementation
- [x] T067 [US7] `internal/pdf/verify/` — port v3 verifier with research D11.
- [x] T068 [US7] `internal/certs/admin.go`, `internal/warden/warden.go`, `internal/httpapi/{certificates.go,documents.go,verify.go}` — `/certificates*`, `/ca/crl`, `/documents/sign`, `/documents/{id}`, `/verify`.
- [ ] T069 [US7] UI `ui/src/views/admin/` (Certificates list/detail/revoke/create, Sign document with Warden secret picker) and `ui/src/views/verify/` (upload or pick a submission, results table).

## Phase 8: User Story 4 — QES with B-Trust BISS (P2)

### Tests (write first, must fail)
- [x] T070 [P] [US4] `internal/qes/qes_test.go` + fuzz — prepare builds signedAttrs/digest over the prepared PDF; complete accepts a valid simulated card signature (raw ECDSA/RSA and CMS forms), refuses: wrong key, other chain than prepared, tampered digest, expired/used preparation, `document_changed` (version moved), leaf not valid now, missing KU; garbage input never panics. 100 %.
- [x] T071 [P] [US4] `internal/signing/qes_flow_test.go` — end-to-end prepare → simulated sign → complete produces a PDF verified by `pdf/verify`; earlier signatures remain valid; signer recorded with card subject/serial/issuer; preparation survives a new service instance (shared storage).
- [ ] T072 [P] [US4] UI `ui/tests/unit/biss.spec.ts` — port detection over 53952–53955, getsigner/sign flow with mocked fetch, clear states (not installed, refused, timeout).

### Implementation
- [x] T073 [US4] `internal/qes/qes.go` (pure verification core) + `internal/signing/qes.go` + `internal/httpapi/qes.go` — prepare/complete (research D6), preparation rows and objects.
- [ ] T074 [US4] UI `ui/src/components/BissButton.vue` + `ui/src/composables/useBiss.ts` (port v3 `useBiss.ts`, no credentials sent anywhere but localhost BISS).

## Phase 9: User Story 5 — Decline, cancel, resend, expiry, reminders (P2)

### Tests (write first, must fail)
- [x] T075 [P] [US5] `internal/signing/decline_test.go` — decline cancels, notifies sender and others, publishes `signing.submission.cancelled`, reason length bounds, only the signer can decline.
- [x] T076 [P] [US5] `internal/tasks/tasks_test.go` — `signing:expire-submissions` and `signing:send-reminders` via the SDK server over bufconn: platform scope only (tenant request refused), caller must be svc/scheduler, expiry exactly once across two runs, reminders exactly once per due signer and bounded by max, sweeps (expired preparations, orphan objects, due CRLs), DB error → Retry.
- [x] T077 [P] [US5] `internal/submissions/control_test.go` — cancel, resend (new invitation, history), replace signer (only unsigned; new Contacts lookup), delete removes objects.

### Implementation
- [x] T078 [US5] `internal/signing/decline.go`, `internal/submissions/control.go` + handlers.
- [x] T079 [US5] `internal/tasks/{expire.go,reminders.go,descriptors.go}` + `internal/app/scheduler.go` (executor server + Registrar, config `task_scheduler`), policy rule `scheduler-execute`.
- [ ] T080 [US5] UI: decline dialog on the signing page; cancel/resend/replace/delete actions and event history timeline on the submission detail; expiry/reminder fields in the create drawer and template defaults.

## Phase 10: User Story 8 — Audit trail (P2)

### Tests (write first, must fail)
- [x] T081 [P] [US8] `internal/pdf/audittrail/audittrail_test.go` — document contains title/id, original and final SHA-256, each signer (method, cert serial/issuer, IP, UA, time), events in order; Cyrillic names render.
- [x] T082 [P] [US8] `internal/jobs/jobs_test.go` — job queued on completion, worker builds + signs with the system certificate + stores + publishes completion + sends mails, retries with backoff, idempotent re-run; verify of the audit trail succeeds and detects tampering.

### Implementation
- [x] T083 [US8] `internal/pdf/audittrail/audittrail.go` (gopdf, embedded DejaVu) and `internal/jobs/worker.go` (drain `signing_jobs`), completion mails/events moved behind the job (research D9).
- [x] T084 [US8] Handlers `/submissions/{id}/audit-trail` and `/package` (zip), UI download buttons.

## Phase 11: User Story 6 — Conditions and formulas (P3)

### Tests (write first, must fail)
- [x] T085 [P] [US6] `internal/rules/rules_test.go` + `rules_fuzz_test.go` + `testdata/vectors.json` — conditions (all ops, all/any, visible/required), formula grammar (precedence, parentheses, round/min/max/sum, unknown field, division by zero → empty, cycles detected, deep nesting bounded), deterministic 2-dp output. 100 %.
- [ ] T086 [P] [US6] `ui/tests/unit/rules.spec.ts` — same vectors file through the TS evaluator (identical results).
- [x] T087 [P] [US6] `internal/signing/rules_flow_test.go` — hidden fields ignored, conditionally required enforced, tampered calculated value replaced by server value in the PDF; template save refuses invalid/cyclic rules naming the field.

### Implementation
- [x] T088 [US6] `internal/rules/{conditions.go,formula.go,graph.go}`; wire into `internal/templates/fields.go` validation and the signing pipeline.
- [ ] T089 [US6] `ui/src/rules/` (TS evaluator), builder condition/formula editors in `FieldProps.vue`, live evaluation on the signing page.

## Phase 12: User Story 9 — Backup and restore (P3)

- [x] T090 [P] [US9] `internal/backup/backup_test.go` — export streams records + objects within `max_backup_bytes`; import skip/overwrite; PIN-encrypted keys kept; sealed keys kept only with the same KEK (key-check value), otherwise dropped and `needs_reissue`; platform admin cross-tenant only; audit.
- [ ] T091 [US9] `internal/backup/backup.go` + handlers `/backup/export`, `/backup/import`; UI backup panel on the admin page (platform/tenant admin).

## Phase 13: Polish & cross-cutting

- [ ] T092 [P] Leak test `tests/integration/leak_test.go` — run a full flow with known PIN, values and key material; assert none appear in logs, audit, events (stream), e-mail variables, backups or DB columns in clear (SC-005).
- [ ] T093 [P] Isolation suite `tests/integration/isolation_test.go` — tenant B against every tenant-A route (templates, pdf, submissions, documents, signing, certificates, verify by submission) → 404 (SC-004).
- [ ] T094 [P] Performance check — 50-page 20 MB PDF local signing < 5 s on CI hardware (SC-003), list endpoints at 10k rows < 1 s.
- [ ] T095 [P] UI polish — icons in the kit safelist (unit test), dark theme check, a11y e2e (`ui/tests/e2e/a11y.spec.ts`) for all routes, empty/error states.
- [ ] T096 [P] `README.md`, `SECURITY.md` (threat model of spec SR, key handling, BISS CSP note), `deploy/README.md`.
- [ ] T097 `make lint vuln cover` green (≥ 80 %, 100 % security packages); `govulncheck` clean; quickstart automated section passes.
- [ ] T098 go-tangra-docker (branch `v4`): `docker-compose.yaml.example` (signing, signing-token, volume, Valkey user, gateway allow), production overlay, `configs/signing.yaml`, `policies/signing.yaml` + consumer policy updates, `init-db.sql`, `.env.example` (`SIGNING_IMAGE`, `SIGNING_DB_PASSWORD`, object store keys), `scripts/prod-init.sh` (SERVICES + KEK + bucket), portal `edge.connect_sources` in the gateway config, PRODUCTION/README sections.

## Phase 14: Release **(release)**

- [ ] T099 **(release)** Framework v4.2.4 (ConnectSources); portal patch release.
- [ ] T100 **(release)** auth: tag `sdk/vX` then minor; notification minor; scheduler and warden patches.
- [ ] T101 **(release)** go-tangra-signing: push old `master` as `v3`, merge `v4` into `main` (`-s ours` + PR), drop TEMP replaces, tag `v4.0.0`; image.
- [ ] T102 **(release)** Stack/prod: DB + role, Valkey user, bucket, KEK, allow-list, policies, portal connect sources, pins; deploy; create the two scheduler platform tasks; UI smoke of quickstart Scenarios 1–9 (operator sign-in).

## Dependencies & sequencing

Setup → Foundational → US1 → US3 → US2 (MVP) → US7 → US4 → US5 → US8 → US6 → US9 →
Polish. Phase 3 (cross-module) starts after Phase 1 and must finish before US3 (auth
Contacts, T028), US2 (notification templates, T029), US4 UI (connect sources,
T026/T027), US5 (scheduler policy, T030) and US7 TSA (warden policy, T030).
Within a phase, tests precede implementation.

## Parallel execution examples

- T005–T011 in parallel (independent test files).
- T026–T031 in parallel (six different repositories).
- US7 (T064–T069) and US4 (T070–T074) in parallel once US2 is done.
- UI tasks (T040/T041, T050, T062/T063) alongside backend handlers once the OpenAPI (T022) is fixed.

## Implementation strategy

MVP = Phases 1, 2, 4, 5, 6 (+ T028/T029): templates, certificates, sequential and
parallel local signing with completion. Then admin/verify (US7), QES (US4), lifecycle
(US5), audit trail (US8), rules (US6), backup (US9), polish, release.

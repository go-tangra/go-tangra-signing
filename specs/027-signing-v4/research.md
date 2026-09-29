# Research: Document Signing Module for v4

**Feature**: 027-signing-v4 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

References are `file:line` in the repositories under `/home/jadmin/projects/go-tangra/`.
"v3" is `go-tangra-signing` (branch `master`).

## Findings (v3 and v4 as they are)

| # | Finding | Evidence |
|---|---------|----------|
| F1 | v3 signs with a vendored fork of goSign (`pkg/pdf/sign`: `Sign`, `PrepareForExternalSigning`, `EmbedExternalSignature`, `DefaultEmbedRevocationStatusFunction`) on `digitorus/pdf`, `digitorus/pkcs7`, `digitorus/timestamp`; overlays with `pdfcpu`; audit PDF with `signintech/gopdf` (never called). | go-tangra-signing/pkg/pdf/sign/sign.go:101,243,300,468; internal/service/local_signing.go; pkg/pdf/fill/fill.go |
| F2 | v3 PIN crypto: PKCS#8 → AES-256-GCM, key = PBKDF2-SHA256(PIN, 32-byte salt, 600k). | pkg/security/cert/pin_crypto.go |
| F3 | v3 marks a signer completed before the PAdES step; a PIN error is only logged. | internal/service/session_service.go (SubmitSigning → applyLocalSignature) |
| F4 | v3 BISS: digest = SHA256 over signedAttrs of a dummy-signed CMS; draft PDF kept in a process map (10 min); the returned PKCS#7 is hex-spliced into `/Contents` unverified. Browser calls `https://localhost:53952-53955` (`/version`, `/getsigner`, `/sign`). | internal/service/biss_signing.go; frontend/src/views/session/composables/useBiss.ts:41,106 |
| F5 | v3 field detection: `menta2k/go-pdfplumber` finds dotted/underscore/line placeholders + font/size. | internal/service/detect.go |
| F6 | v3 conditions/formulas: `pkg/condition`, `pkg/formula` on `expr-lang/expr`; builder discards the result. | pkg/formula/engine.go; frontend builder/index.vue:150-156 |
| F7 | v4 object storage: minio-go, one bucket, keys `tenants/<tenant>/…`, `blob.Store{EnsureBucket,Put(sha256),Get,PresignGet,Delete}` + in-memory fake; downloads streamed by the module after a tenant-scoped authz check. | go-tangra-paperless-v4/internal/blob/blob.go; internal/documents/documents.go:124; internal/httpapi/handlers.go:149 |
| F8 | v4 KEK: per-module `internal/sealed` (AES-GCM envelope, AD-bound, 32-byte KEK from file/env); config refuses to start without it; dev key `deploy/kek.dev`. | go-tangra-paperless-v4/internal/sealed/sealed.go |
| F9 | v4 events: Valkey stream per tenant `platform:events:<tenant>` via `stream.Hub.PublishID`; module wrapper `internal/events` (best effort, ids/status only); consumers XREAD per configured tenant (deployer, dns). | go-tangra-scheduler-v4/internal/stream/hub.go:58; go-tangra-deployer-v4/internal/events/consumer.go |
| F10 | auth never returns e-mail addresses to services: `Profiles.Lookup` → id, display name, avatar; `ListMembers` → ids; both policy-restricted. No role-holder query. | go-tangra-auth/sdk/api/proto/auth/v1/auth.proto:211-245; go-tangra-auth/deploy/policy.yaml:19-29 |
| F11 | notification system e-mail: `SystemTemplate{Key,Subject,Body,Variables,Required,Secret}` in `internal/notify/systemtemplates.go`, seeded at start (never overwrites edited wording); caller `notifyclient.SendKey(ctx, tenant, "<svc>.<key>", email, vars, corr)`; key must start with the caller's service name; tenant default channel, else platform. | go-tangra-notification-v4/internal/notify/systemtemplates.go:96; sdk/pkg/notifyclient/client.go:57 |
| F12 | Scheduler task types: `sdktask.NewServer(handlers, Options{Caller, Scheduler})` + `schedulerclient.Registrar{Dial, Types}`; callee policy `scheduler-execute`; scheduler policy `modules-register` + `discovery.static`. Platform-scoped types run with an empty tenant. | go-tangra-lcm-v4/internal/app/scheduler.go; deploy/policy.yaml; go-tangra-scheduler-v4 spec FR-025–027 |
| F13 | Edge CSP is global (all edge responses), built in `transport/edge/headers.go` `headersFilter()` with `connect-src 'self'`; `FrameSources` (v4.2.2) shows how validated origins are spliced in; `CSPExtra` cannot add a second `connect-src`. | go-tangra/transport/edge/headers.go; server.go:52,236; go-tangra-portal-v4/internal/config/config.go:58 |
| F14 | Fine-grained "own object" authorization exists only in paperless (tuples, `ErrNotFound` masking); scheduler/ticket use coarse permissions + tenant scope. | go-tangra-paperless-v4/internal/authz/authz.go |
| F15 | Uploads: OpenAPI op with `x-freya-max-body-bytes` (binary, body not schema-validated), handler `ParseMultipartForm` + MIME allow-list + size check; gateway enforces route body limit and timeout. | go-tangra-paperless-v4/internal/httpapi/middleware.go; handlers.go |
| F16 | No v4 module uses a PDF library or pdf.js; ports in use: gRPC/HTTP 99x5/99x6 for 9905…9995, admin 9190–9850. | go.mod files; go-tangra-docker/configs/*.yaml |
| F17 | ipam feature 024 fetches Warden secrets on behalf of the signed-in user over the mesh. | go-tangra-ipam-v4/internal/warden/ |

## D1. Signer e-mail addresses: new auth `Profiles.Contacts` (restricted by policy)

**Decision**: auth gains `rpc Contacts(LookupContactsRequest) returns (LookupContactsResponse)`
returning `{user_id, display_name, email}` for **active members of the requested tenant**
(≤ 100 ids), allowed by auth's policy only for `svc/signing`. Signing uses it to fill the
signer's name/e-mail when a submission is created, to address invitations, reminders and
completion mails, and for the certificate subject (spec FR-007, FR-009, FR-032).
Signer picking in the UI uses `ListMembers` + `Lookup` (names only) through signing.

**Rationale**: signers are platform users (decision 2); auth is the source of truth for
e-mail; a dedicated, policy-restricted RPC keeps `Lookup` e-mail-free for everyone else.

**Alternatives**: sender types e-mails (contradicts decision 2, spoofable); notification
resolves user → e-mail (notification does not hold e-mails either); widen `Lookup`
(leaks e-mail to every module allowed today).

## D2. PDF engine: port the v3 in-repo signer, keep the v3 libraries

**Decision** (revised during implementation, 2026-09-29): sign with upstream
`digitorus/pdfsign` — what v3's local signing already used — not the vendored goSign
fork (`pkg/pdf/sign`), which rewrites the catalog with only the new AcroForm field
(earlier signature fields vanish), never attaches its widget to a page and ignores
xref streams. Signer signatures are approval signatures without DocMDP (SHA-256,
signing-certificate-v2). A signer's field values are drawn as image-appearance
annotations by an in-repo incremental writer (`internal/pdf/incr`) in an update
appended before the signature, so earlier signatures stay valid and Cyrillic needs
no PDF font embedding. BISS uses pdfsign with a capture signer (digest out,
placeholder signature in) and the returned card signature is placed by rebuilding
the CMS inside the reserved /Contents space. Administrator signing uses a
certification signature (DocMDP P=2). Dependencies:
`digitorus/pdf`, `digitorus/pkcs7`, `digitorus/timestamp`, `pdfcpu/pdfcpu` (field
overlays, page count/limits), `signintech/gopdf` (audit trail),
`menta2k/go-pdfplumber` (detection), `menta2k/go-transliteration` (CN).

**Rationale**: the v3 code is proven against BISS and Adobe Reader; the defects are in
the service flow, not the signer. These are new dependencies for v4 (see plan
Complexity Tracking); all pinned, `govulncheck` gated, parsed inside the limits of D10.

**Alternatives**: `digitorus/pdfsign` upstream directly (no external-signing split for
BISS); a commercial SDK (licence); writing a PDF writer from scratch (risk, effort).

## D3. Signing pipeline and atomicity

**Decision**: one service call per signing, inside a DB transaction that holds
`SELECT … FOR UPDATE` on the submission row (serialises parallel signers, FR-018):

1. validate state (FR-011) and certificate (FR-035), evaluate conditions/formulas
   server-side (D8);
2. load the current document version (`documents.version`), overlay values + signature
   image (pdfcpu, DejaVu font for Cyrillic), decrypt the key with the PIN (D5), sign
   (PAdES) → new bytes;
3. `blob.Put` new version (`tenants/<t>/submissions/<s>/v<n>.pdf`, sha256 recorded);
4. insert document version n+1, mark signer signed, record event; on the last signer
   complete (D9) — commit.

A failure at any step rolls back; an orphaned object from step 3 is removed by a
best-effort delete and by the retention sweep (objects without a version row). A wrong
PIN increments the failure counter in a separate short transaction (so it survives the
rollback) and returns `pin_invalid` / `certificate_locked`.

**Rationale**: fixes F3 and the "final PDF from blank template" defect: the final
document is always the last signed version; nothing is ever regenerated from the
template.

## D4. Tenant signing CA and certificates

**Decision**: per tenant, a CA (ECDSA P-256, 10 y, `CN=<tenant name> Signing CA`,
pathlen 0) created lazily under an advisory lock; key sealed with the module KEK
(AD `ca:<id>`). Signer certificates (2 y, KU digitalSignature+contentCommitment, EKU
emailProtection + documentSigning `1.3.6.1.5.5.7.3.36`), subject CN transliterated name,
E=email, O=tenant name, serial 128-bit random. A **system certificate** per tenant CA
(sealed) signs audit trails (US8). Administrator certificates (FR-038) are sealed too.
Renewal: when the CA has < 2 y left a new CA is created and becomes the issuer; old CAs
stay for verification and CRLs (FR-036). CRL per CA (7-day nextUpdate), regenerated on
revoke and by the reminders task when due.

## D5. PIN protection and lockout

**Decision**: signer key PKCS#8 encrypted with AES-256-GCM, key =
PBKDF2-HMAC-SHA256(PIN, 32-byte random salt, 600,000 iterations) — Go stdlib
`crypto/pbkdf2`, versioned envelope `{v:1,kdf,iter,salt,nonce,ct}` so parameters can be
raised later; AD = certificate id (a key cannot be moved to another certificate row).
PIN 6–32 chars. Lockout: `failed_pin_count`, `locked_until` on the certificate; 5
failures → 15 min lock + e-mail; success resets. Per-user signing rate limit
(10/min) in Valkey. PIN change re-encrypts in place (old PIN required).

**Alternatives**: argon2id (memory-hard; 64 MiB per concurrent signing needs a worker
bound, and a new dependency) — PBKDF2 at 600k meets OWASP guidance, stdlib, v3-proven;
the lockout makes online guessing the relevant threat.

## D6. QES (B-Trust BISS)

**Decision**: keep the v3 two-pass flow (F4) with state in shared storage:
`qes_preparations` row (signer, cert chain DER, digest, signedAttrs, expires_at = now +
10 min) + prepared PDF in the object store (`…/qes/<id>.pdf`). Complete:
parse CMS/raw signature, verify it over the stored signedAttrs with the public key of
the leaf of the **stored** chain, check the leaf equals the chain chosen at prepare
time and is valid now with KU nonRepudiation or digitalSignature, then splice into
`/Contents`, verify the resulting PDF signature (D11) and continue as D3 steps 3–4.
Expired/used preparations are deleted by the reminders task.

**CSP**: the framework edge gains `ConnectSources` (validated https origins, spliced
into `connect-src`), the portal config `edge.connect_sources`; production config lists
`https://localhost:53952 … 53955`. The CSP is global (F13), so the allowance applies to
every portal page but only for `connect-src` — no script, frame or image source is
added (SR-010 is met in that sense; spec wording aligned).

## D7. Authorization model

**Decision**: coarse permissions (FR-048) checked through auth `Authorization/Check`
like scheduler, plus two relationship checks in code (no tuple store):
- **participant**: the caller's user id equals a signer's `user_id` of the submission —
  grants view/sign/decline of their own signer slot and download per FR-017;
- **sender**: `submissions.created_by` = caller — grants cancel/resend/replace/delete
  without `submissions:manage`.
Every read by id is tenant-scoped under RLS; foreign or unauthorised ids answer 404
(`not_found`). Signing pages require only an authenticated tenant user.

## D8. Conditions and formulas: small in-repo evaluator (Go + TS)

**Decision**: a restricted, fuzzable grammar instead of `expr-lang`:
- conditions: list of `{field, op ∈ {eq,neq,contains,empty,not_empty,checked,unchecked}, value}` + `all|any`, effects `visible`, `required`;
- formulas: numbers, field refs `{Field name}`, `+ - * /`, parentheses, `round(x[,n])`, `min`, `max`, `sum` — Pratt parser, decimal math with 2-dp rounding for output, division by zero → empty.
Implemented in Go (`internal/rules`) and TypeScript (`ui/src/rules`) against a shared
JSON test-vector file (`internal/rules/testdata/vectors.json`, copied into UI tests).
Save-time validation: unknown fields, cycles (topological order), syntax. Server values
are authoritative (FR-042).

**Rationale**: an expression engine executing arbitrary expressions from template
authors over signer input is unnecessary attack surface; the spec's operator set is
small.

## D9. Completion, audit trail, events

**Decision**: on the last signature (same transaction as D3): status completed, final
document = current version; generate the audit-trail PDF (gopdf, DejaVu font) after
commit in a job table `signing_jobs` (kind `audit_trail`, retried) so a slow PDF build
never blocks signing; sign it with the tenant system certificate (PAdES), store it,
then send completion e-mails and publish `signing.submission.completed` (ids and final
document id only). Cancel/decline/expire publish `signing.submission.cancelled` /
`…expired`.

## D10. PDF input limits

**Decision**: upload ≤ 50 MiB (`x-freya-max-body-bytes`), MIME sniff `%PDF-`, pdfcpu
validation in relaxed mode with limits: ≤ 500 pages, ≤ 500 fields per template,
parse time ≤ 20 s (context), encrypted PDFs refused, PDFs containing signatures
accepted only if fields are applied as incremental updates (FR edge case); signature
image ≤ 1 MiB PNG/JPEG re-encoded; image/file field uploads ≤ 5 MiB each. Parsing runs
with a recover guard; failures → `invalid_pdf`.

## D11. Verification trust

**Decision**: trust pool = all tenant signing CAs of the caller's tenant (current and
previous) + the platform trust store (`/etc/ssl/certs` in the image; configurable
`verify.extra_roots_file` for the Bulgarian QES roots). Revocation for tenant
certificates from the module's own records (not the network); for foreign certificates
embedded OCSP/CRL if present, otherwise `unknown`. Integrity: byte-range digest
compare + detection of changes after the last signature (incremental update analysis).

## D12. Scheduled work via the scheduler (no hidden loops)

**Decision**: two **platform-scoped** task types registered with the scheduler SDK:
`signing:expire-submissions` (default cron `*/15 * * * *`) and
`signing:send-reminders` (`0 * * * *`). They iterate tenants with due work under a
system scope, are idempotent (`UPDATE … WHERE status='in_progress' AND expires_at < now`
; reminders guarded by `next_reminder_at` updated in the same statement), and also
sweep expired QES preparations, orphan objects and due CRLs. The two platform tasks are
created once by a platform administrator (quickstart). The audit-trail job table (D9)
is drained by a small in-process worker — it is part of a user action, not a schedule.

## D13. E-mail

**Decision**: notification-v4 system templates `signing.invitation`,
`signing.next_signer`, `signing.certificate_setup`, `signing.reminder`,
`signing.completed`, `signing.declined`, `signing.cancelled`, `signing.expired`,
`signing.certificate_locked`; variables: document name, sender name, signer name,
portal link, reason (declined/cancelled) — never field values. Sent per recipient via
`SendKey` over the tenant channel; failures recorded on the signer (`mail_error`) and
shown in the UI (edge case "missing e-mail channel").

## D14. UI

**Decision**: federated remote `signing` on `@go-tangra/ui` 4.2.3 with `pdfjs-dist`
(worker served from the remote under `/m/signing/`, `isEvalSupported: false`, no CDN)
for the builder and signing page; signature pad in-repo (canvas, pointer events).
Pages: Templates (folders tree + list), Builder, Submissions, Submission detail,
To sign / Signed by me, Signing page, My certificate, Certificates (admin), Verify,
Sign document (admin). All icons added to the kit safelist (lesson from 026).

## D15. Backup

**Decision**: tar.gz (bounded by `limits.max_backup_bytes`, default 2 GiB, streamed)
containing JSON records + objects. Keys: PIN-encrypted signer keys as is; sealed
CA/system/admin keys included sealed with a key-check value of the KEK — import keeps
them only when the target KEK matches, otherwise drops them and marks those
certificates `needs_reissue` (US9-3).

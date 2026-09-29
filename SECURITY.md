# Security policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub's
**"Report a vulnerability"** (Security → Advisories) on
`github.com/go-tangra/go-tangra-signing`. Do not open a public issue. Include
the affected version or commit, the impact and a reproduction. You will get an
acknowledgement within five working days.

## Threat model (summary)

Trust boundaries: browser → gateway → signing (platform token), signing →
auth/notification/warden (mesh, SPIFFE mTLS), scheduler → signing (task
execution), browser → BISS on the signer's own computer (localhost), and
signing → object store / database / Valkey.

| Threat | Mitigation |
|---|---|
| Signing someone else's slot, out of order, after cancel or expiry | Every signing step re-checks the slot owner, the submission state and the order under the submission's row lock; foreign slots answer 404 |
| Cross-tenant reads, downloads or signings | FORCE row-level security on every row, object keys under `tenants/<t>/`, tenant from the platform token; tested on every parameterised route (tests/integration) |
| Stolen signing keys | Personal keys are encrypted with the signer's PIN (PBKDF2-SHA256 600k + AES-256-GCM, bound to the certificate); CA, system and administrator keys are sealed with the module KEK; keys are never returned, logged or exported in clear |
| PIN guessing | Lockout after `signing.lock_attempts` failures for `signing.lock_minutes`, counted in its own transaction so a failed signing cannot roll it back; per-user signing rate limit |
| Tampering with earlier signatures | Values and signatures are incremental updates; certification (DocMDP) only on unsigned documents; verification reports changes after the last signature |
| Forged qualified signatures | The card signature is verified over the prepared digest with the leaf of the chain stored at preparation, the leaf must be valid with nonRepudiation/digitalSignature, preparations expire after 10 minutes and are single use, the document must not have moved on |
| Malicious PDFs and images | Size, page, object and time limits on every parse; image dimensions checked before decoding; parser panics are recovered and refused |
| Rule injection by template authors | A restricted grammar (no general expression engine); rules validated at save time; the server recomputes formulas, its values are authoritative |
| SSRF through the TSA address | http(s) only, no credentials in the URL, the host must resolve to public addresses only |
| Leaking values or secrets | Field values are sealed at rest; e-mails carry names and links only; events carry ids and states; the audit vocabulary refuses value/PIN/key/signature detail keys; an end-to-end leak test checks logs, audit, events, e-mails, backups and every table |
| BISS on localhost | The portal CSP allows only `connect-src` to `https://localhost:53952–53955` (no script, frame or image sources); the browser never sends cookies or tokens there |

## Supported versions

Only the latest `v4.x` release receives security fixes.

# Quickstart / Validation: Document Signing Module for v4

**Feature**: 027-signing-v4 | contracts: [signing-api](./contracts/signing-api.md)

## Prerequisites

- Dev stack `go-tangra/deploy/stack` with signing, auth (with `Profiles.Contacts`),
  notification (signing templates), scheduler, warden, RustFS, Mailpit.
- Two test users in one tenant (`maria`, `ivan`) with e-mail addresses; an operator
  with the Signing Administrator role; a second tenant with one user (isolation).
- A sample PDF with dotted placeholders (`tests/testdata/contract.pdf`).

## Automated

```bash
cd go-tangra-signing-v4
make lint vuln test            # unit, fuzz seeds, contract (OpenAPI ↔ routes)
make test-integration          # RLS, concurrency, object store (testcontainers)
make cover                     # ≥ 80 %, 100 % on authz, pincrypto, rules, qes, pdf/limits
(cd ui && npm run lint && npx vitest run)
```

## Scenarios (browser, operator sign-in)

1. **Template (US1)**: Signing → Templates → New folder "HR/Contracts" → upload
   `contract.pdf` with tag `hr` → Builder → Auto-detect → add Employee signature+date,
   Employer signature, mark Salary required → Save → Activate.
   *Expect*: reopen shows identical fields; a `.txt` renamed `.pdf` is refused.
2. **Sequential signing (US2, US3)**: New submission → Maria (Employee, 1), Ivan
   (Employer, 2), prefill Salary, reminders 1 day → Send. Mailpit: Maria gets
   `signing.invitation` + `signing.certificate_setup`. As Maria: To sign → open →
   set up certificate (PIN) → fill → draw signature → wrong PIN (403, attempts left)
   → correct PIN → signed. Ivan gets the invitation; Ivan signing before Maria is
   refused (`not_your_turn`, try via API). Ivan signs.
   *Expect*: completed; `signing.completed` mails; package download has the final PDF
   (two signatures valid in the module's Verify and in a PDF reader) and the audit
   trail (hashes match).
3. **Lockout (FR-034)**: 5 wrong PINs → 423 with `locked_until`, e-mail
   `signing.certificate_locked`.
4. **QES (US4)**: with BISS installed on the operator workstation (or the simulated
   signer in `tests/qes`), "Sign with qualified card" → card PIN → signed; tampered
   `signature_b64` → `qes_signature_invalid`.
5. **Decline / expiry / reminders (US5)**: decline as a signer → submission cancelled
   and mails; create a submission expiring in 1 minute, run
   `signing:expire-submissions` "Run now" in the scheduler → expired, signing refused;
   `signing:send-reminders` Run now twice → exactly one reminder per due signer.
6. **Conditions/formulas (US6)**: "Married" → shows/requires "Spouse name";
   `{Quantity} * {Unit price}` → Total; tamper Total via API → server value in PDF.
7. **Admin (US7)**: revoke Ivan's certificate → Verify shows `revoked`; Ivan cannot
   sign; admin signs a PDF with a certificate + TSA (Warden secret ref).
8. **Isolation**: user of tenant B requests tenant A's template PDF, submission,
   document, signer page by id → 404 each.
9. **Backup (US9)**: export tenant, import into an empty tenant → templates open,
   documents download, sealed keys kept only with the same KEK.

## Platform setup once

In Scheduler (platform administrator): create platform tasks
`signing:expire-submissions` (`*/15 * * * *`) and `signing:send-reminders`
(`0 * * * *`).

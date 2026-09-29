# Contract: Audit vocabulary and signing history

Two records, both closed vocabularies, never containing field values, PINs, keys,
signature images or document content.

## Audit (`audit_events`, platform audit schema; SR-008)

`action` values, `outcome` ∈ {ok, denied, failed} with a reason code:

| action | object | reason codes (failed/denied) |
|---|---|---|
| folder.create / .update / .delete | folder | folder_not_empty |
| template.create / .update / .fields / .clone / .delete | template | invalid_pdf, template_in_use, version_conflict |
| submission.create / .send / .cancel / .delete / .signer_replace / .resend | submission | template_not_active, not_draft |
| signer.sign | signer | pin_invalid, certificate_locked, not_your_turn, submission_not_open, missing_required, qes_signature_invalid, document_changed |
| signer.decline | signer | |
| qes.prepare | signer | preparation_expired |
| certificate.setup / .pin_change / .renew / .revoke / .lock / .create_admin | certificate | certificate_exists, pin_invalid |
| ca.create / .renew / .crl | certificate | |
| document.sign (admin) / document.verify | document | tsa_failed |
| backup.export / .import | tenant | backup_too_large |
| task.expire / task.remind | platform | |

Actor: user id (or `system` for task types), tenant, correlation id, client IP.
Denied authorization attempts (404-masked) are audited as `denied` with `not_found`.

## Signing history (`events`, FR-044, shown in the UI and the audit trail)

`submission.created, submission.sent, signer.invited, signer.opened, signer.signed
(meta: method, version), signer.declined (meta: reason length only), signer.reminded
(meta: n), signer.replaced, submission.cancelled, submission.expired,
submission.completed, audit_trail.generated, mail.failed (meta: template key, reason
code), certificate.setup, certificate.pin_changed, certificate.locked,
certificate.revoked`.

Decline and cancel **reasons** are stored on the signer/submission row (shown to
participants) and included in e-mails, but not in the audit log.

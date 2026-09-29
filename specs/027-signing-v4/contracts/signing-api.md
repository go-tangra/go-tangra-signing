# Contract: Signing HTTP API (browser, via the gateway)

Prefix `/api/signing/v1`, declared in `api/openapi/signing.yaml` with
`x-freya-permission` per operation. `member` below means the permission
`signing:sign`, granted to every built-in tenant role (members included); the
module then checks the relationship in code (research D7). Errors use the platform envelope
`{error:{code,message,field?}}`; foreign/unauthorised ids → `404 not_found`.
Mutating calls carry the gateway CSRF header.

## Folders and templates (`templates:manage` unless noted)

| Method | Path | Permission | Notes |
|---|---|---|---|
| GET | /folders | signing:read | tree |
| POST | /folders | | `{name,parent_id?}` |
| PATCH | /folders/{id} | | rename / move / sort_order |
| DELETE | /folders/{id} | | 409 `folder_not_empty` |
| GET | /templates | signing:read | `folder_id, tag, status, q, page, page_size` |
| POST | /templates | | multipart `file` (PDF ≤ 50 MiB, `x-freya-max-body-bytes: 52428800`) + `name, description, folder_id, tags` → 201; 400 `invalid_pdf`, 413 `payload_too_large` |
| GET | /templates/{id} | signing:read | incl. fields, parties, version |
| PATCH | /templates/{id} | | name, description, folder_id, tags, status, defaults |
| PUT | /templates/{id}/fields | | `{version, parties, fields}` → 409 `version_conflict`; 400 `invalid_field` / `invalid_rule` with `field` |
| POST | /templates/{id}/clone | | `{name, folder_id?}` |
| DELETE | /templates/{id} | | 409 `template_in_use` |
| GET | /templates/{id}/pdf | signing:read | streamed `application/pdf` |
| POST | /templates/{id}/detect-fields | | → proposed `fields` (not saved) |

## Submissions

| Method | Path | Permission | Notes |
|---|---|---|---|
| GET | /submissions | signing:read | `status, template_id, q, mine, page` |
| POST | /submissions | submissions:create | `{template_id, name?, mode, signers:[{user_id, party, position}], prefill:{field:value}, expires_at?, reminder?}`; 400 `template_not_active`, `invalid_signer`, `invalid_prefill` |
| GET | /submissions/{id} | signing:read \| sender \| participant | participants see status + their own slot |
| POST | /submissions/{id}/send | sender \| submissions:manage | 409 `not_draft` |
| POST | /submissions/{id}/cancel | sender \| submissions:manage | `{reason}` |
| POST | /submissions/{id}/signers/{sid}/resend | sender \| submissions:manage | |
| PUT | /submissions/{id}/signers/{sid} | sender \| submissions:manage | replace `{user_id}`; 409 `signer_final` |
| DELETE | /submissions/{id} | sender \| submissions:manage | removes objects |
| GET | /submissions/{id}/events | signing:read \| sender | history |
| GET | /submissions/{id}/document | signing:read \| sender \| participant (FR-017) | `?version=` current/final; streamed |
| GET | /submissions/{id}/audit-trail | signing:read \| sender \| participant | 404 until generated |
| GET | /submissions/{id}/package | same | zip: final PDF + audit trail |

## Signing (member; caller must be the signer, research D7)

| Method | Path | Notes |
|---|---|---|
| GET | /inbox?state=to_sign\|signed | the caller's signer slots |
| GET | /signing/{signer_id} | session: submission name, party, own fields (+prefill, conditions, formulas), document URL, `certificate:{state: none\|active\|locked\|expired\|revoked, locked_until}`, `can_sign`, reason if not |
| POST | /signing/{signer_id}/open | marks opened (idempotent) |
| POST | /signing/{signer_id}/sign | multipart: `values` JSON, `signature` image ≤ 1 MiB, image/file fields ≤ 5 MiB each, `pin`; → 200 `{status, next}`; 400 `missing_required` (field), `invalid_value`; 403 `pin_invalid` (`attempts_left`), 423 `certificate_locked` (`locked_until`), 409 `not_your_turn` \| `submission_not_open` \| `already_signed`; 429 `rate_limited` |
| POST | /signing/{signer_id}/decline | `{reason}` 1..500 |
| POST | /signing/{signer_id}/qes/prepare | `{values, chain:[base64 DER]}` (+ multipart images) → `{preparation_id, digest_b64, signed_attrs_b64, hash_algorithm:"SHA-256", expires_at}` |
| POST | /signing/{signer_id}/qes/complete | `{preparation_id, signature_b64}` → 400 `qes_signature_invalid`, 410 `preparation_expired`, 409 `document_changed` |

## My certificate (member)

| Method | Path | Notes |
|---|---|---|
| GET | /me/certificate | active certificate (subject, issuer, serial, validity, fingerprint, status) + documents signed with it |
| POST | /me/certificate | `{pin}` → setup (6..32); 409 `certificate_exists` |
| POST | /me/certificate/pin | `{old_pin, new_pin}` |
| POST | /me/certificate/renew | `{pin}` new certificate, old revoked (`superseded`) |
| POST | /me/certificate/revoke | forgotten PIN: revoke own (`key_compromise`/`superseded`) |

## Certificates and documents (`certificates:manage` unless noted)

| Method | Path | Notes |
|---|---|---|
| GET | /certificates | `kind, status, q, page` |
| GET | /certificates/{id} | incl. PEM (never keys) |
| POST | /certificates | admin certificate `{subject_cn, email?, validity_years 1..5}` |
| POST | /certificates/{id}/revoke | `{reason}` → CRL republished |
| GET | /ca/crl | signing:read; DER CRL of the current CA |
| POST | /documents/sign | multipart `file` or `{submission_id, version}` + `{certificate_id, reason, location, contact, tsa_url?, tsa_secret_ref?}` → signed PDF id; TSA credentials fetched from Warden on behalf of the caller |
| GET | /documents/{id} | download admin-signed PDF (1 h) |
| POST | /verify | signing:read; multipart `file` ≤ 50 MiB or `{submission_id, version}` → `{signatures:[{signer, time, reason, location, integrity: valid\|modified, trust: trusted\|untrusted\|unknown, revocation: good\|revoked\|unknown, method}], modified_after_last_signature}` |

## Backup (`backup:manage`)

| Method | Path | Notes |
|---|---|---|
| GET | /backup/export | `?tenant_id=` (platform admin) → streamed tar.gz |
| POST | /backup/import | `?mode=skip\|overwrite`, body tar.gz (`x-freya-max-body-bytes` = limits.max_backup_bytes) → counts + `needs_reissue` list |

## Users (member, for the signer picker)

| Method | Path | Notes |
|---|---|---|
| GET | /users?q= | active tenant members `{user_id, display_name}` (names only; e-mail never sent to the browser) |

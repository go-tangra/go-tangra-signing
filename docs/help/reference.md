---
title: Reference
summary: Field types, template, submission, signer and certificate states, verification results, limits, permissions, roles, administrator signing and backups.
kind: reference
order: 60
tags: [signing, reference, states, limits, permissions, roles, verification]
---
## Field types

Text, Number, Date, Checkbox, Select, Radio, Cells, Image, File, Signature,
Initials and Stamp. Select and Radio offer a list of options; Number fields
may carry a formula; Signature, Initials, Image, Stamp, File and Checkbox take
no font. Every field has a name (unique, up to 120 characters), a party,
**Required**, and optionally a default value and conditions.

Conditions: up to 20 rules per field, values up to 500 characters; formulas
up to 500 characters.

## States

| Template | Meaning |
|---|---|
| Draft | being prepared |
| Active | offered for new submissions |
| Archived | kept, not offered |

| Submission | Meaning |
|---|---|
| Draft | created, not sent |
| In progress | sent, collecting signatures |
| Completed | everyone signed |
| Expired | the expiry passed first |
| Cancelled | cancelled by the sender or declined by a signer |

| Signer | Meaning |
|---|---|
| Waiting | not invited yet (signs later in order) |
| Invited | invitation sent |
| Opened | opened the document |
| Signed | signed |
| Declined | declined, with a reason |

| Certificate | Meaning |
|---|---|
| Active | can sign |
| Expired | past its validity; renew |
| Needs renewal | the signing authority changed; renew |
| Revoked | can no longer sign; verification shows it as revoked |

Certificate kinds: **Certificate authority** (the tenant's signing CA),
**System** (signs audit trails), **Signer** (personal) and **Administrator**.

## Verification

**Signing → Verify** checks a PDF you upload (up to 50 MB) or a version of a
submission's document. Each signature gets:

| Column | Values |
|---|---|
| Integrity | **Intact**: the signed bytes are unchanged; **Modified** |
| Trust | **Trusted**: chains to your tenant's signing CA (current or earlier) or a trusted qualified provider; **Not trusted**; **Unknown** |
| Revocation | **Not revoked**, **Revoked**, or **Not checked** when no revocation data was available |
| Method | **Tenant certificate** or **Qualified card** |

The verdict also warns when the document was **modified after the last
signature**.

## Limits (defaults)

| Limit | Default |
|---|---|
| PDF size | 50 MB |
| PDF pages | 500 |
| fields per template | 500 |
| signers per submission | 50 |
| image in a field | 1 MiB |
| file in a field | 5 MiB |
| signatures per person | 10 per minute |
| PIN length | 6–32 characters |
| wrong PINs before lock | 5, locked for 15 minutes |
| personal certificate validity | 2 years |
| qualified card step | 10 minutes |
| expiry default on a template | 1–365 days |
| reminders | every 1–30 days, at most 1–20 |

## Permissions and roles

| Permission | Allows |
|---|---|
| `signing:sign` | sign or decline documents sent to you, your **To sign** list, your own certificate |
| `signing:read` | view templates, folders and every submission of the tenant; verify documents |
| `templates:manage` | create, edit, clone, archive and delete templates and folders; the builder |
| `submissions:create` | send documents and manage the submissions you sent |
| `submissions:manage` | cancel, resend, replace signers of and delete any submission |
| `certificates:manage` | the signing CA and certificates, revocation, administrator signing |
| `backup:manage` | export and import the tenant's signing data |

| Role | Permissions |
|---|---|
| Signing administrator | all |
| Signing operator | `signing:sign`, `signing:read`, `templates:manage`, `submissions:create`, `submissions:manage` |
| Signing sender | `signing:sign`, `signing:read`, `submissions:create` |
| Signing viewer | `signing:sign`, `signing:read` |

Built-in roles: owner and admin get all; operator gets the Signing operator
set; auditor the Signing viewer set; member gets `signing:sign`.

## Certificates page

With `certificates:manage`, **Signing → Certificates** lists the CA and every
certificate, searchable by subject, e-mail or serial and filterable by kind
and status.

- Open a certificate to see its details and PEM, and **Revoke** it with a
  reason. The tenant CA cannot be revoked there.
- **Download CRL** gets the tenant's revocation list.
- **New administrator certificate**: a name, an optional e-mail and a
  validity of 1–5 years.
- **Sign a document**: sign an uploaded PDF or a submission's document with
  an administrator certificate, with optional **Reason**, **Location**,
  **Contact**, a **Timestamp authority URL** (RFC 3161) and **TSA
  credentials (Warden secret id)**. The download link of the signed PDF works
  for one hour.

## Backup

With `backup:manage`, the **Backup** panel on the Certificates page exports
folders, templates, submissions with their documents and history,
certificates and stored files as one `.tar.gz`. Keys never leave in clear:
sealed keys are restored only where the module key is the same, otherwise
the certificates must be re-issued. **Import backup** either skips or
updates (overwrites) records that already exist and reports created,
updated and skipped records per kind.

# Feature Specification: Document Signing Module for v4

**Feature Branch**: `027-signing-v4`

**Created**: 2026-09-28

**Status**: Planned (plan + 102 tasks, 2026-09-29)

**Spans**: new v4 signing module (API + UI remote), go-tangra-notification-v4 (signing e-mails), go-tangra-scheduler-v4 (reminder and expiry task types), go-tangra-auth (signing permissions and module roles), go-tangra-portal-v4 (UI remote, gateway route, page security policy for the local QES app), go-tangra-docker (stack, object store bucket, mesh policies)

**Input**: User description: "V3 have following module /home/jadmin/projects/go-tangra/go-tangra-signing i like the same with same functionality but for V4"

**Decisions taken with the user (2026-09-28)**:

1. **Signing PKI stays in the module**: each tenant has its own signing CA
   inside the signing module, as in v3. The CA's private key is sealed with the
   module's key-encryption key (never stored in clear). Each signer's private
   key is encrypted with that signer's PIN. Signing certificates are not issued
   by LCM (FR-030–FR-036).
2. **Signers are platform users only**: every signer is a user of the tenant
   who signs after signing in to the portal. There are no public signing links
   and no unauthenticated endpoints (FR-020, SR-002).
3. **The unfinished v3 features are completed**: audit-trail PDF; decline by
   the signer, submission expiry and reminders; template folders and tags; and
   conditional and calculated fields in the template builder (FR-014,
   FR-026–FR-029, FR-040, FR-042, FR-043).

## Context

### What v3 provides

The v3 signing module is a DocuSeal-style service for collecting signatures
from several people on a PDF:

- **Templates**: an administrator uploads a PDF (up to 50 MB) and places
  fields on its pages in a visual builder. Fields are dragged from a palette
  onto the rendered pages, positioned as a percentage of the page, and
  assigned to a party ("First party", "Second party", …). "Auto-detect fields"
  finds placeholder lines (dots, underscores, lines) and their font and size.
  - Field types: text, number, signature, initials, date, checkbox, select,
    radio, image, file, cells and stamp. A payment type is declared but not
    used.
  - Templates can be cloned, edited, archived and deleted.
- **Submissions**: a user creates a submission from a template.
  - They choose 1–50 signers (name, e-mail, role, order) and a signing mode:
    sequential (one after another, in order) or parallel (everyone at once).
  - They can prefill field values.
  - The template's fields are frozen into the submission, so later template
    edits do not change documents already sent.
  - Sending invites the signers by e-mail: in sequential mode only the first,
    in parallel mode all of them.
- **Signer certificates**: signers sign with a personal certificate.
  - A signer without one first receives a "set up your signing certificate"
    e-mail and chooses a PIN.
  - The module then creates a key pair and a certificate issued by the
    tenant's own signing CA (created automatically on first use, valid 10
    years). The certificate is valid 2 years, and a Cyrillic name is
    transliterated to Latin.
  - The private key is encrypted with the PIN.
- **Signing**: the signer opens their signing page, sees the PDF with only
  their own fields, fills them in, and draws or types a signature (or it is
  captured by the QES device). They then sign in one of two ways:
  - **Local certificate**: with their PIN. The module fills the values into
    the PDF and applies a PAdES digital signature (SHA-256, the signer's
    certificate and the tenant CA). The first signature field carries the
    visible signature appearance, and extra signature fields get a visual
    stamp.
  - **Qualified electronic signature (QES)** through B-Trust BISS, a local
    smart-card application on the signer's computer. The module prepares the
    document and its digest. The browser has BISS sign it with the qualified
    card, and the module embeds the returned signature into the PDF.
  - In sequential mode the next signer is then invited. When all signers have
    signed, the submission is completed, everyone receives a "completed"
    e-mail, and a "submission completed" event is published.
- **Decline and cancel**: an administrator can decline a signer on their
  behalf (which cancels the submission and informs the others) or cancel the
  whole submission.
- **Certificates page**: administrators create CA and end-entity
  certificates, list them and revoke them. Revocation publishes a CRL.
- **Documents**: an administrator can sign any stored PDF with an
  administrator-held certificate. This is a certification signature, with
  optional RFC 3161 timestamp and embedded revocation data. Any PDF can be
  verified: signers, validity, issuer trust and revocation.
- **Backup/restore** of templates, submissions, certificates and events (not
  the PDFs), and an **event log** of every signing step.
- **Other modules**: e-mails go through the notification module, and the HR
  module consumed the completed/cancelled events.

### The v4 gap

v4 has no signing module. Porting it is not a copy, because v3 has functional
and security defects that v4 must not inherit:

- Cross-tenant: a submission can be created from another tenant's template.
  The PDF download proxy and field detection have no authentication or
  tenant check.
- Signing ignores the submission state (cancelled, draft and unsent documents
  can be signed) and the sequential order.
- A wrong or missing PIN does not fail signing. The signer is marked as
  having signed without a cryptographic signature.
- The QES result is embedded without checking it against the prepared digest
  and certificate. QES sessions live in one process's memory.
- The final document can lose values: it is regenerated from the blank
  template when no digital signature was applied.
- Permissions are declared but not enforced by the module. Any user can
  create templates, CA certificates, or complete a signer without signing.
- The CA private key is stored in clear when no key-encryption key is
  configured. Backups can export private keys.
- Certificate validity, expiry and revocation are not checked when signing.
  Verification trusts only public roots, so the module's own signatures always
  show as untrusted.
- Declared but never built:
  - template folders and tags
  - the audit-trail PDF
  - decline by the signer
  - submission expiry and reminders
  - conditional and calculated fields
- E-mails are sent as platform-level (not tenant) messages and silently fail
  if a channel named "Default SMTP" is missing.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Build a signing template from a PDF (Priority: P1)

A signing administrator uploads "Employment contract.pdf" to the folder
"HR / Contracts", tags it "hr" and opens the builder. They click
"Auto-detect fields", which places text fields on the dotted lines. They then:
- add a signature field and a date field for the "Employee" party and another
  signature field for the "Employer" party;
- mark the "Salary" field as required;
- save the template and set it active.

**Why this priority**: Nothing can be signed without a template.

**Independent Test**: Upload a PDF, place one field of every supported type
for two parties, save, reopen. Every field keeps its page, position, size,
type, party, font, size and required flag. Auto-detect proposes fields on a
PDF with dotted placeholders.

**Acceptance Scenarios**:

1. **Given** a PDF of up to 50 MB, **When** an administrator uploads it with a
   name, description, folder and tags, **Then** a draft template is created and
   its pages render in the builder. A file that is not a valid PDF, or is too
   large, is refused with a reason.
2. **Given** the builder, **When** the administrator drags fields from the
   palette onto pages, moves and resizes them, assigns them to parties, and
   sets their properties, **Then** saving stores all fields and reopening
   shows them unchanged.
3. **Given** a PDF with placeholder lines, **When** "Auto-detect fields" runs,
   **Then** proposed fields appear at the placeholders with the detected font
   and size, and the administrator can accept, change or remove them.
4. **Given** a template, **Then** it can be renamed, moved between folders,
   re-tagged, cloned (including its fields and PDF), archived, and deleted.
   Deleting is refused while unfinished submissions use it.
5. **Given** folders, **Then** administrators can create, rename, nest, move
   and delete them (a folder must be empty to delete), and filter the template
   list by folder, tag, status and name.

---

### User Story 2 - Send a document for signature and sign it in the portal (Priority: P1)

A user creates a submission from "Employment contract" for two colleagues,
Maria (Employee) and Ivan (Employer). Maria signs first, then Ivan
(sequential). The user prefills the salary and sends it.

Maria receives an e-mail with a link to the portal. She signs in and sees the
contract with only her fields. She fills them, draws her signature, enters her
PIN and signs. Ivan is then invited and signs the same way. Both receive the
completed, digitally signed PDF.

**Why this priority**: This is the core purpose of the module.

**Independent Test**: A two-signer sequential submission in the dev stack.
Each signer signs with the correct PIN, and the result is one PDF containing
both signers' values and two valid PAdES signatures, verified by the module
(and by a standard PDF reader as "signed").

**Acceptance Scenarios**:

1. **Given** an active template, **When** a user with permission to create
   submissions picks 1–50 signers from the tenant's users, assigns each to a
   template party, chooses sequential or parallel mode, optionally prefills
   values and an expiry date, **Then** a draft submission is created. It holds
   a frozen copy of the template's fields.
2. **Given** a draft submission, **When** it is sent, **Then** in sequential
   mode only the first signer is invited, and in parallel mode all signers are
   invited, by e-mail through the notification module. The e-mail links to the
   signer's page in the portal. Each signer also sees the document in their
   "To sign" list in the portal.
3. **Given** an invited signer who has signed in, **When** they open the
   document, **Then** they see the PDF with only their own fields (prefilled
   values shown), and nothing of other signers' pending fields.
4. **Given** the signer fills all required fields and provides their
   signature, **When** they sign with their correct PIN, **Then** the values
   are placed in the PDF, a PAdES signature with their certificate is applied,
   and they are marked as signed. In sequential mode the next signer is then
   invited.
5. **Given** the last signer has signed, **Then** the submission is completed.
   The final PDF contains every signer's values and signatures, and every
   participant (and the sender) receives a "completed" e-mail with access to
   the document. A "submission completed" event is published.
6. **Given** a wrong PIN, **Then** signing fails, nothing is marked as signed,
   and the signer can try again. After 5 consecutive wrong PINs, the signer's
   certificate is locked for 15 minutes (FR-034).
7. **Given** a user who is not the assigned signer, or a signer whose turn has
   not come in sequential mode, or a submission that is draft, cancelled,
   expired or completed, **Then** signing is refused with a clear reason.

---

### User Story 3 - Personal signing certificate with a PIN (Priority: P1)

The first time Maria is asked to sign, she has no signing certificate. The
portal asks her to set one up. She chooses a PIN and gets a certificate
"Maria Ivanova" issued by "Tangra Tenant Signing CA". She can view it
(validity, issuer, fingerprint) on her "My signing certificate" page, change
her PIN, and see which documents she signed with it.

**Why this priority**: Local signatures need a certificate. Without it, the
core flow cannot finish.

**Independent Test**: A user without a certificate is invited. Setup with a
PIN creates a certificate chained to the tenant CA, whose subject is the
user's (transliterated) name and e-mail. The private key cannot be used
without the PIN.

**Acceptance Scenarios**:

1. **Given** an invited signer without an active certificate, **When** they
   open the document, **Then** they are asked to set up a certificate first.
   They also receive a setup e-mail when invited.
2. **Given** setup, **When** the signer chooses a PIN (6–32 characters,
   entered twice), **Then** a key pair and a certificate are created:
   - issued by the tenant's signing CA, which is created on first need;
   - subject is the user's name, transliterated to Latin if needed, plus
     their e-mail;
   - valid 2 years, for digital signatures only.
   The private key is stored encrypted with the PIN. The PIN itself is never
   stored or logged.
3. **Given** a certificate, **Then** its owner can view it, change the PIN
   (old PIN required), and request a new certificate. A forgotten PIN means
   revoking the old certificate and setting up a new one.
4. **Given** an expired, revoked or locked certificate, **Then** it cannot be
   used to sign, and the signer is guided to set up a new one or wait for the
   lock to end.

---

### User Story 4 - Qualified electronic signature with the B-Trust BISS card app (Priority: P2)

Ivan has a qualified certificate on a smart card and the BISS application
installed. On the signing page he chooses "Sign with qualified card". The
portal finds BISS, asks him to choose his card certificate, and BISS asks for
the card PIN. The resulting PDF carries his qualified signature.

**Why this priority**: This is v3 functionality needed for legally qualified
signatures, but the local certificate covers most use.

**Independent Test**: With a test BISS-compatible signer (simulated in tests),
prepare and complete produce a PDF whose signature validates with the card
certificate. A returned signature that does not match the prepared digest or
the chosen certificate is refused.

**Acceptance Scenarios**:

1. **Given** the signing page and BISS running on the signer's computer,
   **When** the signer chooses QES, **Then** the portal detects BISS. It shows
   a clear message if BISS is not installed or not running.
2. **Given** the signer's chosen card certificate chain, **When** the document
   is prepared, **Then** the module fixes the document content with the
   signer's values and returns the digest to be signed. This preparation
   expires after 10 minutes and survives a restart or a second module
   instance.
3. **Given** BISS returns a signature, **When** it is submitted, **Then** the
   module verifies it against the prepared digest and the chosen certificate
   before embedding it. It then marks the signer as signed.
4. **Given** earlier signers already signed the PDF, **Then** a QES signature
   is added without invalidating their signatures.

---

### User Story 5 - Follow, decline, remind, expire, cancel (Priority: P2)

The sender follows the submission's progress: who was invited, opened,
signed, declined. Maria can decline with a reason instead of signing. A
submission not finished by its expiry date expires automatically. Signers who
have not signed get a reminder every 3 days. The sender can cancel it, resend
an invitation, or delete it.

**Why this priority**: This is needed for day-to-day use. The first three
stories work without it.

**Independent Test**: A submission with an expiry in the past is expired by
the scheduled job, and no one can sign it any more. A pending signer receives
reminders at the configured interval. A decline by the signer cancels the
submission and informs the others.

**Acceptance Scenarios**:

1. **Given** a submission, **Then** its page shows each signer's state
   (pending, invited, opened, signed, declined) with times and the event
   history (created, sent, opened, signed, declined, reminded, expired,
   cancelled, completed).
2. **Given** an invited signer, **When** they decline with a reason, **Then**
   the submission is cancelled, the sender and the other signers are informed
   by e-mail, and a "submission cancelled" event is published.
3. **Given** an expiry date, **When** it passes before completion, **Then**
   the submission expires. Signing is refused and the sender is informed.
4. **Given** reminders are enabled for a submission (interval in days,
   maximum count), **Then** each invited signer who has not signed gets a
   reminder e-mail at that interval until they sign, the maximum is reached,
   or the submission ends.
5. **Given** a submission in progress, **When** the sender cancels it with a
   reason, **Then** it is cancelled and everyone is informed. **When** they
   resend an invitation, **Then** the signer gets a new e-mail.
6. Expiry and reminders run as scheduled task types offered to the scheduler
   module (feature 026), not as hidden loops.

---

### User Story 6 - Conditional and calculated fields (Priority: P3)

In the builder, the administrator makes the field "Spouse name" visible and
required only when the checkbox "Married" is ticked. They make "Total" a
calculated number: `Quantity × Unit price`. The signer sees "Spouse name"
appear when ticking "Married", and "Total" updates as they type.

**Why this priority**: This is useful for complex forms but not needed for
most documents.

**Independent Test**: A template with a condition and a formula. The signing
page shows and hides the field and computes the total. The server evaluates
the same rules on submit and refuses a missing conditionally required value,
or a tampered calculated value.

**Acceptance Scenarios**:

1. **Given** the builder, **When** the administrator adds conditions to a
   field (show/hide and required depending on other fields' values: equals,
   not equals, contains, empty, not empty, checked, unchecked; combined with
   all/any), **Then** they are saved with the template.
2. **Given** a number field, **When** the administrator gives it a formula
   over other fields (+ − × ÷, parentheses, and functions round, min, max,
   sum), **Then** the field becomes read-only and calculated.
3. **Given** a signer, **Then** conditions and formulas apply live on the
   signing page. The server recomputes them on submit, and its result is what
   goes into the PDF.
4. Invalid formulas and conditions (unknown field, cycle, bad syntax) are
   refused at save time, and the offending field is named.

---

### User Story 7 - Certificates, document signing and verification for administrators (Priority: P2)

The signing administrator sees the tenant's signing CA and all issued
certificates, and revokes the certificate of a departed employee. They sign a
stored policy PDF with an administrator certificate and a timestamp. They
upload a PDF received from outside to check its signatures.

**Why this priority**: These are v3 administrator functions and are needed to
operate the PKI safely.

**Independent Test**: Revoking a certificate updates the tenant CRL, and
later signing with it is refused. Verification of a document signed by the
module reports the signer as trusted (tenant CA) and valid, or revoked after
revocation. Verification of a document signed by a public QES reports its
chain status.

**Acceptance Scenarios**:

1. **Given** the Certificates page, **Then** administrators list the tenant
   CA and certificates (subject, e-mail, serial, validity, status, owner) and
   can filter by status.
2. **Given** a certificate, **When** it is revoked with a reason, **Then** its
   status changes, the tenant CRL is republished, and it can no longer sign.
3. **Given** an administrator certificate (created by an administrator,
   sealed with the module key), **When** an administrator signs a stored or
   uploaded PDF with reason, location, contact and an optional timestamp
   authority, **Then** a certification signature with embedded revocation data
   is produced.
4. **Given** any PDF (stored or uploaded, up to 50 MB), **When** it is
   verified, **Then** each signature is listed with:
   - signer, time, reason and location;
   - integrity (valid or modified);
   - trust: the tenant signing CA and the platform's trusted roots are trusted;
   - revocation status, from the tenant CRL for the tenant's own certificates.

---

### User Story 8 - Audit-trail certificate for completed documents (Priority: P2)

When a submission completes, the final PDF is accompanied by an audit-trail
page. It lists the document's name and hash, and every signer's name, e-mail,
signing method, certificate, IP address and time. It also lists every event
(sent, opened, signed). The audit trail is part of the signed document
package that participants download.

**Why this priority**: It is evidence of the signing process. v3 declared it
but never produced it.

**Independent Test**: A completed two-signer submission has an audit-trail
PDF whose recorded hashes match the final document, and whose events match
the submission history.

**Acceptance Scenarios**:

1. **Given** a completed submission, **Then** an audit-trail PDF is generated
   with:
   - the document title and ID, and the SHA-256 of the original and the final
     document;
   - each signer's name, e-mail, method (local certificate or QES),
     certificate serial and issuer, IP address, user agent and time;
   - the chronological event list.
2. The audit-trail PDF is itself signed by the tenant signing CA's
   system-signing certificate, so tampering is detectable.
3. Participants can download the final PDF and the audit trail, separately or
   together.

---

### User Story 9 - Backup and restore (Priority: P3)

A platform administrator exports a tenant's signing data and restores it into
another stack.

**Why this priority**: This is v3 parity. The platform's database backups
already cover disaster recovery.

**Independent Test**: Export then import into an empty tenant restores
templates (with their PDFs), folders, submissions, signers, documents, events
and certificates. Private keys are never exported in clear.

**Acceptance Scenarios**:

1. **Given** a tenant, **When** a user with backup permission exports it,
   **Then** they get an archive with all signing records and the stored PDFs.
2. **Given** an archive, **When** it is imported with "skip existing" or
   "overwrite", **Then** the records and PDFs are restored.
3. Private keys are exported only in their encrypted form: PIN-encrypted user
   keys as they are, and sealed CA and administrator keys only when the target
   holds the same module key. Otherwise they are left out, and the affected
   certificates must be re-issued.

### Edge Cases

- **Template edited after sending**: the submission keeps its frozen field
  copy and its original PDF.
- **Signer removed from the tenant, or deactivated, before signing**: signing
  is refused. The sender can cancel, or replace that signer while the
  submission is not completed and the signer has not signed.
- **Same user assigned twice in one submission**: allowed (two parties). They
  sign each part in turn.
- **Parallel signers sign at the same moment**: each signature is applied to
  the latest document version. No signature is lost or overwritten, and
  concurrent signing is serialised per submission.
- **Signed PDF later modified outside the module**: verification reports the
  modification.
- **QES preparation expires or the portal is reloaded**: the signer starts the
  QES step again. Nothing is marked as signed.
- **PDF already containing signatures from outside**: filling fields must not
  invalidate them. If a template's PDF is already signed, fields are added as
  incremental updates, or the template is refused with a reason.
- **Very large PDFs, many pages, many fields**: bounded (50 MB, 500 fields per
  template, 50 signers). Rendering in the builder stays responsive by loading
  pages lazily.
- **Notification module unavailable**: signing continues. E-mails are retried
  and the failure is visible on the submission. Signers still see the
  document in their "To sign" list.
- **Tenant CA expires**: a new tenant CA is created before expiry. Existing
  signatures stay verifiable with the old CA.
- **Missing e-mail channel in the tenant**: the submission shows "e-mail could
  not be sent" per signer, instead of failing silently.

## Requirements *(mandatory)*

### Functional Requirements

**Templates**

- **FR-001**: Users with template management permission MUST be able to
  create a template by uploading a PDF (max 50 MB, validated as PDF), with
  name, description, folder and tags.
- **FR-002**: The builder MUST let them place, move, resize, delete and
  configure fields on any page:
  - supported types: text, number, signature, initials, date, checkbox,
    select, radio, image, file, cells and stamp;
  - properties: name, required, party, font, font size, options (select and
    radio), default value.
  Positions are stored relative to the page size.
- **FR-003**: The builder MUST offer automatic detection of placeholder lines
  as proposed text fields, with detected font and size.
- **FR-004**: Templates MUST support rename, move, tag, clone (PDF and
  fields), archive/activate and delete. Delete is refused while unfinished
  submissions reference the template.
- **FR-005**: Only active templates can be used for new submissions.
- **FR-006**: Template PDFs and generated documents MUST be stored in the
  platform object store, per tenant. They MUST only be served to callers
  authorised for the template or submission (FR-041).

**Submissions and signing**

- **FR-007**: Users with submission permission MUST be able to create a
  submission from an active template of their tenant:
  - 1–50 signers chosen from the tenant's active users, each assigned to a
    template party;
  - signing mode: sequential (order given) or parallel;
  - optional prefill values, expiry date, and reminder settings (interval
    and maximum count).
- **FR-008**: A submission MUST freeze a copy of the template's fields and
  reference the PDF version it was created from.
- **FR-009**: Sending MUST invite the first signer (sequential) or all
  signers (parallel):
  - by e-mail through the notification module, using the tenant's e-mail
    channel, with a link to the signer's page in the portal;
  - the document also appears in the signer's "To sign" list.
- **FR-010**: A signer MUST only see and fill their own fields. Other signers'
  values are visible only once they have signed and are part of the document.
- **FR-011**: Signing MUST be refused unless all of the following hold:
  - the caller is the assigned signer;
  - the submission is in progress and not expired;
  - in sequential mode, all earlier signers have signed;
  - the signer has not signed or declined;
  - every visible required field has a value.
- **FR-012**: When a signer signs with their local certificate, the module
  MUST do the following in one step:
  - place the values in the PDF;
  - apply a PAdES signature (SHA-256, signer certificate plus tenant CA
    chain), giving the first signature field the visible appearance;
  - store the new document version and mark the signer as signed.
  If any step fails (including a wrong PIN), nothing is marked as signed.
- **FR-013**: When the last signer signs, the submission MUST complete:
  - the final document is the last signed version;
  - the audit-trail PDF is generated (FR-043);
  - every participant and the sender are informed;
  - a "submission completed" event is published.
- **FR-014**: A signer MUST be able to decline with a reason. This cancels the
  submission, informs the sender and the other signers, and publishes a
  "submission cancelled" event.
- **FR-015**: The sender (or a user with submission management permission)
  MUST be able to cancel a submission with a reason, resend an invitation,
  replace a signer who has not signed, and delete a submission. Deleting also
  removes its stored documents.
- **FR-016**: The progress view MUST show each signer's state and times, and
  the submission's event history.
- **FR-017**: Participants of a completed submission MUST be able to download
  the final PDF and the audit trail. Signers of an unfinished one can download
  the current version after they have signed.
- **FR-018**: Concurrent signing of one submission MUST be serialised, so
  every signature is applied to the latest document version.
- **FR-019**: Every signer's page MUST also be reachable from a "To sign"
  list showing the caller's pending documents, and a "Signed by me" list.
- **FR-020**: All signing actions require a signed-in platform user of the
  submission's tenant. There are no public or token-only signing pages.

**Qualified signatures (B-Trust BISS)**

- **FR-021**: The signing page MUST offer "Sign with qualified card" and
  talk to the BISS application on the signer's computer: detection, choosing
  the signing certificate, and signing the digest. It MUST show clear
  messages when BISS is absent or refuses.
- **FR-022**: Preparation MUST fix the document content with the signer's
  values and return the digest in the form BISS requires. The prepared state
  MUST be kept for 10 minutes in shared storage, so it survives restarts and
  works with several module instances.
- **FR-023**: On completion the module MUST verify the returned signature
  against the prepared digest and the chosen certificate chain before
  embedding it. It then records the certificate (subject, serial, issuer) as
  the signer's method.
- **FR-024**: Earlier signatures in the document MUST stay valid after a QES
  signature is added.
- **FR-025**: The page security policy of the portal MUST allow browser
  connections to the local BISS application addresses (connections only: no
  scripts, frames or images from them). The portal policy is global, so the
  allowance is not limited to the signing page (research D6).

**Expiry and reminders**

- **FR-026**: A submission past its expiry date MUST become expired: no
  further signing, and the sender is informed.
- **FR-027**: Pending signers MUST receive reminder e-mails at the
  submission's interval, up to its maximum count, while the submission is in
  progress.
- **FR-028**: Expiry and reminders MUST be offered to the scheduler module as
  task types (`signing:expire-submissions`, `signing:send-reminders`), with a
  suggested schedule (every 15 minutes, and hourly). They MUST be safe to run
  repeatedly: no double reminders and no double expiry.
- **FR-029**: Default expiry (days) and reminder settings MUST be
  configurable per template and overridable per submission.

**Signer certificates and the tenant CA**

- **FR-030**: Each tenant MUST have its own signing CA, created on first
  need. Its private key is sealed with the module key-encryption key and never
  stored in clear. The module MUST refuse to start in production without that
  key.
- **FR-031**: A signer MUST be able to set up a personal certificate by
  choosing a PIN of 6–32 characters. The module creates the key pair and a
  certificate from the tenant CA, and stores the private key encrypted with a
  key derived from the PIN (strong, slow key derivation). It never stores the
  PIN.
- **FR-032**: The subject MUST be the user's name (Cyrillic transliterated to
  Latin) and e-mail. The certificate is valid 2 years, for digital signature
  and non-repudiation.
- **FR-033**: The owner MUST be able to view their certificate, change the
  PIN, and request a new certificate (the old one is revoked).
- **FR-034**: After 5 consecutive wrong PINs, the certificate MUST be locked
  for 15 minutes, and the owner is informed by e-mail. Successful use resets
  the counter.
- **FR-035**: Signing MUST check that the certificate is active, not expired,
  not revoked and not locked.
- **FR-036**: The tenant CA MUST be renewed before it expires. Old CAs remain
  available to verify earlier signatures.

**Administration: certificates, documents, verification**

- **FR-037**: Users with certificate management permission MUST be able to
  list the tenant CA and all certificates, filter by status, and revoke a
  certificate with a reason. Revocation republishes the tenant CRL.
- **FR-038**: They MUST be able to create administrator signing
  certificates (sealed with the module key) and use them to sign a stored or
  uploaded PDF:
  - certification signature;
  - reason, location and contact;
  - optional RFC 3161 timestamp authority (its credentials are referenced as
    a Warden secret, never entered in clear);
  - embedded revocation data.
- **FR-039**: Any user with signing view permission MUST be able to verify a
  PDF (stored or uploaded, max 50 MB). The result reports each signature's
  signer, time, reason, location, integrity, trust and revocation status. The
  tenant signing CA and the platform's trusted roots are trusted, and
  revocation is checked against the tenant CRL for tenant certificates.

**Folders, tags, conditions and formulas**

- **FR-040**: Template folders MUST be creatable, renamable, nestable,
  movable and deletable (when empty). Tags are free text, and the template
  list filters by both.
- **FR-041**: Documents, pages and field detection MUST be served only to
  signed-in users authorised for the template or submission in their tenant.
- **FR-042**: Fields MUST support conditions (show/hide and required,
  depending on other fields: equals, not equals, contains, empty, not empty,
  checked, unchecked; all/any) and number fields MUST support formulas (+ − ×
  ÷, parentheses, round, min, max, sum over fields).
  - The signing page applies them live.
  - The server recomputes them on submit, and its values are authoritative.
  - Invalid or cyclic rules are refused at save time.

**Audit trail, events, backup**

- **FR-043**: Every completed submission MUST have an audit-trail PDF,
  signed by the tenant CA's system certificate, containing:
  - the document's identity and hashes (original and final);
  - each signer's name, e-mail, method, certificate, IP address, user agent
    and time;
  - the chronological event list.
- **FR-044**: Every signing step MUST be recorded as an event, and the events
  are shown in the submission's history. Recorded steps:
  - created, sent, invited, opened, signed (with method), declined,
    reminded, expired, cancelled, completed;
  - certificate set up, PIN changed, locked and revoked;
  - document signed and verified.
- **FR-045**: "Submission completed" and "submission cancelled" events MUST
  be published on the platform event bus, for other modules. The payload has
  the submission, template, tenant and final document reference, with no field
  values.
- **FR-046**: A user with backup permission MUST be able to export and import
  a tenant's signing data, including the stored PDFs, with skip/overwrite on
  import. Keys are handled as described in User Story 9, scenario 3.

**Tenancy and permissions**

- **FR-047**: All data is per tenant. Every operation MUST be limited to the
  caller's tenant, and access to another tenant's objects MUST answer "not
  found". Platform administrators may act on any tenant for administration
  and backup.
- **FR-048**: Permissions MUST be enforced by the module:
  - `signing:read` (view templates, submissions, verify)
  - `templates:manage` (templates, folders, builder)
  - `submissions:create`, `submissions:manage` (cancel, resend, replace,
    delete others' submissions)
  - `certificates:manage` (CA, certificates, revoke, administrator
    certificates, document signing)
  - `backup:manage`
  Signing their own assigned documents and managing their own certificate
  needs only tenant membership.
- **FR-049**: The module MUST register the built-in module roles:
  - Signing Administrator (all);
  - Signing Operator (read, templates, submissions);
  - Signing Sender (read, create submissions, manage their own);
  - Signing Viewer (read).

### Security Requirements *(mandatory — Constitution: Development Workflow)*

- **Trust boundaries crossed**:
  - browser → gateway → signing (user API, document downloads)
  - browser → local BISS application (on the signer's computer, outside the
    platform)
  - signing → notification (e-mails over the mesh)
  - scheduler → signing (reminder and expiry task execution over the mesh)
  - signing → auth (permissions, users of the tenant)
  - signing → object store (PDFs)
  - signing → event bus
- **Data classification**:
  - **Confidential**: document contents, field values, signatures, signer
    personal data (name, e-mail, IP address).
  - **Secret**: private keys (PIN-encrypted or module-sealed) and PINs
    (never stored).
- **Authentication/Authorization**:
  - Every user call is authenticated at the gateway and authorised by the
    module's permissions in the caller's tenant.
  - Signing additionally requires being the assigned signer.
  - Module-to-module calls are authenticated by mesh identity and allowed by
    policy.
- **Threat scenarios**:
  - a user signing, reading or downloading another tenant's or another
    signer's document
  - signing out of order or on a cancelled or expired submission
  - PIN guessing
  - a forged QES result
  - CA key theft from the database or backups
  - tampering with a signed document or audit trail
  - a malicious PDF (parser abuse, oversized, crafted to crash the service)
  - leaking field values or keys through logs, events or e-mails
  - a hostile page abusing the local BISS application
- **SR-001**: Every operation MUST be limited to the caller's tenant (FR-047).
  Every object read by ID MUST be checked against the tenant, including:
  - templates used to create submissions
  - PDF and page downloads
  - field detection
- **SR-002**: No endpoint is reachable without an authenticated platform
  session. There are no bearer-token signing links (decision 2).
- **SR-003**: Private keys MUST never be stored, logged, exported or returned
  in clear:
  - CA and administrator keys are sealed with the module key;
  - signer keys are encrypted with a PIN-derived key using a slow key
    derivation;
  - PINs are never stored or logged;
  - decrypted keys live in memory only for the signing operation.
- **SR-004**: PIN attempts MUST be limited (FR-034), and signing attempts per
  user rate-limited.
- **SR-005**: QES results MUST be verified cryptographically against the
  prepared digest and certificate before use (FR-023).
- **SR-006**: Uploaded PDFs MUST be parsed with size, page and object limits,
  and time-bounded. Parsing failures refuse the file and do not crash the
  service.
- **SR-007**: Field values, document contents, signatures and keys MUST NOT
  appear in logs, metrics, events or audit entries. E-mails contain only the
  document name, sender and a portal link.
- **SR-008**: These actions MUST be audited with actor, tenant, object,
  action and outcome:
  - template create, update and delete;
  - submission create, send, cancel, delete and signer replace;
  - every signing, decline, certificate setup, PIN change, lock, revoke and
    administrator document signing.
- **SR-009**: The audit-trail PDF and every applied signature MUST make
  tampering detectable (signed, with document hashes).
- **SR-010**: The portal's security policy MUST permit the local BISS
  addresses for connections only, never for scripts, frames or images.

### Key Entities

- **Template folder**: tenant, name, parent, path, order.
- **Template**:
  - tenant, folder, name, description, tags, status (draft, active,
    archived);
  - PDF reference, file name and size;
  - fields: type, name, page, position, size, party, font, size, required,
    options, conditions, formula;
  - parties;
  - default expiry and reminder settings;
  - created/updated by and when.
- **Submission**:
  - tenant, template, frozen fields, PDF version, signing mode;
  - status (draft, in progress, completed, expired, cancelled);
  - expiry, reminder settings, sender;
  - current document version, final document, audit trail;
  - completion time and cancel reason.
- **Signer (submitter)**:
  - submission, platform user, name, e-mail, party, order;
  - status (pending, invited, opened, signed, declined) with times, decline
    reason;
  - values;
  - signing method (local certificate or QES) with certificate serial and
    issuer;
  - IP address, user agent, reminder count.
- **Signing certificate**:
  - tenant, owner user (or administrator/CA/system role), subject, e-mail,
    serial;
  - validity, status (active, revoked, expired, locked until);
  - issuer, certificate, encrypted private key (PIN or module seal),
    failed-PIN counter.
- **Tenant signing CA**: a certificate with CA role, its CRL and next
  update, and its predecessors.
- **Signing event**: tenant, submission, signer, actor, type, time, IP
  address, metadata without field values.
- **QES preparation**: signer, digest, chosen certificate chain, prepared
  document reference, expiry (10 minutes).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An administrator turns a 5-page PDF into an active two-party
  template, with auto-detected text fields and two signature fields, in under
  5 minutes.
- **SC-002**: A two-signer sequential submission completes end to end in the
  dev stack. The final PDF carries both signers' values and two signatures
  that the module and a standard PDF reader report as valid and unmodified.
- **SC-003**: Signing one document with a local certificate takes under 5
  seconds for a 50-page, 20 MB PDF.
- **SC-004**: 0 cross-tenant reads, downloads or signings, 0 out-of-order or
  post-cancel signings, and 0 signings with a wrong PIN succeed in the
  authorization test suite.
- **SC-005**: 0 private keys, PINs or field values are found in the database
  in clear, in logs, events, audit entries, e-mails or backups, in an
  end-to-end leak test.
- **SC-006**: A forged or mismatched QES signature is refused in 100% of
  tests. A valid simulated QES signature completes the signer.
- **SC-007**: Expiry and reminder task types run from the scheduler. Across
  repeated runs, each due reminder is sent exactly once and each overdue
  submission expires exactly once.
- **SC-008**: Every completed submission has an audit trail whose hashes
  match its final document, and verification detects any later modification.

## Assumptions

- **Object store**: PDFs, signature images and generated documents are kept
  in the platform's S3-compatible object store (RustFS in the stack), as the
  paperless module does, in a signing bucket with per-tenant prefixes.
- **E-mails**: e-mails use tenant-level system templates provided by the
  notification module (as `lcm.certificates_expiring` does in feature 026),
  sent over the tenant's default e-mail channel. Template texts are English
  and Bulgarian, as in v3.
- **Signer list**: the signer list comes from the tenant's active users in
  auth. Signers are not free-text e-mail addresses (decision 2).
- **Payment and phone/SMS**: the payment field type and SMS invitations are
  declared but unused in v3, and are not ported.
- **Signature image**: a drawn or typed signature image is limited to 1 MB.
  It is stored with the submission, and only placed in the PDF.
- **Signing algorithm**: keys are ECDSA P-256 with SHA-256, as in v3. The
  tenant CA is valid 10 years and signer certificates 2 years. These are
  platform settings.
- **Timestamps in the signing flow**: signer signatures carry no timestamp
  authority by default, as in v3. A tenant-level timestamp authority can be
  configured later. The administrator document signing supports one per
  signature (FR-038).
- **HR integration**: v4 has no HR module. The published events are
  available to future consumers.
- **Data migration**: migrating v3 signing data (templates, submissions,
  certificates) into v4 is not required. v4 starts empty.
- **Localization**: the UI is in English, like the other v4 modules. Signing
  e-mails are English, with the Bulgarian templates carried over.

## Dependencies

- v4 framework: mesh identity, per-module policies, module key-encryption key
  (sealed secrets), audit, event bus, and permission registration with
  module roles (feature 019).
- Portal v4:
  - gateway route and federated UI remote;
  - page security policy allowing the local BISS addresses for connections
    (like the KVM console frame sources of feature 025).
- Auth: the tenant's users (for choosing signers, and resolving names), plus
  a new policy-restricted lookup of members' e-mail addresses (research D1).
  Today auth never gives e-mail addresses to other modules.
- Notification v4: tenant e-mail channel, system templates and send
  operation.
- Scheduler v4 (feature 026): registration of the expiry and reminder task
  types.
- Object store (RustFS) bucket and credentials. Warden, for the timestamp
  authority credentials of administrator document signing.
- go-tangra-docker: stack service, database and role, object store bucket,
  mesh policies (signing → notification, scheduler → signing).

## Out of Scope

- Public or anonymous signing links, and signers outside the platform
  (decision 2).
- Payment fields and SMS/phone invitations.
- Issuing signing certificates from LCM (decision 1).
- Long-term validation (LTV) archiving of signer signatures beyond the
  embedded revocation data. Document timestamp renewal.
- Remote (cloud) qualified signing services other than the local B-Trust
  BISS application.
- Migrating v3 signing data.

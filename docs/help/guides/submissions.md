---
title: Send and follow submissions
summary: Send a template to signers in order or all at once, prefill values, set expiry and reminders, follow progress, resend, replace a signer, cancel, delete and download the results.
kind: guide
order: 30
tags: [signing, submissions, signers, reminders, expiry]
requires: submissions:create
---
A submission sends one active template to real people. Sending needs
`submissions:create` (*Signing sender*, *operator* or *administrator*).

## Create and send

1. Open **Signing → Submissions** and click **New submission**.
2. Choose the **Template** (only active templates are offered).
3. Fill in:
   - **Name**: shown to the signers.
   - **Signing order**: **One after another (in order)** or **All at once
     (any order)**. In order, use the arrows to set who signs first.
   - **Signers — one per party**: a colleague of your tenant for each party.
     The same person cannot hold two parties.
   - **Prefill values (optional)**: values for fields, such as a salary,
     written in before anyone signs.
   - **Expires** (optional): after this time unsigned slots can no longer be
     signed.
   - **Send reminders to signers who have not signed**: **Every (days)** 1–30
     and **At most (reminders)** 1–20.
4. Click **Create**. The submission is saved as a draft.
5. Click **Send now**, or **Send later** and use **Send** on its page.

Sending invites the first signer (in order) or every signer (all at once) by
e-mail and puts the document in their **To sign** list. In order, each
following signer is invited when the one before has signed.

Expiry and reminders are run by the platform's scheduler module; without its
signing tasks (an operator sets them up once) nothing expires and no reminders go
out.

The submission keeps its own copy of the template's fields: later changes to
the template do not affect it.

## Follow progress

The list shows every submission you sent; with `signing:read` it shows all
of the tenant's (tick **Only mine** to narrow it). Filter by name, status and
template. Open a submission to see:

- **Signers**: each one's party, status (`Waiting`, `Invited`, `Opened`,
  `Signed`, `Declined`), times, reminders sent, and the certificate or method
  used to sign. An **E-mail problem** badge means the invitation could not be
  delivered.
- **History**: every step, such as *Invitation sent*, *Opened the
  document*, *Signed*, *Reminder sent*, *Audit trail generated*.
- **Details**: order, progress, dates, expiry, reminders, cancel reason.
- **Downloads**: the current document version; when completed, the **Final
  signed document** and the **Audit trail**.

## Change a running submission

You can do this on submissions you sent, or on any submission with
`submissions:manage`:

- **Send the invitation again** (envelope icon) to a signer who is invited or
  has opened the document.
- **Replace** a signer who has not signed or declined: the new person takes
  over the party and is invited when it is their turn.
- **Cancel submission** with a **Reason**: signers who have not signed can no
  longer sign, and they receive the reason.
- **Delete**: removes the submission, its documents and its history. This
  cannot be undone.

## How a submission ends

| Status | When |
|---|---|
| Completed | the last signer signed; the audit trail is generated and everyone is informed |
| Cancelled | the sender cancelled it, or a signer declined |
| Expired | the expiry time passed before everyone signed |

The audit trail is a PDF signed by your tenant's signing authority. It holds
the document's hashes (original and final), each signer's name, e-mail,
method, certificate, IP address, browser and time, and the list of events.

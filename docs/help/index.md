---
title: Signing
summary: Prepare PDF templates with fields for each party, send them to colleagues for signature in order or in parallel, sign with your personal certificate or a qualified smart card, and verify signed documents.
kind: guide
order: 0
tags: [signing, documents, pdf, signatures, overview]
---
The signing module turns a PDF into a document your colleagues fill in and
sign in the portal. Every signature is a real digital (PAdES) signature in
the PDF, made with a certificate issued by your tenant's own signing
authority or with a qualified smart card. When everyone has signed, you get
the final PDF and an audit trail.

## Concepts

- **Template**: a PDF with **fields** placed on its pages, each belonging to
  a **party** (for example *Employee* and *Employer*). Only `active`
  templates can be sent.
- **Submission**: one template sent to real people, one signer per party,
  **in order** or **all at once**. It records every step.
- **Signer**: a platform user of your tenant. Signers see and fill only their
  own fields, then sign. Nobody signs through a public link: signing always
  happens signed in to the portal.
- **Signing certificate**: your personal certificate, protected by a PIN that
  only you know. You set it up the first time you sign.
- **Qualified signature**: a signature with your smart card through the
  B-Trust BISS application on your computer, instead of your personal
  certificate.

## Pages in the portal

| Page | Shows | Needs |
|---|---|---|
| To sign | documents waiting for your signature, and the ones you signed | `signing:sign` |
| Submissions | documents sent for signature and their progress | `submissions:create` |
| Templates | templates, folders and the field builder | `signing:read` |
| My certificate | your signing certificate, PIN, renewal | `signing:sign` |
| Verify | check the signatures of any PDF | `signing:read` |
| Certificates | the tenant's signing authority, every certificate, administrator signing and backups | `certificates:manage` |

## Where to go next

- [Get started](getting-started.md): from a PDF to a signed document.
- [Build templates](guides/templates.md)
- [Send and follow submissions](guides/submissions.md)
- [Sign a document](guides/sign.md): certificate, PIN, qualified card, decline.
- [Example: an employment contract with two parties](examples/employment-contract.md)
- [Reference](reference.md): field types, states, limits, permissions and roles.

---
title: Sign a document
summary: Open a document from To sign, fill in your fields, sign with your personal certificate or a qualified smart card, or decline; manage your certificate and PIN.
kind: guide
order: 40
tags: [signing, sign, certificate, pin, qualified, biss, decline]
requires: signing:sign
---
Every member of the tenant can sign the documents sent to them.

## Find your documents

Open **Signing → To sign**. The **To sign** tab lists documents waiting for
you, with the party you sign as; **Signed by me** lists the ones you signed.
Both update live. You also get an e-mail invitation with a link.

Click **Open**. If someone before you still has to sign, the page tells you
so and you get an e-mail when it is your turn.

## Fill in and sign

The page shows the document with **your** fields only; values of others
appear once they have signed. **Your fields** on the right lists them;
required ones carry `*`, and fields can appear, become required or be
calculated as you fill in others.

1. Fill in every required field.
2. Under **Your signature**, **Draw** your signature with a mouse, pen or
   finger, or **Type your name**. It is placed in the signature fields;
   without a signature field it is optional and shown in the signature's
   appearance.
3. Click **Sign with my certificate** and enter your PIN.

The values are written into the PDF and your digital signature is added on
top. Earlier signatures stay valid. If anything fails, including a wrong
PIN, nothing is signed.

## Sign with a qualified card

If you have a qualified smart card and the B-Trust BISS application on
your computer, click **Sign with qualified card** instead:

1. BISS is detected on your computer; choose your certificate in the BISS
   window.
2. Confirm with your card PIN in the BISS window.

The card's signature is checked before it is added to the PDF. Image and
file fields cannot be sent with a card signature; use your certificate for
such documents.

| Message | What to do |
|---|---|
| B-Trust BISS was not found | install and start BISS with your card in the reader, then try again |
| BISS did not answer in time | check the BISS window and the reader; nothing was signed |
| The signing step expired | the prepared step lasts 10 minutes; click **Start again** |
| The document changed meanwhile | another signer signed first; check the reloaded document and sign again |

## Decline

Click **Decline**, give a **Reason** and confirm. The submission is cancelled
for everyone and the sender gets your reason.

## Your certificate

**Signing → My certificate** holds your personal certificate, issued by the
tenant's signing authority in your name and e-mail and valid two years.

- **Set up**: choose a **PIN** (6 to 32 characters by default) and click
  **Create certificate**. Only the PIN unlocks your signing key; nobody else,
  not even an administrator, can sign with it.
- **Change PIN**: enter the current and the new PIN.
- **Renew**: a new certificate replaces the current one; your PIN stays the
  same. Renew when the status is **Expired** or **Needs renewal** (the
  signing authority changed).
- **Revoke**: the certificate can no longer sign, and documents it signed
  show it as revoked when verified. Set up a new one to sign again.

If you forget your PIN, revoke the certificate and set up a new one.

**Wrong PINs.** Each wrong PIN shows how many attempts are left. After 5 in
a row the certificate is locked for 15 minutes and you get an e-mail. A
successful signature resets the count.

The page also lists the documents you signed with the certificate.

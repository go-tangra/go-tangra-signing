---
title: Build templates
summary: Upload PDFs, organise them in folders, place fields for each party in the builder, add conditions and formulas, set submission defaults, and activate, clone or archive templates.
kind: guide
order: 20
tags: [signing, templates, builder, fields, parties, conditions, formulas]
requires: templates:manage
---
Open **Signing → Templates**. The left pane holds the folder tree; the list
shows each template's status and tags. Filter by **Search by name**,
**Status** or **Tag**. Users with only `signing:read` can open a template
to view its fields but not change it.

## Folders and details

- **New folder** in the folder pane; each folder has buttons to add a
  subfolder, **Rename or move** it, or delete it. Only empty folders can be
  deleted.
- **Upload template** takes a PDF of up to 50 MB (not encrypted) with a
  **Name**, **Description**, **Folder** and **Tags** (comma-separated).
- **Edit details** on a row changes those and the **Defaults for new
  submissions**: **Unsigned submissions expire** after 1–365 days, and
  **Remind signers who have not signed** every 1–30 days, at most 1–20
  times. Senders can change both per submission.

## The builder

Click a template to open the builder.

**Parties.** Each field belongs to one party. Add parties with **Add party**
and rename them (for example `Employee`, `Employer`). Removing a party that
still has fields asks whether to **Move the fields** to another party or
**Delete the fields**. Select a party before placing fields: new fields go
to it.

**Placing fields.** Drag a type from **Fields** onto a page, or click the
type and then the spot. Move and resize fields on the page. **Auto-detect
fields** proposes text fields on dotted or underlined placeholders, with
their font and size; they are marked **proposed** until you save. Review
them and remove the ones you do not need.

**Field properties** (right pane, for the selected field):

- **Name**: unique in the template (up to 120 characters).
- **Type**: Text, Number, Date, Checkbox, Select, Radio, Cells, Image, File,
  Signature, Initials or Stamp.
- **Party** and **Required**.
- **Font size (pt)** for text-like fields (empty: automatic).
- **Options (one per line)** for Select and Radio; a **Default value**.
- **Conditions and formula**, see below.

**All fields** lists every field with its type and page; click one to
select it.

**Save** stores the fields. If someone else saved the template after you
opened it, your save is refused with *Saved by someone else meanwhile*:
reload to get their version (your unsaved changes are lost).

## Conditions and formulas

Under **Conditions and formula**, tick **Depends on other fields** to make
the field depend on others:

- **Effect**: **Show the field only** when the rules hold, or **Require the
  field** when they hold.
- **When**: **all rules hold** or **any rule holds**.
- Each rule picks a **Field**, a **Condition** (`is`, `is not`, `contains`,
  `is empty`, `is filled`; for checkboxes `is checked`, `is not checked`)
  and, where needed, a **Value**. Up to 20 rules.

A **Number** field can instead be calculated with a **Formula** of up to 500
characters: field names in braces, `+ - * /`, parentheses and the functions
`round`, `min`, `max` and `sum`, for example `round({Price} * {Quantity}, 2)`.

The signing page applies conditions and formulas as the signer types. The
server computes them again when the signer signs, and its values are the
ones written into the PDF. Rules that refer to unknown fields or depend on
each other in a circle are refused when you save.

## Status

| Status | Meaning |
|---|---|
| Draft | being prepared; cannot be sent |
| Active | can be used for new submissions |
| Archived | no longer offered for new submissions |

**Activate** and **Archive** are in the builder and on each row. Changing a
template does not change submissions already created from it: each
submission keeps its own copy of the fields.

**Clone** copies the PDF and every field into a new draft. **Delete** is
refused while unfinished submissions use the template.

---
title: "Example: an employment contract with two parties"
summary: A contract template the employee signs first and the employer second, with a prefilled salary, a field that appears only when needed, and the final PDF with its audit trail.
kind: example
order: 50
tags: [signing, example, templates, submissions, conditions, sequential]
requires: templates:manage
---
**Goal.** HR sends employment contracts. The new employee fills in personal
details and signs first; the manager signs second. HR fills in the salary
before sending. If the employee is married, the contract asks for the
spouse's name.

**You need** *Signing operator* or *Signing administrator*.

## 1. The template

1. On **Signing → Templates**, create the folder `HR`, select it and upload
   `contract.pdf` with the name `Employment contract` and the tag `hr`.
2. In the builder, rename **First party** to `Employee` and **Add party**
   `Employer`.
3. Click **Auto-detect fields** to turn the dotted lines of the PDF into
   text fields, keep the useful ones and give them clear names.
4. With `Employee` selected, place:
   - `Full name` (Text, required),
   - `Married` (Checkbox),
   - `Spouse name` (Text),
   - `Employee signature` (Signature, required) and `Signed on` (Date).
5. With `Employer` selected, place `Salary` (Number, required) and
   `Employer signature` (Signature, required).
6. Select `Spouse name`, open **Conditions and formula**, tick **Depends on
   other fields** and set:
   - **Effect**: Require the field
   - **When**: all rules hold
   - Rule: **Field** `Married`, **Condition** `is checked`
7. **Save**, then **Activate**.
8. **Edit details**: tick **Remind signers who have not signed**, every
   `2` days, at most `3`, and **Unsigned submissions expire** after `14`
   days.

## 2. Send a contract

1. On **Signing → Submissions**, click **New submission** and choose
   `Employment contract`.
2. **Name**: `Contract — Maria Petrova`.
3. **Signing order**: One after another. `Employee`: Maria; `Employer`: Ivan.
4. Under **Prefill values**, enter `Salary (Employer)`: `4200`.
5. The expiry and reminders come from the template. Click **Create**, then
   **Send now**.

## 3. What happens

- Maria gets the invitation. She sets up her certificate the first time,
  fills in her name, ticks **Married**, which makes **Spouse name**
  required, draws her signature and signs with her PIN.
- Ivan is invited only then. He sees the salary already filled in and signs.
- The submission becomes **Completed**. Both, and you, are informed.

On the submission's page download the **Final signed document**, which
carries both signatures, and the **Audit trail**. **Signing → Verify** with
**A submission** selected shows both signatures as valid.

If Maria declines, the submission is cancelled and you get her reason; send
a new one after clearing it up.

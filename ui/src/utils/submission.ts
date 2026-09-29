// Display wording of submissions, signers and their history.
import type { SignerStatus, SignerView, Submission, SubmissionStatus } from '@/api/types'

export const SUBMISSION_STATUS_LABELS: Record<SubmissionStatus, string> = {
  draft: 'Draft', in_progress: 'In progress', completed: 'Completed', expired: 'Expired', cancelled: 'Cancelled',
}
export const SUBMISSION_STATUS_COLORS = { draft: 'warning', in_progress: 'info', completed: 'success', expired: 'neutral', cancelled: 'error' } as const

export const SIGNER_STATUS_LABELS: Record<SignerStatus, string> = {
  pending: 'Waiting', invited: 'Invited', opened: 'Opened', signed: 'Signed', declined: 'Declined',
}
export const SIGNER_STATUS_COLORS = { pending: 'neutral', invited: 'info', opened: 'primary', signed: 'success', declined: 'error' } as const

export const EVENT_LABELS: Record<string, string> = {
  'submission.created': 'Created',
  'submission.sent': 'Sent',
  'submission.completed': 'Completed',
  'submission.cancelled': 'Cancelled',
  'submission.expired': 'Expired',
  'signer.invited': 'Invitation sent',
  'signer.opened': 'Opened the document',
  'signer.signed': 'Signed',
  'signer.declined': 'Declined',
  'signer.replaced': 'Signer replaced',
  'invitation.resent': 'Invitation sent again',
  'reminder.sent': 'Reminder sent',
  'mail.failed': 'E-mail not delivered',
  'audit_trail.created': 'Audit trail generated',
}

/** "signer.opened" → its label; an unknown type is humanised ("foo.bar_baz" → "Foo bar baz"). */
export function eventLabel(type: string): string {
  const known = EVENT_LABELS[type]
  if (known) return known
  const s = type.replace(/[._]+/g, ' ').trim()
  return s ? s[0]!.toUpperCase() + s.slice(1) : type
}

/** A signer who can no longer change state. */
export const signerFinal = (s: SignerView): boolean => s.status === 'signed' || s.status === 'declined'
/** The submission still collects signatures (or is not sent yet). */
export const submissionOpen = (s: Submission): boolean => s.status === 'draft' || s.status === 'in_progress'

/** "2 of 3 signed". */
export function progress(s: Submission): string {
  const done = s.signers.filter((x) => x.status === 'signed').length
  return `${done} of ${s.signers.length} signed`
}

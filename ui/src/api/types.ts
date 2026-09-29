// Wire types of the signing API, derived from the generated OpenAPI schema
// (src/api/schema.d.ts, `npm run gen:api`) so a contract change breaks the build.
import type { components } from './schema.d'

type S = components['schemas']

export type Folder = S['Folder']
export type FolderWrite = S['FolderWrite']
export type Party = S['Party']
export type Rule = S['Rule']
export type Conditions = S['Conditions']
export type FieldType = S['FieldType']
/**
 * A placed field. Geometry (x, y, w, h) is a fraction 0..1 of the page with the
 * origin at the TOP-LEFT corner; page is 1-based.
 */
export type Field = S['Field']
export type Reminder = S['Reminder']
export type Template = S['Template']
export type TemplateStatus = Template['status']
export type TemplatePage = S['TemplatePage']
export type TemplatePatch = S['TemplatePatch']
export type TemplateFields = S['TemplateFields']
export type DetectedFields = S['DetectedFields']
export type Refusal = S['Error']

/** List filter of GET /templates (blank values are not sent). */
export interface TemplateFilter {
  /** A folder id, or "root" for templates outside any folder. */
  folder_id?: string | undefined
  tag?: string | undefined
  status?: TemplateStatus | undefined
  q?: string | undefined
}

/** Metadata sent with the PDF on upload (multipart fields). */
export interface TemplateUpload {
  name: string
  description?: string | undefined
  folder_id?: string | undefined
  tags?: string[] | undefined
}

export const FIELD_TYPES: readonly FieldType[] = ['text', 'number', 'signature', 'initials', 'date', 'checkbox', 'select', 'radio', 'image', 'file', 'cells', 'stamp']
export const TEMPLATE_STATUSES: readonly TemplateStatus[] = ['draft', 'active', 'archived']

// --- submissions (US2) ---
export type SignerView = S['SignerView']
export type SignerStatus = SignerView['status']
export type Submission = S['Submission']
export type SubmissionStatus = Submission['status']
export type SubmissionMode = Submission['mode']
export type SubmissionPage = S['SubmissionPage']
export type SubmissionCreate = S['SubmissionCreate']
export type SignerInput = SubmissionCreate['signers'][number]
export type SubmissionEvent = S['Event']
export type EventList = S['EventList']
export type Member = S['Member']
export type MemberList = S['MemberList']

/** List filter of GET /submissions (blank values are not sent). */
export interface SubmissionFilter {
  q?: string | undefined
  status?: SubmissionStatus | undefined
  template_id?: string | undefined
  mine?: boolean | undefined
}

export const SUBMISSION_STATUSES: readonly SubmissionStatus[] = ['draft', 'in_progress', 'completed', 'expired', 'cancelled']
export const SIGNER_STATUSES: readonly SignerStatus[] = ['pending', 'invited', 'opened', 'signed', 'declined']

// --- inbox and signing session (US2) ---
export type InboxItem = S['InboxItem']
export type InboxPage = S['InboxPage']
export type InboxState = 'to_sign' | 'signed'
export type CertificateState = S['CertificateState']
export type CertState = CertificateState['state']
/**
 * The caller's signing session. `values` (prefills plus the values of signers
 * who already signed, field id → string) is served by the module but not yet
 * declared in the OpenAPI Session schema, hence the extension here.
 */
export type Session = S['Session'] & { values?: Record<string, string> | undefined }
export type SignResult = S['SignResult']

// --- certificates (US3) ---
export type Certificate = S['Certificate']
export type MyCertificate = S['MyCertificate']
export type SignedEntry = NonNullable<MyCertificate['signed']>[number]

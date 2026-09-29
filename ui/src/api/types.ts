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

// --- qualified signature with a card through B-Trust BISS (US4) ---
export type QESPrepare = S['QESPrepare']
export type QESPrepared = S['QESPrepared']
export type QESComplete = S['QESComplete']

// --- certificate administration and document signing (US5) ---
export type CertificatePage = S['CertificatePage']
export type CertificateKind = Certificate['kind']
export type CertificateStatus = Certificate['status']
export type AdminCertificateCreate = S['AdminCertificateCreate']
export type RevocationReason = S['Revoke']['reason']
export type SignedDocument = S['SignedDocument']

/** List filter of GET /certificates (blank values are not sent). */
export interface CertificateFilter {
  q?: string | undefined
  kind?: CertificateKind | undefined
  status?: CertificateStatus | undefined
}

/** The PDF to sign or verify: an uploaded file, or a stored submission version (the current one when version is omitted). */
export type DocumentSource = { file: File } | { submission_id: string; version?: number | undefined }

/** The fields of POST /documents/sign besides the document (blank values are not sent). */
export interface DocumentSignOptions {
  certificate_id: string
  reason?: string | undefined
  location?: string | undefined
  contact?: string | undefined
  tsa_url?: string | undefined
  /** A Warden secret id holding the TSA credentials. */
  tsa_secret_ref?: string | undefined
}

export const CERTIFICATE_KINDS: readonly CertificateKind[] = ['ca', 'system', 'signer', 'admin']
export const CERTIFICATE_STATUSES: readonly CertificateStatus[] = ['active', 'revoked', 'expired', 'needs_reissue']
export const REVOCATION_REASONS: readonly RevocationReason[] = ['unspecified', 'key_compromise', 'affiliation_changed', 'superseded', 'cessation_of_operation']

// --- verification (US5) ---
export type VerifySignature = S['VerifySignature']
export type VerifyResult = S['VerifyResult']

// --- backup (backup:manage) ---
/**
 * Per-kind counts of a backup import (internal/backup Counts). POST
 * /backup/import answers a plain JSON object in the OpenAPI document, hence
 * the declaration here.
 */
export interface BackupCounts {
  folders: number
  templates: number
  submissions: number
  certificates: number
  objects: number
}
/** Summary of POST /backup/import. */
export interface BackupResult {
  created: BackupCounts
  updated: BackupCounts
  skipped: BackupCounts
  /** Certificates restored as needs_reissue (their keys could not be restored under this module key). */
  needs_reissue: number
  errors?: string[] | undefined
}
export type BackupMode = 'skip' | 'overwrite'
export const BACKUP_MODES: readonly BackupMode[] = ['skip', 'overwrite']

import type { CertificateKind, CertificateStatus, RevocationReason } from '@/api/types'

export const CERT_KIND_LABELS: Record<CertificateKind, string> = { ca: 'Certificate authority', system: 'System', signer: 'Signer', admin: 'Administrator' }
export const CERT_STATUS_LABELS: Record<CertificateStatus, string> = { active: 'Active', revoked: 'Revoked', expired: 'Expired', needs_reissue: 'Needs renewal' }
export const CERT_STATUS_COLORS = { active: 'success', revoked: 'error', expired: 'neutral', needs_reissue: 'warning' } as const
export const REVOCATION_LABELS: Record<RevocationReason, string> = {
  unspecified: 'Unspecified',
  key_compromise: 'Key compromise',
  affiliation_changed: 'Affiliation changed',
  superseded: 'Superseded',
  cessation_of_operation: 'Cessation of operation',
}

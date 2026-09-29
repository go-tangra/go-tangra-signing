// Certificate administration (certificates:manage): the tenant's certificates
// (CA, system, signer, administrator) with filters, details, revocation, new
// administrator certificates, the CRL, and signing a PDF with an administrator
// certificate (the signed copy is downloadable for an hour).
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, describe, postForm } from '@/api/client'
import type { AdminCertificateCreate, Certificate, CertificateFilter, CertificatePage, DocumentSignOptions, DocumentSource, RevocationReason, SignedDocument } from '@/api/types'
import { appendFields, appendSource } from '@/utils/documents'

export const PAGE_SIZE = 25

export const useCertificates = defineStore('signing-certificates', () => {
  const items = ref<Certificate[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(PAGE_SIZE)
  const filter = ref<CertificateFilter>({})
  const loading = ref(false)
  const error = ref('')

  /** Loads one page with the filter (blank filter values are not sent). */
  async function list(f: CertificateFilter = filter.value, p = 1): Promise<void> {
    loading.value = true
    error.value = ''
    filter.value = { ...f }
    try {
      const res = await api<CertificatePage>('GET', 'certificates', undefined, { query: { q: f.q?.trim() || undefined, kind: f.kind, status: f.status, page: p, page_size: pageSize.value } })
      items.value = res.items ?? []
      total.value = res.total ?? 0
      page.value = p
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  const reload = () => list(filter.value, page.value)

  function replace(c: Certificate): Certificate {
    items.value = items.value.map((x) => (x.id === c.id ? c : x))
    return c
  }

  /** One certificate with its PEM (never the key). */
  const get = (id: string) => api<Certificate>('GET', 'certificates/' + id)

  const revoke = async (id: string, reason: RevocationReason) => replace(await api<Certificate>('POST', `certificates/${id}/revoke`, { reason }))

  /** Issues an administrator certificate (subject, e-mail, 1–5 years). */
  async function create(body: AdminCertificateCreate): Promise<Certificate> {
    const c = await api<Certificate>('POST', 'certificates', body)
    items.value = [c, ...items.value]
    total.value += 1
    return c
  }

  /** Active administrator certificates (the ones that can sign documents). */
  async function signingCertificates(): Promise<Certificate[]> {
    const res = await api<CertificatePage>('GET', 'certificates', undefined, { query: { kind: 'admin', status: 'active', page: 1, page_size: 100 } })
    return res.items ?? []
  }

  /** Signs a PDF (uploaded, or a stored submission version) with an administrator certificate. */
  async function signDocument(src: DocumentSource, o: DocumentSignOptions): Promise<SignedDocument> {
    const form = new FormData()
    appendSource(form, src)
    form.append('certificate_id', o.certificate_id)
    appendFields(form, { reason: o.reason, location: o.location, contact: o.contact, tsa_url: o.tsa_url, tsa_secret_ref: o.tsa_secret_ref })
    return postForm<SignedDocument>('documents/sign', form)
  }

  /** Download URLs (GET, binary): the tenant CA's CRL and a signed document. */
  const crlUrl = () => api.fileUrl('ca/crl')
  const documentUrl = (id: string) => api.fileUrl('documents/' + id)

  return { items, total, page, pageSize, filter, loading, error, list, reload, replace, get, revoke, create, signingCertificates, signDocument, crlUrl, documentUrl }
})

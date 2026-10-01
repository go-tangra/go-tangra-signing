// Certificate administration (certificates:manage): the tenant's certificates
// (CA, system, signer, administrator) with filters, details, revocation, new
// administrator certificates, the CRL, and signing a PDF with an administrator
// certificate (the signed copy is downloadable for an hour).
import { defineStore } from 'pinia'
import type { ListParams } from '@go-tangra/ui'
import { ref } from 'vue'
import { api, describe, pageQuery, postForm } from '@/api/client'
import type { AdminCertificateCreate, Certificate, CertificateFilter, CertificatePage, DocumentSignOptions, DocumentSource, RevocationReason, SignedDocument } from '@/api/types'
import { appendFields, appendSource } from '@/utils/documents'

export const PAGE_SIZE = 25

export const useCertificates = defineStore('signing-certificates', () => {
  const items = ref<Certificate[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(PAGE_SIZE)
  const filter = ref<CertificateFilter>({})
  const params = ref<Partial<ListParams>>({})
  const loading = ref(false)
  const error = ref('')
  let seq = 0

  /**
   * Loads one page with the filter (blank filter values are not sent) and the
   * list parameters (page, page_size, sort, order; omitted ones take the
   * server defaults). Resolves with the page the server returned, or null when
   * the request failed or a newer one superseded it.
   */
  async function list(f: CertificateFilter = filter.value, q: Partial<ListParams> = { page: 1 }): Promise<number | null> {
    const mine = ++seq
    loading.value = true
    error.value = ''
    filter.value = { ...f }
    params.value = { ...q }
    try {
      const res = await api<CertificatePage>('GET', 'certificates', undefined, { query: { q: f.q?.trim() || undefined, kind: f.kind, status: f.status, ...pageQuery(q, pageSize.value) } })
      if (mine !== seq) return null
      items.value = res.items ?? []
      total.value = res.total ?? 0
      page.value = res.page ?? q.page ?? 1
      pageSize.value = res.page_size ?? pageSize.value
      return page.value
    } catch (e) {
      if (mine === seq) error.value = describe(e)
      return null
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  const reload = () => list(filter.value, { ...params.value, page: page.value })

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

import { defineStore } from 'pinia'
import type { ListParams } from '@go-tangra/ui'
import { ref } from 'vue'
import { api, describe, pageQuery } from '@/api/client'
import type { EventList, Member, MemberList, Submission, SubmissionCreate, SubmissionEvent, SubmissionFilter, SubmissionPage } from '@/api/types'

export const PAGE_SIZE = 25

export const useSubmissions = defineStore('signing-submissions', () => {
  const items = ref<Submission[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(PAGE_SIZE)
  const filter = ref<SubmissionFilter>({})
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
  async function list(f: SubmissionFilter = filter.value, q: Partial<ListParams> = { page: 1 }): Promise<number | null> {
    const mine = ++seq
    loading.value = true
    error.value = ''
    filter.value = { ...f }
    params.value = { ...q }
    try {
      const query = { q: f.q, status: f.status, template_id: f.template_id, mine: f.mine ? true : undefined, ...pageQuery(q, pageSize.value) }
      const res = await api<SubmissionPage>('GET', 'submissions', undefined, { query })
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

  function replace(s: Submission): Submission {
    items.value = items.value.map((x) => (x.id === s.id ? s : x))
    return s
  }

  const get = (id: string) => api<Submission>('GET', 'submissions/' + id)

  /** Creates a draft; refused with invalid_signer unless there is exactly one active member per template party. */
  async function create(body: SubmissionCreate): Promise<Submission> {
    const s = await api<Submission>('POST', 'submissions', body)
    items.value = [s, ...items.value]
    total.value += 1
    return s
  }

  const send = async (id: string) => replace(await api<Submission>('POST', `submissions/${id}/send`))
  const cancel = async (id: string, reason: string) => replace(await api<Submission>('POST', `submissions/${id}/cancel`, { reason: reason.trim() }))
  const replaceSigner = async (id: string, sid: string, userId: string) => replace(await api<Submission>('PUT', `submissions/${id}/signers/${sid}`, { user_id: userId }))
  const resend = async (id: string, sid: string) => replace(await api<Submission>('POST', `submissions/${id}/signers/${sid}/resend`))

  async function remove(id: string): Promise<void> {
    await api('DELETE', 'submissions/' + id)
    const before = items.value.length
    items.value = items.value.filter((x) => x.id !== id)
    if (items.value.length < before) total.value = Math.max(0, total.value - 1)
  }

  async function events(id: string): Promise<SubmissionEvent[]> {
    const res = await api<EventList>('GET', `submissions/${id}/events`)
    return res.items ?? []
  }

  /** Active tenant members for the signer picker (names only; needs submissions:create). */
  async function users(q: string): Promise<Member[]> {
    const res = await api<MemberList>('GET', 'users', undefined, { query: { q: q.trim() || undefined } })
    return res.items ?? []
  }

  /** Download URLs (GET, binary): a document version (current when omitted) and the audit trail. */
  const documentUrl = (id: string, version?: number | null) => api.fileUrl(`submissions/${id}/document`) + (version === undefined || version === null ? '' : `?version=${version}`)
  const auditTrailUrl = (id: string) => api.fileUrl(`submissions/${id}/audit-trail`)

  return { items, total, page, pageSize, filter, loading, error, list, reload, replace, get, create, send, cancel, replaceSigner, resend, remove, events, users, documentUrl, auditTrailUrl }
})

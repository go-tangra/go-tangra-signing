import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, describe } from '@/api/client'
import type { EventList, Member, MemberList, Submission, SubmissionCreate, SubmissionEvent, SubmissionFilter, SubmissionPage } from '@/api/types'

export const PAGE_SIZE = 25

export const useSubmissions = defineStore('signing-submissions', () => {
  const items = ref<Submission[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(PAGE_SIZE)
  const filter = ref<SubmissionFilter>({})
  const loading = ref(false)
  const error = ref('')

  /** Loads one page with the filter (blank filter values are not sent). */
  async function list(f: SubmissionFilter = filter.value, p = 1): Promise<void> {
    loading.value = true
    error.value = ''
    filter.value = { ...f }
    try {
      const query = { q: f.q, status: f.status, template_id: f.template_id, mine: f.mine ? true : undefined, page: p, page_size: pageSize.value }
      const res = await api<SubmissionPage>('GET', 'submissions', undefined, { query })
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

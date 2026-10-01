import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { ListParams } from '@go-tangra/ui'
import { api, describe, pageQuery } from '@/api/client'
import type { InboxItem, InboxPage, InboxState } from '@/api/types'

export const INBOX_PAGE_SIZE = 50

export const useInbox = defineStore('signing-inbox', () => {
  const state = ref<InboxState>('to_sign')
  const items = ref<InboxItem[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(INBOX_PAGE_SIZE)
  const params = ref<Partial<ListParams>>({})
  const loading = ref(false)
  const error = ref('')
  let seq = 0

  /**
   * The caller's signer slots in the state (to_sign: waiting for them; signed:
   * done), one page with the list parameters (page, page_size, sort, order).
   * Resolves with the page the server returned, or null when the request
   * failed or a newer one (e.g. for the other tab) superseded it.
   */
  async function list(s: InboxState = state.value, q: Partial<ListParams> = { page: 1 }): Promise<number | null> {
    const mine = ++seq
    state.value = s
    params.value = { ...q }
    loading.value = true
    error.value = ''
    try {
      const res = await api<InboxPage>('GET', 'inbox', undefined, { query: { state: s, ...pageQuery(q, pageSize.value) } })
      // A late answer for the other tab must not overwrite this one.
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

  const reload = () => list(state.value, { ...params.value, page: page.value })

  return { state, items, total, page, pageSize, loading, error, list, reload }
})

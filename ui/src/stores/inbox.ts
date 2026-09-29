import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, describe } from '@/api/client'
import type { InboxItem, InboxPage, InboxState } from '@/api/types'

export const INBOX_PAGE_SIZE = 50

export const useInbox = defineStore('signing-inbox', () => {
  const state = ref<InboxState>('to_sign')
  const items = ref<InboxItem[]>([])
  const total = ref(0)
  const page = ref(1)
  const loading = ref(false)
  const error = ref('')

  /** The caller's signer slots in the state (to_sign: waiting for them; signed: done). */
  async function list(s: InboxState = state.value, p = 1): Promise<void> {
    state.value = s
    loading.value = true
    error.value = ''
    try {
      const res = await api<InboxPage>('GET', 'inbox', undefined, { query: { state: s, page: p, page_size: INBOX_PAGE_SIZE } })
      // A late answer for the other tab must not overwrite this one.
      if (state.value !== s) return
      items.value = res.items ?? []
      total.value = res.total ?? 0
      page.value = p
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  const reload = () => list(state.value, page.value)

  return { state, items, total, page, loading, error, list, reload }
})

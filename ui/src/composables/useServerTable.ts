// One server-paged, server-sorted table (go-tangra specs/032-server-side-tables):
// page / size / sort live in the URL under `key` (kit useListQuery), every
// change of them reloads, `search()` applies changed filters from page 1, and
// the page the server answered (it clamps pages beyond the end) is adopted.
import { watch } from 'vue'
import { useListQuery, type ListParams, type ListQuery, type ListQueryOptions } from '@go-tangra/ui'

export interface ServerTable {
  lq: ListQuery
  /** Reload the current page (live events, after an edit). */
  reload(): Promise<void>
  /** Filters changed: back to page 1 (which reloads), or reload in place. */
  search(): void
}

/** `load` fetches one page and resolves with the page the server returned, or null when it failed or was superseded. */
export function useServerTable(key: string, opts: ListQueryOptions, load: (q: ListParams) => Promise<number | null>): ServerTable {
  const lq = useListQuery(key, opts)

  async function reload(): Promise<void> {
    const page = await lq.track(load(lq.query.value))
    if (page) lq.clampTo(page)
  }

  function search(): void {
    if (lq.page.value !== 1) lq.resetPage()
    else void reload()
  }

  watch(lq.query, () => void reload())
  return { lq, reload, search }
}

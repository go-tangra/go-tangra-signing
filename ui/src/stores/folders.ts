import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, describe } from '@/api/client'
import type { Folder, FolderWrite } from '@/api/types'
import { buildTree } from '@/utils/folderTree'

export const useFolders = defineStore('signing-folders', () => {
  const items = ref<Folder[]>([])
  const loading = ref(false)
  const error = ref('')
  const tree = computed(() => buildTree(items.value))

  async function list(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: Folder[] }>('GET', 'folders')
      items.value = res.items ?? []
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  async function create(name: string, parentId?: string | null): Promise<Folder> {
    const body: FolderWrite = { name: name.trim() }
    if (parentId) body.parent_id = parentId
    const f = await api<Folder>('POST', 'folders', body)
    items.value = [...items.value, f]
    return f
  }

  /** Rename and/or move; a move changes the paths below, so the list is reloaded. */
  async function update(id: string, body: FolderWrite): Promise<Folder> {
    const f = await api<Folder>('PATCH', 'folders/' + id, body)
    await list()
    return f
  }

  const rename = (id: string, name: string) => update(id, { name: name.trim() })
  const move = (id: string, parentId: string | null) => update(id, { parent_id: parentId })

  /** Refused with 409 folder_not_empty while it holds folders or templates. */
  async function remove(id: string): Promise<void> {
    await api('DELETE', 'folders/' + id)
    items.value = items.value.filter((f) => f.id !== id)
  }

  return { items, loading, error, tree, list, create, update, rename, move, remove }
})

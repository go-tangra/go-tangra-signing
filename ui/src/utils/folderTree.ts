// Template folders arrive as a flat list with materialized paths
// ("/HR/Contracts"); the tree, the pickers and the move checks are built here.
import type { TreeNode } from '@go-tangra/ui'
import type { Folder } from '@/api/types'

export interface FolderNode extends TreeNode {
  children: FolderNode[]
  meta: { folder: Folder }
}

const order = (a: Folder, b: Folder) => (a.sort_order ?? 0) - (b.sort_order ?? 0) || a.name.localeCompare(b.name)

/** Nests the flat folder list by parent_id; siblings by sort order, then name. A folder whose parent is missing is shown at the top. */
export function buildTree(folders: readonly Folder[]): FolderNode[] {
  const nodes = new Map<string, FolderNode>()
  for (const f of [...folders].sort(order)) nodes.set(f.id, { id: f.id, label: f.name, children: [], meta: { folder: f } })
  const roots: FolderNode[] = []
  for (const n of nodes.values()) {
    const parent = n.meta.folder.parent_id ? nodes.get(n.meta.folder.parent_id) : undefined
    if (parent) parent.children.push(n)
    else roots.push(n)
  }
  return roots
}

/** The folder and every folder below it (ids). */
export function subtreeIds(folders: readonly Folder[], id: string): Set<string> {
  const out = new Set([id])
  let grew = true
  while (grew) {
    grew = false
    for (const f of folders) {
      if (f.parent_id && out.has(f.parent_id) && !out.has(f.id)) {
        out.add(f.id)
        grew = true
      }
    }
  }
  return out
}

/** Picker options "HR / Contracts" in tree order, leaving out `exclude` and its subtree (a folder cannot move into itself). */
export function folderOptions(folders: readonly Folder[], exclude?: string): { title: string; value: string }[] {
  const skip = exclude ? subtreeIds(folders, exclude) : new Set<string>()
  const out: { title: string; value: string }[] = []
  const walk = (nodes: FolderNode[], trail: string[]) => {
    for (const n of nodes) {
      if (skip.has(n.id)) continue
      const path = [...trail, n.label]
      out.push({ title: path.join(' / '), value: n.id })
      walk(n.children, path)
    }
  }
  walk(buildTree(folders), [])
  return out
}

/** "HR / Contracts" for a folder id ('' when unknown). */
export function folderLabel(folders: readonly Folder[], id: string | null | undefined): string {
  if (!id) return ''
  const f = folders.find((x) => x.id === id)
  return f ? f.path.split('/').filter(Boolean).join(' / ') : ''
}

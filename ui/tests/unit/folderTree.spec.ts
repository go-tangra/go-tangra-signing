import { describe, expect, it } from 'vitest'
import { buildTree, folderLabel, folderOptions, subtreeIds } from '@/utils/folderTree'
import { folder } from './helpers'

// Flat, path-ordered as GET /folders returns them.
const flat = [
  folder({ id: 'hr', name: 'HR', path: '/HR', sort_order: 1 }),
  folder({ id: 'contracts', parent_id: 'hr', name: 'Contracts', path: '/HR/Contracts' }),
  folder({ id: 'offers', parent_id: 'hr', name: 'Offers', path: '/HR/Offers' }),
  folder({ id: 'nda', parent_id: 'contracts', name: 'NDA', path: '/HR/Contracts/NDA' }),
  folder({ id: 'legal', name: 'Legal', path: '/Legal', sort_order: 0 }),
]

describe('folder tree', () => {
  it('nests the flat list by parent; siblings by sort order then name', () => {
    const tree = buildTree(flat)
    const shape = (n: ReturnType<typeof buildTree>): unknown => n.map((x) => (x.children.length ? { [x.label]: shape(x.children) } : x.label))
    expect(shape(tree)).toEqual(['Legal', { HR: [{ Contracts: ['NDA'] }, 'Offers'] }])
    expect(tree[1]!.meta.folder.id).toBe('hr')
  })

  it('a folder whose parent is missing surfaces at the top instead of vanishing', () => {
    expect(buildTree([folder({ id: 'x', parent_id: 'gone', name: 'Orphan', path: '/Gone/Orphan' })]).map((n) => n.label)).toEqual(['Orphan'])
  })

  it('pickers show full paths and leave out a folder being moved and its subtree', () => {
    expect(folderOptions(flat).map((o) => o.title)).toEqual(['Legal', 'HR', 'HR / Contracts', 'HR / Contracts / NDA', 'HR / Offers'])
    expect(folderOptions(flat, 'contracts').map((o) => o.value)).toEqual(['legal', 'hr', 'offers'])
    expect([...subtreeIds(flat, 'hr')].sort()).toEqual(['contracts', 'hr', 'nda', 'offers'])
    expect(folderLabel(flat, 'nda')).toBe('HR / Contracts / NDA')
    expect(folderLabel(flat, null)).toBe('')
  })
})

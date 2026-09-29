import { describe, expect, it } from 'vitest'
import { createField, mergeDetected, nameErrors, newFieldId, nextPartyKey, nextPartyName, partyColor, partyErrors, uniqueName } from '@/utils/fields'
import { field } from './helpers'

describe('builder field rules', () => {
  it('field ids are stable "f-" strings, unique against the taken ones', () => {
    const id = newFieldId()
    expect(id).toMatch(/^f-[a-z0-9]{10}$/)
    expect(newFieldId(new Set([id]))).not.toBe(id)
  })

  it('names are unique case-insensitively ("Text", "Text 2", …)', () => {
    const fs = [field({ id: 'a', name: 'Text' }), field({ id: 'b', name: 'text 2' })]
    expect(uniqueName('Text', fs)).toBe('Text 3')
    expect(uniqueName('Date', fs)).toBe('Date')
    expect(uniqueName('Text', fs, 'a')).toBe('Text')
    expect(nameErrors([field({ id: 'a', name: 'Salary' }), field({ id: 'b', name: ' salary ' }), field({ id: 'c', name: '  ' })])).toEqual({
      a: 'Another field has this name.', b: 'Another field has this name.', c: 'Enter a name.',
    })
  })

  it('a field created from the palette sits at the drop point with defaults for its type', () => {
    const existing = [field({ id: 'x', name: 'Signature' })]
    const f = createField('signature', 2, 0.5, 0.5, 'p2', existing)
    expect(f).toMatchObject({ name: 'Signature 2', type: 'signature', party: 'p2', page: 2, x: 0.375, y: 0.465, w: 0.25, h: 0.07, required: true })
    expect(f.id).toMatch(/^f-/)
    expect(createField('select', 1, 0.2, 0.2, 'p1', []).options).toEqual(['Option 1', 'Option 2'])
    expect(createField('text', 1, 0.2, 0.2, 'p1', [])).not.toHaveProperty('options')
  })

  it('auto-detected proposals get new ids, unique names and the party; duplicates by geometry are dropped', () => {
    const existing = [field({ id: 'f-1', name: 'Text 1', page: 1, x: 0.1, y: 0.2, w: 0.25, h: 0.03 })]
    const detected = [
      field({ id: 'detected-1', name: 'Text 1', page: 1, x: 0.101, y: 0.2, w: 0.25, h: 0.03, party: 'p1', font: 'Helvetica', font_size: 11 }), // same spot as f-1
      field({ id: 'detected-2', name: 'Text 2', page: 1, x: 0.1, y: 0.5, w: 0.3, h: 0.03, party: 'p1', font: 'Helvetica', font_size: 11 }),
      field({ id: 'detected-3', name: 'Text 3', page: 1, x: 0.1, y: 0.5, w: 0.3, h: 0.03, party: 'p1' }), // same spot as detected-2
      field({ id: 'detected-4', name: 'Text 1', page: 2, x: 0.1, y: 0.2, w: 0.25, h: 0.03, party: 'p1' }), // same geometry, other page
    ]
    const added = mergeDetected(existing, detected, 'p2')
    expect(added.map((f) => [f.name, f.page, f.party])).toEqual([['Text 2', 1, 'p2'], ['Text 1 2', 2, 'p2']])
    expect(added.every((f) => f.id.startsWith('f-'))).toBe(true)
    expect(added[0]).toMatchObject({ font: 'Helvetica', font_size: 11 })
  })

  it('party keys are p1, p2… (first free) with unique default names, colours cycle', () => {
    expect(nextPartyKey([{ key: 'p1', name: 'A' }, { key: 'p3', name: 'B' }])).toBe('p2')
    expect(nextPartyName([{ key: 'p1', name: 'Party 2' }])).toBe('Party 3')
    expect(partyErrors([{ key: 'p1', name: 'Employee' }, { key: 'p2', name: 'employee' }, { key: 'p3', name: ' ' }])).toEqual({ p2: 'Another party has this name.', p3: 'Enter a name.' })
    const ps = Array.from({ length: 8 }, (_, i) => ({ key: 'p' + (i + 1), name: String(i) }))
    expect(partyColor(ps, 'p8')).toEqual(partyColor(ps, 'p1'))
    expect(partyColor(ps, 'p2')).not.toEqual(partyColor(ps, 'p1'))
  })
})

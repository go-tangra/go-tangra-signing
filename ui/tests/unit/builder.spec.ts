import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useBuilder } from '@/stores/builder'
import { field, fetchMock, template, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})
afterEach(() => vi.unstubAllGlobals())

function api(extra: (path: string, method: string, body: unknown) => Reply | undefined = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path === 'templates/t1' && method === 'GET') return { body: template() }
    if (path === 'templates/t1/fields' && method === 'PUT') {
      const b = body as { version: number; parties: unknown; fields: unknown }
      return { body: template({ version: b.version + 1, parties: b.parties as never, fields: b.fields as never }) }
    }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function loaded() {
  const b = useBuilder()
  await b.load('t1')
  return b
}

describe('builder store', () => {
  it('loads the template as a clean working copy; the first party is current', async () => {
    fetchMock(api())
    const b = await loaded()
    expect(b.template?.name).toBe('Employment contract')
    expect(b.version).toBe(3)
    expect(b.fields).toHaveLength(1)
    expect(b.party).toBe('p1')
    expect(b.dirty).toBe(false)
  })

  it('a palette drop creates a field at the point for the current party and selects it', async () => {
    fetchMock(api())
    const b = await loaded()
    b.party = 'p2'
    const f = b.addField('date', 2, 0.5, 0.25)
    expect(f).toMatchObject({ type: 'date', page: 2, party: 'p2', name: 'Date', x: 0.425, y: 0.235, w: 0.15, h: 0.03 })
    expect(b.selectedId).toBe(f.id)
    expect(b.dirty).toBe(true)
    b.select('f-a')
    expect(b.party).toBe('p1')
  })

  it('moving and resizing keep the field on its page', async () => {
    fetchMock(api())
    const b = await loaded()
    b.nudge('f-a', 5, 5)
    expect(b.fields[0]).toMatchObject({ x: 0.75, y: 0.97, w: 0.25, h: 0.03 })
    b.resize('f-a', 1, 1)
    expect(b.fields[0]).toMatchObject({ w: 0.25, h: 0.03 })
    b.setBox('f-a', { x: -0.2, y: 0.5, w: 0.4, h: 0.1 })
    expect(b.fields[0]).toMatchObject({ x: 0, y: 0.5, w: 0.4, h: 0.1 })
    b.updateField('f-a', { x: 0.9 })
    expect(b.fields[0]!.x).toBe(0.6)
  })

  it('changing the type fixes options, defaults, formula and font', async () => {
    fetchMock(api())
    const b = await loaded()
    b.updateField('f-a', { default: 'x', font_size: 12 })
    b.changeType('f-a', 'select')
    expect(b.fields[0]).toMatchObject({ type: 'select', options: ['Option 1', 'Option 2'], font_size: 12 })
    expect(b.fields[0]).not.toHaveProperty('default')
    b.changeType('f-a', 'signature')
    expect(b.fields[0]).not.toHaveProperty('options')
    expect(b.fields[0]).not.toHaveProperty('font_size')
  })

  it('parties: add (p3), rename, remove with reassignment or with their fields; the last one stays', async () => {
    fetchMock(api())
    const b = await loaded()
    b.addField('signature', 1, 0.5, 0.8) // for p1
    const p = b.addParty()
    expect(p).toEqual({ key: 'p3', name: 'Party 3' })
    expect(b.party).toBe('p3')
    b.renameParty('p3', 'Witness')
    b.addField('initials', 1, 0.2, 0.9)
    expect(b.fieldCount('p3')).toBe(1)

    b.removeParty('p3', 'p2')
    expect(b.parties.map((x) => x.key)).toEqual(['p1', 'p2'])
    expect(b.fieldCount('p2')).toBe(1)
    expect(b.party).toBe('p2')

    b.removeParty('p1', null)
    expect(b.fields.map((f) => f.party)).toEqual(['p2'])
    b.removeParty('p2', null)
    expect(b.parties).toHaveLength(1)
  })

  it('save sends version, parties and fields and takes the new version', async () => {
    const calls = fetchMock(api())
    const b = await loaded()
    b.addField('checkbox', 1, 0.5, 0.5)
    b.updateField(b.selectedId, { name: 'Agree' })
    expect(await b.save()).toBe(true)
    const put = calls.find((c) => c.method === 'PUT')!
    expect(put.url).toBe('/api/signing/v1/templates/t1/fields')
    expect(put.headers['X-CSRF-Token']).toBe('tok')
    const body = put.body as { version: number; parties: unknown[]; fields: { name: string }[] }
    expect(body.version).toBe(3)
    expect(body.parties).toEqual([{ key: 'p1', name: 'Employee' }, { key: 'p2', name: 'Employer' }])
    expect(body.fields.map((f) => f.name)).toEqual(['Salary', 'Agree'])
    expect(b.version).toBe(4)
    expect(b.dirty).toBe(false)
  })

  it('blank option lines are dropped before saving', async () => {
    const calls = fetchMock(api())
    const b = await loaded()
    b.updateField('f-a', { type: 'radio', options: ['Yes', '', 'No', ''] })
    await b.save()
    expect((calls.at(-1)!.body as { fields: { options: string[] }[] }).fields[0]!.options).toEqual(['Yes', 'No'])
  })

  it('409 version_conflict marks a conflict (reload needed) and keeps the local changes', async () => {
    fetchMock(api((path, method) => (method === 'PUT' ? { status: 409, body: { reason: 'version_conflict' } } : undefined)))
    const b = await loaded()
    b.addField('text', 1, 0.5, 0.5)
    expect(await b.save()).toBe(false)
    expect(b.conflict).toBe(true)
    expect(b.error).toBe('')
    expect(b.dirty).toBe(true)
    expect(b.version).toBe(3)
    await b.load('t1')
    expect(b.conflict).toBe(false)
    expect(b.dirty).toBe(false)
  })

  it('a refusal naming a field shows the reason with the field and selects it', async () => {
    fetchMock(api((path, method) => (method === 'PUT' ? { status: 400, body: { reason: 'invalid_field', field: 'Salary' } } : undefined)))
    const b = await loaded()
    b.addField('text', 1, 0.5, 0.5)
    expect(await b.save()).toBe(false)
    expect(b.error).toBe('A field is not valid. (Salary)')
    expect(b.errorField).toBe('Salary')
    expect(b.selectedId).toBe('f-a')
  })

  it('duplicate names are caught before the request', async () => {
    const calls = fetchMock(api())
    const b = await loaded()
    const f = b.addField('text', 1, 0.5, 0.5)
    b.updateField(f.id, { name: 'SALARY' })
    expect(await b.save()).toBe(false)
    expect(b.error).toBe('Field name: Another field has this name.')
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
  })

  it('auto-detect adds proposals for the current party, de-duplicated by geometry', async () => {
    const detected = [field({ id: 'detected-1', name: 'Text 1', x: 0.1, y: 0.2 }), field({ id: 'detected-2', name: 'Text 2', x: 0.1, y: 0.6, font: 'Helvetica', font_size: 10 })]
    const calls = fetchMock(api((path, method) => (path === 'templates/t1/detect-fields' && method === 'POST' ? { body: { fields: detected } } : undefined)))
    const b = await loaded()
    b.party = 'p2'
    expect(await b.detect()).toBe(1)
    expect(calls.at(-1)!.headers['X-CSRF-Token']).toBe('tok')
    const added = b.fields[1]!
    expect(added).toMatchObject({ name: 'Text 2', party: 'p2', y: 0.6, font: 'Helvetica', font_size: 10 })
    expect(b.proposed.has(added.id)).toBe(true)
    expect(await b.detect()).toBe(0)
    b.removeField(added.id)
    expect(b.proposed.size).toBe(0)
  })

  it('activating saves unsaved changes first, then patches the status', async () => {
    const calls = fetchMock(api((path, method) => (path === 'templates/t1' && method === 'PATCH' ? { body: template({ status: 'active', version: 5 }) } : undefined)))
    const b = await loaded()
    b.addField('signature', 1, 0.5, 0.5)
    expect(await b.setStatus('active')).toBe(true)
    expect(calls.map((c) => c.method)).toEqual(['GET', 'PUT', 'PATCH'])
    expect(calls[2]!.body).toEqual({ status: 'active' })
    expect(b.template?.status).toBe('active')
    expect(b.version).toBe(5)
  })

  it('an activation refusal names the party without fields', async () => {
    fetchMock(api((path, method) => (method === 'PATCH' ? { status: 400, body: { reason: 'invalid_field', field: 'Employer' } } : undefined)))
    const b = await loaded()
    expect(await b.setStatus('active')).toBe(false)
    expect(b.error).toBe('A field is not valid. (Employer)')
  })
})

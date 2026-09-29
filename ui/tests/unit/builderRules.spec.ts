import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import Builder from '@/views/builder/index.vue'
import { useBuilder } from '@/stores/builder'
import { fitRule, newConditions, opsFor, ruleTargets } from '@/rules/editor'
import type { Field } from '@/api/types'
import { OPERATOR, field, fetchMock, setField, template, withAbility, type Reply } from './helpers'

const pdf = vi.hoisted(() => ({ GlobalWorkerOptions: { workerSrc: '' }, getDocument: vi.fn() }))
vi.mock('pdfjs-dist', () => pdf)
const fakeDoc = {
  numPages: 2,
  getPage: async () => ({ getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }), render: () => ({ promise: Promise.resolve(), cancel: () => {} }) }),
  destroy: async () => {},
}
const PDF_BYTES = new TextEncoder().encode('%PDF-1.7\n').buffer as ArrayBuffer

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  pdf.getDocument.mockReset()
  pdf.getDocument.mockImplementation(() => ({ promise: Promise.resolve(fakeDoc) }))
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as never)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
const options = (sel: string) => Array.from((q(sel) as HTMLSelectElement).options).map((o) => o.value).filter(Boolean)
async function click(sel: string): Promise<void> {
  const el = q(sel)
  if (!el) throw new Error('missing ' + sel)
  el.click()
  await flushPromises()
}

const FIELDS: Field[] = [
  field({ id: 'f-a', name: 'Salary', type: 'text' }),
  field({ id: 'f-cb', name: 'Agree', type: 'checkbox', y: 0.3 }),
  field({ id: 'f-sel', name: 'Plan', type: 'select', options: ['Monthly', 'Yearly'], y: 0.4 }),
  field({ id: 'f-price', name: 'Price', type: 'number', y: 0.5 }),
  field({ id: 'f-total', name: 'Total', type: 'number', y: 0.6 }),
  field({ id: 'f-sig', name: 'Signature', type: 'signature', y: 0.7 }),
]

function api(extra: (path: string, method: string, body: unknown) => Reply | undefined = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path === 'templates/t1' && method === 'GET') return { body: template({ fields: FIELDS }) }
    if (path === 'templates/t1/pdf') return { raw: PDF_BYTES, type: 'application/pdf' }
    if (path === 'templates/t1/fields' && method === 'PUT') {
      const b = body as { version: number; parties: never; fields: never }
      return { body: template({ version: b.version + 1, parties: b.parties, fields: b.fields }) }
    }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function open(fieldId: string, handler = api()) {
  const calls = fetchMock(handler)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing/templates', name: 'signing-templates', component: { render: () => h('p', 'list') } },
      { path: '/signing/templates/:id/builder', name: 'signing-builder', component: Builder },
    ],
  })
  await router.push('/signing/templates/t1/builder')
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(OPERATOR).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  await click(`[data-test="field-item-${fieldId}"]`)
  return { w, calls }
}
const byId = (id: string) => useBuilder().fields.find((f) => f.id === id)!

describe('rule editor helpers', () => {
  it('offers operators by field type and fits a rule to its target', () => {
    expect(opsFor('checkbox')).toEqual(['checked', 'unchecked'])
    expect(opsFor('select')).toEqual(['eq', 'neq', 'empty', 'not_empty'])
    expect(opsFor('radio')).toEqual(['eq', 'neq', 'empty', 'not_empty'])
    expect(opsFor('text')).toEqual(['eq', 'neq', 'contains', 'empty', 'not_empty'])
    expect(opsFor('number')).toEqual(['eq', 'neq', 'contains', 'empty', 'not_empty'])
    // Only other fields with a typed value can be looked at.
    expect(ruleTargets(FIELDS, 'f-total').map((f) => f.id)).toEqual(['f-a', 'f-cb', 'f-sel', 'f-price'])
    expect(fitRule({ field: 'f-cb', op: 'eq', value: 'x' }, FIELDS[1])).toEqual({ field: 'f-cb', op: 'checked' })
    expect(fitRule({ field: 'f-sel', op: 'contains', value: 'nope' }, FIELDS[2])).toEqual({ field: 'f-sel', op: 'eq', value: 'Monthly' })
    expect(fitRule({ field: 'f-sel', op: 'neq', value: 'Yearly' }, FIELDS[2])).toEqual({ field: 'f-sel', op: 'neq', value: 'Yearly' })
    expect(fitRule({ field: 'f-a', op: 'empty', value: 'left over' }, FIELDS[0])).toEqual({ field: 'f-a', op: 'empty' })
    expect(newConditions(FIELDS, 'f-a')).toEqual({ mode: 'all', effect: 'visible', rules: [{ field: 'f-cb', op: 'checked' }] })
    expect(newConditions([FIELDS[0]!], 'f-a')).toBeNull()
  })
})

describe('conditions editor', () => {
  it('builds the conditions JSON: effect, mode, rules with operators valid for the target type', async () => {
    const { w } = await open('f-a')
    expect(q('[data-test="field-cond"]')).toBeNull()
    await click('#field-rules-on')
    expect(byId('f-a').conditions).toEqual({ mode: 'all', effect: 'visible', rules: [{ field: 'f-cb', op: 'checked' }] })
    // A checkbox offers checked / unchecked only, and no value.
    expect(options('#field-rules-op-0')).toEqual(['checked', 'unchecked'])
    expect(q('#field-rules-value-0')).toBeNull()
    expect(options('#field-rules-target-0')).toEqual(['f-cb', 'f-sel', 'f-price', 'f-total'])

    // A select compares with one of its options.
    await setField(q('#field-rules-target-0'), 'f-sel')
    expect(options('#field-rules-op-0')).toEqual(['eq', 'neq', 'empty', 'not_empty'])
    expect(options('#field-rules-value-0')).toEqual(['Monthly', 'Yearly'])
    await setField(q('#field-rules-value-0'), 'Yearly')
    await setField(q('#field-rules-effect'), 'required')
    await setField(q('#field-rules-mode'), 'any')

    // A number field: typed value; an operator without a value drops it.
    await click('[data-test="field-rule-add"]')
    await setField(q('#field-rules-target-1'), 'f-price')
    await setField(q('#field-rules-op-1'), 'neq')
    await setField(q('#field-rules-value-1'), '0')
    expect(byId('f-a').conditions).toEqual({
      mode: 'any', effect: 'required', rules: [{ field: 'f-sel', op: 'eq', value: 'Yearly' }, { field: 'f-price', op: 'neq', value: '0' }],
    })
    await setField(q('#field-rules-op-1'), 'empty')
    expect(byId('f-a').conditions!.rules[1]).toEqual({ field: 'f-price', op: 'empty' })

    // Removing every rule removes the conditions.
    await click('[data-test="field-rule-remove-1"]')
    await click('[data-test="field-rule-remove-0"]')
    expect('conditions' in byId('f-a')).toBe(false)
    expect(q('[data-test="field-cond"]')).toBeNull()
    w.unmount()
  })

  it('stops at 20 rules', async () => {
    const { w } = await open('f-a')
    await click('#field-rules-on')
    for (let i = 1; i < 20; i++) await click('[data-test="field-rule-add"]')
    expect(byId('f-a').conditions!.rules).toHaveLength(20)
    expect((q('[data-test="field-rule-add"]') as HTMLButtonElement).disabled).toBe(true)
    w.unmount()
  })
})

describe('formula editor', () => {
  it('is offered on number fields only', async () => {
    const { w } = await open('f-a')
    expect(q('#field-rules-formula')).toBeNull()
    await click('[data-test="field-item-f-total"]')
    expect(q('#field-rules-formula')).not.toBeNull()
    w.unmount()
  })

  it('validates live with the module evaluator and names the offending field', async () => {
    const { w } = await open('f-total')
    const panel = () => q('[data-test="field-rules"]')!.textContent ?? ''
    await setField(q('#field-rules-formula'), 'round({price} * 1,2')
    expect(byId('f-total').formula).toBe('round({price} * 1,2')
    expect(panel()).toContain('expected , or )')
    await setField(q('#field-rules-formula'), '{Price} + {Nope}')
    expect(panel()).toContain('unknown field {nope}')
    await setField(q('#field-rules-formula'), '{Total} + 1')
    expect(panel()).toContain('a formula cannot use its own field')
    await setField(q('#field-rules-formula'), '{Price} * 2')
    expect(q('[data-test="field-rules-problem"]')).toBeNull()
    expect(panel()).not.toContain('unknown field')

    // A cycle through another field is reported with that field's name.
    const b = useBuilder()
    b.updateField('f-price', { formula: '{Total} / 2' })
    await flushPromises()
    expect(q('[data-test="field-rules-problem"]')!.textContent).toContain('Field “Price”: rules form a cycle')

    await setField(q('#field-rules-formula'), '')
    expect('formula' in byId('f-total')).toBe(false)
    w.unmount()
  })

  it('inserts {Field name} at the cursor', async () => {
    const { w } = await open('f-total')
    await setField(q('#field-rules-formula'), 'round(, 2)')
    const input = q('#field-rules-formula') as HTMLInputElement
    input.setSelectionRange(6, 6)
    expect(options('#field-rules-insert')[0]).toBe('f-price') // number fields first
    await setField(q('#field-rules-insert'), 'f-price')
    expect(byId('f-total').formula).toBe('round({Price}, 2)')
    w.unmount()
  })
})

describe('saving rules', () => {
  it('round-trips conditions and formula through the builder save', async () => {
    const { w, calls } = await open('f-total')
    await setField(q('#field-rules-formula'), 'round({Price} * 1.2, 2)')
    await click('#field-rules-on')
    await click('[data-test="builder-save"]')
    const put = calls.find((c) => c.url === '/api/signing/v1/templates/t1/fields' && c.method === 'PUT')!
    expect(put.headers['X-CSRF-Token']).toBe('tok')
    const sent = (put.body as { fields: Field[] }).fields.find((f) => f.id === 'f-total')!
    expect(sent).toMatchObject({ formula: 'round({Price} * 1.2, 2)', conditions: { mode: 'all', effect: 'visible', rules: [{ field: 'f-a', op: 'eq', value: '' }] } })
    expect(useBuilder().dirty).toBe(false)
    expect(useBuilder().version).toBe(4)
    w.unmount()
  })

  it('invalid rules are caught before the request, on the right field', async () => {
    const { w, calls } = await open('f-total')
    await setField(q('#field-rules-formula'), '{Nope} + 1')
    await click('[data-test="field-item-f-a"]')
    await click('[data-test="builder-save"]')
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    expect(q('[data-test="builder-error"]')!.textContent).toContain('Conditions or formula of “Total”: unknown field {nope}')
    expect(useBuilder().selectedId).toBe('f-total')
    expect(q('[data-test="field-rules-server-error"]')!.textContent).toContain('unknown field {nope}')
    w.unmount()
  })

  it('shows the module\'s invalid_rule refusal (field + detail.message) on that field', async () => {
    const { w } = await open('f-total', api((path, method) => (path === 'templates/t1/fields' && method === 'PUT' ? { status: 422, body: { reason: 'invalid_rule', field: 'Total', detail: { message: 'rules form a cycle' } } } : undefined)))
    await setField(q('#field-rules-formula'), '{Price} * 2')
    await click('[data-test="field-item-f-a"]')
    await click('[data-test="builder-save"]')
    expect(q('[data-test="builder-error"]')!.textContent).toBe('A condition or formula is not valid. (Total)')
    const b = useBuilder()
    expect(b.selectedId).toBe('f-total')
    expect(q('[data-test="field-rules-server-error"]')!.textContent).toBe('rules form a cycle')
    // Another field does not show it; editing the rules clears it.
    await click('[data-test="field-item-f-price"]')
    expect(q('[data-test="field-rules-server-error"]')).toBeNull()
    await click('[data-test="field-item-f-total"]')
    await setField(q('#field-rules-formula'), '{Price} * 3')
    expect(q('[data-test="field-rules-server-error"]')).toBeNull()
    w.unmount()
  })
})

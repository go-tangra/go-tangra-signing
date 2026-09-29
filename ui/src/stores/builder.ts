// State of the template builder: the working copy of the parties and fields,
// the selection, the party new fields go to, and the save round-trip with the
// template version (optimistic concurrency: 409 version_conflict when someone
// else saved meanwhile). Geometry always goes through utils/geometry so a field
// never leaves its page.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { ApiError, describe, describeRefusal, refusalDetail, refusalField } from '@/api/client'
import type { Field, FieldType, Party, Template, TemplateStatus } from '@/api/types'
import { clampBox, moveBox, resizeBox, type Box } from '@/utils/geometry'
import { createField, hasOptions, isGraphic, mergeDetected, nameErrors, nextPartyKey, nextPartyName, partyErrors } from '@/utils/fields'
import { validateRules } from '@/rules/rules'
import { useTemplates } from './templates'

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
const snapshotOf = (parties: Party[], fields: Field[]) => JSON.stringify({ parties, fields })

export const useBuilder = defineStore('signing-builder', () => {
  const templates = useTemplates()

  const template = ref<Template | null>(null)
  const parties = ref<Party[]>([])
  const fields = ref<Field[]>([])
  const version = ref(0)
  const saved = ref(snapshotOf([], []))
  const selectedId = ref('')
  const party = ref('')
  /** Ids of auto-detected fields not saved yet (shown as proposals). */
  const proposed = ref<Set<string>>(new Set())

  const loading = ref(false)
  const saving = ref(false)
  const detecting = ref(false)
  const error = ref('')
  /** The field (name) or "parties" the last refusal named. */
  const errorField = ref('')
  /** The conditions / formula refusal of the last save (client check or the module's invalid_rule): field name and message. */
  const ruleError = ref<{ field: string; message: string } | null>(null)
  /** The last save lost against a newer version: the user must reload. */
  const conflict = ref(false)

  const dirty = computed(() => snapshotOf(parties.value, fields.value) !== saved.value)
  const selected = computed(() => fields.value.find((f) => f.id === selectedId.value) ?? null)
  const fieldNameErrors = computed(() => nameErrors(fields.value))
  const partyNameErrors = computed(() => partyErrors(parties.value))

  function reset(t: Template): void {
    template.value = t
    parties.value = clone(t.parties?.length ? t.parties : [{ key: 'p1', name: 'First party' }])
    fields.value = clone(t.fields ?? [])
    version.value = t.version
    saved.value = snapshotOf(parties.value, fields.value)
    if (!parties.value.some((p) => p.key === party.value)) party.value = parties.value[0]!.key
    if (!fields.value.some((f) => f.id === selectedId.value)) selectedId.value = ''
    proposed.value = new Set()
    conflict.value = false
    error.value = ''
    errorField.value = ''
    ruleError.value = null
  }

  /** Loads (or reloads, discarding local changes) the template. */
  async function load(id: string): Promise<void> {
    loading.value = true
    error.value = ''
    // Another template's fields must not linger over this one's pages.
    if (template.value?.id !== id) template.value = null
    try {
      reset(await templates.get(id))
    } catch (e) {
      template.value = null
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  // --- fields ---
  function select(id: string): void {
    selectedId.value = id
    const f = fields.value.find((x) => x.id === id)
    if (f) party.value = f.party
  }

  /** A new field of the type centred on the point (page fractions) for the current party; it becomes selected. */
  function addField(type: FieldType, page: number, fx: number, fy: number): Field {
    const f = createField(type, page, fx, fy, party.value, fields.value)
    fields.value = [...fields.value, f]
    selectedId.value = f.id
    return f
  }

  /** Merges a property change and drops the `unset` properties; geometry is kept inside the page. */
  function updateField(id: string, patch: Partial<Field>, unset: (keyof Field)[] = []): void {
    // A refusal of the rules is stale once they change (the editor re-checks live).
    if ('formula' in patch || 'conditions' in patch || unset.includes('formula') || unset.includes('conditions')) ruleError.value = null
    fields.value = fields.value.map((f) => {
      if (f.id !== id) return f
      const next: Field = { ...f, ...patch }
      for (const k of unset) delete next[k]
      return 'x' in patch || 'y' in patch || 'w' in patch || 'h' in patch ? { ...next, ...clampBox(next) } : next
    })
  }

  /** Changes the type: list types get default options, others lose options, a formula stays only on numbers, graphic types lose the font. */
  function changeType(id: string, type: FieldType): void {
    const f = fields.value.find((x) => x.id === id)
    if (!f || f.type === type) return
    const unset: (keyof Field)[] = []
    const patch: Partial<Field> = { type }
    if (hasOptions(type)) {
      if (!f.options?.length) patch.options = ['Option 1', 'Option 2']
      if (f.default && !(patch.options ?? f.options ?? []).includes(f.default)) unset.push('default')
    } else {
      unset.push('options')
      if (hasOptions(f.type) || isGraphic(type)) unset.push('default')
    }
    if (type !== 'number') unset.push('formula')
    if (isGraphic(type)) unset.push('font', 'font_size')
    updateField(id, patch, unset)
  }

  function setBox(id: string, box: Box): void {
    updateField(id, box)
  }

  function nudge(id: string, dx: number, dy: number): void {
    const f = fields.value.find((x) => x.id === id)
    if (f) updateField(id, moveBox(f, dx, dy))
  }

  function resize(id: string, dw: number, dh: number): void {
    const f = fields.value.find((x) => x.id === id)
    if (f) updateField(id, resizeBox(f, dw, dh))
  }

  function removeField(id: string): void {
    fields.value = fields.value.filter((f) => f.id !== id)
    if (selectedId.value === id) selectedId.value = ''
    if (proposed.value.has(id)) proposed.value = new Set([...proposed.value].filter((x) => x !== id))
  }

  // --- parties ---
  function addParty(): Party {
    const p = { key: nextPartyKey(parties.value), name: nextPartyName(parties.value) }
    parties.value = [...parties.value, p]
    party.value = p.key
    return p
  }

  function renameParty(key: string, name: string): void {
    parties.value = parties.value.map((p) => (p.key === key ? { ...p, name } : p))
  }

  const sameName = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase()
  /** The rules refusal of this field ('' when none). */
  const ruleErrorFor = (f: Field) => (ruleError.value && sameName(ruleError.value.field, f.name) ? ruleError.value.message : '')

  const fieldCount = (key: string) => fields.value.filter((f) => f.party === key).length

  /** Removes a party; its fields move to `reassignTo`, or are deleted when it is null. The last party stays. */
  function removeParty(key: string, reassignTo: string | null): void {
    if (parties.value.length <= 1 || reassignTo === key) return
    fields.value = reassignTo ? fields.value.map((f) => (f.party === key ? { ...f, party: reassignTo } : f)) : fields.value.filter((f) => f.party !== key)
    parties.value = parties.value.filter((p) => p.key !== key)
    if (party.value === key) party.value = reassignTo ?? parties.value[0]!.key
    if (selectedId.value && !fields.value.some((f) => f.id === selectedId.value)) selectedId.value = ''
  }

  // --- save / detect / status ---
  /** Client-side checks before a save; the message names what to fix. */
  function validate(): string {
    const pe = Object.entries(partyNameErrors.value)[0]
    if (pe) return `Party: ${pe[1]}`
    const fe = Object.entries(fieldNameErrors.value)[0]
    if (fe) {
      selectedId.value = fe[0]
      return `Field name: ${fe[1]}`
    }
    // Conditions and formulas, with the module's own evaluator (it checks again).
    const re = validateRules(fields.value)
    if (re) {
      ruleError.value = { field: re.field, message: re.msg }
      const hit = fields.value.find((f) => sameName(f.name, re.field))
      if (hit) selectedId.value = hit.id
      return `Conditions or formula of “${re.field}”: ${re.msg}`
    }
    return ''
  }

  /** PUT /templates/{id}/fields with the version read; true when stored. */
  async function save(): Promise<boolean> {
    const t = template.value
    if (!t) return false
    error.value = ''
    errorField.value = ''
    ruleError.value = null
    const invalid = validate()
    if (invalid) {
      error.value = invalid
      return false
    }
    // Blank option lines are an editing artefact (the textarea's last line).
    fields.value = fields.value.map((f) => (f.options ? { ...f, options: f.options.map((o) => o.trim()).filter(Boolean) } : f))
    saving.value = true
    try {
      const out = await templates.saveFields(t.id, version.value, clone(parties.value), clone(fields.value))
      template.value = out
      version.value = out.version
      saved.value = snapshotOf(parties.value, fields.value)
      proposed.value = new Set()
      return true
    } catch (e) {
      if (e instanceof ApiError && e.reason === 'version_conflict') conflict.value = true
      else {
        error.value = describeRefusal(e)
        errorField.value = refusalField(e) ?? ''
        if (e instanceof ApiError && e.reason === 'invalid_rule' && errorField.value) {
          const msg = refusalDetail(e, 'message')
          ruleError.value = { field: errorField.value, message: typeof msg === 'string' && msg ? msg : describe(e) }
        }
        const hit = fields.value.find((f) => sameName(f.name, errorField.value))
        if (hit) selectedId.value = hit.id
      }
      return false
    } finally {
      saving.value = false
    }
  }

  /** Auto-detect: adds the proposed fields (for the current party) not already covered; returns how many. */
  async function detect(): Promise<number> {
    const t = template.value
    if (!t) return 0
    error.value = ''
    detecting.value = true
    try {
      const added = mergeDetected(fields.value, await templates.detectFields(t.id), party.value)
      fields.value = [...fields.value, ...added]
      proposed.value = new Set([...proposed.value, ...added.map((f) => f.id)])
      return added.length
    } catch (e) {
      error.value = describe(e)
      return 0
    } finally {
      detecting.value = false
    }
  }

  /** Activate / archive. Unsaved changes are saved first (the server checks the stored fields). */
  async function setStatus(status: TemplateStatus): Promise<boolean> {
    const t = template.value
    if (!t) return false
    if (dirty.value && !(await save())) return false
    error.value = ''
    errorField.value = ''
    try {
      const out = await templates.patch(t.id, { status })
      template.value = out
      version.value = out.version
      return true
    } catch (e) {
      error.value = describeRefusal(e)
      errorField.value = refusalField(e) ?? ''
      return false
    }
  }

  return {
    template, parties, fields, version, selectedId, selected, party, proposed, loading, saving, detecting, error, errorField, ruleError, conflict, dirty,
    fieldNameErrors, partyNameErrors, load, reset, select, addField, updateField, changeType, setBox, nudge, resize, removeField, addParty, renameParty,
    fieldCount, removeParty, validate, save, detect, setStatus, ruleErrorFor,
  }
})

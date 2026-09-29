// Builder field and party rules, kept out of the components so they are tested
// without a DOM: stable ids, unique names, defaults per type, party keys and
// colours, and merging auto-detected proposals.
import type { Field, FieldType, Party } from '@/api/types'
import { boxAt, sameSpot } from './geometry'

export const TYPE_LABELS: Record<FieldType, string> = {
  text: 'Text', number: 'Number', signature: 'Signature', initials: 'Initials', date: 'Date', checkbox: 'Checkbox',
  select: 'Select', radio: 'Radio', image: 'Image', file: 'File', cells: 'Cells', stamp: 'Stamp',
}

export const TYPE_ICONS: Record<FieldType, string> = {
  text: 'mdi-form-select', number: 'mdi-sort', signature: 'mdi-pencil', initials: 'mdi-pencil-outline', date: 'mdi-calendar-clock',
  checkbox: 'mdi-check-circle-outline', select: 'mdi-chevron-down', radio: 'mdi-circle-small', image: 'mdi-camera-outline',
  file: 'mdi-paperclip', cells: 'mdi-view-module-outline', stamp: 'mdi-check-decagram',
}

/** dataTransfer type a palette item carries while dragged onto a page. */
export const DRAG_TYPE = 'application/x-signing-field'

/** Types whose value is picked from a list of options. */
export const hasOptions = (t: FieldType): boolean => t === 'select' || t === 'radio'
/** Types the signer draws or uploads rather than types (no font). */
export const isGraphic = (t: FieldType): boolean => t === 'signature' || t === 'initials' || t === 'image' || t === 'stamp' || t === 'file' || t === 'checkbox'

/** A stable unique field id ("f-" plus 10 random base-36 characters). */
export function newFieldId(taken: ReadonlySet<string> = new Set()): string {
  for (;;) {
    const bytes = crypto.getRandomValues(new Uint8Array(10))
    const id = 'f-' + Array.from(bytes, (b) => (b % 36).toString(36)).join('')
    if (!taken.has(id)) return id
  }
}

const norm = (s: string) => s.trim().toLowerCase()

/** `base`, or `base 2`, `base 3`… — the first name no other field uses (case-insensitive). */
export function uniqueName(base: string, fields: readonly Field[], exceptId?: string): string {
  const used = new Set(fields.filter((f) => f.id !== exceptId).map((f) => norm(f.name)))
  if (!used.has(norm(base))) return base
  for (let n = 2; ; n++) if (!used.has(norm(`${base} ${n}`))) return `${base} ${n}`
}

/** Per field id: why its name is not acceptable (empty or used by another field). */
export function nameErrors(fields: readonly Field[]): Record<string, string> {
  const count = new Map<string, number>()
  for (const f of fields) count.set(norm(f.name), (count.get(norm(f.name)) ?? 0) + 1)
  const out: Record<string, string> = {}
  for (const f of fields) {
    if (!f.name.trim()) out[f.id] = 'Enter a name.'
    else if (f.name.trim().length > 120) out[f.id] = 'At most 120 characters.'
    else if ((count.get(norm(f.name)) ?? 0) > 1) out[f.id] = 'Another field has this name.'
  }
  return out
}

/** A new field of the type centred on the drop point (page fractions) for the party. */
export function createField(type: FieldType, page: number, fx: number, fy: number, party: string, fields: readonly Field[]): Field {
  const f: Field = {
    id: newFieldId(new Set(fields.map((x) => x.id))),
    name: uniqueName(TYPE_LABELS[type], fields),
    type,
    party,
    page,
    ...boxAt(type, fx, fy),
    required: type === 'signature' || type === 'initials',
  }
  if (hasOptions(type)) f.options = ['Option 1', 'Option 2']
  return f
}

/**
 * Adds auto-detected proposals for the party: each gets a fresh id and a unique
 * name, and a proposal at the spot of an existing field (or of an earlier
 * proposal) is dropped. Returns the fields that were added.
 */
export function mergeDetected(existing: readonly Field[], detected: readonly Field[], party: string): Field[] {
  const all = [...existing]
  const added: Field[] = []
  for (const d of detected) {
    if (all.some((f) => sameSpot(f, d))) continue
    const f: Field = { ...d, id: newFieldId(new Set(all.map((x) => x.id))), name: uniqueName(d.name || TYPE_LABELS[d.type], all), party }
    all.push(f)
    added.push(f)
  }
  return added
}

/** The next free party key: p1, p2, … (the server accepts [a-z0-9_-]{1,40}). */
export function nextPartyKey(parties: readonly Party[]): string {
  const used = new Set(parties.map((p) => p.key))
  for (let n = 1; ; n++) if (!used.has('p' + n)) return 'p' + n
}

/** A default display name no other party uses. */
export function nextPartyName(parties: readonly Party[]): string {
  const used = new Set(parties.map((p) => norm(p.name)))
  for (let n = parties.length + 1; ; n++) if (!used.has(norm(`Party ${n}`))) return `Party ${n}`
}

/** Per party key: why its name is not acceptable (empty or taken). */
export function partyErrors(parties: readonly Party[]): Record<string, string> {
  const out: Record<string, string> = {}
  const seen = new Set<string>()
  for (const p of parties) {
    if (!p.name.trim()) out[p.key] = 'Enter a name.'
    else if (p.name.trim().length > 80) out[p.key] = 'At most 80 characters.'
    else if (seen.has(norm(p.name))) out[p.key] = 'Another party has this name.'
    seen.add(norm(p.name))
  }
  return out
}

// Party colours, cycled by party position. Full class strings so Tailwind
// emits them; each pairs a border with a translucent fill that reads on both themes.
export const PARTY_COLORS = [
  { box: 'border-primary bg-primary/15', dot: 'bg-primary', text: 'text-primary' },
  { box: 'border-secondary bg-secondary/15', dot: 'bg-secondary', text: 'text-secondary' },
  { box: 'border-accent bg-accent/15', dot: 'bg-accent', text: 'text-accent' },
  { box: 'border-info bg-info/15', dot: 'bg-info', text: 'text-info' },
  { box: 'border-success bg-success/15', dot: 'bg-success', text: 'text-success' },
  { box: 'border-warning bg-warning/15', dot: 'bg-warning', text: 'text-warning' },
  { box: 'border-error bg-error/15', dot: 'bg-error', text: 'text-error' },
] as const

export function partyColor(parties: readonly Party[], key: string): (typeof PARTY_COLORS)[number] {
  const i = Math.max(0, parties.findIndex((p) => p.key === key))
  return PARTY_COLORS[i % PARTY_COLORS.length]!
}

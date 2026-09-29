// State of the signing page: the caller's session (own fields, the values
// already known, the certificate state and whether they may sign now), the
// values being filled in, the image / file uploads, the signature image, and
// the sign / decline round-trips. Visibility and requiredness go through the
// rules hook (src/rules/evaluate.ts) so conditional fields drop in later.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { ApiError, api, describe, describeRefusal, postForm, refusalDetail, refusalField } from '@/api/client'
import type { Field, Session, SignResult } from '@/api/types'
import { evaluate } from '@/rules/evaluate'
import { isFilled, isTextValued, isUpload, valueError } from '@/utils/values'

/** What a sign attempt ended with (the view reacts per kind). */
export type SignOutcome =
  | { kind: 'signed'; result: SignResult }
  | { kind: 'pin_invalid'; attemptsLeft: number | null }
  | { kind: 'locked'; lockedUntil: string }
  | { kind: 'certificate_missing' }
  | { kind: 'field'; field: string; message: string }
  | { kind: 'error'; message: string }

const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)

export const useSession = defineStore('signing-session', () => {
  const session = ref<Session | null>(null)
  const values = ref<Record<string, string>>({})
  const uploads = ref<Record<string, File>>({})
  const signature = ref<Blob | null>(null)
  /** Per field id: why its value is not acceptable (client checks and server refusals). */
  const errors = ref<Record<string, string>>({})
  const loading = ref(false)
  const signing = ref(false)
  const error = ref('')
  let opened = ''

  /** The whole submission's values: known ones (prefills, earlier signers) overlaid with this signer's input. */
  const allValues = computed<Record<string, string>>(() => ({ ...(session.value?.values ?? {}), ...values.value }))
  const evaluation = computed(() => evaluate(session.value?.all_fields?.length ? session.value.all_fields : session.value?.fields ?? [], allValues.value))
  /** The signer's own fields that are visible, in reading order. */
  const fields = computed<Field[]>(() =>
    (session.value?.fields ?? []).filter((f) => !evaluation.value.hidden.has(f.id)).sort((a, b) => a.page - b.page || a.y - b.y || a.x - b.x),
  )
  const isRequired = (f: Field) => evaluation.value.required.has(f.id)
  /** A signature image is needed when the signer has a signature or initials field. */
  const needsSignature = computed(() => fields.value.some((f) => f.type === 'signature' || f.type === 'initials'))

  function reset(s: Session): void {
    session.value = s
    const v: Record<string, string> = {}
    for (const f of s.fields ?? []) {
      if (!isTextValued(f.type)) continue
      v[f.id] = s.values?.[f.id] ?? f.default ?? (f.type === 'checkbox' ? 'false' : '')
    }
    values.value = v
    uploads.value = {}
    errors.value = {}
    error.value = ''
  }

  async function load(signerId: string): Promise<void> {
    loading.value = true
    error.value = ''
    if (session.value?.signer_id !== signerId) session.value = null
    try {
      reset(await api<Session>('GET', 'signing/' + signerId))
    } catch (e) {
      session.value = null
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  /** Marks an invited slot opened (once per page visit; best effort). */
  async function markOpened(): Promise<void> {
    const s = session.value
    if (!s || s.status !== 'invited' || opened === s.signer_id) return
    opened = s.signer_id
    try {
      await api('POST', `signing/${s.signer_id}/open`)
    } catch {
      // Opening is bookkeeping only; signing still works.
    }
  }

  function setValue(id: string, v: string): void {
    values.value = { ...values.value, [id]: v }
    if (errors.value[id]) errors.value = { ...errors.value, [id]: '' }
  }

  function setUpload(id: string, f: File | null): void {
    const next = { ...uploads.value }
    if (f) next[id] = f
    else delete next[id]
    uploads.value = next
    if (errors.value[id]) errors.value = { ...errors.value, [id]: '' }
  }

  /** Client-side checks of every visible own field; true when nothing is wrong. */
  function validate(): boolean {
    const out: Record<string, string> = {}
    for (const f of fields.value) {
      const v = values.value[f.id] ?? ''
      const bad = isTextValued(f.type) ? valueError(f, v) : ''
      if (bad) out[f.id] = bad
      else if (isRequired(f) && !isFilled(f, v, !!uploads.value[f.id])) out[f.id] = f.type === 'checkbox' ? 'Tick this box.' : isUpload(f.type) ? 'Add a file.' : 'Fill in this field.'
    }
    errors.value = out
    return Object.keys(out).length === 0
  }

  /** The multipart body of POST /signing/{id}/sign. */
  function formData(pin: string): FormData {
    const form = new FormData()
    const own: Record<string, string> = {}
    for (const f of fields.value) {
      if (!isTextValued(f.type)) continue
      const v = evaluation.value.computed[f.id] ?? values.value[f.id] ?? ''
      if (v !== '') own[f.id] = v
    }
    form.append('values', JSON.stringify(own))
    form.append('pin', pin)
    if (signature.value) form.append('signature', signature.value, signature.value.type === 'image/jpeg' ? 'signature.jpg' : 'signature.png')
    for (const f of fields.value) {
      const file = uploads.value[f.id]
      if (file && isUpload(f.type)) form.append('upload.' + f.id, file, file.name)
    }
    return form
  }

  async function sign(pin: string): Promise<SignOutcome> {
    const s = session.value
    if (!s) return { kind: 'error', message: 'Nothing to sign.' }
    signing.value = true
    error.value = ''
    try {
      const result = await postForm<SignResult>(`signing/${s.signer_id}/sign`, formData(pin))
      await load(s.signer_id)
      return { kind: 'signed', result }
    } catch (e) {
      const reason = e instanceof ApiError ? e.reason : ''
      if (reason === 'pin_invalid') return { kind: 'pin_invalid', attemptsLeft: num(refusalDetail(e, 'attempts_left')) }
      if (reason === 'certificate_locked') {
        const until = String(refusalDetail(e, 'locked_until') ?? '')
        session.value = { ...s, certificate: { state: 'locked', locked_until: until || null } }
        return { kind: 'locked', lockedUntil: until }
      }
      if (reason === 'certificate_missing') {
        session.value = { ...s, certificate: { state: 'none', locked_until: null } }
        return { kind: 'certificate_missing' }
      }
      const field = refusalField(e)
      const known = field ? fields.value.find((f) => f.id === field) : undefined
      if (known) {
        errors.value = { ...errors.value, [known.id]: describe(e) }
        return { kind: 'field', field: known.id, message: `${describe(e)} (${known.name})` }
      }
      if (['not_your_turn', 'already_signed', 'submission_not_open'].includes(reason)) await load(s.signer_id)
      const message = field === 'signature' ? `${describe(e)} (signature image)` : describeRefusal(e)
      error.value = message
      return { kind: 'error', message }
    } finally {
      signing.value = false
    }
  }

  async function decline(reason: string): Promise<SignResult> {
    const s = session.value
    if (!s) throw new Error('no session')
    const res = await api<SignResult>('POST', `signing/${s.signer_id}/decline`, { reason: reason.trim() })
    await load(s.signer_id)
    return res
  }

  /** URL of the current document to sign (GET, binary). */
  const documentUrl = (signerId: string) => api.fileUrl(`signing/${signerId}/document`)

  return {
    session, values, uploads, signature, errors, loading, signing, error, allValues, evaluation, fields, needsSignature,
    isRequired, reset, load, markOpened, setValue, setUpload, validate, formData, sign, decline, documentUrl,
  }
})

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, describe, sendJSON } from '@/api/client'
import type { DetectedFields, Field, Party, Template, TemplateFilter, TemplatePage, TemplatePatch, TemplateUpload } from '@/api/types'

export const PAGE_SIZE = 25
/** Server limit on a template PDF (x-freya-max-body-bytes on POST /templates). */
export const MAX_PDF_BYTES = 50 * 1024 * 1024

export const useTemplates = defineStore('signing-templates', () => {
  const items = ref<Template[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(PAGE_SIZE)
  const filter = ref<TemplateFilter>({})
  const loading = ref(false)
  const error = ref('')

  /** Loads one page with the filter (blank filter values are not sent). */
  async function list(f: TemplateFilter = filter.value, p = 1): Promise<void> {
    loading.value = true
    error.value = ''
    filter.value = { ...f }
    try {
      const res = await api<TemplatePage>('GET', 'templates', undefined, { query: { ...f, page: p, page_size: pageSize.value } })
      items.value = res.items ?? []
      total.value = res.total ?? 0
      page.value = p
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  const reload = () => list(filter.value, page.value)

  function replace(t: Template): Template {
    items.value = items.value.map((x) => (x.id === t.id ? t : x))
    return t
  }

  /** One template with its parties, fields and version. */
  async function get(id: string): Promise<Template> {
    return api<Template>('GET', 'templates/' + id)
  }

  /** Uploads the PDF as multipart/form-data (file, name, description, folder_id, tags comma-separated) → a draft template. */
  async function create(file: File, meta: TemplateUpload): Promise<Template> {
    const fields: Record<string, string> = { name: meta.name.trim() }
    if (meta.description?.trim()) fields.description = meta.description.trim()
    if (meta.folder_id) fields.folder_id = meta.folder_id
    const tags = (meta.tags ?? []).map((t) => t.trim()).filter(Boolean)
    if (tags.length) fields.tags = tags.join(',')
    const t = await api.upload<Template>('templates', file, fields)
    items.value = [t, ...items.value]
    total.value += 1
    return t
  }

  /** Rename, move, re-tag, set the status or the defaults. */
  async function patch(id: string, body: TemplatePatch): Promise<Template> {
    return replace(await api<Template>('PATCH', 'templates/' + id, body))
  }

  /** Stores the builder's parties and fields; refused with 409 version_conflict when someone saved meanwhile. */
  async function saveFields(id: string, version: number, parties: Party[], fields: Field[]): Promise<Template> {
    // sendJSON keeps the refused field next to invalid_rule's detail message.
    return replace(await sendJSON<Template>('PUT', `templates/${id}/fields`, { version, parties, fields }))
  }

  /** Copies the template with its PDF and fields under a new name. */
  async function clone(id: string, name: string, folderId?: string | null): Promise<Template> {
    const t = await api<Template>('POST', `templates/${id}/clone`, folderId === undefined ? { name } : { name, folder_id: folderId })
    items.value = [t, ...items.value]
    total.value += 1
    return t
  }

  /** Refused with 409 template_in_use while unfinished submissions use it. */
  async function remove(id: string): Promise<void> {
    await api('DELETE', 'templates/' + id)
    const before = items.value.length
    items.value = items.value.filter((x) => x.id !== id)
    if (items.value.length < before) total.value = Math.max(0, total.value - 1)
  }

  /** Proposed fields at the placeholder lines of the PDF (not saved). */
  async function detectFields(id: string): Promise<Field[]> {
    const res = await api<DetectedFields>('POST', `templates/${id}/detect-fields`)
    return res.fields ?? []
  }

  /** URL of the template PDF (GET, binary). */
  const pdfUrl = (id: string) => api.fileUrl(`templates/${id}/pdf`)

  return { items, total, page, pageSize, filter, loading, error, list, reload, replace, get, create, patch, saveFields, clone, remove, detectFields, pdfUrl }
})

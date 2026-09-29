import type { DocumentSource } from '@/api/types'

/** Adds the document parts of a multipart body: `file`, or `submission_id` (+ `version`). */
export function appendSource(form: FormData, src: DocumentSource): void {
  if ('file' in src) {
    form.append('file', src.file, src.file.name)
    return
  }
  form.append('submission_id', src.submission_id)
  if (src.version !== undefined && src.version !== null) form.append('version', String(src.version))
}

/** Adds the non-blank text fields (trimmed). */
export function appendFields(form: FormData, fields: Record<string, string | undefined>): void {
  for (const [k, v] of Object.entries(fields)) {
    const t = v?.trim()
    if (t) form.append(k, t)
  }
}

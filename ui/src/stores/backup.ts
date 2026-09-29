// Tenant backup (backup:manage): export downloads the tenant's signing data
// as a tar.gz archive (POST, CSRF header); import sends an archive back as the
// raw request body (application/gzip) and answers a summary. Existing records
// are skipped or overwritten (mode).
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { postBytes, postDownload } from '@/api/client'
import type { BackupMode, BackupResult } from '@/api/types'

/** The module's import limit (x-freya-max-body-bytes). */
export const MAX_BACKUP_BYTES = 2 * 1024 * 1024 * 1024

/** Offers a blob as a file download (an object URL on a temporary link). */
export function saveBlob(blob: Blob, name: string): void {
  const href = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = href
  a.download = name
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(href), 1000)
}

export const useBackup = defineStore('signing-backup', () => {
  const exporting = ref(false)
  const importing = ref(false)
  const result = ref<BackupResult | null>(null)

  /** Downloads the archive; returns the file name it was saved under. */
  async function exportBackup(): Promise<string> {
    exporting.value = true
    try {
      const { blob, filename } = await postDownload('backup/export')
      const name = filename || `signing-backup-${new Date().toISOString().slice(0, 10)}.tar.gz`
      saveBlob(blob, name)
      return name
    } finally {
      exporting.value = false
    }
  }

  /** Sends the archive as the request body and keeps the summary. */
  async function importBackup(file: Blob, mode: BackupMode): Promise<BackupResult> {
    importing.value = true
    result.value = null
    try {
      result.value = await postBytes<BackupResult>('backup/import', file, 'application/gzip', { mode })
      return result.value
    } finally {
      importing.value = false
    }
  }

  return { exporting, importing, result, exportBackup, importBackup }
})

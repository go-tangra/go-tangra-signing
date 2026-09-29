<script setup lang="ts">
// Backup of the tenant's signing data (backup:manage). Export downloads a
// tar.gz archive (folders, templates, submissions with their documents and
// events, certificates). Import sends an archive back; records that already
// exist are skipped or overwritten (confirmed first). The summary lists what
// was created, updated and skipped per kind, the certificates that must be
// re-issued, and the errors.
import { computed, ref } from 'vue'
import { UiAlert, UiButton, UiCard, UiFilePicker, UiSelect, useConfirm, useToast, type SelectOption } from '@go-tangra/ui'
import { describe } from '@/api/client'
import type { BackupCounts, BackupMode } from '@/api/types'
import { BACKUP_MODES } from '@/api/types'
import { MAX_BACKUP_BYTES, useBackup } from '@/stores/backup'

const store = useBackup()
const toast = useToast()
const confirm = useConfirm()

const MODE_LABELS: Record<BackupMode, string> = { skip: 'Keep existing records (skip)', overwrite: 'Replace existing records (overwrite)' }
const modeOptions: SelectOption[] = BACKUP_MODES.map((m) => ({ title: MODE_LABELS[m], value: m }))
const KINDS: { key: keyof BackupCounts; label: string }[] = [
  { key: 'folders', label: 'Folders' },
  { key: 'templates', label: 'Templates' },
  { key: 'submissions', label: 'Submissions' },
  { key: 'certificates', label: 'Certificates' },
  { key: 'objects', label: 'Stored files' },
]

// --- export ---
const exportError = ref('')
async function doExport(): Promise<void> {
  exportError.value = ''
  try {
    const name = await store.exportBackup()
    toast.show({ kind: 'success', title: 'Backup downloaded', text: name })
  } catch (e) {
    exportError.value = describe(e)
  }
}

// --- import ---
const file = ref<File | null>(null)
const mode = ref<BackupMode>('skip')
const fileError = ref('')
const importError = ref('')
function pick(f: File | null): void {
  file.value = f
  fileError.value = ''
}
function setMode(v: unknown): void {
  mode.value = BACKUP_MODES.includes(v as BackupMode) ? (v as BackupMode) : 'skip'
}
async function doImport(): Promise<void> {
  importError.value = ''
  const f = file.value
  if (!f) {
    fileError.value = 'Choose a backup archive (.tar.gz).'
    return
  }
  if (!f.size) {
    fileError.value = 'The file is empty.'
    return
  }
  if (mode.value === 'overwrite' && !(await confirm.ask({ title: 'Overwrite existing records?', text: 'Templates, submissions and certificates that exist here are replaced by the ones in the backup.', danger: true, confirmLabel: 'Import and overwrite' }))) return
  try {
    const r = await store.importBackup(f, mode.value)
    toast.show({ kind: r.errors?.length ? 'warning' : 'success', title: 'Backup imported' })
  } catch (e) {
    importError.value = describe(e)
  }
}

const summary = computed(() => {
  const r = store.result
  return r ? KINDS.map((k) => ({ ...k, created: r.created?.[k.key] ?? 0, updated: r.updated?.[k.key] ?? 0, skipped: r.skipped?.[k.key] ?? 0 })) : []
})
</script>

<template>
  <UiCard title="Backup" subtitle="Export or restore the tenant's signing data" data-test="backup">
    <div class="grid min-w-0 grid-cols-1 gap-6 md:grid-cols-2">
      <section class="flex min-w-0 flex-col gap-3" aria-labelledby="backup-export-title">
        <h3 id="backup-export-title" class="text-sm font-medium">Export</h3>
        <p class="text-sm text-base-content/80">Downloads folders, templates, submissions with their documents and history, certificates and stored files as one archive (.tar.gz). Keys never leave in clear; sealed keys are restored only where the module key is the same, otherwise the certificate must be re-issued.</p>
        <UiAlert v-if="exportError" kind="error" data-test="backup-export-error">{{ exportError }}</UiAlert>
        <div><UiButton icon="mdi-download" :loading="store.exporting" data-test="backup-export" @click="doExport">Download backup</UiButton></div>
      </section>

      <section class="flex min-w-0 flex-col gap-3" aria-labelledby="backup-import-title">
        <h3 id="backup-import-title" class="text-sm font-medium">Import</h3>
        <UiFilePicker id="backup-file" :model-value="file" label="Backup archive" accept=".gz,.tgz,application/gzip" :max-bytes="MAX_BACKUP_BYTES" :error="fileError || undefined" data-test="backup-file" @update:model-value="pick" @too-large="fileError = 'The backup is too large.'" />
        <UiSelect id="backup-mode" :model-value="mode" label="Records that already exist" :options="modeOptions" :clearable="false" data-test="backup-mode" @update:model-value="setMode" />
        <UiAlert v-if="importError" kind="error" data-test="backup-import-error">{{ importError }}</UiAlert>
        <div><UiButton icon="mdi-upload" :loading="store.importing" data-test="backup-import" @click="doImport">Import backup</UiButton></div>
      </section>
    </div>

    <div v-if="store.result" class="mt-4 flex flex-col gap-3" data-test="backup-result">
      <table class="table table-sm">
        <caption class="sr-only">Import summary per kind</caption>
        <thead>
          <tr><th scope="col">Kind</th><th scope="col" class="text-end">Created</th><th scope="col" class="text-end">Updated</th><th scope="col" class="text-end">Skipped</th></tr>
        </thead>
        <tbody>
          <tr v-for="k in summary" :key="k.key" :data-test="'backup-row-' + k.key">
            <th scope="row" class="font-normal">{{ k.label }}</th>
            <td class="text-end">{{ k.created }}</td>
            <td class="text-end">{{ k.updated }}</td>
            <td class="text-end">{{ k.skipped }}</td>
          </tr>
        </tbody>
      </table>
      <UiAlert v-if="store.result.needs_reissue" kind="warning" data-test="backup-reissue">
        {{ store.result.needs_reissue }} certificate{{ store.result.needs_reissue === 1 ? '' : 's' }} could not be restored with {{ store.result.needs_reissue === 1 ? 'its' : 'their' }} key and must be re-issued.
      </UiAlert>
      <UiAlert v-if="store.result.errors?.length" kind="error" :title="`${store.result.errors.length} error${store.result.errors.length === 1 ? '' : 's'}`" data-test="backup-errors">
        <ul class="list-disc ps-5">
          <li v-for="(e, i) in store.result.errors" :key="i">{{ e }}</li>
        </ul>
      </UiAlert>
    </div>
  </UiCard>
</template>

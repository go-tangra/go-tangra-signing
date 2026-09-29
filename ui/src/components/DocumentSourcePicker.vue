<script setup lang="ts">
// The PDF to work on: an uploaded file, or a stored version of a submission
// (picked from GET /submissions; the current version unless another is
// chosen). Emits the source, or null while it is incomplete. Shared by the
// verify page and the administrators' "Sign document" panel.
import { computed, onMounted, ref, watch } from 'vue'
import { UiAlert, UiFilePicker, UiSelect, UiTabs, type SelectOption, type TabItem } from '@go-tangra/ui'
import { api, describe } from '@/api/client'
import type { DocumentSource, Submission, SubmissionPage } from '@/api/types'
import { MAX_PDF_BYTES } from '@/stores/templates'

const props = withDefaults(defineProps<{
  /** Prefix of the element ids (two pickers may share a page). */
  idPrefix: string
  error?: string | undefined
  disabled?: boolean | undefined
}>(), { error: '', disabled: false })
const emit = defineEmits<{ (e: 'update:modelValue', v: DocumentSource | null): void }>()

type Kind = 'file' | 'submission'
const kind = ref<Kind>('file')
const tabs: TabItem[] = [
  { key: 'file', label: 'Upload a PDF', icon: 'mdi-file-upload-outline' },
  { key: 'submission', label: 'A submission', icon: 'mdi-file-document-multiple-outline' },
]

const file = ref<File | null>(null)
const tooLarge = ref(false)
const submissionId = ref('')
const version = ref('')

const submissions = ref<Submission[]>([])
const loadError = ref('')
const loaded = ref(false)
async function loadSubmissions(): Promise<void> {
  loadError.value = ''
  try {
    const res = await api<SubmissionPage>('GET', 'submissions', undefined, { query: { page: 1, page_size: 100 } })
    submissions.value = (res.items ?? []).filter((x) => (x.current_version ?? 0) >= 1)
  } catch (e) {
    loadError.value = describe(e)
  } finally {
    loaded.value = true
  }
}
watch(kind, (k) => { if (k === 'submission' && !loaded.value) void loadSubmissions() })
onMounted(() => { if (kind.value === 'submission') void loadSubmissions() })

const submissionOptions = computed<SelectOption[]>(() => submissions.value.map((x) => ({ title: x.name, value: x.id })))
const chosen = computed(() => submissions.value.find((x) => x.id === submissionId.value) ?? null)
const versionOptions = computed<SelectOption[]>(() => {
  const n = chosen.value?.current_version ?? 0
  const out: SelectOption[] = []
  for (let v = n; v >= 1; v--) out.push({ title: v === n ? `Version ${v} (current)` : `Version ${v}`, value: String(v) })
  return out
})

const source = computed<DocumentSource | null>(() => {
  if (kind.value === 'file') return file.value && !tooLarge.value ? { file: file.value } : null
  const c = chosen.value
  if (!c) return null
  const v = Number(version.value || c.current_version)
  return { submission_id: c.id, version: v }
})
watch(source, (v) => emit('update:modelValue', v), { immediate: true })

function setKind(k: string): void {
  kind.value = k === 'submission' ? 'submission' : 'file'
}
function pick(f: File | null): void {
  file.value = f
  tooLarge.value = false
}
function setSubmission(v: unknown): void {
  submissionId.value = typeof v === 'string' ? v : ''
  version.value = chosen.value?.current_version ? String(chosen.value.current_version) : ''
}
function setVersion(v: unknown): void {
  version.value = typeof v === 'string' ? v : ''
}

defineExpose({ kind, reload: loadSubmissions })
</script>

<template>
  <div class="flex flex-col gap-3" :data-test="idPrefix + '-source'">
    <UiTabs :model-value="kind" :tabs="tabs" @update:model-value="setKind" />
    <UiFilePicker
      v-if="kind === 'file'"
      :id="idPrefix + '-file'"
      :model-value="file"
      label="PDF file"
      accept="application/pdf"
      :max-bytes="MAX_PDF_BYTES"
      hint="A PDF of up to 50 MB."
      required
      :disabled="disabled"
      :error="(tooLarge ? 'The file is larger than 50 MB.' : props.error) || undefined"
      :data-test="idPrefix + '-file'"
      @update:model-value="pick"
      @too-large="tooLarge = true"
    />
    <template v-else>
      <UiAlert v-if="loadError" kind="error" :data-test="idPrefix + '-submissions-error'">{{ loadError }}</UiAlert>
      <UiSelect
        :id="idPrefix + '-submission'"
        :model-value="submissionId"
        label="Submission"
        :options="submissionOptions"
        :placeholder="loaded && !submissionOptions.length ? 'No submission with a document' : 'Choose a submission'"
        required
        :disabled="disabled"
        :error="props.error || undefined"
        :data-test="idPrefix + '-submission'"
        @update:model-value="setSubmission"
      />
      <UiSelect
        v-if="chosen"
        :id="idPrefix + '-version'"
        :model-value="version"
        label="Document version"
        :options="versionOptions"
        :disabled="disabled"
        hint="Each signature adds a version; the last one carries every signature."
        :data-test="idPrefix + '-version'"
        @update:model-value="setVersion"
      />
    </template>
  </div>
</template>

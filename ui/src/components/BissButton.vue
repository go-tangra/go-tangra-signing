<script setup lang="ts">
// "Sign with qualified card": signs the signer slot with a qualified
// certificate on a smart card through the locally installed B-Trust BISS
// (src/composables/useBiss.ts). The signer's fields are checked first with the
// page's own validation. Each step is announced (looking for BISS, choosing the
// certificate, confirming with the card PIN…), and every way it can end is
// explained: BISS missing, BISS refused (its reason), no answer in time, and
// the module's refusals (signature does not match, the step expired → start
// again, the document changed → the page reloads, the card's certificate is
// not usable, a field is missing → it is highlighted).
import { computed, ref, watch } from 'vue'
import { UiAlert, UiButton } from '@go-tangra/ui'
import type { SignResult } from '@/api/types'
import { toDataUrl, useBiss, type BissPhase, type BissTimeouts } from '@/composables/useBiss'
import { useSession } from '@/stores/session'
import { isUpload } from '@/utils/values'

const props = withDefaults(defineProps<{
  signerId: string
  /** The page's validation of the signer's fields and signature (highlights what is wrong). */
  check: () => boolean
  disabled?: boolean | undefined
  timeouts?: Partial<BissTimeouts> | undefined
}>(), { disabled: false, timeouts: undefined })
const emit = defineEmits<{
  (e: 'signed', result: SignResult): void
  /** A refusal named one of the signer's fields. */
  (e: 'field', id: string): void
  /** The document changed meanwhile: the page reloads the session and the PDF. */
  (e: 'changed'): void
  (e: 'busy', busy: boolean): void
}>()

const s = useSession()
const biss = useBiss(props.timeouts ? { timeouts: props.timeouts } : {})

type State = 'idle' | 'not_installed' | 'refused' | 'timeout' | 'expired' | 'changed' | 'error'
const state = ref<State>('idle')
const message = ref('')
const running = ref(false)
watch(running, (v) => emit('busy', v))

const PHASE_TEXT: Record<BissPhase, string> = {
  idle: '',
  detecting: 'Looking for B-Trust BISS on this computer…',
  choosing: 'Choose your certificate in the BISS window…',
  preparing: 'Preparing the document…',
  signing: 'Confirm with your card PIN in the BISS window…',
  completing: 'Finishing the signature…',
}
const phaseText = computed(() => PHASE_TEXT[biss.phase.value])

/** Image and file fields cannot travel with a qualified signature (the prepare step takes text values only). */
const uploadsBlock = computed(() => s.fields.some((f) => isUpload(f.type) && (s.isRequired(f) || !!s.uploads[f.id])))

const CERT_TEXT = 'The certificate on the card is expired, revoked or not accepted for signing. Use another card or certificate.'

async function start(): Promise<void> {
  if (running.value || props.disabled || uploadsBlock.value) return
  state.value = 'idle'
  message.value = ''
  if (!props.check()) return
  running.value = true
  try {
    const signatureImage = s.signature ? await toDataUrl(s.signature).catch(() => '') : ''
    const out = await biss.run(props.signerId, { values: s.ownValues(), signatureImage: signatureImage || undefined })
    switch (out.kind) {
      case 'signed':
        await s.load(props.signerId)
        emit('signed', out.result)
        break
      case 'not_installed':
        state.value = 'not_installed'
        break
      case 'refused':
        state.value = 'refused'
        message.value = out.reasonText
        break
      case 'timeout':
        state.value = 'timeout'
        break
      case 'server':
        serverRefusal(out.reason, out.message, out.field)
        break
      default:
        state.value = 'error'
        message.value = out.message
    }
  } finally {
    running.value = false
  }
}

function serverRefusal(reason: string, text: string, field: string | undefined): void {
  if (reason === 'preparation_expired') {
    state.value = 'expired'
    message.value = text
    return
  }
  if (reason === 'document_changed') {
    state.value = 'changed'
    message.value = text
    emit('changed')
    return
  }
  state.value = 'error'
  if (reason === 'certificate_unusable') {
    message.value = CERT_TEXT
    return
  }
  const known = field ? s.fields.find((f) => f.id === field) : undefined
  if (known) {
    s.errors = { ...s.errors, [known.id]: text }
    message.value = `${text} (${known.name})`
    emit('field', known.id)
    return
  }
  if (['not_your_turn', 'already_signed', 'submission_not_open'].includes(reason)) void s.load(props.signerId)
  message.value = field ? `${text} (${field})` : text
}

defineExpose({ start, state, phase: biss.phase })
</script>

<template>
  <div class="flex flex-col gap-2" data-test="biss">
    <UiButton
      block
      variant="soft"
      icon="mdi-shield-key-outline"
      :loading="running"
      :disabled="disabled || uploadsBlock"
      data-test="biss-start"
      @click="start"
    >
      Sign with qualified card
    </UiButton>
    <p class="text-xs text-base-content/70" role="status" aria-live="polite" data-test="biss-phase">
      <template v-if="running">{{ phaseText }}</template>
      <template v-else-if="uploadsBlock">Image and file fields cannot be sent with a qualified card signature. Sign with your certificate instead.</template>
      <template v-else>A qualified electronic signature with your smart card, through the B-Trust BISS application.</template>
    </p>

    <UiAlert v-if="state === 'not_installed'" kind="warning" title="B-Trust BISS was not found" data-test="biss-not-installed">
      <p>
        Signing with a qualified card needs the B-Trust BISS application installed and running on this computer, with your card in the reader.
        <a href="https://www.b-trust.bg/" target="_blank" rel="noopener noreferrer" class="link">Get B-Trust BISS from B-Trust</a>, start it, then try again.
      </p>
    </UiAlert>
    <UiAlert v-else-if="state === 'refused'" kind="error" title="BISS did not sign" data-test="biss-refused">{{ message }}</UiAlert>
    <UiAlert v-else-if="state === 'timeout'" kind="warning" title="BISS did not answer in time" data-test="biss-timeout">
      Nothing was signed. Check the BISS window and your card reader, then try again.
    </UiAlert>
    <UiAlert v-else-if="state === 'expired'" kind="warning" title="The signing step expired" data-test="biss-expired">
      <p>{{ message }}</p>
      <UiButton size="sm" class="mt-2" icon="mdi-restart" data-test="biss-retry" @click="start">Start again</UiButton>
    </UiAlert>
    <UiAlert v-else-if="state === 'changed'" kind="info" title="The document changed meanwhile" data-test="biss-changed">
      It was reloaded with the latest version. Check it, then sign again.
    </UiAlert>
    <UiAlert v-else-if="state === 'error'" kind="error" data-test="biss-error">{{ message }}</UiAlert>
  </div>
</template>

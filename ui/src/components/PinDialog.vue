<script setup lang="ts">
// Asks for the PIN of the personal signing certificate (signing, renewal).
// Shows the server's answer: a wrong PIN with the attempts left, or the
// certificate locked until a time (then the PIN cannot be entered). Shared by
// the signing page and the certificate page.
import { computed, ref, watch } from 'vue'
import { UiAlert, UiButton, UiDialog, UiSecretField } from '@go-tangra/ui'
import { when } from '@/utils/format'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title?: string | undefined
  text?: string | undefined
  confirmLabel?: string | undefined
  loading?: boolean | undefined
  /** A refusal to show (wrong PIN is worded from attemptsLeft). */
  error?: string | undefined
  attemptsLeft?: number | null | undefined
  lockedUntil?: string | undefined
}>(), { title: 'Enter your PIN', text: '', confirmLabel: 'Sign', loading: false, error: '', attemptsLeft: null, lockedUntil: '' })
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'submit', pin: string): void }>()

const pin = ref('')
const missing = ref(false)
watch(() => props.modelValue, (open) => {
  if (open) {
    pin.value = ''
    missing.value = false
  }
})
// A refused PIN is cleared so the next attempt starts empty.
watch(() => [props.error, props.attemptsLeft], () => { if (props.error) pin.value = '' })

const locked = computed(() => !!props.lockedUntil)
const attemptsText = computed(() => (props.attemptsLeft === null || props.attemptsLeft === undefined ? '' : props.attemptsLeft === 1 ? '1 attempt left before the certificate is locked.' : `${props.attemptsLeft} attempts left before the certificate is locked.`))

function submit(): void {
  if (locked.value || props.loading) return
  if (!pin.value) {
    missing.value = true
    return
  }
  emit('submit', pin.value)
}
function setPin(v: unknown): void {
  pin.value = String(v ?? '')
  missing.value = false
}
</script>

<template>
  <UiDialog :model-value="modelValue" :title="title" size="sm" data-test="pin-dialog" @update:model-value="emit('update:modelValue', $event)">
    <div class="flex flex-col gap-3">
      <p v-if="text" class="text-sm text-base-content/80">{{ text }}</p>
      <UiAlert v-if="locked" kind="error" title="Certificate locked" data-test="pin-locked">
        Too many wrong PINs. You can try again after {{ when(lockedUntil) }}.
      </UiAlert>
      <UiAlert v-else-if="error" kind="error" data-test="pin-error">
        {{ error }}<template v-if="attemptsText"> {{ attemptsText }}</template>
      </UiAlert>
      <UiSecretField
        v-if="!locked"
        id="pin-input"
        :model-value="pin"
        label="PIN"
        required
        autocomplete="off"
        :error="missing ? 'Enter your PIN.' : undefined"
        :disabled="loading"
        data-test="pin-input"
        @update:model-value="setPin"
        @enter="submit"
      />
    </div>
    <template #actions>
      <UiButton variant="text" data-test="pin-cancel" @click="emit('update:modelValue', false)">{{ locked ? 'Close' : 'Cancel' }}</UiButton>
      <UiButton v-if="!locked" icon="mdi-key" :loading="loading" data-test="pin-submit" @click="submit">{{ confirmLabel }}</UiButton>
    </template>
  </UiDialog>
</template>

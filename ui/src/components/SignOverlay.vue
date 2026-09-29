<script setup lang="ts">
// The signer's own fields of one page as inputs at their positions (percent
// of the page, like the builder's boxes, applied through the CSSOM). Text,
// number, cells and dates are typed; select and radio pick an option;
// checkboxes tick; image and file fields take an upload; signature, initials
// and stamp fields show the signature image (a click goes to the pad).
// Formula fields are read-only and show the value computed from the other
// fields (the module recomputes it on submit).
import { computed } from 'vue'
import type { Field } from '@/api/types'
import { TYPE_LABELS } from '@/utils/fields'
import { vCssom } from '@/utils/cssom'
import { isSignatureLike } from '@/utils/values'

const props = withDefaults(defineProps<{
  page: number
  fields: Field[]
  values: Record<string, string>
  /** Field id → the chosen file's name. */
  files: Record<string, string>
  errors: Record<string, string>
  required: ReadonlySet<string>
  /** Field id → the formula value (formula fields are read-only). */
  calculated?: Record<string, string> | undefined
  /** Object URL of the signature image (shown in signature-like fields). */
  signatureUrl?: string | undefined
  disabled?: boolean | undefined
}>(), { signatureUrl: '', disabled: false, calculated: () => ({}) })
const emit = defineEmits<{
  (e: 'value', id: string, v: string): void
  (e: 'upload', id: string, f: File | null): void
  (e: 'signature'): void
}>()

const onPage = computed(() => props.fields.filter((f) => f.page === props.page))
const pct = (v: number) => `${(v * 100).toFixed(3)}%`
const isComputed = (f: Field) => Object.prototype.hasOwnProperty.call(props.calculated, f.id)
const label = (f: Field) => `${f.name} (${TYPE_LABELS[f.type]}${isComputed(f) ? ', calculated' : props.required.has(f.id) ? ', required' : ''})`
const inputOf = (e: Event) => (e.target as HTMLInputElement | HTMLSelectElement).value

function pickFile(f: Field, e: Event): void {
  const input = e.target as HTMLInputElement
  emit('upload', f.id, input.files?.[0] ?? null)
  input.value = ''
}
</script>

<template>
  <div class="pointer-events-none absolute inset-0" :data-test="'sign-overlay-' + page">
    <div
      v-for="f in onPage"
      :key="f.id"
      v-cssom="{ left: pct(f.x), top: pct(f.y), width: pct(f.w), height: pct(f.h) }"
      class="pointer-events-auto absolute flex overflow-hidden rounded-sm border text-xs text-black"
      :class="errors[f.id] ? 'border-2 border-error bg-error/10' : required.has(f.id) ? 'border-primary bg-primary/10' : 'border-base-content/40 bg-info/10'"
      :data-test="'sign-box-' + f.id"
    >
      <select
        v-if="f.type === 'select'"
        :id="'sign-field-' + f.id"
        class="h-full w-full bg-transparent px-0.5 outline-none focus-visible:ring-2 focus-visible:ring-primary"
        :value="values[f.id] ?? ''"
        :aria-label="label(f)"
        :aria-invalid="!!errors[f.id] || undefined"
        :disabled="disabled"
        :data-test="'sign-input-' + f.id"
        @change="emit('value', f.id, inputOf($event))"
      >
        <option value="">—</option>
        <option v-for="o in f.options ?? []" :key="o" :value="o">{{ o }}</option>
      </select>

      <fieldset v-else-if="f.type === 'radio'" :id="'sign-field-' + f.id" class="flex h-full w-full flex-wrap items-center gap-x-2 px-0.5" :aria-invalid="!!errors[f.id] || undefined" :data-test="'sign-input-' + f.id">
        <legend class="sr-only">{{ label(f) }}</legend>
        <label v-for="o in f.options ?? []" :key="o" class="inline-flex items-center gap-0.5">
          <input type="radio" class="size-3" :name="'sign-radio-' + f.id" :value="o" :checked="values[f.id] === o" :disabled="disabled" @change="emit('value', f.id, o)">
          <span>{{ o }}</span>
        </label>
      </fieldset>

      <label v-else-if="f.type === 'checkbox'" class="flex h-full w-full items-center justify-center">
        <input
          :id="'sign-field-' + f.id"
          type="checkbox"
          class="size-full max-h-5 max-w-5"
          :checked="values[f.id] === 'true'"
          :aria-label="label(f)"
          :aria-invalid="!!errors[f.id] || undefined"
          :disabled="disabled"
          :data-test="'sign-input-' + f.id"
          @change="emit('value', f.id, ($event.target as HTMLInputElement).checked ? 'true' : 'false')"
        >
      </label>

      <label v-else-if="f.type === 'image' || f.type === 'file'" class="flex h-full w-full cursor-pointer items-center gap-1 truncate px-0.5 focus-within:ring-2 focus-within:ring-primary">
        <input
          :id="'sign-field-' + f.id"
          type="file"
          class="sr-only"
          :accept="f.type === 'image' ? 'image/png,image/jpeg' : undefined"
          :aria-label="label(f)"
          :aria-invalid="!!errors[f.id] || undefined"
          :disabled="disabled"
          :data-test="'sign-input-' + f.id"
          @change="pickFile(f, $event)"
        >
        <span class="truncate">{{ files[f.id] || (f.type === 'image' ? 'Add image…' : 'Add file…') }}</span>
      </label>

      <button
        v-else-if="isSignatureLike(f.type)"
        :id="'sign-field-' + f.id"
        type="button"
        class="flex h-full w-full items-center justify-center outline-none focus-visible:ring-2 focus-visible:ring-primary"
        :aria-label="`${label(f)}: ${f.type === 'stamp' ? 'filled from the certificate' : signatureUrl ? 'your signature' : 'draw or type your signature in the panel'}`"
        :disabled="disabled"
        :data-test="'sign-input-' + f.id"
        @click="emit('signature')"
      >
        <img v-if="signatureUrl && f.type !== 'stamp'" :src="signatureUrl" alt="" class="max-h-full max-w-full object-contain">
        <span v-else class="truncate text-black/70">{{ f.type === 'stamp' ? 'Stamp' : f.type === 'initials' ? 'Initials' : 'Sign here' }}</span>
      </button>

      <input
        v-else-if="isComputed(f)"
        :id="'sign-field-' + f.id"
        type="text"
        readonly
        class="h-full w-full cursor-default bg-base-200/60 px-0.5 outline-none focus-visible:ring-2 focus-visible:ring-primary"
        :value="calculated[f.id]"
        :aria-label="label(f)"
        :aria-invalid="!!errors[f.id] || undefined"
        :data-test="'sign-input-' + f.id"
        data-computed="true"
      >

      <input
        v-else
        :id="'sign-field-' + f.id"
        :type="f.type === 'date' ? 'date' : 'text'"
        :inputmode="f.type === 'number' ? 'decimal' : undefined"
        :maxlength="f.type === 'cells' ? 200 : 2000"
        class="h-full w-full bg-transparent px-0.5 outline-none focus-visible:ring-2 focus-visible:ring-primary"
        :value="values[f.id] ?? ''"
        :placeholder="f.name"
        :aria-label="label(f)"
        :aria-invalid="!!errors[f.id] || undefined"
        :disabled="disabled"
        :data-test="'sign-input-' + f.id"
        @input="emit('value', f.id, inputOf($event))"
      >
    </div>
  </div>
</template>

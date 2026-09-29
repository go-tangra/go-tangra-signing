<script setup lang="ts">
// Properties of the selected field: name, type, party, required, font size,
// options (select / radio), default value, and the conditions and formula
// (FieldRules).
import { computed } from 'vue'
import { UiButton, UiCheckbox, UiInput, UiNumberInput, UiSelect, UiTextarea, type SelectOption } from '@go-tangra/ui'
import type { Field, FieldType, Party } from '@/api/types'
import { FIELD_TYPES } from '@/api/types'
import { TYPE_LABELS, hasOptions, isGraphic } from '@/utils/fields'
import FieldRules from './FieldRules.vue'

const props = withDefaults(defineProps<{
  field: Field
  parties: Party[]
  /** Every field of the template as edited (conditions and formula reference them). */
  fields?: Field[] | undefined
  nameError?: string | undefined
  /** Why this field's conditions or formula were refused. */
  ruleError?: string | undefined
  readonly?: boolean | undefined
}>(), { fields: () => [], nameError: '', ruleError: '', readonly: false })
const emit = defineEmits<{
  (e: 'update', patch: Partial<Field>, unset?: (keyof Field)[]): void
  (e: 'type', t: FieldType): void
  (e: 'remove'): void
}>()

const typeOptions: SelectOption[] = FIELD_TYPES.map((t) => ({ title: TYPE_LABELS[t], value: t }))
const partyOptions = computed<SelectOption[]>(() => props.parties.map((p) => ({ title: p.name || p.key, value: p.key })))
const optionsText = computed(() => (props.field.options ?? []).join('\n'))
const defaultOptions = computed<SelectOption[]>(() => (props.field.options ?? []).filter(Boolean).map((o) => ({ title: o, value: o })))
const noDefault = computed(() => isGraphic(props.field.type) && props.field.type !== 'checkbox')
const id = (k: string) => `field-prop-${k}`

function setOptions(v: unknown): void {
  const list = String(v ?? '').split('\n').map((s) => s.trim())
  // Keep a trailing empty line while typing; blanks in between are dropped.
  const opts = list.filter((s, i) => s || i === list.length - 1)
  const patch: Partial<Field> = { options: opts }
  const unset: (keyof Field)[] = props.field.default && !opts.includes(props.field.default) ? ['default'] : []
  emit('update', patch, unset)
}
function setFontSize(v: unknown): void {
  const n = typeof v === 'number' ? v : Number(v)
  if (v === '' || v === null || v === undefined || !Number.isFinite(n) || n <= 0) emit('update', {}, ['font_size'])
  else emit('update', { font_size: Math.min(72, n) })
}
function setDefault(v: unknown): void {
  const s = typeof v === 'string' ? v : v === true ? 'true' : ''
  if (s) emit('update', { default: s })
  else emit('update', {}, ['default'])
}
</script>

<template>
  <div class="flex flex-col gap-3" data-test="field-props">
    <UiInput :id="id('name')" :model-value="field.name" label="Name" required :error="nameError || undefined" :disabled="readonly" data-test="field-name" @update:model-value="emit('update', { name: String($event ?? '') })" />
    <UiSelect :id="id('type')" :model-value="field.type" label="Type" :options="typeOptions" :clearable="false" :disabled="readonly" data-test="field-type" @update:model-value="typeof $event === 'string' && $event && emit('type', $event as FieldType)" />
    <UiSelect :id="id('party')" :model-value="field.party" label="Party" :options="partyOptions" :clearable="false" :disabled="readonly" data-test="field-party" @update:model-value="typeof $event === 'string' && $event && emit('update', { party: $event })" />
    <UiCheckbox :id="id('required')" :model-value="!!field.required" label="Required" :disabled="readonly" data-test="field-required" @update:model-value="emit('update', { required: $event })" />
    <UiNumberInput v-if="!isGraphic(field.type)" :id="id('font-size')" :model-value="field.font_size ?? ''" label="Font size (pt)" hint="Empty: automatic." :min="4" :max="72" :step="0.5" :disabled="readonly" data-test="field-font-size" @update:model-value="setFontSize" />
    <UiTextarea v-if="hasOptions(field.type)" :id="id('options')" :model-value="optionsText" label="Options (one per line)" :rows="4" :disabled="readonly" data-test="field-options" @update:model-value="setOptions" />
    <UiSelect v-if="hasOptions(field.type)" :id="id('default')" :model-value="field.default ?? ''" label="Default value" :options="defaultOptions" placeholder="None" :disabled="readonly" data-test="field-default" @update:model-value="setDefault" />
    <UiCheckbox v-else-if="field.type === 'checkbox'" :id="id('default')" :model-value="field.default === 'true'" label="Checked by default" :disabled="readonly" data-test="field-default" @update:model-value="setDefault" />
    <UiInput v-else-if="!noDefault" :id="id('default')" :model-value="field.default ?? ''" label="Default value" :disabled="readonly" data-test="field-default" @update:model-value="setDefault" />

    <FieldRules :field="field" :fields="fields.length ? fields : [field]" :readonly="readonly" :server-error="ruleError" @update="(patch, unset) => emit('update', patch, unset)" />

    <UiButton v-if="!readonly" variant="soft" color="error" size="sm" icon="mdi-delete-outline" data-test="field-remove" @click="emit('remove')">Remove field</UiButton>
    <p class="text-xs text-base-content/70">Page {{ field.page }} · x {{ (field.x * 100).toFixed(1) }}% · y {{ (field.y * 100).toFixed(1) }}% · {{ (field.w * 100).toFixed(1) }} × {{ (field.h * 100).toFixed(1) }}%</p>
  </div>
</template>

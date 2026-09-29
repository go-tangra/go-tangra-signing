<script setup lang="ts">
// Conditions and formula of the selected field. Conditions show or require
// the field when all / any of up to 20 rules hold over other fields (the
// operators depend on the other field's type; select and radio compare with
// one of their options). Number fields take a formula over other fields
// ({Field name}, + - * /, parentheses, round / min / max / sum); it is checked
// live with the same evaluator the module uses, against the builder's current
// fields, and a refusal names the offending field. The module checks again on
// save (invalid_rule, shown here with its message).
import { computed, ref } from 'vue'
import { UiAlert, UiButton, UiCheckbox, UiInput, UiSelect, type SelectOption } from '@go-tangra/ui'
import type { Conditions, Field, Rule } from '@/api/types'
import { validateRules } from '@/rules/rules'
import { MAX_FORMULA, MAX_RULES, MAX_RULE_VALUE, OP_LABELS, fitRule, needsValue, newConditions, newRule, opsFor, ruleTargets, type RuleOp } from '@/rules/editor'
import { TYPE_LABELS, hasOptions } from '@/utils/fields'

const props = withDefaults(defineProps<{
  field: Field
  /** Every field of the template as currently edited (targets and live validation). */
  fields: Field[]
  readonly?: boolean | undefined
  /** The last refusal of this field's rules (client check or the module's invalid_rule). */
  serverError?: string | undefined
}>(), { readonly: false, serverError: '' })
const emit = defineEmits<{ (e: 'update', patch: Partial<Field>, unset?: (keyof Field)[]): void }>()

const id = (k: string) => `field-rules-${k}`
const MODES: SelectOption[] = [{ title: 'all rules hold', value: 'all' }, { title: 'any rule holds', value: 'any' }]
const EFFECTS: SelectOption[] = [{ title: 'Show the field only', value: 'visible' }, { title: 'Require the field', value: 'required' }]

const cond = computed(() => props.field.conditions ?? null)
const targets = computed(() => ruleTargets(props.fields, props.field.id))
const byId = computed(() => new Map(props.fields.map((f) => [f.id, f])))

function targetOptions(r: Rule): SelectOption[] {
  const list = targets.value.map((f) => ({ title: `${f.name} (${TYPE_LABELS[f.type]})`, value: f.id }))
  // A rule on a field that can no longer be a target (removed, retyped) stays visible.
  if (r.field && !list.some((o) => o.value === r.field)) list.push({ title: byId.value.get(r.field)?.name ?? 'Missing field', value: r.field })
  return list
}
function opOptions(r: Rule): SelectOption[] {
  const t = byId.value.get(r.field)
  const ops = t ? opsFor(t.type) : []
  if (!ops.includes(r.op)) ops.push(r.op)
  return ops.map((o) => ({ title: OP_LABELS[o], value: o }))
}
function valueOptions(r: Rule): SelectOption[] | null {
  const t = byId.value.get(r.field)
  return t && hasOptions(t.type) ? (t.options ?? []).filter(Boolean).map((o) => ({ title: o, value: o })) : null
}

function setConditions(c: Conditions | null): void {
  if (c && c.rules.length) emit('update', { conditions: c })
  else emit('update', {}, ['conditions'])
}
function toggle(on: boolean): void {
  setConditions(on ? newConditions(props.fields, props.field.id) : null)
}
function patchCond(p: Partial<Conditions>): void {
  if (cond.value) setConditions({ ...cond.value, ...p })
}
function setRule(i: number, r: Rule): void {
  if (!cond.value) return
  patchCond({ rules: cond.value.rules.map((x, j) => (j === i ? r : x)) })
}
function setTarget(i: number, v: unknown): void {
  const r = cond.value?.rules[i]
  if (!r || typeof v !== 'string' || !v) return
  setRule(i, fitRule({ ...r, field: v }, byId.value.get(v)))
}
function setOp(i: number, v: unknown): void {
  const r = cond.value?.rules[i]
  if (!r || typeof v !== 'string' || !v) return
  setRule(i, fitRule({ ...r, op: v as RuleOp }, byId.value.get(r.field)))
}
function setValue(i: number, v: unknown): void {
  const r = cond.value?.rules[i]
  if (r) setRule(i, { ...r, value: String(v ?? '') })
}
function addRule(): void {
  const t = targets.value[0]
  if (!cond.value || !t || cond.value.rules.length >= MAX_RULES) return
  const next = [...cond.value.rules, newRule(t)]
  patchCond({ rules: next })
}
function removeRule(i: number): void {
  if (cond.value) setConditions({ ...cond.value, rules: cond.value.rules.filter((_, j) => j !== i) })
}
const valueError = (r: Rule) => ((r.value ?? '').length > MAX_RULE_VALUE ? `At most ${MAX_RULE_VALUE} characters.` : undefined)

// --- formula ---
const formula = computed(() => props.field.formula ?? '')
function setFormula(v: unknown): void {
  const s = String(v ?? '')
  if (s.trim()) emit('update', { formula: s })
  else emit('update', {}, ['formula'])
}
/** Inserts {Field name} at the cursor of the formula input (or at its end). */
function insertField(v: unknown): void {
  const f = byId.value.get(String(v ?? ''))
  if (!f) return
  const ref = `{${f.name.trim()}}`
  const el = document.getElementById(id('formula')) as HTMLInputElement | null
  const cur = formula.value
  const start = el?.selectionStart ?? cur.length
  const end = el?.selectionEnd ?? start
  setFormula(cur.slice(0, start) + ref + cur.slice(end))
  insertKey.value++
}
/** Re-creates the insert picker after each pick so it shows its placeholder again. */
const insertKey = ref(0)
const insertOptions = computed<SelectOption[]>(() =>
  props.fields.filter((f) => f.id !== props.field.id).sort((a, b) => Number(b.type === 'number') - Number(a.type === 'number')).map((f) => ({ title: `${f.name} (${TYPE_LABELS[f.type]})`, value: f.id })),
)

// --- live validation (the module's evaluator, over the builder's fields) ---
const problem = computed(() => (props.field.conditions || props.field.formula ? validateRules(props.fields) : null))
const ownProblem = computed(() => (problem.value && problem.value.field.trim().toLowerCase() === props.field.name.trim().toLowerCase() ? problem.value.msg : ''))
const formulaError = computed(() => {
  if (formula.value.length > MAX_FORMULA) return `At most ${MAX_FORMULA} characters.`
  return props.field.formula && ownProblem.value ? ownProblem.value : undefined
})
</script>

<template>
  <section class="flex flex-col gap-3 rounded-box border border-base-300 p-3" aria-labelledby="field-rules-title" data-test="field-rules">
    <p id="field-rules-title" class="text-sm font-medium">Conditions and formula</p>

    <UiAlert v-if="serverError" kind="error" data-test="field-rules-server-error">{{ serverError }}</UiAlert>

    <UiCheckbox
      :id="id('on')"
      :model-value="!!cond"
      label="Depends on other fields"
      :hint="!cond && !targets.length ? 'Add another text, number, date, checkbox, select or radio field first.' : undefined"
      :disabled="readonly || (!cond && !targets.length)"
      data-test="field-cond-on"
      @update:model-value="toggle"
    />

    <div v-if="cond" class="flex flex-col gap-2" data-test="field-cond">
      <UiSelect :id="id('effect')" :model-value="cond.effect" label="Effect" :options="EFFECTS" :clearable="false" size="sm" :disabled="readonly" data-test="field-cond-effect" @update:model-value="typeof $event === 'string' && $event && patchCond({ effect: $event as Conditions['effect'] })" />
      <UiSelect :id="id('mode')" :model-value="cond.mode" label="When" :options="MODES" :clearable="false" size="sm" :disabled="readonly" data-test="field-cond-mode" @update:model-value="typeof $event === 'string' && $event && patchCond({ mode: $event as Conditions['mode'] })" />

      <ol class="flex flex-col gap-2" data-test="field-cond-rules">
        <li v-for="(r, i) in cond.rules" :key="i" class="flex flex-col gap-1.5 rounded-field bg-base-200 p-2" :data-test="'field-rule-' + i">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-medium">Rule {{ i + 1 }}</span>
            <UiButton v-if="!readonly" size="xs" variant="text" color="error" icon="mdi-close" icon-only :label="`Remove rule ${i + 1}`" :data-test="'field-rule-remove-' + i" @click="removeRule(i)" />
          </div>
          <UiSelect :id="id('target-' + i)" :model-value="r.field" label="Field" :options="targetOptions(r)" :clearable="false" size="sm" :disabled="readonly" :data-test="'field-rule-field-' + i" @update:model-value="setTarget(i, $event)" />
          <UiSelect :id="id('op-' + i)" :model-value="r.op" label="Condition" :options="opOptions(r)" :clearable="false" size="sm" :disabled="readonly" :data-test="'field-rule-op-' + i" @update:model-value="setOp(i, $event)" />
          <template v-if="needsValue(r.op)">
            <UiSelect v-if="valueOptions(r)" :id="id('value-' + i)" :model-value="r.value ?? ''" label="Value" :options="valueOptions(r)!" :clearable="false" size="sm" :disabled="readonly" :data-test="'field-rule-value-' + i" @update:model-value="setValue(i, $event)" />
            <UiInput v-else :id="id('value-' + i)" :model-value="r.value ?? ''" label="Value" size="sm" :error="valueError(r)" :disabled="readonly" :data-test="'field-rule-value-' + i" @update:model-value="setValue(i, $event)" />
          </template>
        </li>
      </ol>
      <UiButton v-if="!readonly" size="sm" variant="soft" icon="mdi-plus" :disabled="cond.rules.length >= MAX_RULES || !targets.length" data-test="field-rule-add" @click="addRule">Add rule</UiButton>
      <p v-if="cond.rules.length >= MAX_RULES" class="text-xs text-base-content/70">At most {{ MAX_RULES }} rules.</p>
    </div>

    <template v-if="field.type === 'number'">
      <UiInput
        :id="id('formula')"
        :model-value="formula"
        label="Formula"
        hint="Calculated from other fields, e.g. round({Price} * {Quantity}, 2). Operators + - * / and min, max, sum, round."
        :error="formulaError"
        :disabled="readonly"
        data-test="field-formula"
        @update:model-value="setFormula"
      />
      <UiSelect v-if="!readonly && insertOptions.length" :id="id('insert')" :key="insertKey" :model-value="''" label="Insert a field" :options="insertOptions" placeholder="Choose a field…" size="sm" data-test="field-formula-insert" @update:model-value="insertField" />
    </template>

    <UiAlert v-if="problem && !(field.formula && ownProblem)" kind="warning" data-test="field-rules-problem">
      Field “{{ problem.field }}”: {{ problem.msg }}
    </UiAlert>
  </section>
</template>

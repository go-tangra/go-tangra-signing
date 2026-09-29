<script setup lang="ts">
// Picks an active tenant member (GET /users?q=, names only). The list is
// searched as the user types (debounced); the chosen member's name stays
// shown even when a later search no longer lists it.
import { computed, onBeforeUnmount, ref } from 'vue'
import { UiCombobox, type SelectOption } from '@go-tangra/ui'
import { describe } from '@/api/client'
import type { Member } from '@/api/types'
import { useSubmissions } from '@/stores/submissions'

const props = withDefaults(defineProps<{
  id: string
  label: string
  modelValue?: string | undefined
  /** Name of the current value, when known beforehand. */
  selectedName?: string | undefined
  error?: string | undefined
  required?: boolean | undefined
  /** User ids not to offer (already picked elsewhere). */
  exclude?: readonly string[] | undefined
}>(), { modelValue: '', selectedName: '', error: '', required: false, exclude: () => [] })
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void; (e: 'pick', m: Member | null): void }>()

const store = useSubmissions()
const found = ref<Member[]>([])
const known = new Map<string, Member>()
const failure = ref('')
let timer: ReturnType<typeof setTimeout> | null = null
let seq = 0

async function search(q: string): Promise<void> {
  const mine = ++seq
  try {
    const list = await store.users(q)
    if (mine !== seq) return
    for (const m of list) known.set(m.user_id, m)
    found.value = list
    failure.value = ''
  } catch (e) {
    if (mine === seq) failure.value = describe(e)
  }
}
function onSearch(q: string): void {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => void search(q), 250)
}
onBeforeUnmount(() => { if (timer) clearTimeout(timer) })
// The first list loads right away.
void search('')

const options = computed<SelectOption[]>(() => {
  const skip = new Set(props.exclude.filter((x) => x !== props.modelValue))
  const out = found.value.filter((m) => !skip.has(m.user_id)).map((m) => ({ title: m.display_name || m.user_id, value: m.user_id }))
  if (props.modelValue && !out.some((o) => o.value === props.modelValue)) {
    const m = known.get(props.modelValue)
    out.unshift({ title: m?.display_name || props.selectedName || props.modelValue, value: props.modelValue })
  }
  return out
})

function pick(v: unknown): void {
  const id = typeof v === 'string' ? v : ''
  emit('update:modelValue', id)
  emit('pick', id ? known.get(id) ?? { user_id: id, display_name: props.selectedName || id } : null)
}
</script>

<template>
  <UiCombobox
    :id="id"
    :model-value="modelValue"
    :label="label"
    :options="options"
    :required="required"
    :error="error || failure || undefined"
    placeholder="Search by name"
    @search="onSearch"
    @update:model-value="pick"
  />
</template>

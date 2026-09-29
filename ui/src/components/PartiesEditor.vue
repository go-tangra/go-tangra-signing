<script setup lang="ts">
// The template's parties (signer roles). Keys are generated (p1, p2, …) and
// never shown for editing; display names are editable. The selected party is
// the one new fields go to. Removing a party that has fields asks whether to
// move them to another party or delete them.
import { computed, ref } from 'vue'
import { UiButton, UiDialog, UiInput, UiSelect, type SelectOption } from '@go-tangra/ui'
import type { Party } from '@/api/types'
import { partyColor } from '@/utils/fields'

const props = withDefaults(defineProps<{
  parties: Party[]
  current: string
  counts: Record<string, number>
  errors?: Record<string, string> | undefined
  readonly?: boolean | undefined
}>(), { errors: () => ({}), readonly: false })
const emit = defineEmits<{
  (e: 'select', key: string): void
  (e: 'add'): void
  (e: 'rename', key: string, name: string): void
  (e: 'remove', key: string, reassignTo: string | null): void
}>()

const removing = ref<Party | null>(null)
const target = ref('')
const others = computed<SelectOption[]>(() => props.parties.filter((p) => p.key !== removing.value?.key).map((p) => ({ title: p.name || p.key, value: p.key })))

function askRemove(p: Party): void {
  if (!props.counts[p.key]) {
    emit('remove', p.key, null)
    return
  }
  removing.value = p
  target.value = others.value[0]?.value ?? ''
}
function finish(reassign: boolean): void {
  const p = removing.value
  removing.value = null
  if (p) emit('remove', p.key, reassign ? target.value || null : null)
}
</script>

<template>
  <div class="flex flex-col gap-2" data-test="parties-editor">
    <ul class="flex flex-col gap-2">
      <li v-for="p in parties" :key="p.key" class="flex items-start gap-2" :data-test="'party-' + p.key">
        <button
          type="button"
          class="btn btn-circle btn-text btn-xs mt-1.5"
          :aria-pressed="current === p.key"
          :aria-label="`New fields for ${p.name || p.key}`"
          :data-test="'party-select-' + p.key"
          @click="emit('select', p.key)"
        >
          <span class="size-3 rounded-full" :class="[partyColor(parties, p.key).dot, current === p.key ? 'ring-2 ring-base-content ring-offset-1 ring-offset-base-100' : 'opacity-60']" />
        </button>
        <div class="min-w-0 grow">
          <UiInput :id="'party-name-' + p.key" :model-value="p.name" :label="`Party ${p.key}`" sr-only-label size="sm" :error="errors[p.key]" :disabled="readonly" :data-test="'party-name-' + p.key" @update:model-value="emit('rename', p.key, String($event ?? ''))" />
          <span class="text-xs text-base-content/70">{{ counts[p.key] ?? 0 }} field{{ counts[p.key] === 1 ? '' : 's' }}</span>
        </div>
        <UiButton v-if="!readonly && parties.length > 1" size="xs" variant="text" color="error" icon="mdi-delete-outline" icon-only :label="`Remove ${p.name || p.key}`" class="mt-1" :data-test="'party-remove-' + p.key" @click="askRemove(p)" />
      </li>
    </ul>
    <UiButton v-if="!readonly" size="sm" variant="soft" icon="mdi-account-plus-outline" data-test="party-add" @click="emit('add')">Add party</UiButton>

    <UiDialog :model-value="!!removing" :title="`Remove ${removing?.name ?? ''}?`" size="sm" data-test="party-remove-dialog" @update:model-value="removing = null">
      <p class="mb-3 text-sm">This party has {{ removing ? counts[removing.key] : 0 }} field(s). Move them to another party, or delete them with the party.</p>
      <UiSelect id="party-reassign" v-model="target" label="Move the fields to" :options="others" :clearable="false" data-test="party-reassign" />
      <template #actions>
        <UiButton variant="text" @click="removing = null">Cancel</UiButton>
        <UiButton variant="soft" color="error" data-test="party-remove-delete" @click="finish(false)">Delete the fields</UiButton>
        <UiButton data-test="party-remove-move" @click="finish(true)">Move the fields</UiButton>
      </template>
    </UiDialog>
  </div>
</template>

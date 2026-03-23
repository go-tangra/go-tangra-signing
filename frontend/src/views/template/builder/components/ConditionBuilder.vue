<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { Button, Select, Input, Space, Divider } from 'ant-design-vue';
import { LucidePlus, LucideTrash } from 'shell/vben/icons';
import { $t } from 'shell/locales';
import type { Field, FieldConditionGroup } from '../../../../models/field';

interface Props {
  field: Field;
  availableFields: Field[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'update:conditions': [conditions: FieldConditionGroup[]];
}>();

const conditions = ref<FieldConditionGroup[]>(props.field.condition_groups || []);

watch(conditions, (newConditions) => {
  emit('update:conditions', newConditions);
}, { deep: true });

const operators = [
  { value: 'equals', label: 'Equals' },
  { value: 'not_equals', label: 'Not equals' },
  { value: 'contains', label: 'Contains' },
  { value: 'not_contains', label: 'Not contains' },
  { value: 'greater_than', label: 'Greater than' },
  { value: 'less_than', label: 'Less than' },
  { value: 'is_empty', label: 'Is empty' },
  { value: 'is_not_empty', label: 'Is not empty' },
];

const actions = [
  { value: 'show', label: 'Show' },
  { value: 'hide', label: 'Hide' },
  { value: 'require', label: 'Require' },
  { value: 'disable', label: 'Disable' },
];

const logicOptions = [
  { value: 'AND', label: 'ALL conditions (AND)' },
  { value: 'OR', label: 'ANY condition (OR)' },
];

function addGroup() {
  conditions.value = [
    ...conditions.value,
    {
      logic: 'AND',
      conditions: [{ field_id: '', operator: 'equals', value: '' }],
      action: 'show',
    },
  ];
}

function addCondition(groupIndex: number) {
  const group = conditions.value[groupIndex];
  conditions.value = conditions.value.map((g, i) =>
    i === groupIndex
      ? { ...g, conditions: [...g.conditions, { field_id: '', operator: 'equals' as const, value: '' }] }
      : g
  );
}

function removeCondition(groupIndex: number, condIndex: number) {
  const group = conditions.value[groupIndex];
  const newConds = group.conditions.filter((_, i) => i !== condIndex);
  if (newConds.length === 0) {
    conditions.value = conditions.value.filter((_, i) => i !== groupIndex);
  } else {
    conditions.value = conditions.value.map((g, i) =>
      i === groupIndex ? { ...g, conditions: newConds } : g
    );
  }
}

function removeGroup(groupIndex: number) {
  conditions.value = conditions.value.filter((_, i) => i !== groupIndex);
}

const fieldOptions = computed(() =>
  props.availableFields.map(f => ({
    value: f.id,
    label: f.name || f.id,
  }))
);
</script>

<template>
  <div class="conditions-builder">
    <p class="cb-muted mb-4 text-sm">
      Define when this field is shown, hidden, required or disabled based on other fields.
    </p>

    <!-- Empty state -->
    <div v-if="!conditions.length" class="cb-empty">
      <p class="cb-muted mb-3 text-sm">No rules yet.</p>
      <Button type="primary" @click="addGroup">
        <component :is="LucidePlus" class="mr-1 size-3" />
        Add Rule Group
      </Button>
    </div>

    <!-- Rule groups -->
    <div v-else class="space-y-4">
      <div
        v-for="(group, groupIndex) in conditions"
        :key="groupIndex"
        class="cb-group"
      >
        <!-- Group header -->
        <div class="cb-group-header">
          <span class="cb-muted text-xs font-medium uppercase tracking-wide">
            Rule {{ groupIndex + 1 }}
          </span>
          <span class="cb-separator">|</span>
          <Select
            :value="group.logic"
            size="small"
            style="width: 160px"
            :options="logicOptions"
            @change="(val: string) => group.logic = val as any"
          />
          <span class="cb-muted text-sm">then</span>
          <Select
            :value="group.action"
            size="small"
            style="width: 120px"
            :options="actions"
            @change="(val: string) => group.action = val as any"
          />
          <span class="cb-muted text-sm">this field</span>
        </div>

        <!-- Condition rows -->
        <div class="space-y-2 p-4">
          <div
            v-for="(condition, condIndex) in group.conditions"
            :key="condIndex"
            class="flex items-center gap-2"
          >
            <Select
              :value="condition.field_id"
              placeholder="Select field"
              size="small"
              style="flex: 1; min-width: 140px"
              :options="fieldOptions"
              @change="(val: string) => condition.field_id = val"
            />
            <Select
              :value="condition.operator"
              size="small"
              style="width: 150px"
              :options="operators"
              @change="(val: string) => condition.operator = val as any"
            />
            <Input
              v-if="condition.operator !== 'is_empty' && condition.operator !== 'is_not_empty'"
              :value="condition.value"
              placeholder="Value"
              size="small"
              style="flex: 1; min-width: 100px"
              @change="(e: any) => condition.value = e.target.value"
            />
            <div v-else style="flex: 1; min-width: 100px" />
            <Button
              type="text"
              danger
              size="small"
              @click="removeCondition(groupIndex, condIndex)"
            >
              <component :is="LucideTrash" class="size-3" />
            </Button>
          </div>

          <div class="cb-actions">
            <Button size="small" @click="addCondition(groupIndex)">
              <component :is="LucidePlus" class="mr-1 size-3" />
              Add Condition
            </Button>
            <Button type="text" danger size="small" @click="removeGroup(groupIndex)">
              <component :is="LucideTrash" class="mr-1 size-3" />
              Delete Rule
            </Button>
          </div>
        </div>
      </div>

      <Button block type="dashed" @click="addGroup">
        + Add Rule Group
      </Button>
    </div>
  </div>
</template>

<style scoped>
.cb-muted { color: hsl(var(--muted-foreground)); }
.cb-separator { color: hsl(var(--border)); }
.cb-empty {
  border: 2px dashed hsl(var(--border));
  border-radius: 0.5rem;
  padding: 2rem 0;
  text-align: center;
}
.cb-group {
  overflow: hidden;
  border-radius: 0.5rem;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
}
.cb-group-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  border-bottom: 1px solid hsl(var(--border));
  background: hsl(var(--muted));
  padding: 0.75rem 1rem;
}
.cb-actions {
  margin-top: 0.75rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid hsl(var(--border));
  padding-top: 0.75rem;
}
</style>

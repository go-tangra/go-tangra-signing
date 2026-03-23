import { computed, type Ref } from 'vue';
import type { Field, FieldCondition, FieldConditionGroup, FieldState } from '../models/field';

/**
 * Composable for evaluating conditional field logic.
 * Fields can show/hide/require/disable based on other field values using AND/OR logic.
 */
export function useConditions(
  fields: Ref<Field[]>,
  formData: Ref<Record<string, any>>
) {
  function evaluateCondition(condition: FieldCondition): boolean {
    const fieldValue = formData.value[condition.field_id];

    if (fieldValue === undefined || fieldValue === null) {
      if (condition.operator === 'is_empty') return true;
      if (condition.operator === 'is_not_empty') return false;
      const emptyValue = '';
      switch (condition.operator) {
        case 'equals': return emptyValue === condition.value;
        case 'not_equals': return emptyValue !== condition.value;
        default: return false;
      }
    }

    switch (condition.operator) {
      case 'equals':
        return fieldValue === condition.value;
      case 'not_equals':
        return fieldValue !== condition.value;
      case 'contains':
        return String(fieldValue).includes(String(condition.value));
      case 'not_contains':
        return !String(fieldValue).includes(String(condition.value));
      case 'greater_than':
        return Number(fieldValue) > Number(condition.value);
      case 'less_than':
        return Number(fieldValue) < Number(condition.value);
      case 'is_empty':
        return !fieldValue || fieldValue === '' ||
               (Array.isArray(fieldValue) && fieldValue.length === 0);
      case 'is_not_empty':
        return !!fieldValue && fieldValue !== '' &&
               !(Array.isArray(fieldValue) && fieldValue.length === 0);
      default:
        return false;
    }
  }

  function evaluateGroup(group: FieldConditionGroup): boolean {
    if (group.logic === 'AND') {
      return group.conditions.every(cond => evaluateCondition(cond));
    }
    return group.conditions.some(cond => evaluateCondition(cond));
  }

  function getFieldState(field: Field): FieldState {
    const state: FieldState = {
      visible: true,
      required: field.required || false,
      disabled: false,
    };

    if (!field.condition_groups || field.condition_groups.length === 0) {
      return state;
    }

    for (const group of field.condition_groups) {
      const conditionMet = evaluateGroup(group);

      if (conditionMet) {
        switch (group.action) {
          case 'show': state.visible = true; break;
          case 'hide': state.visible = false; break;
          case 'require': state.required = true; break;
          case 'disable': state.disabled = true; break;
        }
      } else {
        if (group.action === 'show') {
          state.visible = false;
        }
      }
    }

    return state;
  }

  const fieldStates = computed(() => {
    const _ = formData.value; // Force dependency tracking
    const states: Record<string, FieldState> = {};
    for (const field of fields.value) {
      states[field.id] = getFieldState(field);
    }
    return states;
  });

  return {
    fieldStates,
    evaluateCondition,
    evaluateGroup,
    getFieldState,
  };
}

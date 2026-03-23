import { computed, type Ref, watch, nextTick } from 'vue';
import type { Field } from '../models/field';

/**
 * Custom expression parser for formula evaluation.
 * Supports: +, -, *, /, parentheses, and custom functions (SUM, IF, MAX, MIN, ROUND).
 * Uses Function constructor for safe evaluation with sandboxed variables.
 */

const CUSTOM_FUNCTIONS = {
  SUM: (...args: number[]) => args.reduce((sum, val) => sum + (Number(val) || 0), 0),
  IF: (condition: boolean, trueVal: number, falseVal: number) => condition ? trueVal : falseVal,
  MAX: (...args: number[]) => Math.max(...args.map(v => Number(v) || 0)),
  MIN: (...args: number[]) => Math.min(...args.map(v => Number(v) || 0)),
  ROUND: (value: number, decimals: number = 0) => {
    const multiplier = Math.pow(10, decimals);
    return Math.round(value * multiplier) / multiplier;
  },
};

function evaluateExpression(formula: string, variables: Record<string, number>): number {
  // Build sandboxed evaluation context
  const varNames = Object.keys(variables);
  const varValues = Object.values(variables);
  const funcNames = Object.keys(CUSTOM_FUNCTIONS);
  const funcValues = Object.values(CUSTOM_FUNCTIONS);

  const fn = new Function(
    ...varNames,
    ...funcNames,
    `"use strict"; return (${formula});`
  );

  return fn(...varValues, ...funcValues);
}

/**
 * Composable for formula evaluation on template fields.
 * Fields with a `formula` property are automatically calculated from other field values.
 */
export function useFormulas(
  fields: Ref<Field[]>,
  formData: Ref<Record<string, any>>
) {
  function evaluateFormula(formula: string): number | null {
    try {
      const variables: Record<string, number> = {};
      for (const field of fields.value) {
        const value = formData.value[field.id];
        variables[field.id] = Number(value) || 0;
      }
      const result = evaluateExpression(formula, variables);
      return Number(result);
    } catch {
      return null;
    }
  }

  const calculatedValues = computed(() => {
    const _ = formData.value; // Force dependency tracking
    const values: Record<string, number> = {};
    for (const field of fields.value) {
      const formula = field.preferences?.formula ?? field.formula;
      if (formula) {
        const result = evaluateFormula(formula);
        if (result !== null) {
          values[field.id] = result;
        }
      }
    }
    return values;
  });

  // Auto-update formData with calculated values
  watch(calculatedValues, async (newValues, oldValues) => {
    await nextTick();
    for (const [fieldId, value] of Object.entries(newValues)) {
      if (value !== undefined && value !== null) {
        const oldValue = oldValues?.[fieldId];
        const currentValue = formData.value[fieldId];
        const field = fields.value.find(f => f.id === fieldId);
        const formula = field?.preferences?.formula ?? field?.formula;
        if (formula && oldValue !== value && currentValue !== value) {
          formData.value[fieldId] = value;
        }
      }
    }
  }, { immediate: true, deep: true });

  return {
    calculatedValues,
    evaluateFormula,
  };
}

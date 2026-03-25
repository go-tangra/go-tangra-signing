<script setup lang="ts">
/**
 * Sticky bottom panel for the signing session.
 *
 * Displays the active field's input, font/size selectors for text fields,
 * Previous/Next navigation, progress, and the submit button.
 */

import { computed } from 'vue';
import type { SigningFieldState, SigningProgress } from '../composables/useSigningFlow';

interface Props {
  activeField?: SigningFieldState;
  activeIndex: number;
  totalFields: number;
  progress: SigningProgress;
  canSubmit: boolean;
  isLastField: boolean;
  submitting: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  next: [];
  previous: [];
  submit: [];
  'clear-all': [];
  'update:value': [value: string];
  'update:font': [font: string];
  'update:fontSize': [size: number];
  'open-signature': [];
}>();

const fonts = [
  { label: 'Times New Roman', value: 'TimesNewRomanPSMT' },
  { label: 'Arial', value: 'Arial' },
  { label: 'Helvetica', value: 'Helvetica' },
  { label: 'Courier New', value: 'CourierNewPSMT' },
  { label: 'Georgia', value: 'Georgia' },
  { label: 'Verdana', value: 'Verdana' },
] as const;

const sizes = [8, 9, 10, 11, 12, 14, 16, 18, 20, 24] as const;

const isSignatureField = computed(() => {
  const t = props.activeField?.type;
  return t === 'signature' || t === 'initials';
});

const isDateField = computed(() => props.activeField?.type === 'date');

const isCheckboxField = computed(() => props.activeField?.type === 'checkbox');

const isTextField = computed(() => !isSignatureField.value && !isDateField.value && !isCheckboxField.value);

const progressPercent = computed(() => {
  if (props.progress.total === 0) return 0;
  return props.progress.percent;
});

function handleInput(event: Event): void {
  const target = event.target as HTMLInputElement;
  emit('update:value', target.value);
}

function handleDateInput(event: Event): void {
  const target = event.target as HTMLInputElement;
  emit('update:value', target.value);
}

function handleCheckboxChange(event: Event): void {
  const target = event.target as HTMLInputElement;
  emit('update:value', target.checked ? 'true' : 'false');
}

function handleFontChange(event: Event): void {
  const target = event.target as HTMLSelectElement;
  emit('update:font', target.value);
}

function handleSizeChange(event: Event): void {
  const target = event.target as HTMLSelectElement;
  emit('update:fontSize', Number(target.value));
}
</script>

<template>
  <div class="signing-panel">
    <!-- Progress bar -->
    <div class="signing-panel__progress">
      <div class="signing-panel__progress-info">
        <span>Field {{ activeIndex + 1 }} of {{ totalFields }}</span>
        <span>{{ progress.filled }} / {{ progress.total }} completed</span>
      </div>
      <div class="signing-panel__progress-bar">
        <div
          class="signing-panel__progress-fill"
          :style="{ width: `${progressPercent}%` }"
          :class="{ 'signing-panel__progress-fill--done': progressPercent === 100 }"
        />
      </div>
    </div>

    <!-- Field input area -->
    <div v-if="activeField" class="signing-panel__input-area">
      <div class="signing-panel__field-label">
        {{ activeField.name }}
        <span v-if="activeField.required" class="signing-panel__required">*</span>
      </div>

      <div class="signing-panel__input-row">
        <!-- Font controls for text fields -->
        <template v-if="isTextField">
          <select
            class="signing-panel__font-select"
            :value="activeField.font || 'TimesNewRomanPSMT'"
            @change="handleFontChange"
          >
            <option
              v-for="f in fonts"
              :key="f.value"
              :value="f.value"
              :style="{ fontFamily: f.value }"
            >
              {{ f.label }}
            </option>
          </select>
          <select
            class="signing-panel__size-select"
            :value="activeField.fontSize || 11"
            @change="handleSizeChange"
          >
            <option v-for="s in sizes" :key="s" :value="s">{{ s }}pt</option>
          </select>
          <input
            type="text"
            class="signing-panel__text-input"
            :value="activeField.value"
            :placeholder="activeField.name || 'Enter text...'"
            :style="{
              fontFamily: activeField.font || 'TimesNewRomanPSMT',
              fontSize: `${activeField.fontSize || 11}pt`,
            }"
            autofocus
            @input="handleInput"
          />
        </template>

        <!-- Date input -->
        <input
          v-else-if="isDateField"
          type="date"
          class="signing-panel__text-input"
          :value="activeField.value"
          @input="handleDateInput"
        />

        <!-- Checkbox -->
        <label v-else-if="isCheckboxField" class="signing-panel__checkbox-label">
          <input
            type="checkbox"
            class="signing-panel__checkbox"
            :checked="activeField.value === 'true'"
            @change="handleCheckboxChange"
          />
          <span>{{ activeField.name }}</span>
        </label>

        <!-- Signature -->
        <button
          v-else-if="isSignatureField"
          type="button"
          class="signing-panel__signature-btn"
          @click="emit('open-signature')"
        >
          <template v-if="activeField.value">
            Signed &#10003;
          </template>
          <template v-else>
            &#9998; Click to draw signature
          </template>
        </button>
      </div>
    </div>

    <!-- Navigation & actions -->
    <div class="signing-panel__nav">
      <button
        type="button"
        class="signing-panel__nav-btn"
        :disabled="activeIndex === 0"
        @click="emit('previous')"
      >
        &larr; Previous
      </button>
      <button
        type="button"
        class="signing-panel__clear-btn"
        @click="emit('clear-all')"
      >
        Clear All
      </button>
      <div class="signing-panel__nav-right">
        <button
          v-if="!isLastField"
          type="button"
          class="signing-panel__nav-btn signing-panel__nav-btn--primary"
          @click="emit('next')"
        >
          Next &rarr;
        </button>
        <button
          v-if="isLastField || canSubmit"
          type="button"
          class="signing-panel__submit-btn"
          :disabled="!canSubmit || submitting"
          @click="emit('submit')"
        >
          <template v-if="submitting">Submitting...</template>
          <template v-else>Sign Document</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.signing-panel {
  position: sticky;
  bottom: 0;
  z-index: 30;
  background: hsl(var(--card));
  border-top: 1px solid hsl(var(--border));
  box-shadow: 0 -4px 12px hsl(var(--foreground) / 0.08);
}

.signing-panel__progress {
  padding: 10px 16px 0;
}

.signing-panel__progress-info {
  display: flex;
  justify-content: space-between;
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
  margin-bottom: 4px;
}

.signing-panel__progress-bar {
  height: 4px;
  border-radius: 2px;
  background: hsl(var(--muted));
  overflow: hidden;
}

.signing-panel__progress-fill {
  height: 100%;
  border-radius: 2px;
  background: hsl(var(--primary));
  transition: width 0.3s ease;
}

.signing-panel__progress-fill--done {
  background: hsl(143 72% 42%);
}

.signing-panel__input-area {
  padding: 10px 16px;
}

.signing-panel__field-label {
  font-size: 0.8125rem;
  font-weight: 600;
  color: hsl(var(--foreground));
  margin-bottom: 6px;
}

.signing-panel__required {
  color: hsl(var(--destructive));
}

.signing-panel__input-row {
  display: flex;
  gap: 6px;
  align-items: center;
}

.signing-panel__font-select,
.signing-panel__size-select {
  border: 1px solid hsl(var(--input));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  font-size: 0.8125rem;
  padding: 6px 8px;
  outline: none;
  cursor: pointer;
  flex-shrink: 0;
}

.signing-panel__font-select {
  min-width: 130px;
}

.signing-panel__size-select {
  min-width: 64px;
}

.signing-panel__font-select:focus,
.signing-panel__size-select:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 2px hsl(var(--primary) / 0.1);
}

.signing-panel__text-input {
  flex: 1;
  min-width: 0;
  border: 1px solid hsl(var(--input));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  padding: 6px 10px;
  outline: none;
}

.signing-panel__text-input:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 2px hsl(var(--primary) / 0.1);
}

.signing-panel__text-input::placeholder {
  color: hsl(var(--muted-foreground));
}

.signing-panel__checkbox-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.875rem;
  color: hsl(var(--foreground));
  cursor: pointer;
}

.signing-panel__checkbox {
  width: 18px;
  height: 18px;
  accent-color: hsl(var(--primary));
  cursor: pointer;
}

.signing-panel__signature-btn {
  flex: 1;
  padding: 10px 16px;
  border: 2px dashed hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--muted));
  color: hsl(var(--primary));
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.signing-panel__signature-btn:hover {
  background: hsl(var(--primary) / 0.08);
  border-color: hsl(var(--primary) / 0.4);
}

.signing-panel__nav {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 16px 12px;
}

.signing-panel__nav-right {
  margin-left: auto;
  display: flex;
  gap: 8px;
}

.signing-panel__nav-btn {
  padding: 6px 14px;
  font-size: 0.8125rem;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  cursor: pointer;
  transition: background 0.15s;
}

.signing-panel__nav-btn:hover:not(:disabled) {
  background: hsl(var(--muted));
}

.signing-panel__nav-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.signing-panel__nav-btn--primary {
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  border-color: hsl(var(--primary));
}

.signing-panel__nav-btn--primary:hover:not(:disabled) {
  opacity: 0.9;
}

.signing-panel__clear-btn {
  padding: 6px 14px;
  font-size: 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  transition: background 0.15s;
}

.signing-panel__clear-btn:hover {
  background: hsl(var(--muted));
}

.signing-panel__submit-btn {
  padding: 6px 20px;
  font-size: 0.8125rem;
  font-weight: 600;
  border: none;
  border-radius: var(--radius);
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  cursor: pointer;
  transition: opacity 0.15s;
}

.signing-panel__submit-btn:hover:not(:disabled) {
  opacity: 0.9;
}

.signing-panel__submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>

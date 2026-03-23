<script setup lang="ts">
/**
 * Field area overlay rendered on top of PDF pages during a signing session.
 *
 * Shows a clickable label at the field position. The actual input happens
 * in the SigningPanel (sticky bottom panel), not inline on the PDF.
 */

import { computed, ref, onMounted, onUnmounted } from 'vue';
import type { SigningFieldState } from '../composables/useSigningFlow';

interface Props {
  field: SigningFieldState;
  active: boolean;
  filled: boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  click: [fieldId: string];
}>();

const rootRef = ref<HTMLDivElement | null>(null);
const containerHeight = ref(0);

function updateContainerHeight(): void {
  const parent = rootRef.value?.parentElement;
  if (parent) {
    containerHeight.value = parent.clientHeight;
  }
}

let resizeObserver: ResizeObserver | null = null;

onMounted(() => {
  updateContainerHeight();
  const parent = rootRef.value?.parentElement;
  if (parent) {
    resizeObserver = new ResizeObserver(() => updateContainerHeight());
    resizeObserver.observe(parent);
  }
});

onUnmounted(() => {
  resizeObserver?.disconnect();
});

const style = computed(() => ({
  left: `${props.field.xPercent}%`,
  top: `${props.field.yPercent}%`,
  width: `${Math.max(props.field.widthPercent, 8)}%`,
  minHeight: `${Math.max(props.field.heightPercent, 2.4)}%`,
}));

const fieldIcon = computed(() => {
  if (props.field.type === 'signature' || props.field.type === 'initials') return '\u270D';
  if (props.field.type === 'date') return '\uD83D\uDCC5';
  if (props.field.type === 'checkbox') return '\u2611';
  return 'Aa';
});

const displayValue = computed(() => {
  if ((props.field.type === 'signature' || props.field.type === 'initials') && props.field.value) {
    return '[Signed]';
  }
  if (props.field.value) return props.field.value;
  return null;
});

const inputStyle = computed(() => {
  const s: Record<string, string> = {};
  if (props.field.font) {
    s.fontFamily = props.field.font;
  }
  if (props.field.fontSize && containerHeight.value > 0) {
    // Convert PDF points to CSS pixels that match the rendered PDF scale.
    // PDF page height is ~842pt (A4). The container displays the full page,
    // so 1 PDF point = containerHeight / 842 CSS pixels.
    const pxPerPt = containerHeight.value / 842;
    s.fontSize = `${props.field.fontSize * pxPerPt}px`;
  }
  return s;
});
</script>

<template>
  <div
    ref="rootRef"
    class="session-field-area"
    :class="{
      'session-field-area--active': active,
      'session-field-area--filled': filled,
      'session-field-area--empty': !filled,
    }"
    :style="style"
    @click.stop="emit('click', field.fieldId)"
  >
    <div class="session-field-area__content">
      <template v-if="filled && displayValue">
        <span class="session-field-area__value" :style="inputStyle">
          {{ displayValue }}
        </span>
      </template>
      <template v-else>
        <span class="session-field-area__icon">{{ fieldIcon }}</span>
        <span class="session-field-area__name">{{ field.name }}</span>
      </template>
    </div>
  </div>
</template>

<style scoped>
.session-field-area {
  position: absolute;
  display: flex;
  align-items: center;
  cursor: pointer;
  border-radius: 3px;
  transition: all 0.2s;
  min-width: 60px;
  min-height: 24px;
}

.session-field-area--empty {
  border: 2px solid #fa8c16;
  background: rgba(255, 165, 0, 0.08);
  z-index: 1;
}

.session-field-area--empty:hover {
  background: rgba(255, 165, 0, 0.15);
}

.session-field-area--filled {
  border: 2px solid #52c41a;
  background: rgba(82, 196, 26, 0.06);
  z-index: 1;
}

.session-field-area--active {
  border: 2px solid #1677ff;
  background: rgba(22, 119, 255, 0.08);
  box-shadow: 0 0 0 2px rgba(22, 119, 255, 0.25);
  z-index: 2;
}

.session-field-area--active.session-field-area--empty {
  animation: pulse-subtle 2s ease-in-out infinite;
}

.session-field-area__content {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 2px 6px;
}

.session-field-area__icon {
  flex-shrink: 0;
  font-size: 0.75rem;
}

.session-field-area__name {
  font-size: 0.75rem;
  color: #d46b08;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.session-field-area--active .session-field-area__name {
  color: #1677ff;
}

.session-field-area__value {
  font-size: 0.8125rem;
  color: #389e0d;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@keyframes pulse-subtle {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.75; }
}
</style>

/**
 * Composable that manages field state during a signing session.
 *
 * Tracks which fields have been filled, provides progress information,
 * and builds the submission payload for the API.
 */

import { ref, computed, type Ref, type ComputedRef } from 'vue';
import type {
  SessionField,
  FieldValueSubmission,
} from '../../../api/session-client';

export interface SigningFieldState {
  readonly fieldId: string;
  readonly name: string;
  readonly type: string;
  readonly required: boolean;
  readonly pageNumber: number;
  readonly xPercent: number;
  readonly yPercent: number;
  readonly widthPercent: number;
  readonly heightPercent: number;
  readonly value: string;
  readonly font?: string;
  readonly fontSize?: number;
}

export interface SigningProgress {
  readonly filled: number;
  readonly total: number;
  readonly percent: number;
}

export interface UseSigningFlowReturn {
  readonly fields: Ref<readonly SigningFieldState[]>;
  readonly activeFieldIndex: Ref<number>;
  readonly activeField: ComputedRef<SigningFieldState | undefined>;
  readonly progress: ComputedRef<SigningProgress>;
  readonly canSubmit: ComputedRef<boolean>;
  readonly isLastField: ComputedRef<boolean>;
  readonly signatureImage: Ref<string>;
  isFieldFilled(field: SigningFieldState): boolean;
  initFields(sessionFields: readonly SessionField[]): void;
  setFieldValue(fieldId: string, value: string): void;
  setFieldFont(fieldId: string, font: string): void;
  setFieldFontSize(fieldId: string, fontSize: number): void;
  setActiveField(index: number): void;
  goToField(fieldId: string): void;
  goToNext(): void;
  goToPrevious(): void;
  setSignatureImage(base64: string): void;
  buildSubmitPayload(): {
    readonly fieldValues: readonly FieldValueSubmission[];
    readonly signatureImage?: string;
  };
}

export function useSigningFlow(): UseSigningFlowReturn {
  const fields: Ref<readonly SigningFieldState[]> = ref([]);
  const activeFieldIndex = ref(-1);
  const signatureImage = ref('');

  function initFields(sessionFields: readonly SessionField[]): void {
    fields.value = sessionFields.map((f) => ({
      fieldId: f.fieldId,
      name: f.name,
      type: f.type,
      required: f.required,
      pageNumber: f.pageNumber,
      xPercent: f.xPercent,
      yPercent: f.yPercent,
      widthPercent: f.widthPercent,
      heightPercent: f.heightPercent,
      value: f.prefilledValue ?? '',
      font: f.font,
      fontSize: f.fontSize,
    }));
    activeFieldIndex.value = fields.value.length > 0 ? 0 : -1;
  }

  function setFieldValue(fieldId: string, value: string): void {
    fields.value = fields.value.map((f) =>
      f.fieldId === fieldId ? { ...f, value } : f,
    );
  }

  function setFieldFont(fieldId: string, font: string): void {
    fields.value = fields.value.map((f) =>
      f.fieldId === fieldId ? { ...f, font } : f,
    );
  }

  function setFieldFontSize(fieldId: string, fontSize: number): void {
    fields.value = fields.value.map((f) =>
      f.fieldId === fieldId ? { ...f, fontSize } : f,
    );
  }

  function setActiveField(index: number): void {
    activeFieldIndex.value = index;
  }

  function goToField(fieldId: string): void {
    const index = fields.value.findIndex((f) => f.fieldId === fieldId);
    if (index >= 0) {
      activeFieldIndex.value = index;
    }
  }

  function goToNext(): void {
    if (activeFieldIndex.value < fields.value.length - 1) {
      activeFieldIndex.value++;
    }
  }

  function goToPrevious(): void {
    if (activeFieldIndex.value > 0) {
      activeFieldIndex.value--;
    }
  }

  function isFieldFilled(field: SigningFieldState): boolean {
    if (field.type === 'signature' || field.type === 'initials') {
      return signatureImage.value.length > 0;
    }
    if (field.type === 'checkbox') return true;
    return field.value.trim().length > 0;
  }

  function setSignatureImage(base64: string): void {
    signatureImage.value = base64;
  }

  const activeField = computed<SigningFieldState | undefined>(() => {
    const idx = activeFieldIndex.value;
    if (idx < 0 || idx >= fields.value.length) return undefined;
    return fields.value[idx];
  });

  const isLastField = computed<boolean>(() =>
    activeFieldIndex.value === fields.value.length - 1,
  );

  const progress = computed<SigningProgress>(() => {
    const total = fields.value.length;
    const filled = fields.value.filter((f) => {
      if (f.type === 'signature') return signatureImage.value.length > 0;
      if (f.type === 'checkbox') return true; // checkboxes always count
      return f.value.trim().length > 0;
    }).length;
    const percent = total > 0 ? Math.round((filled / total) * 100) : 0;
    return { filled, total, percent };
  });

  const canSubmit = computed<boolean>(() => {
    const requiredFields = fields.value.filter((f) => f.required);
    return requiredFields.every((f) => {
      if (f.type === 'signature') return signatureImage.value.length > 0;
      if (f.type === 'checkbox') return true;
      return f.value.trim().length > 0;
    });
  });

  function buildSubmitPayload(): {
    readonly fieldValues: readonly FieldValueSubmission[];
    readonly signatureImage?: string;
  } {
    const fieldValues: FieldValueSubmission[] = fields.value.map((f) => ({
      fieldId: f.fieldId,
      value: f.value,
    }));

    return {
      fieldValues,
      ...(signatureImage.value ? { signatureImage: signatureImage.value } : {}),
    };
  }

  return {
    fields,
    activeFieldIndex,
    activeField,
    progress,
    canSubmit,
    isLastField,
    signatureImage,
    isFieldFilled,
    initFields,
    setFieldValue,
    setFieldFont,
    setFieldFontSize,
    setActiveField,
    goToField,
    goToNext,
    goToPrevious,
    setSignatureImage,
    buildSubmitPayload,
  };
}

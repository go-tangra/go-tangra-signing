import { ref, computed } from 'vue';
import type { FieldType } from '../../../../models/field';
import { fieldNames } from '../../../../models/field';
import type { TemplateFieldDef } from '../../../../stores/signing-template.state';

export interface BuilderField {
  readonly id: string;
  readonly submitterIndex: number;
  readonly name: string;
  readonly type: FieldType;
  readonly required: boolean;
  readonly pageNumber: number;
  readonly xPercent: number;
  readonly yPercent: number;
  readonly widthPercent: number;
  readonly heightPercent: number;
  readonly font?: string;
  readonly fontSize?: number;
}

type DefaultDimensions = Readonly<{ w: number; h: number }>;

const DEFAULT_DIMENSIONS: Readonly<Record<FieldType, DefaultDimensions>> = {
  text: { w: 20, h: 3 },
  number: { w: 20, h: 3 },
  signature: { w: 20, h: 6 },
  initials: { w: 10, h: 5 },
  date: { w: 15, h: 3 },
  checkbox: { w: 3, h: 3 },
  select: { w: 20, h: 3 },
  radio: { w: 20, h: 3 },
  multiple: { w: 20, h: 3 },
  image: { w: 15, h: 8 },
  file: { w: 15, h: 8 },
  cells: { w: 25, h: 3 },
  stamp: { w: 15, h: 8 },
  payment: { w: 20, h: 3 },
};

function clampPercent(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

export function useFieldBuilder() {
  const fields = ref<readonly BuilderField[]>([]);
  const selectedFieldId = ref<string | undefined>();
  const isDirty = ref(false);

  let fieldCounter = 0;

  const selectedField = computed<BuilderField | undefined>(() =>
    fields.value.find((f) => f.id === selectedFieldId.value),
  );

  function addField(
    type: FieldType,
    pageNumber: number,
    xPercent: number,
    yPercent: number,
    submitterIndex: number = 0,
  ): BuilderField {
    fieldCounter++;
    const dims = DEFAULT_DIMENSIONS[type];
    const clampedX = clampPercent(xPercent, 0, 100 - dims.w);
    const clampedY = clampPercent(yPercent, 0, 100 - dims.h);

    const newField: BuilderField = {
      id: `field_${fieldCounter}`,
      submitterIndex,
      name: `${fieldNames[type]} ${fieldCounter}`,
      type,
      required: false,
      pageNumber,
      xPercent: clampedX,
      yPercent: clampedY,
      widthPercent: dims.w,
      heightPercent: dims.h,
    };

    fields.value = [...fields.value, newField];
    selectedFieldId.value = newField.id;
    isDirty.value = true;
    return newField;
  }

  function removeField(id: string): void {
    fields.value = fields.value.filter((f) => f.id !== id);
    if (selectedFieldId.value === id) {
      selectedFieldId.value = undefined;
    }
    isDirty.value = true;
  }

  function moveField(id: string, xPercent: number, yPercent: number): void {
    fields.value = fields.value.map((f) => {
      if (f.id !== id) return f;
      const clampedX = clampPercent(xPercent, 0, 100 - f.widthPercent);
      const clampedY = clampPercent(yPercent, 0, 100 - f.heightPercent);
      return { ...f, xPercent: clampedX, yPercent: clampedY };
    });
    isDirty.value = true;
  }

  function resizeField(
    id: string,
    widthPercent: number,
    heightPercent: number,
  ): void {
    fields.value = fields.value.map((f) => {
      if (f.id !== id) return f;
      const w = clampPercent(widthPercent, 2, 100 - f.xPercent);
      const h = clampPercent(heightPercent, 1, 100 - f.yPercent);
      return { ...f, widthPercent: w, heightPercent: h };
    });
    isDirty.value = true;
  }

  function updateField(
    id: string,
    updates: Partial<Pick<BuilderField, 'name' | 'type' | 'required' | 'submitterIndex'>>,
  ): void {
    fields.value = fields.value.map((f) => {
      if (f.id !== id) return f;
      return { ...f, ...updates };
    });
    isDirty.value = true;
  }

  function selectField(id: string | undefined): void {
    selectedFieldId.value = id;
  }

  function addDetectedField(
    name: string,
    type: FieldType,
    pageNumber: number,
    xPercent: number,
    yPercent: number,
    widthPercent: number,
    heightPercent: number,
    font?: string,
    fontSize?: number,
  ): BuilderField {
    fieldCounter++;
    const newField: BuilderField = {
      id: `field_${fieldCounter}`,
      submitterIndex: 0,
      name,
      type,
      required: false,
      pageNumber,
      xPercent: clampPercent(xPercent, 0, 100 - widthPercent),
      yPercent: clampPercent(yPercent, 0, 100 - heightPercent),
      widthPercent,
      heightPercent,
      font,
      fontSize,
    };
    fields.value = [...fields.value, newField];
    isDirty.value = true;
    return newField;
  }

  function loadFields(existingFields: readonly TemplateFieldDef[]): void {
    fields.value = existingFields.map((f) => ({
      id: f.id,
      submitterIndex: f.submitterIndex,
      name: f.name,
      type: f.type as FieldType,
      required: f.required,
      pageNumber: f.pageNumber,
      xPercent: f.xPercent,
      yPercent: f.yPercent,
      widthPercent: f.widthPercent,
      heightPercent: f.heightPercent,
      font: f.font,
      fontSize: f.fontSize,
    }));
    fieldCounter = existingFields.length;
    isDirty.value = false;
  }

  function toTemplateFields(): TemplateFieldDef[] {
    return fields.value.map((f) => ({
      id: f.id,
      name: f.name,
      type: f.type,
      required: f.required,
      pageNumber: f.pageNumber,
      xPercent: f.xPercent,
      yPercent: f.yPercent,
      widthPercent: f.widthPercent,
      heightPercent: f.heightPercent,
      submitterIndex: f.submitterIndex,
      font: f.font,
      fontSize: f.fontSize,
    }));
  }

  function markClean(): void {
    isDirty.value = false;
  }

  function removeFieldsBySubmitterIndex(submitterIndex: number): void {
    fields.value = fields.value.filter(
      (f) => f.submitterIndex !== submitterIndex,
    );
    isDirty.value = true;
  }

  return {
    fields,
    selectedFieldId,
    selectedField,
    isDirty,
    addField,
    addDetectedField,
    removeField,
    moveField,
    resizeField,
    updateField,
    selectField,
    loadFields,
    toTemplateFields,
    markClean,
    removeFieldsBySubmitterIndex,
  };
}

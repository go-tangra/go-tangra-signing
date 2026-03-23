// Field types supported by the signing template builder
export type FieldType =
  | 'text'
  | 'number'
  | 'signature'
  | 'initials'
  | 'date'
  | 'image'
  | 'file'
  | 'select'
  | 'radio'
  | 'checkbox'
  | 'multiple'
  | 'cells'
  | 'stamp'
  | 'payment';

export type ConditionOperator =
  | 'equals'
  | 'not_equals'
  | 'contains'
  | 'not_contains'
  | 'greater_than'
  | 'less_than'
  | 'is_empty'
  | 'is_not_empty';

export type ConditionAction = 'show' | 'hide' | 'require' | 'disable';

export type LogicOperator = 'AND' | 'OR';

export interface FieldCondition {
  field_id: string;
  operator: ConditionOperator;
  value: any;
}

export interface FieldConditionGroup {
  logic: LogicOperator;
  conditions: FieldCondition[];
  action: ConditionAction;
}

export interface FieldArea {
  attachment_id: string;
  page: number;
  x: number;
  y: number;
  w: number;
  h: number;
  cell_w?: number;
  cell_count?: number;
  option_id?: string;
}

export interface FieldOption {
  id: string;
  value: string;
  label?: string;
}

export interface FieldValidation {
  pattern?: string;
  message?: string;
  min?: number;
  max?: number;
  step?: string;
}

export interface FieldPreferences {
  format?: string;
  align?: string;
  font?: string;
  font_size?: number;
  color?: string;
  price?: number;
  currency?: string;
  formula?: string;
  with_logo?: boolean;
  with_signature_id?: boolean;
}

export interface Field {
  id: string;
  submitter_id: string;
  name: string;
  type: FieldType;
  required: boolean;
  readonly?: boolean;
  default_value?: string;
  label?: string;
  title?: string;
  description?: string;
  options?: FieldOption[];
  validation?: FieldValidation;
  preferences?: FieldPreferences;
  condition_groups?: FieldConditionGroup[];
  areas?: FieldArea[];
  formula?: string;
}

export interface FieldState {
  visible: boolean;
  required: boolean;
  disabled: boolean;
}

// Constants
export const fieldNames: Record<FieldType, string> = {
  text: 'Text',
  number: 'Number',
  signature: 'Signature',
  initials: 'Initials',
  date: 'Date',
  image: 'Image',
  file: 'File',
  select: 'Select',
  checkbox: 'Checkbox',
  multiple: 'Multiple',
  radio: 'Radio',
  cells: 'Cells',
  stamp: 'Stamp',
  payment: 'Payment',
};

export const fieldIcons: Record<FieldType, string> = {
  text: 'lucide:type',
  number: 'lucide:hash',
  signature: 'lucide:pen-tool',
  initials: 'lucide:letter-text',
  date: 'lucide:calendar',
  image: 'lucide:image',
  file: 'lucide:paperclip',
  select: 'lucide:list',
  checkbox: 'lucide:check-square',
  multiple: 'lucide:check-check',
  radio: 'lucide:circle-dot',
  cells: 'lucide:columns-3',
  stamp: 'lucide:stamp',
  payment: 'lucide:credit-card',
};

export const submitterColors = [
  { bg: 'bg-red-100/80', border: 'border-red-500/80', dot: 'bg-red-500' },
  { bg: 'bg-sky-100/80', border: 'border-sky-500/80', dot: 'bg-sky-500' },
  { bg: 'bg-emerald-100/80', border: 'border-emerald-500/80', dot: 'bg-emerald-500' },
  { bg: 'bg-yellow-100/80', border: 'border-yellow-300/80', dot: 'bg-yellow-300' },
  { bg: 'bg-purple-100/80', border: 'border-purple-600/80', dot: 'bg-purple-600' },
  { bg: 'bg-pink-100/80', border: 'border-pink-500/80', dot: 'bg-pink-500' },
  { bg: 'bg-cyan-100/80', border: 'border-cyan-500/80', dot: 'bg-cyan-500' },
  { bg: 'bg-orange-100/80', border: 'border-orange-500/80', dot: 'bg-orange-500' },
  { bg: 'bg-lime-100/80', border: 'border-lime-500/80', dot: 'bg-lime-500' },
  { bg: 'bg-indigo-100/80', border: 'border-indigo-500/80', dot: 'bg-indigo-500' },
];

export const submitterNames = [
  'First Party', 'Second Party', 'Third Party', 'Fourth Party', 'Fifth Party',
  'Sixth Party', 'Seventh Party', 'Eighth Party', 'Ninth Party', 'Tenth Party',
];

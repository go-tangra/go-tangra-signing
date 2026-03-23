import { defineStore } from 'pinia';

import { signingApi } from '../api/client';
import type { Paging } from '../types';

export interface TemplateFieldDef {
  id: string;
  name: string;
  type: string;
  required: boolean;
  pageNumber: number;
  xPercent: number;
  yPercent: number;
  widthPercent: number;
  heightPercent: number;
  submitterIndex: number;
  font?: string;
  fontSize?: number;
}

export interface SigningTemplate {
  id?: string;
  tenantId?: number;
  name?: string;
  slug?: string;
  description?: string;
  folderId?: string;
  status?: string;
  source?: string;
  fileKey?: string;
  fileName?: string;
  fileSize?: number;
  fields?: TemplateFieldDef[];
  createdBy?: number;
  createTime?: string;
  updateTime?: string;
}

interface ListTemplatesResponse {
  templates?: SigningTemplate[];
  total?: number;
}

interface GetTemplateResponse {
  template?: SigningTemplate;
}

interface CreateTemplateResponse {
  template?: SigningTemplate;
}

interface UpdateTemplateResponse {
  template?: SigningTemplate;
}

interface CloneTemplateResponse {
  template?: SigningTemplate;
}

interface UpdateTemplateFieldsResponse {
  template?: SigningTemplate;
}

interface GetTemplatePdfUrlResponse {
  url?: string;
}

// Map frontend field type (lowercase) to proto enum name
const fieldTypeToProto: Record<string, string> = {
  text: 'TEMPLATE_FIELD_TYPE_TEXT',
  number: 'TEMPLATE_FIELD_TYPE_NUMBER',
  signature: 'TEMPLATE_FIELD_TYPE_SIGNATURE',
  initials: 'TEMPLATE_FIELD_TYPE_INITIALS',
  date: 'TEMPLATE_FIELD_TYPE_DATE',
  checkbox: 'TEMPLATE_FIELD_TYPE_CHECKBOX',
  select: 'TEMPLATE_FIELD_TYPE_SELECT',
  radio: 'TEMPLATE_FIELD_TYPE_RADIO',
  image: 'TEMPLATE_FIELD_TYPE_IMAGE',
  file: 'TEMPLATE_FIELD_TYPE_FILE',
  cells: 'TEMPLATE_FIELD_TYPE_CELLS',
  stamp: 'TEMPLATE_FIELD_TYPE_STAMP',
  payment: 'TEMPLATE_FIELD_TYPE_PAYMENT',
};

// Map proto enum name back to frontend lowercase
const protoToFieldType: Record<string, string> = Object.fromEntries(
  Object.entries(fieldTypeToProto).map(([k, v]) => [v, k]),
);

function buildQuery(params: Record<string, unknown>): string {
  const searchParams = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') {
      searchParams.append(key, String(value));
    }
  }
  const query = searchParams.toString();
  return query ? `?${query}` : '';
}

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = reader.result as string;
      // Strip the data URL prefix (e.g. "data:application/pdf;base64,")
      const base64 = result.includes(',') ? result.split(',')[1]! : result;
      resolve(base64);
    };
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

export const useSigningTemplateStore = defineStore('signing-template', () => {
  async function listTemplates(
    paging?: Paging,
    formValues?: { nameFilter?: string; status?: string } | null,
  ): Promise<ListTemplatesResponse> {
    return await signingApi.get<ListTemplatesResponse>(
      `/signing/templates${buildQuery({
        nameFilter: formValues?.nameFilter,
        status: formValues?.status,
        page: paging?.page,
        pageSize: paging?.pageSize,
      })}`,
    );
  }

  async function getTemplate(id: string): Promise<GetTemplateResponse> {
    const resp = await signingApi.get<GetTemplateResponse>(`/signing/templates/${id}`);
    // Convert proto enum names back to frontend lowercase
    if (resp.template?.fields) {
      resp.template.fields = resp.template.fields.map(f => ({
        ...f,
        type: protoToFieldType[f.type] ?? f.type,
      }));
    }
    return resp;
  }

  async function createTemplate(
    metadata: { name: string; description?: string },
    file: File,
  ): Promise<CreateTemplateResponse> {
    const fileContent = await fileToBase64(file);
    return await signingApi.post<CreateTemplateResponse>('/signing/templates', {
      name: metadata.name,
      description: metadata.description,
      fileName: file.name,
      fileContent,
    });
  }

  async function updateTemplate(
    id: string,
    data: { name?: string; description?: string; status?: string },
  ): Promise<UpdateTemplateResponse> {
    return await signingApi.put<UpdateTemplateResponse>(`/signing/templates/${id}`, data);
  }

  async function deleteTemplate(id: string): Promise<void> {
    return await signingApi.delete<void>(`/signing/templates/${id}`);
  }

  async function cloneTemplate(id: string, name?: string): Promise<CloneTemplateResponse> {
    return await signingApi.post<CloneTemplateResponse>(`/signing/templates/${id}/clone`, { name });
  }

  async function updateTemplateFields(
    id: string,
    fields: TemplateFieldDef[],
  ): Promise<UpdateTemplateFieldsResponse> {
    // Convert frontend field types to proto enum names
    const protoFields = fields.map(f => ({
      ...f,
      type: fieldTypeToProto[f.type] ?? f.type,
    }));
    return await signingApi.put<UpdateTemplateFieldsResponse>(
      `/signing/templates/${id}/fields`,
      { fields: protoFields },
    );
  }

  async function getTemplatePdfUrl(id: string): Promise<string> {
    const resp = await signingApi.get<GetTemplatePdfUrlResponse>(
      `/signing/templates/${id}/pdf-url`,
    );
    return resp.url ?? '';
  }

  interface DetectedField {
    name: string;
    type: string;
    pageNumber: number;
    xPercent: number;
    yPercent: number;
    widthPercent: number;
    heightPercent: number;
    font?: string;
    fontSize?: number;
  }

  interface DetectFieldsResponse {
    fields: DetectedField[];
    pages: number;
  }

  async function detectFields(templateId: string): Promise<DetectFieldsResponse> {
    const response = await fetch(
      `/modules/signing/api/v1/signing/templates/detect-fields?id=${encodeURIComponent(templateId)}`,
    );
    if (!response.ok) {
      const err = await response.json().catch(() => ({}));
      throw new Error(err.error ?? `Detection failed: ${response.status}`);
    }
    return response.json();
  }

  function $reset() {}

  return {
    $reset,
    listTemplates,
    getTemplate,
    createTemplate,
    updateTemplate,
    deleteTemplate,
    cloneTemplate,
    updateTemplateFields,
    getTemplatePdfUrl,
    detectFields,
  };
});

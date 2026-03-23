import { defineStore } from 'pinia';

import { signingApi } from '../api/client';
import type { Paging } from '../types';

export interface Submission {
  id?: string;
  tenantId?: number;
  templateId?: string;
  slug?: string;
  signingMode?: string;
  status?: string;
  source?: string;
  createdBy?: number;
  completedAt?: string;
  createTime?: string;
  signedDocumentKey?: string;
  currentPdfKey?: string;
}

export interface SubmitterInput {
  name?: string;
  email?: string;
  phone?: string;
  role?: string;
}

interface ListSubmissionsResponse {
  submissions?: Submission[];
  total?: number;
}

interface GetSubmissionResponse {
  submission?: Submission;
}

interface CreateSubmissionResponse {
  submission?: Submission;
}

interface SendSubmissionResponse {
  submission?: Submission;
}

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

export const useSigningSubmissionStore = defineStore('signing-submission', () => {
  async function listSubmissions(
    paging?: Paging,
    formValues?: { templateId?: string; status?: string } | null,
  ): Promise<ListSubmissionsResponse> {
    return await signingApi.get<ListSubmissionsResponse>(
      `/signing/submissions${buildQuery({
        templateId: formValues?.templateId,
        status: formValues?.status,
        page: paging?.page,
        pageSize: paging?.pageSize,
      })}`,
    );
  }

  async function getSubmission(id: string): Promise<GetSubmissionResponse> {
    return await signingApi.get<GetSubmissionResponse>(`/signing/submissions/${id}`);
  }

  async function createSubmission(data: {
    templateId: string;
    signingMode?: string;
    source?: string;
    submitters: SubmitterInput[];
  }): Promise<CreateSubmissionResponse> {
    return await signingApi.post<CreateSubmissionResponse>('/signing/submissions', data);
  }

  async function sendSubmission(id: string): Promise<SendSubmissionResponse> {
    return await signingApi.post<SendSubmissionResponse>(`/signing/submissions/${id}/send`);
  }

  function $reset() {}

  return {
    $reset,
    listSubmissions,
    getSubmission,
    createSubmission,
    sendSubmission,
  };
});

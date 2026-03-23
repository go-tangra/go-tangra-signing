import { defineStore } from 'pinia';

import { signingApi } from '../api/client';
import type { Paging } from '../types';

export interface Certificate {
  id?: string;
  tenantId?: number;
  subjectCn?: string;
  subjectOrg?: string;
  serialNumber?: string;
  notBefore?: string;
  notAfter?: string;
  isCa?: boolean;
  parentId?: string;
  status?: string;
  certPem?: string;
  revokedAt?: string;
  createTime?: string;
}

interface ListCertificatesResponse {
  certificates?: Certificate[];
  total?: number;
}

interface GetCertificateResponse {
  certificate?: Certificate;
}

interface CreateCertificateResponse {
  certificate?: Certificate;
}

interface RevokeCertificateResponse {
  certificate?: Certificate;
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

export const useSigningCertificateStore = defineStore('signing-certificate', () => {
  async function listCertificates(
    paging?: Paging,
    formValues?: { isCa?: boolean; status?: string } | null,
  ): Promise<ListCertificatesResponse> {
    return await signingApi.get<ListCertificatesResponse>(
      `/signing/certificates${buildQuery({
        isCa: formValues?.isCa,
        status: formValues?.status,
        page: paging?.page,
        pageSize: paging?.pageSize,
      })}`,
    );
  }

  async function getCertificate(id: string): Promise<GetCertificateResponse> {
    return await signingApi.get<GetCertificateResponse>(`/signing/certificates/${id}`);
  }

  async function createCertificate(data: {
    subjectCn: string;
    subjectOrg?: string;
    isCa?: boolean;
    parentId?: string;
    validityYears?: number;
  }): Promise<CreateCertificateResponse> {
    return await signingApi.post<CreateCertificateResponse>('/signing/certificates', data);
  }

  async function revokeCertificate(id: string, reason?: string): Promise<RevokeCertificateResponse> {
    return await signingApi.post<RevokeCertificateResponse>(`/signing/certificates/${id}/revoke`, { reason });
  }

  function $reset() {}

  return {
    $reset,
    listCertificates,
    getCertificate,
    createCertificate,
    revokeCertificate,
  };
});

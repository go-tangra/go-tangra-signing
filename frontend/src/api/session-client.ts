/**
 * API client for signing session endpoints.
 *
 * Used by signers accessing their signing session via token-based URLs.
 * Routes through the same admin gateway as other signing endpoints.
 */

import { signingApi } from './client';

export interface SessionField {
  readonly fieldId: string;
  readonly name: string;
  readonly type: string;
  readonly required: boolean;
  readonly pageNumber: number;
  readonly xPercent: number;
  readonly yPercent: number;
  readonly widthPercent: number;
  readonly heightPercent: number;
  readonly prefilledValue?: string;
  readonly font?: string;
  readonly fontSize?: number;
}

export interface SigningSession {
  readonly submissionName: string;
  readonly templateName: string;
  readonly documentUrl: string;
  readonly signerName: string;
  readonly signerEmail: string;
  readonly fields: readonly SessionField[];
  readonly message?: string;
  readonly status: string;
}

export interface FieldValueSubmission {
  readonly fieldId: string;
  readonly value: string;
}

export interface SubmitSigningResponse {
  readonly completed: boolean;
  readonly message: string;
}

export async function getSigningSession(
  token: string,
): Promise<SigningSession> {
  return signingApi.get<SigningSession>(`/signing/sessions/${token}`);
}

export async function submitSigning(
  token: string,
  data: {
    readonly fieldValues: readonly FieldValueSubmission[];
    readonly signatureImage?: string;
  },
): Promise<SubmitSigningResponse> {
  return signingApi.post<SubmitSigningResponse>(
    `/signing/sessions/${token}/submit`,
    data,
  );
}

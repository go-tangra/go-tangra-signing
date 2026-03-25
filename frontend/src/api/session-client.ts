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
  readonly signingMethod?: string; // LOCAL_CERT, BISS, SIMPLE
  readonly certificateReady?: boolean;
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
    readonly pin?: string;
  },
): Promise<SubmitSigningResponse> {
  return signingApi.post<SubmitSigningResponse>(
    `/signing/sessions/${token}/submit`,
    data,
  );
}

// Certificate setup types and API calls

export interface CertificateSetup {
  readonly signerName: string;
  readonly signerEmail: string;
  readonly status: string; // PENDING_SETUP, COMPLETED, EXPIRED
  readonly message: string;
}

export interface CompleteCertificateSetupResponse {
  readonly completed: boolean;
  readonly certificateCn: string;
  readonly message: string;
}

export async function getCertificateSetup(
  token: string,
): Promise<CertificateSetup> {
  return signingApi.get<CertificateSetup>(
    `/signing/certificate-setup/${token}`,
  );
}

export async function completeCertificateSetup(
  token: string,
  pin: string,
): Promise<CompleteCertificateSetupResponse> {
  return signingApi.post<CompleteCertificateSetupResponse>(
    `/signing/certificate-setup/${token}`,
    { pin },
  );
}

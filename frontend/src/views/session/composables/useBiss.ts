/**
 * Composable for B-Trust BISS (Browser Independent Signing Service) integration.
 *
 * BISS is a locally installed app that enables Qualified Electronic Signature (QES)
 * signing from the browser. It runs as a local HTTPS server on ports 53952-53955.
 *
 * Two-phase PAdES flow:
 * 1. Frontend calls backend PrepareForBissSigning → gets PDF hash + server-signed hash
 * 2. Frontend sends hash to BISS → gets PKCS#7 signature
 * 3. Frontend calls backend CompleteBissSigning → signature embedded into PDF
 */

import { ref, type Ref } from 'vue';

import { signingApi } from '../../../api/client';
import type { FieldValueSubmission } from '../../../api/session-client';

export interface BissStatus {
  readonly available: boolean;
  readonly version?: string;
  readonly port?: number;
}

export interface BissCertificate {
  readonly certificate: string;
  readonly chain: readonly string[];
}

export interface UseBissReturn {
  readonly status: Ref<BissStatus>;
  readonly detecting: Ref<boolean>;
  readonly signing: Ref<boolean>;
  readonly error: Ref<string>;
  readonly certificate: Ref<BissCertificate | null>;
  readonly signedSuccessfully: Ref<boolean>;
  detect(): Promise<BissStatus>;
  selectCertificate(): Promise<BissCertificate | null>;
  signDocument(token: string, fieldValues: readonly FieldValueSubmission[]): Promise<boolean>;
}

const BISS_PORTS = [53952, 53953, 53954, 53955] as const;
const BISS_TIMEOUT = 3000;

async function tryPort(port: number): Promise<{ version: string; port: number } | null> {
  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), BISS_TIMEOUT);
    const resp = await fetch(`https://localhost:${port}/version`, {
      signal: controller.signal,
    });
    clearTimeout(timer);
    if (!resp.ok) return null;
    const data = await resp.json();
    if (data.version) return { version: data.version, port };
    return null;
  } catch {
    return null;
  }
}

// Shared state — all useBiss() instances share the same BISS connection
const _status = ref<BissStatus>({ available: false });
const _detecting = ref(false);
const _signing = ref(false);
const _error = ref('');
const _certificate = ref<BissCertificate | null>(null);
const _signedSuccessfully = ref(false);

export function useBiss(): UseBissReturn {
  const status = _status;
  const detecting = _detecting;
  const signing = _signing;
  const error = _error;
  const certificate = _certificate;
  const signedSuccessfully = _signedSuccessfully;

  async function detect(): Promise<BissStatus> {
    detecting.value = true;
    error.value = '';
    try {
      const results = await Promise.all(BISS_PORTS.map(tryPort));
      const found = results.find((r) => r !== null);
      if (found) {
        status.value = { available: true, version: found.version, port: found.port };
      } else {
        status.value = { available: false };
        error.value = 'BISS is not running. Please start the B-Trust BISS application.';
      }
    } catch (e: any) {
      status.value = { available: false };
      error.value = e?.message ?? 'Failed to detect BISS';
    } finally {
      detecting.value = false;
    }
    return status.value;
  }

  async function selectCertificate(): Promise<BissCertificate | null> {
    if (!status.value.available || !status.value.port) {
      error.value = 'BISS is not available';
      return null;
    }
    error.value = '';
    signing.value = true;
    try {
      const resp = await fetch(`https://localhost:${status.value.port}/getsigner`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          selector: { keyUsages: ['nonRepudiation'] },
          showValidCerts: true,
        }),
      });
      const data = await resp.json();
      if (data.status === 'ok' && data.chain?.length > 0) {
        certificate.value = { certificate: data.chain[0], chain: data.chain };
        return certificate.value;
      }
      error.value = data.reasonText || `Certificate selection failed (${data.reasonCode})`;
      return null;
    } catch (e: any) {
      error.value = e?.message ?? 'Failed to select certificate';
      return null;
    } finally {
      signing.value = false;
    }
  }

  /**
   * Full BISS signing flow:
   * 1. Call backend to prepare PDF and get hash
   * 2. Send hash to BISS for signing
   * 3. Send PKCS#7 result back to backend
   */
  async function signDocument(
    token: string,
    fieldValues: readonly FieldValueSubmission[],
  ): Promise<boolean> {
    if (!status.value.available || !status.value.port || !certificate.value) {
      error.value = 'BISS not available or no certificate selected';
      return false;
    }

    error.value = '';
    signing.value = true;
    signedSuccessfully.value = false;

    try {
      // Phase 1: Prepare PDF on server, get hash
      const prepareResp = await signingApi.post<{
        hashBase64: string;
        signedHashBase64: string;
        serverCertBase64: string;
        sessionId: string;
      }>(`/signing/sessions/${token}/prepare-biss`, { fieldValues });

      // Phase 2: Send hash to BISS for signing
      const bissResp = await fetch(`https://localhost:${status.value.port}/sign`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          version: '1.0',
          contents: [prepareResp.hashBase64],
          signedContents: [prepareResp.signedHashBase64],
          signedContentsCert: [prepareResp.serverCertBase64],
          contentType: 'digest',
          hashAlgorithm: 'SHA256',
          signatureType: 'signature',
          signerCertificateB64: certificate.value.certificate,
          confirmText: ['hash'],
        }),
      });
      const bissData = await bissResp.json();

      if (bissData.status !== 'ok' || !bissData.signatures?.length) {
        error.value = bissData.reasonText || `BISS signing failed (${bissData.reasonCode})`;
        return false;
      }

      // Phase 3: Send signature to server for embedding
      const completeResp = await signingApi.post<{
        completed: boolean;
        message: string;
      }>(`/signing/sessions/${token}/complete-biss`, {
        sessionId: prepareResp.sessionId,
        pkcs7SignatureBase64: bissData.signatures[0],
        certificateChain: certificate.value.chain,
      });

      if (completeResp.completed) {
        signedSuccessfully.value = true;
        return true;
      }

      error.value = completeResp.message || 'Failed to complete signing';
      return false;
    } catch (e: any) {
      error.value = e?.message ?? 'BISS signing failed';
      return false;
    } finally {
      signing.value = false;
    }
  }

  return {
    status,
    detecting,
    signing,
    error,
    certificate,
    signedSuccessfully,
    detect,
    selectCertificate,
    signDocument,
  };
}

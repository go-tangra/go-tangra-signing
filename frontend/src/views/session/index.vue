<script setup lang="ts">
/**
 * Signing Session Page
 *
 * The signer opens `/signing/session/{token}` to view the PDF document
 * and fill in their assigned fields. Layout: PDF viewer on top,
 * sticky signing panel at the bottom with field input and navigation.
 */

import { ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { Modal } from 'ant-design-vue';

import { getSigningSession, submitSigning } from '../../api/session-client';
import type { SigningSession } from '../../api/session-client';
import { useSigningFlow } from './composables/useSigningFlow';
import { useBiss } from './composables/useBiss';
import SessionPdfViewer from './components/SessionPdfViewer.vue';
import SigningPanel from './components/SigningPanel.vue';
import SignatureCanvas from './components/SignatureCanvas.vue';

const route = useRoute();
const router = useRouter();
const token = route.params.token as string;

const session = ref<SigningSession | null>(null);
const pageState = ref<'loading' | 'signing' | 'completed' | 'declined' | 'error'>('loading');
const errorMessage = ref('');
const submitting = ref(false);
const resultMessage = ref('');
const showSignatureModal = ref(false);
const showPinModal = ref(false);
const pinInput = ref('');
const pinError = ref('');
const pdfViewerRef = ref<InstanceType<typeof SessionPdfViewer> | null>(null);

const signingFlow = useSigningFlow();
const biss = useBiss();

const requiresPin = ref(false);

async function handleBissSign(): Promise<void> {
  showSignatureModal.value = false;
  submitting.value = true;
  errorMessage.value = '';

  try {
    // Build field values from current state
    const fieldValues = signingFlow.fields.value.map((f) => ({
      fieldId: f.fieldId,
      value: f.value,
    }));

    const success = await biss.signDocument(token, fieldValues);
    if (success) {
      resultMessage.value = 'Document signed with Qualified Electronic Signature (QES).';
      pageState.value = 'completed';
    } else {
      errorMessage.value = biss.error.value || 'BISS signing failed';
    }
  } catch (err: any) {
    errorMessage.value = err?.message ?? 'BISS signing failed';
  } finally {
    submitting.value = false;
  }
}

async function loadSession(): Promise<void> {
  pageState.value = 'loading';
  errorMessage.value = '';

  try {
    const data = await getSigningSession(token);
    session.value = data;

    if (data.status === 'COMPLETED' || data.status === 'SUBMITTER_STATUS_COMPLETED') {
      pageState.value = 'completed';
      return;
    }

    if (data.status === 'DECLINED' || data.status === 'SUBMITTER_STATUS_DECLINED') {
      pageState.value = 'declined';
      return;
    }

    // Check signing method — if LOCAL_CERT and cert not ready, redirect to setup
    if (data.signingMethod === 'LOCAL_CERT' && !data.certificateReady) {
      errorMessage.value =
        'Your signing certificate is not set up yet. Please check your email for the certificate setup link.';
      pageState.value = 'error';
      return;
    }

    requiresPin.value = data.signingMethod === 'LOCAL_CERT';
    signingFlow.initFields(data.fields);
    pageState.value = 'signing';
  } catch (err: any) {
    errorMessage.value = err?.message ?? 'Failed to load signing session';
    pageState.value = 'error';
  }
}

function handleFieldClick(fieldId: string): void {
  signingFlow.goToField(fieldId);
  const field = signingFlow.fields.value.find((f) => f.fieldId === fieldId);
  if (field && pdfViewerRef.value) {
    pdfViewerRef.value.scrollToPage(field.pageNumber);
  }
}

function handleValueUpdate(value: string): void {
  const field = signingFlow.activeField.value;
  if (field) {
    signingFlow.setFieldValue(field.fieldId, value);
  }
}

function handleFontUpdate(font: string): void {
  const field = signingFlow.activeField.value;
  if (field) {
    signingFlow.setFieldFont(field.fieldId, font);
  }
}

function handleFontSizeUpdate(size: number): void {
  const field = signingFlow.activeField.value;
  if (field) {
    signingFlow.setFieldFontSize(field.fieldId, size);
  }
}

function handleOpenSignature(): void {
  showSignatureModal.value = true;
}

function handleSignatureUpdate(base64: string): void {
  signingFlow.setSignatureImage(base64);
  const field = signingFlow.activeField.value;
  if (field) {
    signingFlow.setFieldValue(field.fieldId, base64 ? 'signed' : '');
  }
}

function handleSignatureModalOk(): void {
  showSignatureModal.value = false;
}

function handleClearAll(): void {
  signingFlow.initFields(session.value?.fields ?? []);
  signingFlow.setSignatureImage('');
}

function handleNext(): void {
  signingFlow.goToNext();
  scrollToActiveField();
}

function handlePrevious(): void {
  signingFlow.goToPrevious();
  scrollToActiveField();
}

function scrollToActiveField(): void {
  const field = signingFlow.activeField.value;
  if (field && pdfViewerRef.value) {
    pdfViewerRef.value.scrollToPage(field.pageNumber);
  }
}

async function handleSubmit(): Promise<void> {
  if (!signingFlow.canSubmit.value) return;

  // If local cert signing, show PIN modal first
  if (requiresPin.value) {
    pinInput.value = '';
    pinError.value = '';
    showPinModal.value = true;
    return;
  }

  await doSubmit();
}

async function handlePinSubmit(): Promise<void> {
  if (pinInput.value.length < 4) {
    pinError.value = 'PIN must be at least 4 characters';
    return;
  }
  pinError.value = '';
  showPinModal.value = false;
  await doSubmit(pinInput.value);
}

async function doSubmit(pin?: string): Promise<void> {
  submitting.value = true;
  errorMessage.value = '';
  try {
    const payload = signingFlow.buildSubmitPayload();
    const result = await submitSigning(token, {
      ...payload,
      ...(pin ? { pin } : {}),
    });
    resultMessage.value = result.message || 'Document signed successfully!';
    pageState.value = 'completed';
  } catch (err: any) {
    const msg = err?.message ?? 'Failed to submit signature';
    // If PIN was wrong, re-show the modal
    if (pin && msg.toLowerCase().includes('pin')) {
      pinError.value = msg;
      showPinModal.value = true;
    } else {
      errorMessage.value = msg;
    }
  } finally {
    submitting.value = false;
  }
}

function getActiveFieldId(): string | undefined {
  return signingFlow.activeField.value?.fieldId;
}

onMounted(() => {
  loadSession();
});
</script>

<template>
  <div class="session-page">
    <!-- Loading -->
    <div v-if="pageState === 'loading'" class="session-page__status">
      <div class="session-page__status-text">Loading signing session...</div>
    </div>

    <!-- Error -->
    <div v-else-if="pageState === 'error'" class="session-page__status session-page__status--error">
      <div class="session-page__status-title">Signing session not found</div>
      <div class="session-page__status-text">{{ errorMessage }}</div>
    </div>

    <!-- Completed -->
    <div v-else-if="pageState === 'completed'" class="session-page__status session-page__status--success">
      <div class="session-page__status-icon">&#10003;</div>
      <div class="session-page__status-title">
        {{ resultMessage || 'You have already signed this document' }}
      </div>
    </div>

    <!-- Declined -->
    <div v-else-if="pageState === 'declined'" class="session-page__status session-page__status--declined">
      <div class="session-page__status-title">This signing request was declined</div>
    </div>

    <!-- Signing flow -->
    <template v-else-if="pageState === 'signing' && session">
      <!-- Header -->
      <div class="session-page__header">
        <h1 class="session-page__title">{{ session.templateName || 'Sign Document' }}</h1>
        <span class="session-page__signer-info">
          Signing as: <strong>{{ session.signerName }}</strong>
        </span>
      </div>

      <!-- Message banner -->
      <div v-if="session.message" class="session-page__message-banner">
        {{ session.message }}
      </div>

      <!-- PDF viewer (takes remaining space above the sticky panel) -->
      <div class="session-page__pdf">
        <SessionPdfViewer
          ref="pdfViewerRef"
          :pdf-url="session.documentUrl"
          :fields="signingFlow.fields.value"
          :active-field-id="getActiveFieldId()"
          :is-field-filled="signingFlow.isFieldFilled"
          @field-click="handleFieldClick"
        />
      </div>

      <!-- Sticky bottom panel -->
      <SigningPanel
        :active-field="signingFlow.activeField.value"
        :active-index="signingFlow.activeFieldIndex.value"
        :total-fields="signingFlow.fields.value.length"
        :progress="signingFlow.progress.value"
        :can-submit="signingFlow.canSubmit.value"
        :is-last-field="signingFlow.isLastField.value"
        :submitting="submitting"
        @next="handleNext"
        @previous="handlePrevious"
        @submit="handleSubmit"
        @clear-all="handleClearAll"
        @update:value="handleValueUpdate"
        @update:font="handleFontUpdate"
        @update:font-size="handleFontSizeUpdate"
        @open-signature="handleOpenSignature"
      />

      <!-- Error toast -->
      <div v-if="errorMessage" class="session-page__error-toast">
        {{ errorMessage }}
      </div>
    </template>

    <!-- Signature modal -->
    <Modal
      v-model:open="showSignatureModal"
      title="Draw your signature"
      width="560px"
      :footer="null"
      @ok="handleSignatureModalOk"
    >
      <SignatureCanvas
        :width="500"
        :height="200"
        @update:signature="handleSignatureUpdate"
        @biss-sign="handleBissSign"
      />
      <div class="session-page__modal-actions">
        <button
          type="button"
          class="session-page__modal-done-btn"
          @click="handleSignatureModalOk"
        >
          Done
        </button>
      </div>
    </Modal>

    <!-- PIN modal for local certificate signing -->
    <Modal
      v-model:open="showPinModal"
      title="Enter your certificate PIN"
      width="400px"
      :footer="null"
      :maskClosable="false"
    >
      <div class="session-page__pin-modal">
        <p class="session-page__pin-description">
          Enter the PIN you set when creating your signing certificate.
        </p>
        <form @submit.prevent="handlePinSubmit">
          <input
            v-model="pinInput"
            type="password"
            class="session-page__pin-input"
            placeholder="Enter PIN"
            autocomplete="current-password"
            autofocus
          />
          <div v-if="pinError" class="session-page__pin-error">{{ pinError }}</div>
          <div class="session-page__pin-actions">
            <button
              type="button"
              class="session-page__pin-cancel"
              @click="showPinModal = false"
            >
              Cancel
            </button>
            <button
              type="submit"
              class="session-page__pin-submit"
              :disabled="pinInput.length < 4 || submitting"
            >
              <template v-if="submitting">Signing...</template>
              <template v-else>Sign Document</template>
            </button>
          </div>
        </form>
      </div>
    </Modal>
  </div>
</template>

<style scoped>
.session-page {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  background: hsl(var(--background-deep));
}

.session-page__header {
  position: sticky;
  top: 0;
  z-index: 40;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 20px;
  background: hsl(var(--header));
  border-bottom: 1px solid hsl(var(--border));
  box-shadow: 0 1px 3px hsl(var(--foreground) / 0.06);
}

.session-page__title {
  font-size: 1.0625rem;
  font-weight: 600;
  color: hsl(var(--foreground));
  margin: 0;
}

.session-page__signer-info {
  font-size: 0.8125rem;
  color: hsl(var(--muted-foreground));
}

.session-page__signer-info strong {
  color: hsl(var(--foreground));
}

.session-page__message-banner {
  margin: 12px 16px 0;
  padding: 10px 14px;
  border-radius: var(--radius);
  background: hsl(var(--primary) / 0.08);
  color: hsl(var(--primary));
  font-size: 0.8125rem;
}

.session-page__pdf {
  flex: 1;
  min-height: 0;
  padding-bottom: 180px; /* Space for the sticky bottom panel */
}

.session-page__status {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100vh;
  gap: 12px;
  padding: 40px;
}

.session-page__status-icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: hsl(var(--primary) / 0.1);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 2rem;
  color: hsl(var(--primary));
}

.session-page__status-title {
  font-size: 1.25rem;
  font-weight: 600;
  color: hsl(var(--foreground));
  text-align: center;
}

.session-page__status-text {
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  text-align: center;
}

.session-page__status--error .session-page__status-title {
  color: hsl(var(--destructive));
}

.session-page__error-toast {
  position: fixed;
  bottom: 200px;
  left: 50%;
  transform: translateX(-50%);
  padding: 10px 20px;
  background: hsl(var(--destructive));
  color: hsl(var(--destructive-foreground, 0 0% 100%));
  border-radius: var(--radius);
  font-size: 0.8125rem;
  box-shadow: 0 4px 12px hsl(var(--foreground) / 0.2);
  z-index: 100;
}

.session-page__modal-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}

.session-page__modal-done-btn {
  padding: 6px 20px;
  font-size: 0.8125rem;
  font-weight: 500;
  border: none;
  border-radius: var(--radius);
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  cursor: pointer;
  transition: opacity 0.15s;
}

.session-page__modal-done-btn:hover {
  opacity: 0.9;
}

.session-page__pin-modal {
  padding: 4px 0;
}

.session-page__pin-description {
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  margin: 0 0 16px;
  line-height: 1.5;
}

.session-page__pin-input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid hsl(var(--input));
  border-radius: var(--radius);
  background: hsl(var(--background));
  font-size: 0.9375rem;
  color: hsl(var(--foreground));
  outline: none;
  letter-spacing: 2px;
}

.session-page__pin-input:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 2px hsl(var(--primary) / 0.1);
}

.session-page__pin-error {
  font-size: 0.8125rem;
  color: hsl(var(--destructive));
  margin-top: 8px;
}

.session-page__pin-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
}

.session-page__pin-cancel {
  padding: 8px 16px;
  font-size: 0.8125rem;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  cursor: pointer;
}

.session-page__pin-cancel:hover {
  background: hsl(var(--muted));
}

.session-page__pin-submit {
  padding: 8px 20px;
  font-size: 0.8125rem;
  font-weight: 600;
  border: none;
  border-radius: var(--radius);
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  cursor: pointer;
}

.session-page__pin-submit:hover:not(:disabled) {
  opacity: 0.9;
}

.session-page__pin-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>

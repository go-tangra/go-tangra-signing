<script setup lang="ts">
/**
 * Signing Session Page
 *
 * The signer opens `/signing/session/{token}` to view the PDF document
 * and fill in their assigned fields. Layout: PDF viewer on top,
 * sticky signing panel at the bottom with field input and navigation.
 */

import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { Modal } from 'ant-design-vue';

import { getSigningSession, submitSigning } from '../../api/session-client';
import type { SigningSession } from '../../api/session-client';
import { useSigningFlow } from './composables/useSigningFlow';
import { useBiss } from './composables/useBiss';
import SessionPdfViewer from './components/SessionPdfViewer.vue';
import SigningPanel from './components/SigningPanel.vue';
import SignatureCanvas from './components/SignatureCanvas.vue';

const route = useRoute();
const token = route.params.token as string;

const session = ref<SigningSession | null>(null);
const pageState = ref<'loading' | 'signing' | 'completed' | 'declined' | 'error'>('loading');
const errorMessage = ref('');
const submitting = ref(false);
const resultMessage = ref('');
const showSignatureModal = ref(false);
const pdfViewerRef = ref<InstanceType<typeof SessionPdfViewer> | null>(null);

const signingFlow = useSigningFlow();
const biss = useBiss();

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

  submitting.value = true;
  try {
    const payload = signingFlow.buildSubmitPayload();
    const result = await submitSigning(token, payload);
    resultMessage.value = result.message || 'Document signed successfully!';
    pageState.value = 'completed';
  } catch (err: any) {
    errorMessage.value = err?.message ?? 'Failed to submit signature';
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
  </div>
</template>

<style scoped>
.session-page {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  background: #f5f5f5;
}

.session-page__header {
  position: sticky;
  top: 0;
  z-index: 40;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 20px;
  background: #fff;
  border-bottom: 1px solid #e5e7eb;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.06);
}

.session-page__title {
  font-size: 1.0625rem;
  font-weight: 600;
  color: #1f2937;
  margin: 0;
}

.session-page__signer-info {
  font-size: 0.8125rem;
  color: #6b7280;
}

.session-page__signer-info strong {
  color: #374151;
}

.session-page__message-banner {
  margin: 12px 16px 0;
  padding: 10px 14px;
  border-radius: 8px;
  background: #eff6ff;
  color: #1d4ed8;
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
  background: rgba(22, 119, 255, 0.1);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 2rem;
  color: #1677ff;
}

.session-page__status-title {
  font-size: 1.25rem;
  font-weight: 600;
  color: #1f2937;
  text-align: center;
}

.session-page__status-text {
  font-size: 0.875rem;
  color: #6b7280;
  text-align: center;
}

.session-page__status--error .session-page__status-title {
  color: #ef4444;
}

.session-page__error-toast {
  position: fixed;
  bottom: 200px;
  left: 50%;
  transform: translateX(-50%);
  padding: 10px 20px;
  background: #ef4444;
  color: #fff;
  border-radius: 8px;
  font-size: 0.8125rem;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
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
  border-radius: 6px;
  background: #1677ff;
  color: #fff;
  cursor: pointer;
  transition: opacity 0.15s;
}

.session-page__modal-done-btn:hover {
  opacity: 0.9;
}
</style>

<script setup lang="ts">
/**
 * Certificate Setup Page (public, token-based)
 *
 * The signer opens `/signing/certificate-setup/{token}` to set a PIN
 * and create their signing certificate. After setup, they receive
 * their signing invitation email.
 */

import { ref, onMounted } from 'vue';
import { useRoute } from 'vue-router';

import {
  getCertificateSetup,
  completeCertificateSetup,
} from '../../api/session-client';
import type { CertificateSetup } from '../../api/session-client';

const route = useRoute();
const token = route.params.token as string;

const setup = ref<CertificateSetup | null>(null);
const pageState = ref<'loading' | 'form' | 'completed' | 'error'>('loading');
const errorMessage = ref('');
const submitting = ref(false);
const successMessage = ref('');

const pin = ref('');
const pinConfirm = ref('');
const showPin = ref(false);
const pinError = ref('');

const PIN_MIN = 4;
const PIN_MAX = 32;

function validatePin(): boolean {
  pinError.value = '';

  if (pin.value.length < PIN_MIN) {
    pinError.value = `PIN must be at least ${PIN_MIN} characters`;
    return false;
  }
  if (pin.value.length > PIN_MAX) {
    pinError.value = `PIN must be at most ${PIN_MAX} characters`;
    return false;
  }
  if (pin.value !== pinConfirm.value) {
    pinError.value = 'PINs do not match';
    return false;
  }
  return true;
}

async function loadSetup(): Promise<void> {
  pageState.value = 'loading';
  errorMessage.value = '';

  try {
    const data = await getCertificateSetup(token);
    setup.value = data;

    if (data.status === 'COMPLETED') {
      pageState.value = 'completed';
      successMessage.value = data.message || 'Your certificate has already been set up.';
      return;
    }

    pageState.value = 'form';
  } catch (err: any) {
    errorMessage.value = err?.message ?? 'Failed to load certificate setup';
    pageState.value = 'error';
  }
}

async function handleSubmit(): Promise<void> {
  if (!validatePin()) return;

  submitting.value = true;
  errorMessage.value = '';

  try {
    const result = await completeCertificateSetup(token, pin.value);
    successMessage.value =
      result.message || `Certificate created for ${result.certificateCn}. You will receive your signing invitation shortly.`;
    pageState.value = 'completed';
  } catch (err: any) {
    errorMessage.value = err?.message ?? 'Failed to create certificate';
  } finally {
    submitting.value = false;
  }
}

onMounted(() => {
  loadSetup();
});
</script>

<template>
  <div class="cert-setup">
    <!-- Loading -->
    <div v-if="pageState === 'loading'" class="cert-setup__card">
      <div class="cert-setup__loading">Loading...</div>
    </div>

    <!-- Error -->
    <div v-else-if="pageState === 'error'" class="cert-setup__card cert-setup__card--error">
      <div class="cert-setup__icon cert-setup__icon--error">!</div>
      <h2 class="cert-setup__title">Setup Not Found</h2>
      <p class="cert-setup__text">{{ errorMessage }}</p>
    </div>

    <!-- Completed -->
    <div v-else-if="pageState === 'completed'" class="cert-setup__card cert-setup__card--success">
      <div class="cert-setup__icon cert-setup__icon--success">&#10003;</div>
      <h2 class="cert-setup__title">Certificate Ready</h2>
      <p class="cert-setup__text">{{ successMessage }}</p>
      <p class="cert-setup__hint">
        Check your email for the signing invitation link.
      </p>
    </div>

    <!-- PIN Form -->
    <div v-else-if="pageState === 'form' && setup" class="cert-setup__card">
      <div class="cert-setup__icon cert-setup__icon--key">&#128274;</div>
      <h2 class="cert-setup__title">Set Up Your Signing Certificate</h2>
      <p class="cert-setup__text">
        Hello <strong>{{ setup.signerName }}</strong>, please create a PIN to protect your
        signing certificate. You will need this PIN each time you sign a document.
      </p>

      <form class="cert-setup__form" @submit.prevent="handleSubmit">
        <div class="cert-setup__field">
          <label class="cert-setup__label" for="pin">PIN</label>
          <div class="cert-setup__input-wrapper">
            <input
              id="pin"
              v-model="pin"
              :type="showPin ? 'text' : 'password'"
              class="cert-setup__input"
              :placeholder="`Enter PIN (${PIN_MIN}-${PIN_MAX} characters)`"
              :minlength="PIN_MIN"
              :maxlength="PIN_MAX"
              autocomplete="new-password"
            />
            <button
              type="button"
              class="cert-setup__toggle-pin"
              @click="showPin = !showPin"
            >
              {{ showPin ? 'Hide' : 'Show' }}
            </button>
          </div>
        </div>

        <div class="cert-setup__field">
          <label class="cert-setup__label" for="pin-confirm">Confirm PIN</label>
          <input
            id="pin-confirm"
            v-model="pinConfirm"
            :type="showPin ? 'text' : 'password'"
            class="cert-setup__input"
            :placeholder="'Re-enter your PIN'"
            :minlength="PIN_MIN"
            :maxlength="PIN_MAX"
            autocomplete="new-password"
          />
        </div>

        <div v-if="pinError" class="cert-setup__error">{{ pinError }}</div>
        <div v-if="errorMessage" class="cert-setup__error">{{ errorMessage }}</div>

        <button
          type="submit"
          class="cert-setup__submit"
          :disabled="submitting || pin.length < PIN_MIN || pinConfirm.length < PIN_MIN"
        >
          <template v-if="submitting">Creating Certificate...</template>
          <template v-else>Create Certificate</template>
        </button>
      </form>

      <p class="cert-setup__security-note">
        Your PIN is never stored on the server. It encrypts your private signing key.
        If you forget your PIN, a new certificate will need to be created.
      </p>
    </div>
  </div>
</template>

<style scoped>
.cert-setup {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background: hsl(var(--background-deep));
  padding: 20px;
}

.cert-setup__card {
  width: 100%;
  max-width: 460px;
  background: hsl(var(--card));
  color: hsl(var(--card-foreground));
  border-radius: var(--radius);
  border: 1px solid hsl(var(--border));
  box-shadow: 0 4px 24px hsl(var(--foreground) / 0.06);
  padding: 40px 32px;
  text-align: center;
}

.cert-setup__loading {
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
}

.cert-setup__icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 1.75rem;
  margin: 0 auto 16px;
}

.cert-setup__icon--key {
  background: hsl(var(--primary) / 0.1);
  color: hsl(var(--primary));
}

.cert-setup__icon--success {
  background: hsl(143 72% 42% / 0.1);
  color: hsl(143 72% 42%);
}

.cert-setup__icon--error {
  background: hsl(var(--destructive) / 0.1);
  color: hsl(var(--destructive));
  font-weight: 700;
}

.cert-setup__title {
  font-size: 1.25rem;
  font-weight: 600;
  color: hsl(var(--foreground));
  margin: 0 0 8px;
}

.cert-setup__text {
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  line-height: 1.5;
  margin: 0 0 24px;
}

.cert-setup__hint {
  font-size: 0.8125rem;
  color: hsl(var(--muted-foreground));
  margin-top: 8px;
}

.cert-setup__form {
  text-align: left;
}

.cert-setup__field {
  margin-bottom: 16px;
}

.cert-setup__label {
  display: block;
  font-size: 0.8125rem;
  font-weight: 600;
  color: hsl(var(--foreground));
  margin-bottom: 6px;
}

.cert-setup__input-wrapper {
  position: relative;
  display: flex;
  align-items: center;
}

.cert-setup__input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid hsl(var(--input));
  border-radius: var(--radius);
  background: hsl(var(--background));
  font-size: 0.875rem;
  color: hsl(var(--foreground));
  outline: none;
  transition: border-color 0.15s;
}

.cert-setup__input:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 2px hsl(var(--primary) / 0.1);
}

.cert-setup__toggle-pin {
  position: absolute;
  right: 8px;
  padding: 4px 10px;
  border: none;
  background: transparent;
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
  cursor: pointer;
}

.cert-setup__toggle-pin:hover {
  color: hsl(var(--primary));
}

.cert-setup__error {
  font-size: 0.8125rem;
  color: hsl(var(--destructive));
  margin-bottom: 12px;
}

.cert-setup__submit {
  width: 100%;
  padding: 12px;
  border: none;
  border-radius: var(--radius);
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  font-size: 0.875rem;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
}

.cert-setup__submit:hover:not(:disabled) {
  opacity: 0.9;
}

.cert-setup__submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.cert-setup__security-note {
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
  margin-top: 20px;
  line-height: 1.5;
  text-align: center;
}
</style>

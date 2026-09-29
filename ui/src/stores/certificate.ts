import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, describe } from '@/api/client'
import type { Certificate, MyCertificate, SignedEntry } from '@/api/types'

/** The caller's personal signing certificate (PIN protected) and what they signed with it. */
export const useMyCertificate = defineStore('signing-my-certificate', () => {
  const certificate = ref<Certificate | null>(null)
  const signed = ref<SignedEntry[]>([])
  const loaded = ref(false)
  const loading = ref(false)
  const error = ref('')

  function apply(m: MyCertificate | undefined): void {
    certificate.value = m?.certificate ?? null
    signed.value = m?.signed ?? []
    loaded.value = true
  }

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      apply(await api<MyCertificate>('GET', 'me/certificate'))
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  /** Issues the certificate protected by the PIN (the server enforces the PIN rules). */
  async function setup(pin: string): Promise<void> {
    apply(await api<MyCertificate>('POST', 'me/certificate', { pin }))
    await load()
  }

  const changePin = (oldPin: string, newPin: string) => api('POST', 'me/certificate/pin', { old_pin: oldPin, new_pin: newPin })

  async function renew(pin: string): Promise<void> {
    apply(await api<MyCertificate>('POST', 'me/certificate/renew', { pin }))
    await load()
  }

  async function revoke(): Promise<void> {
    await api('POST', 'me/certificate/revoke')
    await load()
  }

  return { certificate, signed, loaded, loading, error, load, setup, changePin, renew, revoke }
})

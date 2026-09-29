// Verification of a signed PDF (signing:read): an uploaded file or a stored
// submission version → every signature's integrity, trust and revocation.
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { describe, postForm } from '@/api/client'
import type { DocumentSource, VerifyResult } from '@/api/types'
import { appendSource } from '@/utils/documents'

export const useVerify = defineStore('signing-verify', () => {
  const result = ref<VerifyResult | null>(null)
  const loading = ref(false)
  const error = ref('')

  async function verify(src: DocumentSource): Promise<void> {
    loading.value = true
    error.value = ''
    result.value = null
    try {
      const form = new FormData()
      appendSource(form, src)
      result.value = await postForm<VerifyResult>('verify', form)
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }

  function reset(): void {
    result.value = null
    error.value = ''
  }

  return { result, loading, error, verify, reset }
})

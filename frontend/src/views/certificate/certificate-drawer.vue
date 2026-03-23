<script lang="ts" setup>
import { ref, computed } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { $t } from 'shell/locales';
import { notification, Form, Input, InputNumber, Switch, Button } from 'ant-design-vue';

import { useSigningCertificateStore } from '../../stores/signing-certificate.state';
import type { Certificate } from '../../stores/signing-certificate.state';

const certStore = useSigningCertificateStore();

const [Drawer, drawerApi] = useVbenDrawer({
  onOpenChange(isOpen: boolean) {
    if (isOpen) {
      const data = drawerApi.getData<{ row: Certificate; mode: string }>();
      if (data) {
        mode.value = data.mode as 'create' | 'view';
        if (data.mode === 'create') {
          formState.value = {
            subjectCn: '',
            subjectOrg: '',
            isCa: false,
            validityYears: 3,
          };
        } else if (data.row) {
          formState.value = { ...data.row };
        }
      }
    }
  },
});

const mode = ref<'create' | 'view'>('view');
const formState = ref<Record<string, any>>({});
const loading = ref(false);

const isReadonly = computed(() => mode.value === 'view');
const title = computed(() =>
  mode.value === 'create' ? $t('signing.page.certificate.create') : $t('ui.button.view'),
);

async function handleSave() {
  loading.value = true;
  try {
    await certStore.createCertificate({
      subjectCn: formState.value.subjectCn,
      subjectOrg: formState.value.subjectOrg,
      isCa: formState.value.isCa,
      parentId: formState.value.parentId,
      validityYears: formState.value.validityYears,
    });
    notification.success({ message: 'Certificate created' });
    drawerApi.close();
  } catch (e: any) {
    notification.error({ message: e?.message ?? 'Operation failed' });
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <Drawer :title="title">
    <Form layout="vertical">
      <Form.Item :label="$t('signing.page.certificate.subjectCn')">
        <Input v-model:value="formState.subjectCn" :disabled="isReadonly" placeholder="e.g. My Signing Certificate" />
      </Form.Item>
      <Form.Item :label="$t('signing.page.certificate.subjectOrg')">
        <Input v-model:value="formState.subjectOrg" :disabled="isReadonly" placeholder="e.g. Acme Corp" />
      </Form.Item>
      <Form.Item v-if="mode === 'create'" :label="$t('signing.page.certificate.isCa')">
        <Switch v-model:checked="formState.isCa" />
      </Form.Item>
      <Form.Item v-if="mode === 'create'" :label="$t('signing.page.certificate.validityYears')">
        <InputNumber v-model:value="formState.validityYears" :min="1" :max="30" />
      </Form.Item>
      <Form.Item v-if="mode === 'create' && !formState.isCa" :label="$t('signing.page.certificate.parentCa')">
        <Input v-model:value="formState.parentId" placeholder="CA Certificate UUID (optional)" />
      </Form.Item>

      <template v-if="mode === 'view'">
        <Form.Item :label="$t('signing.page.certificate.serialNumber')">
          <Input :value="formState.serialNumber" disabled />
        </Form.Item>
        <Form.Item :label="$t('signing.page.certificate.status')">
          <Input :value="formState.status" disabled />
        </Form.Item>
        <Form.Item :label="$t('signing.page.certificate.notBefore')">
          <Input :value="formState.notBefore" disabled />
        </Form.Item>
        <Form.Item :label="$t('signing.page.certificate.notAfter')">
          <Input :value="formState.notAfter" disabled />
        </Form.Item>
      </template>
    </Form>
    <template #footer>
      <Button v-if="!isReadonly" type="primary" :loading="loading" @click="handleSave">
        {{ $t('ui.button.save') }}
      </Button>
    </template>
  </Drawer>
</template>

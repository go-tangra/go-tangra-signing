<script lang="ts" setup>
import { ref, computed } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { $t } from 'shell/locales';
import { notification, Form, Input, Select, Button, Upload } from 'ant-design-vue';
import { LucideUpload } from 'shell/vben/icons';

import { useSigningTemplateStore } from '../../stores/signing-template.state';
import type { SigningTemplate } from '../../stores/signing-template.state';

const templateStore = useSigningTemplateStore();

const [Drawer, drawerApi] = useVbenDrawer({
  onOpenChange(isOpen: boolean) {
    if (isOpen) {
      const data = drawerApi.getData<{ row: SigningTemplate; mode: string }>();
      if (data) {
        mode.value = data.mode as 'create' | 'edit' | 'view';
        if (data.mode !== 'create' && data.row) {
          formState.value = { ...data.row };
        } else {
          formState.value = { name: '', description: '', status: 'TEMPLATE_STATUS_DRAFT' };
        }
        selectedFile.value = null;
      }
    }
  },
});

const mode = ref<'create' | 'edit' | 'view'>('view');
const formState = ref<Partial<SigningTemplate>>({});
const loading = ref(false);
const selectedFile = ref<File | null>(null);

const isReadonly = computed(() => mode.value === 'view');
const title = computed(() => {
  switch (mode.value) {
    case 'create': return $t('signing.page.template.create');
    case 'edit': return $t('signing.page.template.edit');
    default: return $t('ui.button.view');
  }
});

function handleBeforeUpload(file: File): boolean {
  if (file.type !== 'application/pdf') {
    notification.error({ message: 'Only PDF files are allowed' });
    return false;
  }
  if (file.size > 50 * 1024 * 1024) {
    notification.error({ message: 'File size must be less than 50MB' });
    return false;
  }
  selectedFile.value = file;
  // Return false to prevent auto-upload; we handle it manually
  return false;
}

function handleRemoveFile() {
  selectedFile.value = null;
}

function formatFileSize(bytes?: number): string {
  if (!bytes) return '-';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

async function handleSave() {
  if (mode.value === 'create' && !selectedFile.value) {
    notification.warning({ message: 'Please upload a PDF file' });
    return;
  }
  if (!formState.value.name?.trim()) {
    notification.warning({ message: 'Template name is required' });
    return;
  }

  loading.value = true;
  try {
    if (mode.value === 'create' && selectedFile.value) {
      await templateStore.createTemplate(
        {
          name: formState.value.name ?? '',
          description: formState.value.description,
        },
        selectedFile.value,
      );
      notification.success({ message: 'Template created' });
    } else if (mode.value === 'edit' && formState.value.id) {
      await templateStore.updateTemplate(formState.value.id, {
        name: formState.value.name,
        description: formState.value.description,
        status: formState.value.status,
      });
      notification.success({ message: 'Template updated' });
    }
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
      <Form.Item :label="$t('signing.page.template.name')" required>
        <Input v-model:value="formState.name" :disabled="isReadonly" />
      </Form.Item>
      <Form.Item :label="$t('signing.page.template.description')">
        <Input.TextArea v-model:value="formState.description" :disabled="isReadonly" :rows="3" />
      </Form.Item>

      <!-- PDF Upload (create mode only) -->
      <Form.Item v-if="mode === 'create'" label="PDF Document" required>
        <Upload.Dragger
          :before-upload="handleBeforeUpload"
          :file-list="selectedFile ? [{ uid: '-1', name: selectedFile.name, status: 'done' }] : []"
          :max-count="1"
          accept=".pdf"
          @remove="handleRemoveFile"
        >
          <p class="ant-upload-drag-icon">
            <component :is="LucideUpload" class="mx-auto size-8 text-gray-400" />
          </p>
          <p class="ant-upload-text">Click or drag a PDF file to upload</p>
          <p class="ant-upload-hint">Only PDF files up to 50MB are supported</p>
        </Upload.Dragger>
      </Form.Item>

      <!-- File info (edit/view mode) -->
      <Form.Item v-if="mode !== 'create' && formState.fileName" label="PDF Document">
        <div class="flex items-center gap-2 rounded border border-gray-200 bg-gray-50 px-3 py-2">
          <span class="font-medium">{{ formState.fileName }}</span>
          <span class="text-gray-400">({{ formatFileSize(formState.fileSize) }})</span>
        </div>
      </Form.Item>

      <Form.Item v-if="mode === 'edit'" :label="$t('signing.page.template.status')">
        <Select v-model:value="formState.status" :disabled="isReadonly">
          <Select.Option value="TEMPLATE_STATUS_DRAFT">{{ $t('signing.page.template.statusDraft') }}</Select.Option>
          <Select.Option value="TEMPLATE_STATUS_ACTIVE">{{ $t('signing.page.template.statusActive') }}</Select.Option>
          <Select.Option value="TEMPLATE_STATUS_ARCHIVED">{{ $t('signing.page.template.statusArchived') }}</Select.Option>
        </Select>
      </Form.Item>
    </Form>
    <template #footer>
      <Button v-if="!isReadonly" type="primary" :loading="loading" @click="handleSave">
        {{ $t('ui.button.save') }}
      </Button>
    </template>
  </Drawer>
</template>

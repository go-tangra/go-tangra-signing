<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, ref } from 'vue';

import { Page, useVbenDrawer } from 'shell/vben/common-ui';
import {
  LucideDownload,
  LucideEye,
  LucideFileText,
  LucideSend,
} from 'shell/vben/icons';

import {
  Modal,
  notification,
  Space,
  Button,
  Tag,
} from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { $t } from 'shell/locales';
import { useSigningSubmissionStore } from '../../stores/signing-submission.state';
import type { Submission } from '../../stores/signing-submission.state';

import SubmissionDrawer from './submission-drawer.vue';

const submissionStore = useSigningSubmissionStore();

const statusColorMap: Record<string, string> = {
  SUBMISSION_STATUS_DRAFT: 'default',
  SUBMISSION_STATUS_PENDING: 'blue',
  SUBMISSION_STATUS_IN_PROGRESS: 'processing',
  SUBMISSION_STATUS_COMPLETED: 'green',
  SUBMISSION_STATUS_EXPIRED: 'orange',
  SUBMISSION_STATUS_CANCELLED: 'red',
};

const formOptions = {
  collapsed: false,
  showCollapseButton: false,
  submitOnEnter: true,
  schema: [
    {
      component: 'Select',
      fieldName: 'status',
      label: $t('signing.page.submission.status'),
      componentProps: {
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
        options: [
          { label: $t('signing.page.submission.statusDraft'), value: 'SUBMISSION_STATUS_DRAFT' },
          { label: $t('signing.page.submission.statusInProgress'), value: 'SUBMISSION_STATUS_IN_PROGRESS' },
          { label: $t('signing.page.submission.statusCompleted'), value: 'SUBMISSION_STATUS_COMPLETED' },
        ],
      },
    },
  ],
};

const gridOptions: VxeGridProps<Submission> = {
  height: 'auto',
  stripe: false,
  toolbarConfig: {
    custom: true,
    export: true,
    refresh: true,
    zoom: true,
  },
  exportConfig: {},
  rowConfig: { isHover: true },
  pagerConfig: {
    enabled: true,
    pageSize: 20,
    pageSizes: [10, 20, 50, 100],
  },
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        const resp = await submissionStore.listSubmissions(
          { page: page.currentPage, pageSize: page.pageSize },
          formValues,
        );
        return {
          items: resp.submissions ?? [],
          total: resp.total ?? 0,
        };
      },
    },
  },
  columns: [
    { title: $t('ui.table.seq'), type: 'seq', width: 50 },
    {
      title: $t('signing.page.submission.slug'),
      field: 'slug',
      width: 120,
    },
    {
      title: $t('signing.page.submission.templateId'),
      field: 'templateId',
      width: 150,
    },
    {
      title: $t('signing.page.submission.signingMode'),
      field: 'signingMode',
      width: 130,
      slots: { default: 'signingMode' },
    },
    {
      title: $t('signing.page.submission.status'),
      field: 'status',
      width: 140,
      slots: { default: 'status' },
    },
    {
      title: $t('ui.table.createdAt'),
      field: 'createTime',
      formatter: 'formatDateTime',
      width: 160,
    },
    {
      title: $t('ui.table.action'),
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      width: 260,
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [SubmissionDrawerComponent, submissionDrawerApi] = useVbenDrawer({
  connectedComponent: SubmissionDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function handleCreate() {
  submissionDrawerApi.setData({ row: {}, mode: 'create' });
  submissionDrawerApi.open();
}

function handleView(row: Submission) {
  submissionDrawerApi.setData({ row, mode: 'view' });
  submissionDrawerApi.open();
}

async function handleSend(row: Submission) {
  if (!row.id) return;
  try {
    await submissionStore.sendSubmission(row.id);
    notification.success({ message: $t('signing.page.submission.sendSuccess') });
    await gridApi.query();
  } catch (e: any) {
    notification.error({ message: e?.message ?? 'Send failed' });
  }
}

function getStatusLabel(status?: string): string {
  switch (status) {
    case 'SUBMISSION_STATUS_DRAFT': return $t('signing.page.submission.statusDraft');
    case 'SUBMISSION_STATUS_PENDING': return $t('signing.page.submission.statusPending');
    case 'SUBMISSION_STATUS_IN_PROGRESS': return $t('signing.page.submission.statusInProgress');
    case 'SUBMISSION_STATUS_COMPLETED': return $t('signing.page.submission.statusCompleted');
    case 'SUBMISSION_STATUS_EXPIRED': return $t('signing.page.submission.statusExpired');
    case 'SUBMISSION_STATUS_CANCELLED': return $t('signing.page.submission.statusCancelled');
    default: return '-';
  }
}

const previewVisible = ref(false);
const previewUrl = ref('');

function getDocumentPdfKey(row: Submission): string | undefined {
  return row.signedDocumentKey || row.currentPdfKey || undefined;
}

function getDocumentUrl(key: string): string {
  return `/modules/signing/api/v1/signing/templates/pdf?key=${encodeURIComponent(key)}`;
}

function handleDownload(row: Submission) {
  const key = getDocumentPdfKey(row);
  if (!key) return;
  const url = getDocumentUrl(key);
  const link = document.createElement('a');
  link.href = url;
  link.download = `signed-${row.slug || row.id}.pdf`;
  link.click();
}

function handlePreview(row: Submission) {
  const key = getDocumentPdfKey(row);
  if (!key) return;
  previewUrl.value = getDocumentUrl(key);
  previewVisible.value = true;
}

function getModeLabel(mode?: string): string {
  switch (mode) {
    case 'SIGNING_MODE_SEQUENTIAL': return $t('signing.page.submission.modeSequential');
    case 'SIGNING_MODE_PARALLEL': return $t('signing.page.submission.modeParallel');
    default: return '-';
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('signing.page.submission.title')">
      <template #toolbar-tools>
        <Space>
          <Button type="primary" @click="handleCreate">
            {{ $t('signing.page.submission.create') }}
          </Button>
        </Space>
      </template>
      <template #signingMode="{ row }">
        <Tag color="blue">{{ getModeLabel(row.signingMode) }}</Tag>
      </template>
      <template #status="{ row }">
        <Tag :color="statusColorMap[row.status] ?? 'default'">
          {{ getStatusLabel(row.status) }}
        </Tag>
      </template>
      <template #action="{ row }">
        <Space>
          <Button
            type="link"
            size="small"
            :icon="h(LucideEye)"
            :title="$t('ui.button.view')"
            @click.stop="handleView(row)"
          />
          <Button
            v-if="row.signedDocumentKey || row.currentPdfKey"
            type="link"
            size="small"
            :icon="h(LucideFileText)"
            title="Preview Document"
            @click.stop="handlePreview(row)"
          />
          <Button
            v-if="row.signedDocumentKey || row.currentPdfKey"
            type="link"
            size="small"
            :icon="h(LucideDownload)"
            title="Download Document"
            @click.stop="handleDownload(row)"
          />
          <a-popconfirm
            v-if="row.status === 'SUBMISSION_STATUS_DRAFT'"
            :title="$t('signing.page.submission.confirmSend')"
            :ok-text="$t('ui.button.ok')"
            :cancel-text="$t('ui.button.cancel')"
            @confirm="handleSend(row)"
          >
            <Button
              type="primary"
              size="small"
              :icon="h(LucideSend)"
            >
              {{ $t('signing.page.submission.send') }}
            </Button>
          </a-popconfirm>
        </Space>
      </template>
    </Grid>

    <!-- Document Preview Modal -->
    <Modal
      v-model:open="previewVisible"
      title="Document Preview"
      width="900px"
      :footer="null"
      destroy-on-close
    >
      <iframe
        v-if="previewUrl"
        :src="previewUrl"
        style="width: 100%; height: 75vh; border: none; border-radius: 4px;"
      />
    </Modal>

    <SubmissionDrawerComponent />
  </Page>
</template>

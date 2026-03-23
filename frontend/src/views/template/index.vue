<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h } from 'vue';
import { useRouter } from 'vue-router';

import { Page, useVbenDrawer } from 'shell/vben/common-ui';
import {
  LucideEye,
  LucideTrash,
  LucidePencil,
  LucideFileSignature,
  LucideLayoutGrid,
} from 'shell/vben/icons';

import {
  notification,
  Space,
  Button,
  Tag,
} from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { $t } from 'shell/locales';
import { useSigningTemplateStore } from '../../stores/signing-template.state';
import type { SigningTemplate } from '../../stores/signing-template.state';

import TemplateDrawer from './template-drawer.vue';

const router = useRouter();
const templateStore = useSigningTemplateStore();

const statusColorMap: Record<string, string> = {
  TEMPLATE_STATUS_DRAFT: 'default',
  TEMPLATE_STATUS_ACTIVE: 'green',
  TEMPLATE_STATUS_ARCHIVED: 'orange',
};

const formOptions = {
  collapsed: false,
  showCollapseButton: false,
  submitOnEnter: true,
  schema: [
    {
      component: 'Input',
      fieldName: 'nameFilter',
      label: $t('signing.page.template.name'),
      componentProps: {
        placeholder: $t('ui.placeholder.input'),
        allowClear: true,
      },
    },
  ],
};

function formatFileSize(bytes?: number): string {
  if (!bytes) return '-';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const gridOptions: VxeGridProps<SigningTemplate> = {
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
        const resp = await templateStore.listTemplates(
          { page: page.currentPage, pageSize: page.pageSize },
          formValues,
        );
        return {
          items: resp.templates ?? [],
          total: resp.total ?? 0,
        };
      },
    },
  },
  columns: [
    { title: $t('ui.table.seq'), type: 'seq', width: 50 },
    {
      title: $t('signing.page.template.name'),
      field: 'name',
      minWidth: 200,
      slots: { default: 'name' },
    },
    {
      title: 'File',
      field: 'fileName',
      minWidth: 160,
      slots: { default: 'file_info' },
    },
    {
      title: $t('signing.page.template.status'),
      field: 'status',
      width: 120,
      slots: { default: 'status' },
    },
    {
      title: $t('signing.page.template.source'),
      field: 'source',
      width: 120,
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
      width: 200,
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [TemplateDrawerComponent, templateDrawerApi] = useVbenDrawer({
  connectedComponent: TemplateDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function openDrawer(row: SigningTemplate, mode: 'create' | 'edit' | 'view') {
  templateDrawerApi.setData({ row, mode });
  templateDrawerApi.open();
}

function handleCreate() {
  openDrawer({} as SigningTemplate, 'create');
}

function handleView(row: SigningTemplate) {
  openDrawer(row, 'view');
}

function handleEdit(row: SigningTemplate) {
  openDrawer(row, 'edit');
}

function handleEditFields(row: SigningTemplate) {
  if (!row.id) return;
  router.push({ name: 'SigningTemplateBuilder', params: { id: row.id } });
}

async function handleDelete(row: SigningTemplate) {
  if (!row.id) return;
  try {
    await templateStore.deleteTemplate(row.id);
    notification.success({ message: $t('signing.page.template.deleteSuccess') });
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}

function getStatusLabel(status?: string): string {
  switch (status) {
    case 'TEMPLATE_STATUS_DRAFT': return $t('signing.page.template.statusDraft');
    case 'TEMPLATE_STATUS_ACTIVE': return $t('signing.page.template.statusActive');
    case 'TEMPLATE_STATUS_ARCHIVED': return $t('signing.page.template.statusArchived');
    default: return '-';
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('signing.page.template.title')">
      <template #toolbar-tools>
        <Space>
          <Button type="primary" @click="handleCreate">
            {{ $t('signing.page.template.create') }}
          </Button>
        </Space>
      </template>
      <template #name="{ row }">
        <div class="flex items-center gap-2">
          <component :is="LucideFileSignature" class="size-4" />
          <span>{{ row.name }}</span>
        </div>
      </template>
      <template #file_info="{ row }">
        <div v-if="row.fileName" class="text-sm">
          <div>{{ row.fileName }}</div>
          <div class="text-gray-400">{{ formatFileSize(row.fileSize) }}</div>
        </div>
        <span v-else class="text-gray-400">-</span>
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
            type="link"
            size="small"
            :icon="h(LucidePencil)"
            :title="$t('ui.button.edit')"
            @click.stop="handleEdit(row)"
          />
          <Button
            type="link"
            size="small"
            :icon="h(LucideLayoutGrid)"
            title="Edit Fields"
            @click.stop="handleEditFields(row)"
          />
          <a-popconfirm
            :cancel-text="$t('ui.button.cancel')"
            :ok-text="$t('ui.button.ok')"
            :title="$t('signing.page.template.confirmDelete')"
            @confirm="handleDelete(row)"
          >
            <Button
              danger
              type="link"
              size="small"
              :icon="h(LucideTrash)"
              :title="$t('ui.button.delete', { moduleName: '' })"
            />
          </a-popconfirm>
        </Space>
      </template>
    </Grid>

    <TemplateDrawerComponent />
  </Page>
</template>

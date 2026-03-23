<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, ref } from 'vue';

import { Page, useVbenDrawer } from 'shell/vben/common-ui';
import {
  LucideEye,
  LucideShieldCheck,
  LucideXCircle,
} from 'shell/vben/icons';

import {
  notification,
  Space,
  Button,
  Tag,
} from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { $t } from 'shell/locales';
import { useSigningCertificateStore } from '../../stores/signing-certificate.state';
import type { Certificate } from '../../stores/signing-certificate.state';

import CertificateDrawer from './certificate-drawer.vue';

const certStore = useSigningCertificateStore();

const statusColorMap: Record<string, string> = {
  CERT_STATUS_ACTIVE: 'green',
  CERT_STATUS_REVOKED: 'red',
  CERT_STATUS_EXPIRED: 'orange',
};

const formOptions = {
  collapsed: false,
  showCollapseButton: false,
  submitOnEnter: true,
  schema: [
    {
      component: 'Select',
      fieldName: 'status',
      label: $t('signing.page.certificate.status'),
      componentProps: {
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
        options: [
          { label: $t('signing.page.certificate.statusActive'), value: 'CERT_STATUS_ACTIVE' },
          { label: $t('signing.page.certificate.statusRevoked'), value: 'CERT_STATUS_REVOKED' },
          { label: $t('signing.page.certificate.statusExpired'), value: 'CERT_STATUS_EXPIRED' },
        ],
      },
    },
  ],
};

const gridOptions: VxeGridProps<Certificate> = {
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
        const resp = await certStore.listCertificates(
          { page: page.currentPage, pageSize: page.pageSize },
          formValues,
        );
        return {
          items: resp.certificates ?? [],
          total: resp.total ?? 0,
        };
      },
    },
  },
  columns: [
    { title: $t('ui.table.seq'), type: 'seq', width: 50 },
    {
      title: $t('signing.page.certificate.subjectCn'),
      field: 'subjectCn',
      minWidth: 200,
      slots: { default: 'subjectCn' },
    },
    {
      title: $t('signing.page.certificate.subjectOrg'),
      field: 'subjectOrg',
      width: 160,
    },
    {
      title: $t('signing.page.certificate.isCa'),
      field: 'isCa',
      width: 80,
      slots: { default: 'isCa' },
    },
    {
      title: $t('signing.page.certificate.status'),
      field: 'status',
      width: 120,
      slots: { default: 'status' },
    },
    {
      title: $t('signing.page.certificate.notAfter'),
      field: 'notAfter',
      formatter: 'formatDateTime',
      width: 160,
    },
    {
      title: $t('ui.table.action'),
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      width: 140,
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [CertDrawerComponent, certDrawerApi] = useVbenDrawer({
  connectedComponent: CertificateDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function handleCreate() {
  certDrawerApi.setData({ row: {}, mode: 'create' });
  certDrawerApi.open();
}

function handleView(row: Certificate) {
  certDrawerApi.setData({ row, mode: 'view' });
  certDrawerApi.open();
}

async function handleRevoke(row: Certificate) {
  if (!row.id) return;
  try {
    await certStore.revokeCertificate(row.id);
    notification.success({ message: $t('signing.page.certificate.revokeSuccess') });
    await gridApi.query();
  } catch (e: any) {
    notification.error({ message: e?.message ?? 'Revoke failed' });
  }
}

function getStatusLabel(status?: string): string {
  switch (status) {
    case 'CERT_STATUS_ACTIVE': return $t('signing.page.certificate.statusActive');
    case 'CERT_STATUS_REVOKED': return $t('signing.page.certificate.statusRevoked');
    case 'CERT_STATUS_EXPIRED': return $t('signing.page.certificate.statusExpired');
    default: return '-';
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('signing.page.certificate.title')">
      <template #toolbar-tools>
        <Space>
          <Button type="primary" @click="handleCreate">
            {{ $t('signing.page.certificate.create') }}
          </Button>
        </Space>
      </template>
      <template #subjectCn="{ row }">
        <div class="flex items-center gap-2">
          <component :is="LucideShieldCheck" class="size-4" />
          <span>{{ row.subjectCn }}</span>
        </div>
      </template>
      <template #isCa="{ row }">
        <Tag :color="row.isCa ? 'purple' : 'default'">
          {{ row.isCa ? 'CA' : 'End-Entity' }}
        </Tag>
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
          <a-popconfirm
            v-if="row.status === 'CERT_STATUS_ACTIVE'"
            :cancel-text="$t('ui.button.cancel')"
            :ok-text="$t('ui.button.ok')"
            :title="$t('signing.page.certificate.confirmRevoke')"
            @confirm="handleRevoke(row)"
          >
            <Button
              danger
              type="link"
              size="small"
              :icon="h(LucideXCircle)"
              :title="$t('signing.page.certificate.revoke')"
            />
          </a-popconfirm>
        </Space>
      </template>
    </Grid>

    <CertDrawerComponent />
  </Page>
</template>

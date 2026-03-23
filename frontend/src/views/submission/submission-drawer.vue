<script lang="ts" setup>
import { ref, computed } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { $t } from 'shell/locales';
import { notification, Form, Input, Select, Button, Divider, Tag, message } from 'ant-design-vue';
import { LucidePlus, LucideTrash } from 'shell/vben/icons';

import { signingApi } from '../../api/client';
import { useSigningSubmissionStore } from '../../stores/signing-submission.state';
import { useSigningTemplateStore } from '../../stores/signing-template.state';
import type { Submission, SubmitterInput } from '../../stores/signing-submission.state';

interface SubmitterDetail {
  readonly name: string;
  readonly email: string;
  readonly role: string;
  readonly status: string;
  readonly slug: string;
  readonly signedAt?: string;
}

const submitterStatusColors: Record<string, string> = {
  SUBMITTER_STATUS_PENDING: 'default',
  SUBMITTER_STATUS_OPENED: 'blue',
  SUBMITTER_STATUS_COMPLETED: 'green',
  SUBMITTER_STATUS_DECLINED: 'red',
};

function getSubmitterStatusLabel(status?: string): string {
  switch (status) {
    case 'SUBMITTER_STATUS_PENDING': return 'Pending';
    case 'SUBMITTER_STATUS_OPENED': return 'Opened';
    case 'SUBMITTER_STATUS_COMPLETED': return 'Completed';
    case 'SUBMITTER_STATUS_DECLINED': return 'Declined';
    default: return status ?? '-';
  }
}

function buildSigningLink(slug: string): string {
  return `${window.location.origin}/signing/session/${slug}`;
}

async function copyToClipboard(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    message.success('Link copied');
  } catch {
    message.error('Failed to copy');
  }
}

const submissionStore = useSigningSubmissionStore();
const templateStore = useSigningTemplateStore();

interface SelectOption {
  value: string;
  label: string;
}

const templateOptions = ref<SelectOption[]>([]);
const userOptions = ref<SelectOption[]>([]);

async function loadTemplates() {
  try {
    // Only load ACTIVE templates
    const resp = await templateStore.listTemplates(
      { page: 1, pageSize: 100 },
      { status: 'TEMPLATE_STATUS_ACTIVE' },
    );
    templateOptions.value = (resp.templates ?? []).map(t => ({
      value: t.id ?? '',
      label: `${t.name}${t.fileName ? ` (${t.fileName})` : ''}`,
    }));
  } catch {
    templateOptions.value = [];
  }
}

async function loadUsers() {
  try {
    const data = await signingApi.get<{ items: Array<{ id: number; username: string; realname: string; email: string }>; total: number }>(
      '/signing/users?noPaging=true',
    );
    userOptions.value = (data.items ?? []).map((u) => ({
      value: String(u.id),
      label: `${u.realname || u.username || ''} (${u.email || '-'})`,
      name: u.realname || u.username || '',
      email: u.email || '',
    }));
  } catch {
    userOptions.value = [];
  }
}

const [Drawer, drawerApi] = useVbenDrawer({
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      const data = drawerApi.getData<{ row: Submission; mode: string }>();
      if (data) {
        mode.value = data.mode as 'create' | 'view';
        if (data.mode === 'create') {
          formState.value = {
            templateId: '',
            signingMode: 'SIGNING_MODE_SEQUENTIAL',
          };
          submitters.value = [{ name: '', email: '', role: '' }];
          await Promise.all([loadTemplates(), loadUsers()]);
        } else if (data.row) {
          formState.value = { ...data.row };
          submitters.value = [];
          submitterDetails.value = [];
          // Load submitter details for view mode
          if (data.row.id) {
            try {
              const resp = await signingApi.get<{
                submitters?: Array<{
                  name?: string;
                  email?: string;
                  role?: string;
                  status?: string;
                  slug?: string;
                  signedAt?: string;
                }>;
              }>(`/signing/submissions/${data.row.id}/submitters`);
              submitterDetails.value = (resp.submitters ?? []).map((s) => ({
                name: s.name ?? '',
                email: s.email ?? '',
                role: s.role ?? '',
                status: s.status ?? 'SUBMITTER_STATUS_PENDING',
                slug: s.slug ?? '',
                signedAt: s.signedAt,
              }));
            } catch {
              submitterDetails.value = [];
            }
          }
        }
      }
    }
  },
});

const mode = ref<'create' | 'view'>('view');
const formState = ref<Partial<Submission>>({});
const submitters = ref<SubmitterInput[]>([]);
const submitterDetails = ref<readonly SubmitterDetail[]>([]);
const loading = ref(false);

const isReadonly = computed(() => mode.value === 'view');
const title = computed(() =>
  mode.value === 'create' ? $t('signing.page.submission.create') : $t('ui.button.view'),
);

function handleUserSelect(index: number, userId: string) {
  const user = userOptions.value.find(u => u.value === userId) as any;
  if (user) {
    submitters.value = submitters.value.map((s, i) =>
      i === index ? { ...s, name: user.name || '', email: user.email || '' } : s
    );
  }
}

function addSubmitter() {
  submitters.value = [...submitters.value, { name: '', email: '', role: '' }];
}

function removeSubmitter(index: number) {
  submitters.value = submitters.value.filter((_, i) => i !== index);
}

async function handleSave() {
  if (!formState.value.templateId) {
    notification.warning({ message: 'Please select a template' });
    return;
  }
  if (!submitters.value.some(s => s.name || s.email)) {
    notification.warning({ message: 'Please add at least one signer' });
    return;
  }

  loading.value = true;
  try {
    await submissionStore.createSubmission({
      templateId: formState.value.templateId,
      signingMode: formState.value.signingMode,
      submitters: submitters.value,
    });
    notification.success({ message: 'Submission created' });
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
      <!-- Template selector (active only) -->
      <Form.Item :label="$t('signing.page.submission.templateId')" required>
        <Select
          v-if="mode === 'create'"
          v-model:value="formState.templateId"
          :options="templateOptions"
          :placeholder="$t('ui.placeholder.select')"
          show-search
          :filter-option="(input: string, option: any) => option.label.toLowerCase().includes(input.toLowerCase())"
        />
        <Input v-else :value="formState.templateId" disabled />
      </Form.Item>

      <!-- Signing mode -->
      <Form.Item :label="$t('signing.page.submission.signingMode')">
        <Select v-model:value="formState.signingMode" :disabled="isReadonly">
          <Select.Option value="SIGNING_MODE_SEQUENTIAL">{{ $t('signing.page.submission.modeSequential') }}</Select.Option>
          <Select.Option value="SIGNING_MODE_PARALLEL">{{ $t('signing.page.submission.modeParallel') }}</Select.Option>
        </Select>
      </Form.Item>

      <!-- Signers (create mode) -->
      <template v-if="mode === 'create'">
        <Divider>{{ $t('signing.page.submission.submitters') }}</Divider>
        <div v-for="(sub, index) in submitters" :key="index" class="sub-card">
          <div class="sub-card-header">
            <span class="text-sm font-medium">#{{ index + 1 }}</span>
            <Button v-if="submitters.length > 1" type="text" danger size="small" @click="removeSubmitter(index)">
              <component :is="LucideTrash" class="size-3" />
            </Button>
          </div>

          <!-- User selector — picks from system users -->
          <Form.Item label="User" class="mb-2">
            <Select
              :placeholder="$t('ui.placeholder.select')"
              show-search
              allow-clear
              :filter-option="(input: string, option: any) => option.label.toLowerCase().includes(input.toLowerCase())"
              :options="userOptions"
              @change="(val: string) => handleUserSelect(index, val)"
            />
          </Form.Item>

          <Form.Item :label="$t('signing.page.submission.submitterName')" class="mb-2">
            <Input v-model:value="sub.name" :placeholder="$t('ui.placeholder.input')" />
          </Form.Item>
          <Form.Item :label="$t('signing.page.submission.submitterEmail')" class="mb-2">
            <Input v-model:value="sub.email" type="email" :placeholder="$t('ui.placeholder.input')" />
          </Form.Item>
          <Form.Item :label="$t('signing.page.submission.submitterRole')" class="mb-0">
            <Input v-model:value="sub.role" placeholder="e.g. Buyer, Seller, Witness" />
          </Form.Item>
        </div>
        <Button block type="dashed" @click="addSubmitter">
          <component :is="LucidePlus" class="mr-1 size-3" />
          {{ $t('signing.page.submission.addSubmitter') }}
        </Button>
      </template>

      <!-- View mode details -->
      <template v-if="mode === 'view'">
        <Form.Item :label="$t('signing.page.submission.status')">
          <Input :value="formState.status" disabled />
        </Form.Item>

        <!-- Submitter details -->
        <template v-if="submitterDetails.length > 0">
          <Divider>{{ $t('signing.page.submission.submitters') }}</Divider>
          <div
            v-for="(sub, index) in submitterDetails"
            :key="index"
            class="sub-card"
          >
            <div class="sub-card-header">
              <span class="sub-card-name">{{ sub.name || '-' }}</span>
              <Tag :color="submitterStatusColors[sub.status] ?? 'default'">
                {{ getSubmitterStatusLabel(sub.status) }}
              </Tag>
            </div>
            <div class="sub-card-detail">{{ sub.email || '-' }}</div>
            <div v-if="sub.role" class="sub-card-detail sub-card-detail--muted">
              Role: {{ sub.role }}
            </div>
            <div v-if="sub.signedAt" class="sub-card-detail sub-card-detail--muted">
              Signed: {{ sub.signedAt }}
            </div>
            <div v-if="sub.slug" class="sub-card-link">
              <Input
                :value="buildSigningLink(sub.slug)"
                size="small"
                readonly
              />
              <Button
                size="small"
                type="text"
                :title="'Copy link'"
                @click="copyToClipboard(buildSigningLink(sub.slug))"
              >
                Copy
              </Button>
            </div>
          </div>
        </template>
      </template>
    </Form>
    <template #footer>
      <Button v-if="!isReadonly" type="primary" :loading="loading" @click="handleSave">
        {{ $t('ui.button.save') }}
      </Button>
    </template>
  </Drawer>
</template>

<style scoped>
.sub-card {
  margin-bottom: 1rem;
  border-radius: 0.5rem;
  border: 1px solid hsl(var(--border));
  padding: 0.75rem;
}
.sub-card-header {
  margin-bottom: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.sub-card-name {
  font-size: 0.875rem;
  font-weight: 500;
  color: hsl(var(--foreground));
}
.sub-card-detail {
  font-size: 0.8125rem;
  color: hsl(var(--foreground));
  margin-bottom: 2px;
}
.sub-card-detail--muted {
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
}
.sub-card-link {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 6px;
}
</style>

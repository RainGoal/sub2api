<template>
  <div class="space-y-4">
    <section class="card p-4 sm:p-5">
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-6">
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.start') }}</span>
          <input :value="filters.start" type="datetime-local" class="input" @input="updateInput('start', $event)" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.end') }}</span>
          <input :value="filters.end" type="datetime-local" class="input" @input="updateInput('end', $event)" />
        </label>
        <label class="block sm:col-span-2 xl:col-span-1">
          <span class="input-label">{{ t('admin.conversationAudit.filters.sessionId') }}</span>
          <input :value="filters.session_id" type="text" class="input font-mono" @input="updateInput('session_id', $event)" @keyup.enter="$emit('search')" />
        </label>
        <label class="block sm:col-span-2 xl:col-span-1">
          <span class="input-label">{{ t('admin.conversationAudit.filters.requestId') }}</span>
          <input :value="filters.request_id" type="text" class="input font-mono" @input="updateInput('request_id', $event)" @keyup.enter="$emit('search')" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.outcome') }}</span>
          <Select :model-value="filters.outcome_status" :options="outcomeOptions" @update:model-value="updateSelect('outcome_status', $event)" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.capture') }}</span>
          <Select :model-value="filters.capture_status" :options="captureOptions" @update:model-value="updateSelect('capture_status', $event)" />
        </label>
      </div>

      <div v-if="advanced" class="mt-4 grid grid-cols-1 gap-4 border-t border-gray-200 pt-4 dark:border-dark-700 sm:grid-cols-2 xl:grid-cols-6">
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.userId') }}</span>
          <input :value="filters.user_id" type="number" min="1" class="input" @input="updateInput('user_id', $event)" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.groupId') }}</span>
          <input :value="filters.group_id" type="number" min="1" class="input" @input="updateInput('group_id', $event)" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.apiKeyId') }}</span>
          <input :value="filters.api_key_id" type="number" min="1" class="input" @input="updateInput('api_key_id', $event)" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.protocol') }}</span>
          <input :value="filters.protocol" type="text" class="input font-mono" @input="updateInput('protocol', $event)" @keyup.enter="$emit('search')" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.endpoint') }}</span>
          <input :value="filters.inbound_endpoint" type="text" class="input font-mono" @input="updateInput('inbound_endpoint', $event)" @keyup.enter="$emit('search')" />
        </label>
        <label class="block">
          <span class="input-label">{{ t('admin.conversationAudit.filters.model') }}</span>
          <input :value="filters.requested_model" type="text" class="input font-mono" @input="updateInput('requested_model', $event)" @keyup.enter="$emit('search')" />
        </label>
      </div>

      <div class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 pt-4 dark:border-dark-700">
        <button type="button" class="btn btn-secondary" @click="advanced = !advanced">
          <Icon name="filter" size="sm" class="mr-1.5" />
          {{ advanced ? t('admin.conversationAudit.actions.lessFilters') : t('admin.conversationAudit.actions.moreFilters') }}
        </button>
        <div class="flex flex-wrap gap-3">
          <button type="button" class="btn btn-danger" :disabled="loading" @click="$emit('preview-delete')">
            <Icon name="trash" size="sm" class="mr-1.5" />
            {{ t('admin.conversationAudit.actions.deleteByFilter') }}
          </button>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="$emit('reset')">{{ t('common.reset') }}</button>
          <button type="button" class="btn btn-primary" :disabled="loading" @click="$emit('search')">
            <Icon name="search" size="sm" class="mr-1.5" />
            {{ t('common.search') }}
          </button>
        </div>
      </div>
    </section>

    <section class="card overflow-hidden">
      <DataTable :columns="columns" :data="records" :loading="loading" row-key="audit_id" :clickable-rows="true" @row-click="$emit('view', $event)">
        <template #cell-created_at="{ row }">
          <div class="whitespace-nowrap">
            <div class="font-medium text-gray-900 dark:text-white">{{ formatTime(row.created_at) }}</div>
            <div class="mt-0.5 font-mono text-xs text-gray-400">{{ row.transport_mode }}</div>
          </div>
        </template>
        <template #cell-identity="{ row }">
          <div class="max-w-[240px]">
            <div class="truncate font-medium text-gray-900 dark:text-white" :title="row.user_name">{{ row.user_name || `#${row.user_id}` }}</div>
            <div class="mt-0.5 truncate text-xs text-gray-400" :title="row.api_key_name">{{ row.api_key_name || `Key #${row.api_key_id}` }}</div>
            <div v-if="row.group_name || row.group_id" class="mt-0.5 truncate text-xs text-gray-400">{{ row.group_name || `Group #${row.group_id}` }}</div>
          </div>
        </template>
        <template #cell-route="{ row }">
          <div class="max-w-[300px]">
            <div class="truncate font-mono text-xs text-gray-800 dark:text-gray-200" :title="row.inbound_endpoint">{{ row.inbound_endpoint }}</div>
            <div class="mt-1 flex max-w-full items-center gap-2">
              <span class="shrink-0 text-[11px] font-medium text-primary-600 dark:text-primary-400">{{ row.protocol }}</span>
              <span class="truncate text-xs text-gray-400" :title="row.effective_model || row.requested_model">{{ row.effective_model || row.requested_model || '—' }}</span>
            </div>
          </div>
        </template>
        <template #cell-outcome="{ row }">
          <div class="flex max-w-[220px] flex-wrap items-center gap-1.5">
            <span :class="outcomeClass(row.outcome_status)">{{ outcomeLabel(row.outcome_status) }}</span>
            <span :class="captureClass(row.capture_status)">{{ captureLabel(row.capture_status) }}</span>
            <span v-if="row.http_status" class="font-mono text-xs text-gray-400">HTTP {{ row.http_status }}</span>
          </div>
          <div v-if="row.error_code" class="mt-1 max-w-[220px] truncate font-mono text-xs text-red-500" :title="row.error_code">{{ row.error_code }}</div>
        </template>
        <template #cell-storage="{ row }">
          <div class="whitespace-nowrap font-mono text-xs text-gray-700 dark:text-dark-200">
            {{ formatBytes(row.request_stored_bytes + row.response_stored_bytes) }}
          </div>
          <div class="mt-1 whitespace-nowrap text-xs text-gray-400">
            {{ formatBytes(row.request_original_bytes + row.response_original_bytes) }}
          </div>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex items-center justify-end gap-1" @click.stop>
            <button type="button" class="icon-btn" :title="t('admin.conversationAudit.actions.view')" :aria-label="t('admin.conversationAudit.actions.view')" @click="$emit('view', row)">
              <Icon name="eye" size="sm" />
            </button>
            <button type="button" class="icon-btn text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/40" :title="t('common.delete')" :aria-label="t('common.delete')" @click="$emit('delete', row)">
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </template>
        <template #empty>
          <div class="flex flex-col items-center py-8">
            <Icon name="database" size="xl" class="mb-3 h-10 w-10 text-gray-300 dark:text-dark-600" />
            <p class="text-sm font-medium text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.records.empty') }}</p>
          </div>
        </template>
      </DataTable>

      <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60">
        <div class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-400">
          <span>{{ t('admin.conversationAudit.records.page', { page }) }}</span>
          <Select class="w-24" :model-value="String(pageSize)" :options="pageSizeOptions" @update:model-value="$emit('page-size', Number($event))" />
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="icon-btn" :disabled="loading || page <= 1" :title="t('admin.conversationAudit.actions.previous')" :aria-label="t('admin.conversationAudit.actions.previous')" @click="$emit('previous')">
            <Icon name="chevronLeft" size="sm" />
          </button>
          <button type="button" class="icon-btn" :disabled="loading || !hasNext" :title="t('admin.conversationAudit.actions.next')" :aria-label="t('admin.conversationAudit.actions.next')" @click="$emit('next')">
            <Icon name="chevronRight" size="sm" />
          </button>
        </div>
      </footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from '@/components/common/DataTable.vue'
import Select from '@/components/common/Select.vue'
import type { Column } from '@/components/common/types'
import Icon from '@/components/icons/Icon.vue'
import type { ConversationAuditFilters, ConversationAuditRecord } from '../types'
import { formatBytes } from '../viewModel'

const props = defineProps<{
  records: ConversationAuditRecord[]
  filters: ConversationAuditFilters
  loading: boolean
  page: number
  pageSize: number
  hasNext: boolean
}>()
const emit = defineEmits<{
  'update:filters': [value: ConversationAuditFilters]
  search: []
  reset: []
  next: []
  previous: []
  'page-size': [value: number]
  view: [record: ConversationAuditRecord]
  delete: [record: ConversationAuditRecord]
  'preview-delete': []
}>()
const { t, locale } = useI18n()
const advanced = ref(false)

const columns = computed<Column[]>(() => [
  { key: 'created_at', label: t('admin.conversationAudit.records.time') },
  { key: 'identity', label: t('admin.conversationAudit.records.identity') },
  { key: 'route', label: t('admin.conversationAudit.records.route') },
  { key: 'outcome', label: t('admin.conversationAudit.records.outcome') },
  { key: 'storage', label: t('admin.conversationAudit.records.storage') },
  { key: 'actions', label: t('common.actions') },
])
const outcomeOptions = computed(() => [
  { value: '', label: t('admin.conversationAudit.filters.all') },
  ...['completed', 'error', 'timeout', 'partial', 'cancelled', 'unknown'].map((value) => ({ value, label: t(`admin.conversationAudit.outcome.${value}`) })),
])
const captureOptions = computed(() => [
  { value: '', label: t('admin.conversationAudit.filters.all') },
  ...['complete', 'truncated', 'metadata_only', 'degraded'].map((value) => ({ value, label: t(`admin.conversationAudit.capture.${value}`) })),
])
const pageSizeOptions = [20, 50, 100].map((value) => ({ value: String(value), label: String(value) }))

function updateField(key: keyof ConversationAuditFilters, value: string) {
  emit('update:filters', { ...props.filters, [key]: value })
}
function updateInput(key: keyof ConversationAuditFilters, event: Event) {
  updateField(key, (event.target as HTMLInputElement).value)
}
function updateSelect(key: keyof ConversationAuditFilters, value: string | number | boolean | null) {
  updateField(key, String(value ?? ''))
}
function formatTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value))
}
function outcomeLabel(value?: string): string { return t(`admin.conversationAudit.outcome.${value || 'unknown'}`) }
function captureLabel(value: string): string { return t(`admin.conversationAudit.capture.${value}`) }
function outcomeClass(value?: string): string {
  const base = 'inline-flex rounded px-2 py-0.5 text-xs font-semibold '
  if (value === 'completed') return base + 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300'
  if (value === 'partial' || value === 'timeout') return base + 'bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300'
  return base + 'bg-red-100 text-red-700 dark:bg-red-950/50 dark:text-red-300'
}
function captureClass(value: string): string {
  const base = 'inline-flex rounded px-2 py-0.5 text-xs font-medium '
  return value === 'complete'
    ? base + 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
    : base + 'bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300'
}
</script>

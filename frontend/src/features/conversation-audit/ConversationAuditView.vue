<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1600px] space-y-5 pb-8">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.conversationAudit.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.description') }}</p>
        </div>
        <div v-if="config" class="flex items-center gap-3 text-xs text-gray-500 dark:text-dark-400">
          <span class="h-2 w-2 rounded-full" :class="config.effective_enabled ? 'bg-emerald-500' : 'bg-gray-400'"></span>
          <span>{{ config.effective_enabled ? t('admin.conversationAudit.status.enabled') : t('admin.conversationAudit.status.disabled') }}</span>
          <span class="font-mono">v{{ config.config_version }}</span>
        </div>
      </header>

      <RuntimeOverview :runtime="runtime" :loading="loading.runtime" @refresh="loadRuntime" />

      <div class="tabs inline-flex" role="tablist" :aria-label="t('admin.conversationAudit.title')">
        <button type="button" role="tab" class="tab" :class="{ 'tab-active': activeTab === 'records' }" :aria-selected="activeTab === 'records'" @click="activeTab = 'records'">
          {{ t('admin.conversationAudit.tabs.records') }}
        </button>
        <button type="button" role="tab" class="tab" :class="{ 'tab-active': activeTab === 'config' }" :aria-selected="activeTab === 'config'" @click="activeTab = 'config'">
          {{ t('admin.conversationAudit.tabs.config') }}
        </button>
      </div>

      <RecordWorkspace
        v-if="activeTab === 'records'"
        :records="records"
        :filters="filters"
        :loading="loading.records"
        :page="cursorStack.length"
        :page-size="pageSize"
        :has-next="Boolean(nextCursor)"
        @update:filters="filters = $event"
        @search="applyFilters"
        @reset="resetFilters"
        @next="nextPage"
        @previous="previousPage"
        @page-size="changePageSize"
        @view="openRecord"
        @delete="deleteTarget = $event"
        @preview-delete="openDeletePreview"
      />

      <div v-else-if="loading.config && !configDraft" class="card flex min-h-64 items-center justify-center">
        <div class="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-b-primary-600 dark:border-dark-700 dark:border-b-primary-400"></div>
      </div>
      <div v-else-if="!configDraft" class="card p-6 text-sm text-red-600 dark:text-red-300" role="alert">
        <p>{{ configError || t('admin.conversationAudit.errors.loadConfig') }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="loadConfig">{{ t('admin.conversationAudit.actions.retry') }}</button>
      </div>
      <ConfigPanel
        v-else
        :model-value="configDraft"
        :dirty="configDirty"
        :saving="loading.saving"
        @update:model-value="configDraft = $event"
        @save="saveConfig"
        @reset="resetConfig"
      />
    </div>

    <RecordDetailDialog :show="detailVisible" :detail="detail" :loading="loading.detail" @close="closeDetail" />
    <ConfirmDialog
      :show="Boolean(deleteTarget)"
      :title="t('admin.conversationAudit.delete.singleTitle')"
      :message="t('admin.conversationAudit.delete.singleMessage')"
      :confirm-text="t('common.delete')"
      danger
      @confirm="confirmSingleDelete"
      @cancel="deleteTarget = null"
    />
    <DeletePreviewDialog
      :show="deletePreviewVisible"
      :preview="deletePreview"
      :loading="loading.preview"
      :deleting="loading.deleting"
      @close="closeDeletePreview"
      @confirm="confirmFilterDelete"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import conversationAuditAPI from './api'
import ConfigPanel from './components/ConfigPanel.vue'
import DeletePreviewDialog from './components/DeletePreviewDialog.vue'
import RecordDetailDialog from './components/RecordDetailDialog.vue'
import RecordWorkspace from './components/RecordWorkspace.vue'
import RuntimeOverview from './components/RuntimeOverview.vue'
import type {
  ConversationAuditConfig,
  ConversationAuditDeletePreview,
  ConversationAuditRecord,
  ConversationAuditRecordDetail,
  ConversationAuditRuntime,
} from './types'
import {
  buildConversationAuditUpdate,
  cloneConversationAuditFilters,
  defaultConversationAuditFilters,
  filterValidationCode,
} from './viewModel'

const { t } = useI18n()
const appStore = useAppStore()
const activeTab = ref<'records' | 'config'>('records')
const config = ref<ConversationAuditConfig | null>(null)
const configDraft = ref<ConversationAuditConfig | null>(null)
const configError = ref('')
const runtime = ref<ConversationAuditRuntime | null>(null)
const records = ref<ConversationAuditRecord[]>([])
const filters = ref(defaultConversationAuditFilters())
const appliedFilters = ref(cloneConversationAuditFilters(filters.value))
const cursorStack = ref<string[]>([''])
const nextCursor = ref('')
const pageSize = ref(50)
const detailVisible = ref(false)
const detail = ref<ConversationAuditRecordDetail | null>(null)
const deleteTarget = ref<ConversationAuditRecord | null>(null)
const deletePreviewVisible = ref(false)
const deletePreview = ref<ConversationAuditDeletePreview | null>(null)
const loading = reactive({ config: false, runtime: false, records: false, saving: false, detail: false, preview: false, deleting: false })

const configDirty = computed(() => {
  if (!config.value || !configDraft.value) return false
  return JSON.stringify(buildConversationAuditUpdate(config.value)) !== JSON.stringify(buildConversationAuditUpdate(configDraft.value))
})

function errorMessage(error: unknown, fallbackKey: string): string {
  const code = extractApiErrorCode(error)
  if (code) {
    const key = `admin.conversationAudit.errors.${code}`
    const translated = t(key)
    if (translated !== key) return translated
  }
  return extractApiErrorMessage(error, t(fallbackKey))
}

async function loadConfig() {
  loading.config = true
  configError.value = ''
  try {
    const result = await conversationAuditAPI.getConfig()
    config.value = { ...result }
    configDraft.value = { ...result }
  } catch (error) {
    configError.value = errorMessage(error, 'admin.conversationAudit.errors.loadConfig')
  } finally {
    loading.config = false
  }
}

async function loadRuntime() {
  loading.runtime = true
  try {
    runtime.value = await conversationAuditAPI.getRuntime()
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.loadRuntime'))
  } finally {
    loading.runtime = false
  }
}

async function loadRecords() {
  loading.records = true
  try {
    const cursor = cursorStack.value[cursorStack.value.length - 1] || ''
    const result = await conversationAuditAPI.listRecords(appliedFilters.value, cursor, pageSize.value)
    records.value = result.items
    nextCursor.value = result.next_cursor || ''
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.loadRecords'))
  } finally {
    loading.records = false
  }
}

function validateFilters(): boolean {
  const code = filterValidationCode(filters.value)
  if (!code) return true
  appStore.showError(t(`admin.conversationAudit.errors.${code}`))
  return false
}

function applyFilters() {
  if (!validateFilters()) return
  appliedFilters.value = cloneConversationAuditFilters(filters.value)
  cursorStack.value = ['']
  nextCursor.value = ''
  void loadRecords()
}

function resetFilters() {
  filters.value = defaultConversationAuditFilters()
  applyFilters()
}

function nextPage() {
  if (!nextCursor.value || loading.records) return
  cursorStack.value = [...cursorStack.value, nextCursor.value]
  void loadRecords()
}

function previousPage() {
  if (cursorStack.value.length <= 1 || loading.records) return
  cursorStack.value = cursorStack.value.slice(0, -1)
  void loadRecords()
}

function changePageSize(value: number) {
  if (![20, 50, 100].includes(value)) return
  pageSize.value = value
  cursorStack.value = ['']
  void loadRecords()
}

function resetConfig() {
  if (config.value) configDraft.value = { ...config.value }
}

async function saveConfig() {
  if (!configDraft.value || !configDirty.value) return
  loading.saving = true
  try {
    const saved = await conversationAuditAPI.updateConfig(buildConversationAuditUpdate(configDraft.value))
    config.value = { ...saved }
    configDraft.value = { ...saved }
    appStore.showSuccess(t('admin.conversationAudit.messages.saved'))
    await loadRuntime()
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.saveConfig'))
  } finally {
    loading.saving = false
  }
}

async function openRecord(record: ConversationAuditRecord) {
  detailVisible.value = true
  detail.value = null
  loading.detail = true
  try {
    detail.value = await conversationAuditAPI.getRecord(record)
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.loadDetail'))
    detailVisible.value = false
  } finally {
    loading.detail = false
  }
}

function closeDetail() {
  detailVisible.value = false
  detail.value = null
}

async function confirmSingleDelete() {
  const target = deleteTarget.value
  deleteTarget.value = null
  if (!target) return
  loading.deleting = true
  try {
    await conversationAuditAPI.deleteRecord(target)
    appStore.showSuccess(t('admin.conversationAudit.messages.deleted', { count: 1 }))
    await loadRecords()
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.delete'))
  } finally {
    loading.deleting = false
  }
}

async function openDeletePreview() {
  if (!validateFilters()) return
  deletePreviewVisible.value = true
  deletePreview.value = null
  loading.preview = true
  try {
    deletePreview.value = await conversationAuditAPI.previewDelete(filters.value)
  } catch (error) {
    deletePreviewVisible.value = false
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.previewDelete'))
  } finally {
    loading.preview = false
  }
}

function closeDeletePreview() {
  if (loading.deleting) return
  deletePreviewVisible.value = false
  deletePreview.value = null
}

async function confirmFilterDelete() {
  const token = deletePreview.value?.confirmation_token
  if (!token || loading.deleting) return
  loading.deleting = true
  try {
    const result = await conversationAuditAPI.deleteByFilter(token)
    deletePreviewVisible.value = false
    deletePreview.value = null
    appStore.showSuccess(t('admin.conversationAudit.messages.deleted', { count: result.deleted_records }))
    applyFilters()
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.conversationAudit.errors.deleteConfirmation'))
    deletePreview.value = null
  } finally {
    loading.deleting = false
  }
}

onMounted(() => {
  void Promise.allSettled([loadConfig(), loadRuntime(), loadRecords()])
})
</script>

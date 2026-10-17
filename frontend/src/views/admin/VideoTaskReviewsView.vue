<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="card flex flex-wrap items-start justify-between gap-4 p-4">
          <p class="max-w-4xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.videoTaskReviews.hint') }}</p>
          <button type="button" class="btn btn-secondary" :disabled="loading || submitting" @click="loadTasks">{{ t('common.refresh') }}</button>
        </div>
        <p v-if="loadError" role="alert" class="mt-3 text-sm text-red-400">{{ loadError }}</p>
      </template>
      <template #table>
        <DataTable :columns="columns" :data="tasks" :loading="loading" row-key="state_id">
          <template #cell-task="{ row }">
            <div class="max-w-sm space-y-1">
              <p class="break-all font-mono text-xs">{{ row.public_id }}</p>
              <p class="font-medium">{{ row.model }}</p>
              <p class="text-xs text-gray-500">{{ row.resolution || '—' }} · {{ t('admin.videoTaskReviews.seconds', { count: row.duration }) }}</p>
            </div>
          </template>
          <template #cell-owner="{ row }">
            <div class="space-y-1 text-xs text-gray-500 dark:text-gray-400">
              <p>{{ t('admin.videoTaskReviews.user') }} #{{ row.user_id }}</p>
              <p>{{ t('admin.videoTaskReviews.apiKey') }} #{{ row.api_key_id }}</p>
              <p>{{ t('admin.videoTaskReviews.account') }} #{{ row.account_id }}</p>
            </div>
          </template>
          <template #cell-issue="{ row }">
            <p class="font-medium text-amber-500">{{ reviewReason(row.reason) }}</p>
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.videoTaskReviews.deadline') }}: {{ formatTime(row.deadline) }}</p>
          </template>
          <template #cell-actions="{ row }">
            <button type="button" class="btn btn-secondary" :disabled="submitting" :data-testid="`review-${row.state_id}`" @click="openReview(row)">{{ t('admin.videoTaskReviews.review') }}</button>
          </template>
          <template #empty><p class="py-8 text-sm text-gray-500">{{ loadError || t('admin.videoTaskReviews.empty') }}</p></template>
        </DataTable>
      </template>
      <template #pagination>
        <div class="flex items-center justify-end gap-3 text-sm">
          <button type="button" class="btn btn-secondary" :disabled="loading || submitting || offset === 0" @click="changePage(-1)">{{ t('admin.videoTaskReviews.previous') }}</button>
          <span class="text-gray-500">{{ t('admin.videoTaskReviews.page', { page: offset / pageSize + 1 }) }}</span>
          <button type="button" class="btn btn-secondary" :disabled="loading || submitting || !!loadError || tasks.length < pageSize" @click="changePage(1)">{{ t('admin.videoTaskReviews.next') }}</button>
        </div>
      </template>
    </TablePageLayout>
    <BaseDialog :show="!!selected" :title="t('admin.videoTaskReviews.detailTitle')" width="wide" :show-close-button="!submitting" :close-on-escape="!submitting" @close="closeReview">
      <div v-if="selected" class="space-y-5">
        <div class="space-y-2 border-l-2 border-amber-500 pl-3 text-sm">
          <p class="break-all font-mono">{{ selected.public_id }}</p>
          <p class="text-amber-500">{{ reviewReason(selected.reason) }}</p>
          <p class="break-all text-xs text-gray-500">{{ t('admin.videoTaskReviews.upstreamID') }}: {{ selected.upstream_id || '—' }}</p>
          <p class="text-xs text-gray-500">{{ t('admin.videoTaskReviews.requestedAt') }}: {{ formatTime(selected.requested_at) }}</p>
          <p class="text-xs text-gray-500">{{ t('admin.videoTaskReviews.deadline') }}: {{ formatTime(selected.deadline) }}</p>
        </div>
        <details class="rounded border border-gray-200 p-3 dark:border-dark-700">
          <summary class="cursor-pointer text-sm font-medium">{{ t('admin.videoTaskReviews.pricingEvidence') }}</summary>
          <div class="mt-3 grid min-w-0 gap-3 sm:grid-cols-2">
            <div v-for="(snapshot, index) in [selected.sales_snapshot, selected.account_cost]" :key="index" class="min-w-0">
              <p class="mb-2 text-xs text-gray-500">{{ t(index === 0 ? 'admin.videoTaskReviews.sales' : 'admin.videoTaskReviews.purchase') }}</p>
              <pre class="max-h-56 overflow-auto whitespace-pre-wrap break-all text-xs">{{ snapshot ? JSON.stringify(snapshot, null, 2) : t('admin.videoTaskReviews.noSnapshot') }}</pre>
            </div>
          </div>
        </details>
        <details v-if="selected.decisions?.length" class="rounded border border-gray-200 p-3 dark:border-dark-700">
          <summary class="cursor-pointer text-sm font-medium">{{ t('admin.videoTaskReviews.history') }}</summary>
          <ol class="mt-3 space-y-3">
            <li v-for="(decision, index) in selected.decisions" :key="index" class="border-l border-gray-300 pl-3 text-xs dark:border-dark-600">
              <p>{{ actionLabel(decision.action) }} · {{ t('admin.videoTaskReviews.actor') }} #{{ decision.actor_id }} · {{ formatTime(decision.decided_at) }}</p>
              <p class="mt-1 whitespace-pre-wrap break-all text-gray-500">{{ decision.reason }}</p>
            </li>
          </ol>
        </details>
        <div>
          <label for="review-action" class="input-label">{{ t('admin.videoTaskReviews.action') }}</label>
          <select id="review-action" v-model="action" class="input" :disabled="submitting" @change="acknowledged = false">
            <option value="retry_poll" :disabled="!selected.upstream_id">{{ actionLabel('retry_poll') }}</option>
            <option value="confirmed_failed">{{ actionLabel('confirmed_failed') }}</option>
            <option value="confirmed_canceled">{{ actionLabel('confirmed_canceled') }}</option>
          </select>
        </div>
        <div>
          <label for="review-reason" class="input-label">{{ t('admin.videoTaskReviews.reason') }}</label>
          <textarea id="review-reason" v-model="reason" rows="3" maxlength="2000" class="input" :disabled="submitting" :placeholder="t('admin.videoTaskReviews.reasonPlaceholder')" />
        </div>
        <label v-if="action !== 'retry_poll'" class="flex items-start gap-3 text-sm text-amber-500">
          <input v-model="acknowledged" type="checkbox" class="mt-1" :disabled="submitting" data-testid="review-confirm-release" />
          <span>{{ t('admin.videoTaskReviews.releaseConfirmation') }}</span>
        </label>
        <p v-if="resolveError" role="alert" class="text-sm text-red-400">{{ resolveError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" :disabled="submitting" @click="closeReview">{{ t('common.cancel') }}</button>
        <button type="button" class="btn" :class="action === 'retry_poll' ? 'btn-primary' : 'btn-danger'" :disabled="!canSubmit" data-testid="review-submit" @click="submitDecision">{{ submitting ? t('common.loading') : t('admin.videoTaskReviews.submit') }}</button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listVideoTaskReviews, resolveVideoTaskReview, type VideoTaskReview, type VideoTaskReviewAction } from '@/api/admin/videoTaskReviews'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { Column } from '@/components/common/types'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const pageSize = 20
const tasks = ref<VideoTaskReview[]>([])
const selected = ref<VideoTaskReview | null>(null)
const loading = ref(false)
const submitting = ref(false)
const loadError = ref('')
const resolveError = ref('')
const offset = ref(0)
const action = ref<VideoTaskReviewAction>('retry_poll')
const reason = ref('')
const acknowledged = ref(false)
const columns = computed<Column[]>(() => [
  { key: 'task', label: t('admin.videoTaskReviews.task') },
  { key: 'owner', label: t('admin.videoTaskReviews.owner') },
  { key: 'issue', label: t('admin.videoTaskReviews.issue') },
  { key: 'actions', label: t('common.actions') }
])
const canSubmit = computed(() => !!selected.value && !submitting.value && !!reason.value.trim() &&
  (action.value === 'retry_poll' ? !!selected.value.upstream_id : acknowledged.value))

function formatTime(value: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function reviewReason(value: string): string {
  const keys: Record<string, string> = { submission_outcome_unknown: 'unknownSubmission', provider_outcome_overdue: 'providerOverdue', completion_metering_unavailable: 'meteringUnavailable' }
  return t(`admin.videoTaskReviews.${keys[value] || 'unknownReason'}`)
}

function actionLabel(value: VideoTaskReviewAction): string { return t(`admin.videoTaskReviews.${value}`) }
async function loadTasks() {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    tasks.value = await listVideoTaskReviews(pageSize, offset.value)
  } catch (error) {
    tasks.value = []
    loadError.value = t((error as { status?: number })?.status === 403 ? 'admin.videoTaskReviews.forbidden' : 'admin.videoTaskReviews.loadFailed')
  } finally { loading.value = false }
}

function changePage(direction: number) {
  offset.value = Math.max(0, offset.value + direction * pageSize)
  void loadTasks()
}

function openReview(task: VideoTaskReview) {
  selected.value = task
  action.value = task.upstream_id ? 'retry_poll' : 'confirmed_failed'
  reason.value = ''
  resolveError.value = ''
  acknowledged.value = false
}

function closeReview() { if (!submitting.value) selected.value = null }

async function submitDecision() {
  if (!canSubmit.value || !selected.value) return
  submitting.value = true
  resolveError.value = ''
  try {
    const result = await resolveVideoTaskReview(selected.value.state_id, action.value, reason.value.trim())
    selected.value = null
    appStore.showSuccess(t(result.settlement_status === 'settled' ? 'admin.videoTaskReviews.alreadySettled' : 'admin.videoTaskReviews.resolved'))
    await loadTasks()
  } catch (error) {
    resolveError.value = t((error as { status?: number })?.status === 403 ? 'admin.videoTaskReviews.forbidden' : 'admin.videoTaskReviews.resolveFailed')
  } finally { submitting.value = false }
}

onMounted(loadTasks)
</script>

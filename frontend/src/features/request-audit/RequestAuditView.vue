<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-5 pb-8">
      <header class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.16em] text-primary-400">{{ t('nav.securityAudit') }}</p>
          <h1 class="mt-1 text-2xl font-semibold tracking-tight text-gray-950 dark:text-white">{{ t('admin.requestAudit.title') }}</h1>
          <p class="mt-2 max-w-3xl text-sm text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.description') }}</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading.list || loading.status || loading.config || loading.bans || loading.save" @click="refresh">{{ t('admin.requestAudit.refresh') }}</button>
      </header>

      <div role="note" class="border-l-2 border-amber-500 bg-amber-500/10 px-4 py-3 text-sm text-amber-800 dark:text-amber-200">
        {{ t('admin.requestAudit.sensitiveNotice') }}
      </div>

      <div class="grid gap-3 sm:grid-cols-3">
        <div class="card px-4 py-3"><span class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.requestAudit.capture') }}</span><p class="mt-1 font-semibold" :class="config?.enabled ? 'text-emerald-500' : 'text-gray-500'">{{ config ? t(`admin.requestAudit.${config.enabled ? 'enabled' : 'disabled'}`) : t('admin.requestAudit.unknown') }}</p></div>
        <div class="card px-4 py-3"><span class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.requestAudit.retention') }}</span><p class="mt-1 font-semibold text-gray-900 dark:text-white">{{ config ? t('admin.requestAudit.days', { count: config.retention_days }) : '—' }}</p></div>
        <div class="card px-4 py-3"><span class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.requestAudit.banEnforcement') }}</span><p class="mt-1 font-semibold" :class="config?.session_ban_enabled ? 'text-amber-500' : 'text-gray-500'">{{ config ? t(`admin.requestAudit.${config.session_ban_enabled ? 'enabled' : 'disabled'}`) : t('admin.requestAudit.unknown') }}</p></div>
      </div>
      <div class="card flex flex-wrap items-center justify-between gap-3 px-4 py-3 text-sm" data-test="health-summary">
        <div class="min-w-0"><p class="font-medium" :class="healthState === 'healthy' ? 'text-emerald-500' : 'text-amber-500'">{{ t(`admin.requestAudit.health.${healthState}`) }}</p><p class="mt-1 break-all text-xs text-gray-500">{{ errors.status || t('admin.requestAudit.instanceHint') }}</p></div>
        <p v-if="status" class="font-mono text-xs text-gray-500">{{ t('admin.requestAudit.lossCounts', { requests: status.dropped_requests, events: status.dropped_events, writes: status.write_failures }) }}</p>
      </div>
      <p v-if="errors.config && activeTab !== 'settings'" role="alert" class="text-sm text-red-500">{{ errors.config }}</p>
      <div class="tabs inline-flex max-w-full overflow-x-auto" role="tablist" :aria-label="t('admin.requestAudit.title')">
        <button v-for="tab in tabs" :key="tab" class="tab" :class="{ 'tab-active': activeTab === tab }" role="tab" :aria-selected="activeTab === tab" :data-test="`tab-${tab}`" @click="selectTab(tab)">{{ t(`admin.requestAudit.tabs.${tab}`) }}</button>
      </div>

      <section v-if="activeTab === 'requests'" class="card overflow-hidden">
        <form class="grid gap-3 border-b border-gray-200 p-4 dark:border-dark-700 sm:grid-cols-2 xl:grid-cols-4" @submit.prevent="search">
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.from') }}<input v-model="filters.from" type="datetime-local" class="input w-full" /></label>
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.to') }}<input v-model="filters.to" type="datetime-local" class="input w-full" /></label>
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.apiKeyId') }}<input v-model="filters.apiKey" type="number" min="1" class="input w-full" /></label>
          <RequestAuditUserFilter :key="userFilterRevision" v-model="filters.user" v-model:label="filters.userLabel" />
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.session') }}<input v-model="filters.session" class="input w-full" maxlength="255" /></label>
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.kind') }}<select v-model="filters.kind" class="input w-full"><option value="">{{ t('admin.requestAudit.all') }}</option><option v-for="kind in kinds" :key="kind" :value="kind">{{ t(`admin.requestAudit.kinds.${kind}`) }}</option></select></label>
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.integrity') }}<select v-model="filters.state" class="input w-full"><option value="">{{ t('admin.requestAudit.all') }}</option><option v-for="state in states" :key="state" :value="state">{{ t(`admin.requestAudit.states.${state}`) }}</option></select></label>
          <label class="space-y-1 text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.httpStatus') }}<input v-model="filters.httpStatus" data-test="http-status-filter" type="number" min="100" max="599" class="input w-full" /></label>
          <div class="flex items-end gap-2"><button type="submit" class="btn btn-primary" :disabled="loading.list">{{ t('admin.requestAudit.search') }}</button><button type="button" class="btn btn-secondary" :disabled="loading.list" @click="resetFilters">{{ t('common.reset') }}</button></div>
        </form>
        <p v-if="errors.list" role="alert" class="p-4 text-sm text-red-500">{{ errors.list }}</p>
        <p v-if="loading.list" role="status" class="p-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
        <p v-else-if="!records.items.length && !errors.list" class="p-12 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('admin.requestAudit.empty') }}</p>
        <div v-else-if="records.items.length" class="overflow-x-auto">
          <table class="w-full whitespace-nowrap text-left text-sm">
            <thead class="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300">
              <tr><th v-for="column in ['time', 'user', 'apiKey', 'group', 'request', 'result', 'integrity', 'dataSize', 'actions']" :key="column" class="px-4 py-3 font-medium" :class="{ 'sticky right-0 bg-gray-50 dark:bg-dark-800': column === 'actions' }">{{ t(`admin.requestAudit.${column}`) }}</th></tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="item in records.items" :key="item.id" class="align-top hover:bg-gray-50 dark:hover:bg-dark-800/60" :data-test="`record-${item.id}`">
                <td class="px-4 py-4 font-mono text-xs tabular-nums text-gray-600 dark:text-dark-200"><time :datetime="item.received_at">{{ new Date(item.received_at).toLocaleDateString(locale) }}<span class="mt-1 block text-gray-500 dark:text-dark-300">{{ new Date(item.received_at).toLocaleTimeString(locale) }}</span></time></td>
                <td class="px-4 py-4">
                  <p class="max-w-48 truncate font-medium text-gray-900 dark:text-white" :title="userName(item)">{{ userName(item) }}</p>
                  <p v-if="item.user_username && item.user_email" class="mt-1 max-w-48 truncate text-xs text-gray-500 dark:text-dark-300" :title="item.user_email">{{ item.user_email }}</p>
                </td>
                <td class="px-4 py-4"><p class="max-w-44 truncate text-gray-700 dark:text-dark-100" :title="keyName(item)">{{ keyName(item) }}</p></td>
                <td class="px-4 py-3"><RequestAuditPlatform :platform="item.group_platform" :group-name="groupName(item)" /></td>
                <td class="px-4 py-4">
                  <p class="max-w-64 truncate font-mono text-xs text-gray-800 dark:text-dark-100" :title="`${item.method || ''} ${item.route_pattern || item.kind}`"><span class="text-primary-600 dark:text-primary-400">{{ item.method }}</span> {{ item.route_pattern || kindLabel(item.kind) }}</p>
                  <p class="mt-1 max-w-56 truncate text-xs text-gray-500 dark:text-dark-300" :title="item.model || ''">{{ item.model || '—' }}</p>
                  <p class="mt-1 max-w-56 truncate font-mono text-[11px] text-gray-400 dark:text-dark-400" :title="item.session_id || ''">{{ item.session_id || t('admin.requestAudit.noSession') }}</p>
                </td>
                <td class="px-4 py-4"><span class="font-mono text-xs font-semibold" :class="(item.http_status || 0) >= 400 ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-dark-100'">{{ item.http_status || item.outcome || '—' }}</span><p class="mt-1 font-mono text-xs text-gray-500 dark:text-dark-300">{{ item.duration_ms ?? '—' }} ms</p></td>
                <td class="px-4 py-3"><span class="inline-flex items-center gap-1.5 rounded border px-2 py-1 text-xs" :class="item.capture_state === 'complete' ? 'border-emerald-500/20 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400' : 'border-amber-500/20 bg-amber-500/5 text-amber-600 dark:text-amber-400'"><span class="h-1 w-1 rounded-full bg-current" aria-hidden="true" />{{ stateLabel(item.capture_state) }}</span></td>
                <td class="px-4 py-4 font-mono text-xs tabular-nums text-gray-600 dark:text-dark-200">{{ formatBytes(item.stored_body_bytes || 0) }}<p class="mt-1 text-gray-400 dark:text-dark-400">/ {{ formatBytes(item.observed_body_bytes || 0) }}</p></td>
                <td class="sticky right-0 bg-white px-4 py-3 dark:bg-dark-900"><button class="btn btn-secondary btn-sm" :data-test="`detail-${item.id}`" @click="openDetail(item.id)">{{ t('admin.requestAudit.details') }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex justify-end gap-2 border-t border-gray-200 p-3 dark:border-dark-700"><button class="btn btn-secondary btn-sm" :disabled="!cursorHistory.length || loading.list" @click="previousPage">{{ t('admin.requestAudit.previous') }}</button><button class="btn btn-secondary btn-sm" :disabled="!records.has_more || loading.list" @click="nextPage">{{ t('admin.requestAudit.next') }}</button></div>
      </section>

      <section v-else-if="activeTab === 'bans'" class="card overflow-hidden">
        <div class="flex flex-wrap items-end gap-3 border-b border-gray-200 p-4 dark:border-dark-700">
          <label class="space-y-1 text-xs text-gray-500">{{ t('admin.requestAudit.apiKey') }}<input v-model="banFilters.apiKey" type="number" min="1" class="input block" /></label>
          <label class="space-y-1 text-xs text-gray-500">{{ t('admin.requestAudit.session') }}<input v-model="banFilters.session" class="input block" maxlength="255" /></label>
          <label class="space-y-1 text-xs text-gray-500">{{ t('admin.requestAudit.banState') }}<select v-model="banFilters.state" class="input block"><option value="">{{ t('admin.requestAudit.all') }}</option><option v-for="state in ['active', 'expired', 'revoked']" :key="state" :value="state">{{ t(`admin.requestAudit.banStates.${state}`) }}</option></select></label>
          <button class="btn btn-primary" :disabled="loading.bans" @click="loadBans()">{{ t('admin.requestAudit.search') }}</button>
        </div>
        <p class="border-b border-gray-200 px-4 py-3 text-xs text-gray-500 dark:border-dark-700">{{ t('admin.requestAudit.banScope') }}</p>
        <p v-if="errors.bans" role="alert" class="p-4 text-sm text-red-500">{{ errors.bans }}</p>
        <p v-if="loading.bans" class="p-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
        <p v-else-if="!bans.items.length && !errors.bans" class="p-12 text-center text-sm text-gray-500">{{ t('admin.requestAudit.noBans') }}</p>
        <div v-else class="divide-y divide-gray-200 dark:divide-dark-700">
          <article v-for="ban in bans.items" :key="ban.id" class="flex flex-wrap items-start justify-between gap-4 p-4">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2"><span class="font-medium">{{ t('admin.requestAudit.apiKey') }} {{ ban.api_key_id }}</span><span class="rounded border border-gray-300 px-2 py-0.5 text-xs dark:border-dark-600">{{ t(`admin.requestAudit.banStates.${banState(ban)}`) }}</span></div>
              <p class="mt-1 break-all font-mono text-xs text-gray-500">{{ ban.session_id }}</p>
              <p class="mt-3 whitespace-pre-wrap break-words text-sm">{{ ban.public_reason }}</p>
              <p v-if="ban.internal_note" class="mt-2 whitespace-pre-wrap break-words text-xs text-gray-500">{{ t('admin.requestAudit.internalNote') }}: {{ ban.internal_note }}</p>
              <p class="mt-2 text-xs text-gray-500">{{ formatDate(ban.created_at) }} · {{ t('admin.requestAudit.createdBy') }} {{ ban.created_by || '—' }} · {{ t('admin.requestAudit.expires') }}: {{ ban.expires_at ? formatDate(ban.expires_at) : t('admin.requestAudit.never') }}</p>
              <p v-if="ban.revoked_at" class="mt-1 text-xs text-gray-500">{{ t('admin.requestAudit.revokedBy') }} {{ ban.revoked_by || '—' }} · {{ formatDate(ban.revoked_at) }}</p>
            </div>
            <button v-if="banState(ban) === 'active'" class="btn btn-secondary btn-sm" :disabled="loading.revoke" @click="revokeTarget = ban">{{ t('admin.requestAudit.revoke') }}</button>
          </article>
        </div>
        <div v-if="bans.has_more" class="border-t border-gray-200 p-3 text-right dark:border-dark-700"><button class="btn btn-secondary btn-sm" :disabled="loading.bans" @click="loadBans(bans.next_cursor, true)">{{ t('admin.requestAudit.more') }}</button></div>
      </section>

      <section v-else class="grid gap-5 lg:grid-cols-2">
        <form class="card space-y-5 p-5" @submit.prevent="requestSaveConfig">
          <h2 class="font-semibold">{{ t('admin.requestAudit.configuration') }}</h2>
          <p v-if="errors.config" role="alert" class="text-sm text-red-500">{{ errors.config }}</p>
          <p v-if="loading.config" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
          <fieldset v-if="draft" :disabled="loading.save || loading.config" class="space-y-5">
            <label class="flex items-center justify-between gap-4"><span class="text-sm">{{ t('admin.requestAudit.capture') }}</span><input v-model="draft.enabled" type="checkbox" role="switch" :aria-label="t('admin.requestAudit.capture')" class="h-4 w-4" /></label>
            <label class="block space-y-2 text-sm">{{ t('admin.requestAudit.retention') }}<input v-model.number="draft.retention_days" type="number" min="1" max="30" required class="input block w-36" /></label>
            <p class="text-xs text-gray-500">{{ t('admin.requestAudit.retentionHint') }}</p>
            <label class="flex items-center justify-between gap-4"><span class="text-sm">{{ t('admin.requestAudit.banEnforcement') }}</span><input v-model="draft.session_ban_enabled" type="checkbox" role="switch" :aria-label="t('admin.requestAudit.banEnforcement')" class="h-4 w-4" /></label>
            <p class="text-xs leading-6 text-gray-500">{{ t('admin.requestAudit.availabilityHint') }}</p>
            <p class="text-xs leading-6 text-gray-500">{{ t('admin.requestAudit.websocketHint') }}</p>
            <div class="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700"><button type="button" class="btn btn-secondary" :disabled="loading.save" @click="loadConfig">{{ t('admin.requestAudit.reload') }}</button><button type="submit" data-test="save-config" class="btn btn-primary" :disabled="loading.save || !configDirty">{{ loading.save ? t('common.saving') : t('common.save') }}</button></div>
          </fieldset>
          <button v-else type="button" class="btn btn-secondary" :disabled="loading.config" @click="loadConfig">{{ t('admin.requestAudit.retry') }}</button>
        </form>
        <div class="card p-5"><div class="flex items-center justify-between gap-3"><h2 class="font-semibold">{{ t('admin.requestAudit.runtime') }}</h2><button class="btn btn-secondary btn-sm" :disabled="loading.status" @click="loadStatus">{{ t('admin.requestAudit.refresh') }}</button></div><p class="my-3 text-xs leading-6 text-gray-500">{{ t('admin.requestAudit.instanceHint') }}</p><p v-if="errors.status" role="alert" class="mb-3 text-sm text-red-500">{{ errors.status }}</p><p v-if="loading.status" role="status" class="mb-3 text-sm text-gray-500">{{ t('common.loading') }}</p><RequestAuditRuntime v-if="status" :status="status" /></div>
      </section>
    </div>

    <BaseDialog :show="detailOpen" :title="t('admin.requestAudit.details')" width="extra-wide" @close="closeDetail">
      <p v-if="errors.detail" role="alert" class="text-sm text-red-500">{{ errors.detail }}</p>
      <p v-if="loading.detail && !detail" class="py-8 text-center text-gray-500">{{ t('common.loading') }}</p>
      <div v-if="detail" class="space-y-4">
        <RequestAuditMetadata :record="detail" />
        <div class="flex flex-wrap items-center gap-2"><button class="btn btn-secondary btn-sm" :disabled="!detail.api_key_id || !detail.session_id" @click="showSessionRequests">{{ t('admin.requestAudit.sameSession') }}</button><button class="btn btn-danger btn-sm" data-test="open-ban" :disabled="!detail.api_key_id || !detail.session_id || loading.detail || !!detailBans.length" @click="openBan">{{ t('admin.requestAudit.ban') }}</button><span v-if="!detail.api_key_id || !detail.session_id" class="text-xs text-gray-500">{{ t('admin.requestAudit.banUnavailable') }}</span></div>
        <div v-if="detailBans.length" class="rounded border border-amber-500/30 p-3 text-sm text-amber-500">{{ t('admin.requestAudit.alreadyBanned') }}: {{ detailBans[0]?.public_reason }}</div>
        <RequestAuditPayloads :key="detail.id" :record="detail" />
      </div>
    </BaseDialog>

    <BaseDialog :show="banOpen" :title="t('admin.requestAudit.ban')" :close-on-escape="!loading.createBan" @close="closeBan">
      <form id="request-audit-ban" class="space-y-4" @submit.prevent="createBan">
        <p class="break-all text-xs text-gray-500">{{ t('admin.requestAudit.apiKey') }} {{ detail?.api_key_id }} · {{ detail?.session_id }}</p>
        <p class="text-xs text-amber-500">{{ t('admin.requestAudit.banScope') }}</p>
        <p v-if="!config?.session_ban_enabled" class="text-xs text-amber-500">{{ t('admin.requestAudit.banPaused') }}</p>
        <label class="block space-y-1 text-sm">{{ t('admin.requestAudit.publicReason') }}<textarea v-model="banDraft.public_reason" class="input min-h-24 w-full" maxlength="500" required /></label>
        <label class="block space-y-1 text-sm">{{ t('admin.requestAudit.internalNote') }}<textarea v-model="banDraft.internal_note" class="input w-full" maxlength="4000" /></label>
        <label class="block space-y-1 text-sm">{{ t('admin.requestAudit.expiresOptional') }}<input v-model="banDraft.expires" type="datetime-local" class="input block w-full" /></label>
        <p v-if="errors.createBan" role="alert" class="text-sm text-red-500">{{ errors.createBan }}</p>
        <div v-if="unknownBan" role="status" class="space-y-2 border border-amber-500/30 p-3 text-sm text-amber-500"><p>{{ t('admin.requestAudit.resultUnknown') }}</p><button type="button" data-test="check-ban-result" class="btn btn-secondary btn-sm" :disabled="loading.createBan" @click="confirmBanResult">{{ t('admin.requestAudit.checkResult') }}</button></div>
      </form>
      <template #footer><button class="btn btn-secondary" :disabled="loading.createBan" @click="closeBan">{{ t('common.cancel') }}</button><button form="request-audit-ban" type="submit" data-test="create-ban" class="btn btn-danger" :disabled="loading.createBan || !!unknownBan || !banDraft.public_reason.trim()">{{ loading.createBan ? t('common.saving') : t('admin.requestAudit.ban') }}</button></template>
    </BaseDialog>

    <ConfirmDialog :show="confirmRetention" :title="t('admin.requestAudit.retentionConfirmTitle')" :message="t('admin.requestAudit.retentionConfirm')" danger @confirm="saveConfig" @cancel="confirmRetention = false" />
    <ConfirmDialog :show="!!revokeTarget" :title="t('admin.requestAudit.revoke')" :message="t('admin.requestAudit.revokeConfirm')" @confirm="revokeBan" @cancel="revokeTarget = null" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import RequestAuditRuntime from './RequestAuditRuntime.vue'
import RequestAuditMetadata from './RequestAuditMetadata.vue'
import RequestAuditPlatform from './RequestAuditPlatform.vue'
import RequestAuditPayloads from './RequestAuditPayloads.vue'
import RequestAuditUserFilter from './RequestAuditUserFilter.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMetadata, extractI18nErrorMessage } from '@/utils/apiError'
import api from './api'
import { REQUEST_AUDIT_KINDS } from './types'
import type { RequestAuditBan, RequestAuditBanPage, RequestAuditConfig, RequestAuditFilters, RequestAuditPage, RequestAuditRecord, RequestAuditStatus } from './types'

const { t, locale } = useI18n()
const app = useAppStore()
const tabs = ['requests', 'bans', 'settings'] as const
type Tab = typeof tabs[number]
const activeTab = ref<Tab>('requests')
const kinds = REQUEST_AUDIT_KINDS
const states = ['complete', 'capturing', 'partial']
const loading = reactive({ list: false, detail: false, config: false, status: false, save: false, bans: false, createBan: false, revoke: false })
const errors = reactive({ list: '', detail: '', config: '', status: '', bans: '', createBan: '' })
const emptyFilters = { from: '', to: '', apiKey: '', user: '', userLabel: '', session: '', kind: '', state: '', httpStatus: '' }
const filters = reactive({ ...emptyFilters })
const userFilterRevision = ref(0)
const appliedFilters = ref<RequestAuditFilters>({ limit: 50 })
const banFilters = reactive({ apiKey: '', session: '', state: '' })
const records = ref<RequestAuditPage>({ items: [], next_cursor: '', has_more: false })
const bans = ref<RequestAuditBanPage>({ items: [], next_cursor: '', has_more: false })
const config = ref<RequestAuditConfig | null>(null)
const draft = ref<RequestAuditConfig | null>(null)
const status = ref<RequestAuditStatus | null>(null)
const cursor = ref('')
const cursorHistory = ref<string[]>([])
const detail = ref<RequestAuditRecord | null>(null)
const detailBans = ref<RequestAuditBan[]>([])
const detailOpen = ref(false)
const banOpen = ref(false)
const banDraft = reactive({ public_reason: '', internal_note: '', expires: '' })
const unknownBan = ref<{ id?: string, sourceID: string } | null>(null)
const revokeTarget = ref<RequestAuditBan | null>(null)
const confirmRetention = ref(false)
let detailGeneration = 0
let recordsGeneration = 0
let bansGeneration = 0
const healthState = computed(() => {
  if (!status.value || !status.value.config_known) return 'unknown'
  if (!status.value.running) return 'stopped'
  if (status.value.dropped_requests || status.value.dropped_events || status.value.write_failures || status.value.ban_check_failures || status.value.cleanup_failures || (status.value.writer_backoff_until && Date.parse(status.value.writer_backoff_until) > Date.now())) return 'degraded'
  return status.value.config.enabled ? 'healthy' : 'disabled'
})
const configDirty = computed(() => config.value && draft.value && (config.value.enabled !== draft.value.enabled || config.value.retention_days !== draft.value.retention_days || config.value.session_ban_enabled !== draft.value.session_ban_enabled))
const errorText = (error: unknown) => extractI18nErrorMessage(error, t, 'admin.requestAudit.errors', t('admin.requestAudit.failed'))
const stateLabel = (state: string) => states.includes(state) ? t(`admin.requestAudit.states.${state}`) : state
const kindLabel = (kind: RequestAuditRecord['kind']) => kinds.includes(kind) ? t(`admin.requestAudit.kinds.${kind}`) : kind
const userName = (record: RequestAuditRecord) => record.user_username || record.user_email || (record.user_id ? t('admin.requestAudit.userFallback', { id: record.user_id }) : t('admin.requestAudit.unidentifiedUser'))
const keyName = (record: RequestAuditRecord) => record.api_key_name || (record.api_key_id ? t('admin.requestAudit.keyFallback', { id: record.api_key_id }) : t('admin.requestAudit.unidentifiedKey'))
const groupName = (record: RequestAuditRecord) => record.group_name || (record.group_id ? t('admin.requestAudit.groupFallback', { id: record.group_id }) : t('admin.requestAudit.unidentifiedGroup'))
const formatDate = (value: string) => new Date(value).toLocaleString(locale.value)
const formatBytes = (bytes: number) => bytes < 1024 ? `${bytes} B` : bytes < 1048576 ? `${(bytes / 1024).toFixed(1)} KiB` : `${(bytes / 1048576).toFixed(1)} MiB`
const banState = (ban: RequestAuditBan) => ban.revoked_at ? 'revoked' : ban.expires_at && Date.parse(ban.expires_at) <= Date.now() ? 'expired' : 'active'

function buildFilters(): RequestAuditFilters {
  const from = filters.from ? new Date(filters.from).toISOString() : undefined
  const to = filters.to ? new Date(filters.to).toISOString() : undefined
  if (from && to && from > to) throw new Error(t('admin.requestAudit.invalidTimeWindow'))
  return { from, to, api_key_id: filters.apiKey ? Number(filters.apiKey) : undefined, user_id: filters.user ? Number(filters.user) : undefined, session_id: filters.session || undefined, kind: filters.kind || undefined, capture_state: filters.state || undefined, http_status: filters.httpStatus ? Number(filters.httpStatus) : undefined, limit: 50 }
}
async function loadRecords() {
  const generation = ++recordsGeneration
  loading.list = true; errors.list = ''
  try { const result = await api.list({ ...appliedFilters.value, cursor: cursor.value || undefined }); if (generation === recordsGeneration) records.value = result } catch (error) { if (generation === recordsGeneration) { errors.list = errorText(error); records.value = { items: [], next_cursor: '', has_more: false } } } finally { if (generation === recordsGeneration) loading.list = false }
}
function search() {
  try { appliedFilters.value = buildFilters(); cursor.value = ''; cursorHistory.value = []; void loadRecords() } catch (error) { errors.list = errorText(error) }
}
function resetFilters() { userFilterRevision.value++; Object.assign(filters, emptyFilters); search() }
function nextPage() { if (loading.list || !records.value.has_more) return; cursorHistory.value.push(cursor.value); cursor.value = records.value.next_cursor; void loadRecords() }
function previousPage() { if (loading.list || !cursorHistory.value.length) return; cursor.value = cursorHistory.value.pop() || ''; void loadRecords() }
async function loadConfig() {
  if (loading.config || loading.save) return
  loading.config = true; errors.config = ''
  try { config.value = await api.getConfig(); draft.value = { ...config.value } } catch (error) { errors.config = errorText(error) } finally { loading.config = false }
}
async function loadStatus() {
  if (loading.status) return
  loading.status = true; errors.status = ''
  try { status.value = await api.status() } catch (error) { errors.status = errorText(error); status.value = null } finally { loading.status = false }
}
function requestSaveConfig() {
  if (!draft.value || loading.save) return
  if (draft.value.retention_days < (config.value?.retention_days || 0)) { confirmRetention.value = true; return }
  void saveConfig()
}
async function saveConfig() {
  confirmRetention.value = false
  if (!draft.value || loading.save || loading.config) return
  if (!Number.isInteger(draft.value.retention_days) || draft.value.retention_days < 1 || draft.value.retention_days > 30) { errors.config = t('admin.requestAudit.errors.INVALID_RETENTION_DAYS'); return }
  loading.save = true; errors.config = ''
  try { config.value = await api.updateConfig(draft.value); draft.value = { ...config.value }; app.showSuccess(t('admin.requestAudit.saved')); void loadStatus() } catch (error) { errors.config = errorText(error) } finally { loading.save = false }
}
function selectTab(tab: Tab) { activeTab.value = tab; if (tab === 'bans') void loadBans(); if (tab === 'settings') void loadStatus() }
function refresh() { void loadConfig(); void loadStatus(); if (activeTab.value === 'bans') void loadBans(); else void loadRecords() }
async function loadBans(next = '', append = false) {
  const generation = ++bansGeneration
  loading.bans = true; errors.bans = ''
  try {
    const result = await api.listBans({ api_key_id: banFilters.apiKey ? Number(banFilters.apiKey) : undefined, session_id: banFilters.session || undefined, state: (banFilters.state || undefined) as 'active' | 'expired' | 'revoked' | undefined, cursor: next || undefined, limit: 50 })
    if (generation === bansGeneration) bans.value = append ? { ...result, items: [...bans.value.items, ...result.items] } : result
  } catch (error) { if (generation === bansGeneration) errors.bans = errorText(error) } finally { if (generation === bansGeneration) loading.bans = false }
}
async function openDetail(id: string) {
  const generation = ++detailGeneration
  detailOpen.value = true; detail.value = null; detailBans.value = []; errors.detail = ''; loading.detail = true
  try {
    const result = await api.get(id)
    if (generation !== detailGeneration) return
    detail.value = result
    if (result.api_key_id && result.session_id) {
      const current = await api.listBans({ api_key_id: result.api_key_id, session_id: result.session_id, state: 'active', limit: 1 })
      if (generation === detailGeneration) detailBans.value = current.items
    }
  } catch (error) { if (generation === detailGeneration) errors.detail = errorText(error) } finally { if (generation === detailGeneration) loading.detail = false }
}
function closeDetail() { if (banOpen.value) return; detailGeneration++; detailOpen.value = false; detail.value = null; detailBans.value = []; loading.detail = false }
function showSessionRequests() {
  if (!detail.value?.api_key_id || !detail.value.session_id) return
  Object.assign(filters, emptyFilters, { apiKey: String(detail.value.api_key_id), session: detail.value.session_id })
  closeDetail(); activeTab.value = 'requests'; search()
}
function openBan() { if (!detail.value?.api_key_id || !detail.value.session_id || detailBans.value.length) return; Object.assign(banDraft, { public_reason: '', internal_note: '', expires: '' }); errors.createBan = ''; unknownBan.value = null; banOpen.value = true }
function closeBan() { if (!loading.createBan) banOpen.value = false }
function uncertainWrite(error: unknown) {
  const code = extractApiErrorCode(error)
  const status = error && typeof error === 'object' && 'status' in error ? Number(error.status) : undefined
  return code === 'BAN_RESULT_UNKNOWN' || status === 0 || (status !== undefined && status >= 500) || code === 'ERR_NETWORK' || code === 'ECONNABORTED'
}
async function createBan() {
  if (!detail.value || loading.createBan || unknownBan.value || !banDraft.public_reason.trim()) return
  if (banDraft.expires && (!Number.isFinite(Date.parse(banDraft.expires)) || Date.parse(banDraft.expires) <= Date.now())) { errors.createBan = t('admin.requestAudit.errors.INVALID_BAN_EXPIRY'); return }
  const sourceID = detail.value.id
  loading.createBan = true; errors.createBan = ''
  try {
    const created = await api.createBan({ source_audit_id: sourceID, public_reason: banDraft.public_reason.trim(), internal_note: banDraft.internal_note.trim(), expires_at: banDraft.expires ? new Date(banDraft.expires).toISOString() : null })
    detailBans.value = [created]; banOpen.value = false; app.showSuccess(t('admin.requestAudit.banned')); void loadBans()
  } catch (error) {
    if (uncertainWrite(error)) unknownBan.value = { id: String(extractApiErrorMetadata(error)?.ban_id || '') || undefined, sourceID }
    errors.createBan = errorText(error)
  } finally { loading.createBan = false }
}
async function confirmBanResult() {
  if (!unknownBan.value || loading.createBan) return
  const pending = unknownBan.value
  loading.createBan = true
  try {
    const current = pending.id ? [await api.getBan(pending.id)] : (await api.listBans({ source_audit_id: pending.sourceID, state: 'active', limit: 1 })).items
    const active = current.filter((ban) => banState(ban) === 'active')
    if (active.length) { detailBans.value = active; unknownBan.value = null; banOpen.value = false; app.showSuccess(t('admin.requestAudit.banned')); void loadBans() } else { allowConfirmedBanRetry() }
  } catch (error) {
    if (pending.id && extractApiErrorCode(error) === 'BAN_NOT_FOUND') allowConfirmedBanRetry()
    else errors.createBan = errorText(error)
  } finally { loading.createBan = false }
}
function allowConfirmedBanRetry() {
  unknownBan.value = null
  detailBans.value = []
  errors.createBan = t('admin.requestAudit.banRetryReady')
}
async function revokeBan() {
  if (!revokeTarget.value || loading.revoke) return
  const id = revokeTarget.value.id; revokeTarget.value = null; loading.revoke = true
  try { await api.revokeBan(id); app.showSuccess(t('admin.requestAudit.revoked')); await loadBans() } catch (error) {
    if (uncertainWrite(error)) {
      try { const current = await api.getBan(id); if (current.revoked_at) { app.showSuccess(t('admin.requestAudit.revoked')); await loadBans(); return } } catch { /* Keep the result explicitly unknown. */ }
      errors.bans = t('admin.requestAudit.resultUnknown')
    } else errors.bans = errorText(error)
  } finally { loading.revoke = false }
}

onMounted(() => { void loadConfig(); void loadStatus(); void loadRecords() })
onUnmounted(() => { detailGeneration++; recordsGeneration++; bansGeneration++ })
</script>

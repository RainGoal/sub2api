<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-end justify-between gap-4 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.upstreamBalances.title') }}</h2>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.description') }}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-secondary" :disabled="busy || !config?.wallets.length" @click="refreshAll"><Icon name="refresh" size="sm" />{{ t('admin.upstreamBalances.refreshAll') }}</button>
          <button class="btn btn-primary" :disabled="busy || !config || !sites.length || discoveryLoading || discoveryFailed" @click="openWallet()"><Icon name="plus" size="sm" />{{ t('admin.upstreamBalances.addWallet') }}</button>
        </div>
      </header>

      <div v-if="loading" class="flex min-h-48 items-center justify-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</div>
      <div v-else-if="loadError" role="alert" class="rounded border border-red-300 p-6 text-sm dark:border-red-900">
        <p>{{ t(forbidden ? 'admin.upstreamBalances.forbidden' : 'admin.upstreamBalances.loadFailed') }}</p>
        <button v-if="!forbidden" class="btn btn-secondary mt-4" @click="load">{{ t('admin.upstreamBalances.retry') }}</button>
      </div>
      <template v-else-if="config">
        <form class="flex flex-wrap items-end gap-4 rounded border border-gray-200 p-4 dark:border-dark-700" @submit.prevent="saveSettings">
          <div class="mr-auto self-center">
            <label class="flex items-center gap-2 text-sm font-medium"><input v-model="autoEnabled" data-test="auto-enabled" type="checkbox" :disabled="busy" />{{ t('admin.upstreamBalances.autoRefresh') }}</label>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.autoRefreshHint') }}</p>
          </div>
          <div>
            <label for="balance-interval" class="input-label">{{ t('admin.upstreamBalances.interval') }}</label>
            <input id="balance-interval" v-model.number="interval" class="input w-28" type="number" min="5" max="1440" step="1" required :disabled="busy" />
          </div>
          <button type="submit" class="btn btn-secondary" :disabled="busy || !settingsDirty">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
        </form>

        <div class="grid grid-cols-3 divide-x divide-gray-200 rounded border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
          <div class="p-4"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.walletCount') }}</p><p class="mt-1 text-2xl font-semibold tabular-nums">{{ config.wallets.length }}</p></div>
          <div class="p-4"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.lowCount') }}</p><p class="mt-1 text-2xl font-semibold tabular-nums text-amber-600 dark:text-amber-400">{{ lowCount }}</p></div>
          <div class="p-4"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.errorCount') }}</p><p class="mt-1 text-2xl font-semibold tabular-nums" :class="errorCount ? 'text-red-500' : ''">{{ errorCount }}</p></div>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
          <p>{{ t('admin.upstreamBalances.priorityHint') }}</p>
          <button class="inline-flex items-center gap-1 hover:text-primary-500" :disabled="busy || discoveryLoading" @click="load"><Icon name="refresh" size="sm" />{{ t('admin.upstreamBalances.reload') }}</button>
        </div>
        <div v-if="discoveryFailed" role="alert" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.upstreamBalances.discoveryFailed') }} <button class="underline" :disabled="discoveryLoading" @click="discover">{{ t('admin.upstreamBalances.retry') }}</button></div>
        <DataTable :columns="columns" :data="rows" row-key="id">
          <template #cell-name="{ row }">
            <p class="font-medium">{{ row.name }}</p>
            <p class="mt-1 max-w-72 break-all text-xs text-gray-500 dark:text-gray-400">{{ row.site_url }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.accountCount', { count: row.snapshot?.account_count ?? accountCount(row) }) }} · {{ t(row.single_account ? 'admin.upstreamBalances.merged' : 'admin.upstreamBalances.manualGroup') }}</p>
            <p v-if="!row.enabled" class="mt-1 text-xs text-gray-500">{{ t('admin.upstreamBalances.manualOnly') }}</p>
          </template>
          <template #cell-balance="{ row }">
            <p class="whitespace-nowrap font-mono text-lg font-semibold tabular-nums" :class="isLow(row.snapshot) ? 'text-amber-600 dark:text-amber-400' : ''">{{ balanceText(row.snapshot) }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.thresholdValue', { amount: row.threshold }) }}</p>
            <p v-if="isStale(row.snapshot)" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ t('admin.upstreamBalances.historical') }}</p>
          </template>
          <template #cell-status="{ row }">
            <span class="inline-block rounded px-2 py-1 text-xs font-medium" :class="statusClass(row.snapshot)">{{ statusText(row.snapshot) }}</span>
            <p v-if="row.snapshot?.error_code" class="mt-2 max-w-64 text-xs text-gray-500 dark:text-gray-400">{{ errorText(row.snapshot.error_code) }}</p>
          </template>
          <template #cell-updated="{ row }">
            <p class="whitespace-nowrap text-xs">{{ timeText(row.snapshot?.last_success_at) }}</p>
            <p v-if="row.snapshot?.last_attempt_at && row.snapshot.status !== 'ok'" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.upstreamBalances.lastAttempt', { time: timeText(row.snapshot.last_attempt_at) }) }}</p>
          </template>
          <template #cell-actions="{ row }">
            <div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary btn-sm" :disabled="busy" @click="refreshWallet(row.id)">{{ t(refreshing.has(row.id) ? 'admin.upstreamBalances.refreshing' : 'common.refresh') }}</button>
              <a v-if="safeRechargeURL(row.recharge_url)" class="btn btn-secondary btn-sm" :href="safeRechargeURL(row.recharge_url)" target="_blank" rel="noopener noreferrer">{{ t('admin.upstreamBalances.recharge') }}</a>
              <button class="btn btn-secondary btn-sm" :disabled="busy || discoveryFailed || discoveryLoading" @click="openWallet(row)">{{ t('common.edit') }}</button>
              <button class="btn btn-secondary btn-sm" :disabled="busy" @click="removing = row">{{ t('common.delete') }}</button>
            </div>
          </template>
          <template #empty>
            <div class="py-8 text-center">
              <Icon name="creditCard" size="xl" class="mx-auto text-gray-400" />
              <p class="mt-3 font-medium">{{ t('admin.upstreamBalances.empty') }}</p>
              <p class="mx-auto mt-2 max-w-xl text-sm text-gray-500 dark:text-gray-400">{{ t(sites.length ? 'admin.upstreamBalances.emptyHint' : 'admin.upstreamBalances.noSites') }}</p>
              <button v-if="sites.length" class="btn btn-primary mt-4" :disabled="busy" @click="openWallet()">{{ t('admin.upstreamBalances.addWallet') }}</button>
            </div>
          </template>
        </DataTable>
      </template>
    </div>
    <UpstreamWalletDialog v-if="dialogOpen && config" :show="dialogOpen" :wallet="editing" :wallets="config.wallets" :sites="sites" :saving="saving" @close="dialogOpen = false" @save="saveWallet" />
    <ConfirmDialog :show="!!removing" :title="t('admin.upstreamBalances.removeTitle')" :message="t('admin.upstreamBalances.removeHint', { name: removing?.name ?? '' })" danger @cancel="removing = null" @confirm="removeWallet" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import UpstreamWalletDialog from '@/components/admin/UpstreamWalletDialog.vue'
import { useAppStore } from '@/stores'
import { upstreamBalancesAPI, type UpstreamBalanceConfig, type UpstreamBalanceSite, type UpstreamBalanceSnapshot, type UpstreamWallet } from '@/api/admin/upstreamBalances'

const { t, te, locale } = useI18n()
const appStore = useAppStore()
const config = ref<UpstreamBalanceConfig | null>(null)
const snapshots = ref<UpstreamBalanceSnapshot[]>([])
const sites = ref<UpstreamBalanceSite[]>([])
const loading = ref(true)
const saving = ref(false)
const loadError = ref(false)
const forbidden = ref(false)
const discoveryLoading = ref(false)
const discoveryFailed = ref(false)
const autoEnabled = ref(false)
const interval = ref(30)
const dialogOpen = ref(false)
const editing = ref<UpstreamWallet | null>(null)
const removing = ref<UpstreamWallet | null>(null)
const refreshing = ref(new Set<string>())
const refreshingAll = ref(false)
const busy = computed(() => loading.value || saving.value || refreshingAll.value || refreshing.value.size > 0)
const settingsDirty = computed(() => config.value && (autoEnabled.value !== config.value.enabled || interval.value !== config.value.interval_minutes))
const now = ref(Date.now())
const rows = computed(() => (config.value?.wallets ?? []).map(wallet => ({ ...wallet, snapshot: snapshots.value.find(item => item.wallet_id === wallet.id) })).sort((a, b) => Number(isLow(b.snapshot)) - Number(isLow(a.snapshot)) || a.name.localeCompare(b.name)))
const lowCount = computed(() => rows.value.filter(row => isLow(row.snapshot)).length)
const errorCount = computed(() => rows.value.filter(row => row.snapshot?.error_code || ['failed', 'unsupported'].includes(row.snapshot?.status ?? '')).length)
const columns = computed(() => [
  { key: 'name', label: t('admin.upstreamBalances.walletName') },
  { key: 'balance', label: t('admin.upstreamBalances.balance') },
  { key: 'status', label: t('admin.upstreamBalances.queryStatus') },
  { key: 'updated', label: t('admin.upstreamBalances.lastSuccess') },
  { key: 'actions', label: t('common.actions') },
])

async function discover() {
  discoveryLoading.value = true
  try {
    sites.value = (await upstreamBalancesAPI.discover()).sites ?? []
    discoveryFailed.value = false
  } catch {
    discoveryFailed.value = true
  } finally {
    discoveryLoading.value = false
  }
}

async function load() {
  loading.value = true
  loadError.value = false
  forbidden.value = false
  try {
    const result = await upstreamBalancesAPI.list()
    config.value = result.config
    snapshots.value = result.items ?? []
    autoEnabled.value = result.config.enabled
    interval.value = result.config.interval_minutes
    await discover()
  } catch (error) {
    loadError.value = true
    forbidden.value = (error as { status?: number }).status === 403
  } finally {
    loading.value = false
  }
}

function errorText(code: string): string {
  const key = `admin.upstreamBalances.errors.${code}`
  return te(key) ? t(key) : t('admin.upstreamBalances.queryFailed')
}

async function persist(next: UpstreamBalanceConfig): Promise<boolean> {
  if (saving.value) return false
  saving.value = true
  try {
    config.value = await upstreamBalancesAPI.saveConfig(next)
    appStore.showSuccess(t('admin.upstreamBalances.saved'))
    return true
  } catch (error) {
    const code = (error as { code?: string; status?: number }).code
    appStore.showError((error as { status?: number }).status === 409 ? t('admin.upstreamBalances.conflict') : code && te(`admin.upstreamBalances.errors.${code}`) ? errorText(code) : t('admin.upstreamBalances.saveFailed'))
    return false
  } finally {
    saving.value = false
  }
}

async function saveSettings() {
  if (!config.value || busy.value) return
  if (!Number.isInteger(interval.value) || interval.value < 5 || interval.value > 1440) {
    appStore.showError(t('admin.upstreamBalances.validationInterval'))
    return
  }
  await persist({ ...config.value, enabled: autoEnabled.value, interval_minutes: interval.value })
}

function openWallet(wallet?: UpstreamWallet) {
  editing.value = config.value?.wallets.find(item => item.id === wallet?.id) ?? null
  dialogOpen.value = true
}

async function saveWallet(wallet: UpstreamWallet) {
  if (!config.value || busy.value) return
  const wallets = wallet.id ? config.value.wallets.map(item => item.id === wallet.id ? wallet : item) : [...config.value.wallets, wallet]
  if (await persist({ ...config.value, wallets })) {
    dialogOpen.value = false
    await updateCachedSnapshots()
  }
}

async function removeWallet() {
  if (!config.value || !removing.value || busy.value) return
  const id = removing.value.id
  removing.value = null
  if (await persist({ ...config.value, wallets: config.value.wallets.filter(item => item.id !== id) })) {
    snapshots.value = snapshots.value.filter(item => item.wallet_id !== id)
  }
}

let snapshotRevision = 0
let disposed = false
async function refreshWallet(id: string, notify = true): Promise<boolean> {
  if (refreshing.value.has(id) || saving.value) return false
  snapshotRevision++
  refreshing.value.add(id)
  try {
    const snapshot = await upstreamBalancesAPI.refresh(id)
    snapshots.value = [...snapshots.value.filter(item => item.wallet_id !== id), snapshot]
    now.value = Date.now()
    if (notify && !disposed) {
      if (snapshot.status === 'ok') appStore.showSuccess(t('admin.upstreamBalances.refreshed'))
      else appStore.showError(snapshot.error_code ? errorText(snapshot.error_code) : statusText(snapshot))
    }
    return snapshot.status === 'ok'
  } catch (error) {
    const code = (error as { code?: string }).code
    if (notify && !disposed) appStore.showError(code ? errorText(code) : t('admin.upstreamBalances.queryFailed'))
    return false
  } finally {
    refreshing.value.delete(id)
  }
}

async function refreshAll() {
  if (!config.value || busy.value) return
  refreshingAll.value = true
  const pending = config.value.wallets.map(wallet => wallet.id)
  let successful = 0
  const worker = async () => {
    while (pending.length && !disposed) {
      const id = pending.shift()!
      if (await refreshWallet(id, false)) successful++
    }
  }
  try {
    await Promise.all([worker(), worker()])
    if (!disposed) appStore.showInfo(t('admin.upstreamBalances.refreshSummary', { success: successful, total: config.value.wallets.length }))
  } finally {
    refreshingAll.value = false
  }
}

async function updateCachedSnapshots() {
  const revision = snapshotRevision
  const version = config.value?.version
  try {
    const result = await upstreamBalancesAPI.list()
    if (revision === snapshotRevision && version === config.value?.version && result.config.version === version) snapshots.value = result.items ?? []
  } catch {
    // A cache read failure preserves the last known balance; this never queries an upstream.
  }
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  timer = setInterval(() => {
    now.value = Date.now()
    if (!document.hidden && !busy.value && !dialogOpen.value && !loadError.value) void updateCachedSnapshots()
  }, 60000)
})
onUnmounted(() => {
  disposed = true
  clearInterval(timer)
})

function accountCount(wallet: UpstreamWallet): number {
  return wallet.single_account ? sites.value.find(site => site.site_url === wallet.site_url)?.accounts.length ?? 0 : wallet.account_ids.length
}

function balanceText(snapshot?: UpstreamBalanceSnapshot): string {
  if (snapshot?.kind !== 'wallet' || snapshot.balance == null || !Number.isFinite(snapshot.balance)) return '—'
  return `${snapshot.currency || 'USD'} ${new Intl.NumberFormat(locale.value, { minimumFractionDigits: 2, maximumFractionDigits: 4 }).format(snapshot.balance)}`
}

function isStale(snapshot?: UpstreamBalanceSnapshot): boolean {
  return !!snapshot && snapshot.balance != null && (snapshot.status !== 'ok' || (!!snapshot.fresh_until && new Date(snapshot.fresh_until).getTime() < now.value))
}

function isLow(snapshot?: UpstreamBalanceSnapshot): boolean {
  return snapshot?.kind === 'wallet' && snapshot.status === 'ok' && !!snapshot.low_balance && !isStale(snapshot)
}

function statusText(snapshot?: UpstreamBalanceSnapshot): string {
  if (snapshot?.status === 'unconfigured' && snapshot.error_code) return t('admin.upstreamBalances.needsConfiguration')
  if (snapshot?.status === 'unsupported') {
    if (snapshot.kind === 'key_quota') return t('admin.upstreamBalances.keyQuota')
    if (snapshot.kind === 'subscription') return t('admin.upstreamBalances.subscription')
  }
  if (snapshot?.status === 'ok' && isStale(snapshot)) return t('admin.upstreamBalances.stale')
  if (isLow(snapshot)) return t('admin.upstreamBalances.lowBalance')
  return t(`admin.upstreamBalances.status.${snapshot?.status ?? 'unconfigured'}`)
}

function statusClass(snapshot?: UpstreamBalanceSnapshot): string {
  if (isLow(snapshot) || (snapshot?.status === 'ok' && isStale(snapshot))) return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  if (snapshot?.status === 'failed' || snapshot?.status === 'unsupported') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (snapshot?.status === 'ok') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
}

function timeText(value?: string | null): string {
  if (!value) return t('admin.upstreamBalances.never')
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? t('admin.upstreamBalances.never') : date.toLocaleString(locale.value)
}

function safeRechargeURL(value: string): string | undefined {
  try {
    const url = new URL(value)
    if (['http:', 'https:'].includes(url.protocol) && !url.username && !url.password) return url.href
  } catch { /* Invalid saved links are never made clickable. */ }
  return undefined
}
</script>

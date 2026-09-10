<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import type { Column } from '@/components/common/types'
import { salesAPI, type SalesPartner, type SalesPartnerInput, type SalesOverview, type SalesCustomer, type SalesLedger, type SalesSettlement } from '@/api/admin/sales'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMetadata, extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

type Tab = 'partners' | 'customers' | 'ledger' | 'settlements'
type Action = 'createSettlement' | 'confirm' | 'pay' | 'adjust'
const { t, te, locale } = useI18n()
const app = useAppStore()
const tab = ref<Tab>('partners')
const selected = ref<SalesPartner | null>(null)
const overview = ref<SalesOverview | null>(null)
const records = ref<(SalesPartner | SalesCustomer | SalesLedger | SalesSettlement)[]>([])
const loading = ref(false)
const error = ref('')
const page = ref(1)
const total = ref(0)
const pageSize = ref(20)
const filters = reactive({ search: '', start_date: '', end_date: '' })
const settings = reactive({ enabled: false, main_frontend_url: '' })
const settingsLoaded = ref(false)
const settingsOpen = ref(false)
const settingsForm = reactive({ enabled: false, main_frontend_url: '' })
const partnerOpen = ref(false)
const editingID = ref<number>()
const partnerForm = reactive<SalesPartnerInput>(newPartner())
const saving = ref(false)
const dialogError = ref('')
const action = ref<Action | null>(null)
const settlement = ref<SalesSettlement | null>(null)
const finance = reactive({ month: '', payment_reference: '', commission: 0, source_ledger_id: '', note: '', request_key: '' })
let requestSequence = 0
const switches = ['promotion_enabled', 'accrual_enabled', 'payout_frozen'] as const
const monetaryFields = ['revenue', 'cost', 'profit', 'commission', 'net_commission', 'payout_amount', 'carry_amount']
const metricKeys = ['customer_count', 'revenue', 'cost', 'profit', 'commission', 'unsettled_commission', 'pending_payout', 'paid_commission'] as const
const lastClosedMonth = computed(() => { const d = new Date(); return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() - 1, 1)).toISOString().slice(0, 7) })
const referral = computed(() => selected.value && settings.main_frontend_url ? `${settings.main_frontend_url}/api/v1/sales/referral?code=${encodeURIComponent(selected.value.code)}` : '')
const columns = computed<Column[]>(() => {
  const keys = tab.value === 'partners' ? ['name', 'user_id', 'hostname', 'commission_rate', ...switches, 'actions']
    : tab.value === 'customers' ? ['email', 'user_id', 'created_at', 'revenue', 'profit', 'commission']
    : tab.value === 'ledger' ? ['id', 'occurred_at', 'customer_user_id', 'kind', 'revenue', 'cost', 'profit', 'commission_rate', 'commission', 'settlement_id', 'note']
    : ['id', 'month', 'net_commission', 'payout_amount', 'carry_amount', 'status', 'payment_reference', 'actions']
  return keys.map(key => ({ key, label: key === 'id' ? 'ID' : key === 'user_id' ? t('sales.userId') : key === 'actions' ? t('common.actions') : t(`sales.${key}`), class: monetaryFields.includes(key) ? 'text-right font-mono' : '' }))
})
function money(value: number) { return new Intl.NumberFormat(locale.value, { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 8 }).format(value) }
function newPartner(): SalesPartnerInput { return { user_id: 0, name: '', code: '', hostname: '', commission_rate: 0, promotion_enabled: true, accrual_enabled: true, payout_frozen: false } }
function message(cause: unknown) {
  if (extractApiErrorCode(cause) === 'SALES_INVALID') {
    const field = extractApiErrorMetadata(cause)?.field
    const key = typeof field === 'string' ? `sales.validation.${field}` : ''
    if (key && te(key)) return t(key)
  }
  return extractI18nErrorMessage(cause, t, 'sales.errors', t('sales.loadError'))
}
function params() { return { page: page.value, page_size: pageSize.value, partner_id: selected.value?.id, search: filters.search || undefined, start_date: filters.start_date || undefined, end_date: filters.end_date || undefined } }
async function load() {
  if (filters.start_date && filters.end_date && filters.start_date > filters.end_date) { error.value = t('sales.invalidDates'); return }
  const seq = ++requestSequence
  loading.value = true; error.value = ''; records.value = []; overview.value = null
  try {
    const query = params()
    const [list, summary] = await Promise.all([salesAPI[tab.value](query), selected.value ? salesAPI.overview(selected.value.id, query) : Promise.resolve(null)])
    if (seq !== requestSequence) return
    records.value = list.items || []; total.value = list.total; overview.value = summary
    if (summary) selected.value = summary.partner
  } catch (cause) { if (seq === requestSequence) error.value = message(cause) }
  finally { if (seq === requestSequence) loading.value = false }
}
function changeTab(value: Tab) { tab.value = value; page.value = 1; void load() }
function select(partner: SalesPartner) { selected.value = partner; filters.search = ''; changeTab('customers') }
function back() { selected.value = null; overview.value = null; changeTab('partners') }
function changePage(value: number) { page.value = value; void load() }
function filter() { page.value = 1; void load() }
function edit(partner?: SalesPartner) {
  editingID.value = partner?.id; Object.assign(partnerForm, partner || newPartner()); dialogError.value = ''; partnerOpen.value = true
}
async function openSettings() {
  try { Object.assign(settings, await salesAPI.settings()); settingsLoaded.value = true; Object.assign(settingsForm, settings); dialogError.value = ''; settingsOpen.value = true }
  catch (cause) { app.showError(message(cause)) }
}
function openAction(value: Action, item?: SalesSettlement) {
  action.value = value; settlement.value = item || null; dialogError.value = ''
  const requestKey = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
  Object.assign(finance, { month: lastClosedMonth.value, payment_reference: '', commission: 0, source_ledger_id: '', note: '', request_key: requestKey })
}
async function save(kind: 'settings' | 'partner' | 'finance') {
  if (saving.value) return
  if (kind === 'partner' && !/^[a-z0-9][a-z0-9-]{2,47}$/i.test(partnerForm.code.trim())) {
    dialogError.value = t('sales.validation.code')
    return
  }
  saving.value = true; dialogError.value = ''
  try {
    if (kind === 'settings') { Object.assign(settings, await salesAPI.saveSettings({ ...settingsForm })); settingsLoaded.value = true; settingsOpen.value = false }
    else if (kind === 'partner') {
      const saved = await salesAPI.savePartner({ user_id: partnerForm.user_id, name: partnerForm.name, code: partnerForm.code, hostname: partnerForm.hostname, commission_rate: partnerForm.commission_rate, promotion_enabled: partnerForm.promotion_enabled, accrual_enabled: partnerForm.accrual_enabled, payout_frozen: partnerForm.payout_frozen }, editingID.value)
      if (selected.value?.id === saved.id) selected.value = saved
      partnerOpen.value = false
    } else {
      if (!selected.value) return
      if (action.value === 'createSettlement') await salesAPI.createSettlement({ partner_id: selected.value.id, month: finance.month, request_key: finance.request_key })
      if (action.value === 'confirm' && settlement.value) await salesAPI.confirmSettlement(settlement.value.id, finance.request_key)
      if (action.value === 'pay' && settlement.value) await salesAPI.paySettlement(settlement.value.id, { request_key: finance.request_key, payment_reference: finance.payment_reference })
      if (action.value === 'adjust') await salesAPI.adjust({ partner_id: selected.value.id, source_ledger_id: finance.source_ledger_id ? Number(finance.source_ledger_id) : undefined, commission: finance.commission, note: finance.note, request_key: finance.request_key })
      action.value = null
    }
    app.showSuccess(t('sales.saved')); void load()
  } catch (cause) { dialogError.value = message(cause) }
  finally { saving.value = false }
}
onMounted(() => { void load(); void salesAPI.settings().then(value => { Object.assign(settings, value); settingsLoaded.value = true }).catch(cause => app.showError(message(cause))) })
</script>

<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-3">
        <h2 v-if="selected" class="text-xl font-semibold text-gray-900 dark:text-white">{{ selected.name }}</h2>
        <h2 v-else class="text-xl font-semibold text-gray-900 dark:text-white sm:hidden">{{ t('sales.title') }}</h2>
        <div class="ml-auto flex flex-wrap gap-2">
          <button v-if="selected" class="btn btn-secondary" @click="back">{{ t('sales.back') }}</button>
          <button class="btn btn-secondary" @click="openSettings">{{ t('sales.settings') }}</button>
          <button class="btn btn-primary" @click="edit(selected || undefined)">{{ t(selected ? 'sales.editPartner' : 'sales.addPartner') }}</button>
        </div>
      </header>
      <p v-if="settingsLoaded && !settings.enabled" class="rounded-lg border border-amber-300/30 bg-amber-500/10 p-3 text-sm text-amber-700 dark:text-amber-300">{{ t('sales.errors.SALES_DISABLED') }}</p>
      <section v-if="selected" class="card space-y-3 p-4">
        <div class="flex flex-wrap items-center justify-between gap-2"><span class="font-mono text-sm">{{ selected.hostname }} · {{ selected.commission_rate }}%</span><span v-if="selected.payout_frozen" class="text-sm text-amber-600 dark:text-amber-300">{{ t('sales.frozen') }}</span></div>
        <label class="block text-sm"><span class="text-gray-500 dark:text-dark-400">{{ t('sales.referral') }}</span><input class="input mt-1 font-mono text-xs" :value="referral" readonly @focus="($event.target as HTMLInputElement).select()" /></label>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.referralHint') }}</p>
      </section>
      <section v-if="overview" class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="key in metricKeys" :key="key" class="card min-w-0 p-4"><p class="text-xs text-gray-500 dark:text-dark-400">{{ t(`sales.${key}`) }}</p><p class="mt-2 break-all font-mono text-lg font-semibold">{{ key === 'customer_count' ? overview[key] : money(overview[key]) }}</p></div>
      </section>
      <p v-if="overview?.pending_events" class="text-sm text-amber-600 dark:text-amber-300">{{ t('sales.pending', { count: overview.pending_events }) }}</p>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.scope') }}</p>
      <div v-if="selected" class="flex flex-wrap gap-2" role="tablist">
        <button v-for="item in (['customers', 'ledger', 'settlements'] as const)" :key="item" class="btn" :class="tab === item ? 'btn-primary' : 'btn-secondary'" role="tab" :aria-selected="tab === item" @click="changeTab(item)">{{ t(`sales.${item}`) }}</button>
      </div>
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="filter">
        <label v-if="!selected" class="min-w-48 flex-1 text-xs"><span>{{ t('sales.search') }}</span><input v-model="filters.search" class="input mt-1" type="search" /></label>
        <template v-else><label class="text-xs"><span>{{ t('sales.start') }}</span><input v-model="filters.start_date" class="input mt-1" type="date" /></label><label class="text-xs"><span>{{ t('sales.end') }}</span><input v-model="filters.end_date" class="input mt-1" type="date" /></label></template>
        <button class="btn btn-secondary" :disabled="loading">{{ t('sales.apply') }}</button>
        <button v-if="tab === 'settlements'" type="button" class="btn btn-primary" :disabled="loading || !!overview?.pending_events" @click="openAction('createSettlement')">{{ t('sales.createSettlement') }}</button>
        <button v-if="tab === 'ledger'" type="button" class="btn btn-secondary" @click="openAction('adjust')">{{ t('sales.adjust') }}</button>
      </form>
      <p v-if="selected" class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.dateHint') }}</p>
      <div v-if="error" role="alert" class="card space-y-3 p-5"><p class="text-red-600 dark:text-red-400">{{ error }}</p><button class="btn btn-secondary" @click="load">{{ t('sales.retry') }}</button></div>
      <div v-else class="card overflow-hidden">
        <DataTable :columns="columns" :data="records" :loading="loading">
          <template v-for="key in monetaryFields" :key="key" #[`cell-${key}`]="{ row }"><span class="font-mono">{{ money(row[key]) }}</span></template>
          <template v-for="key in switches" :key="key" #[`cell-${key}`]="{ row }">{{ t(row[key] ? 'sales.active' : 'sales.inactive') }}</template>
          <template #cell-commission_rate="{ row }">{{ row.commission_rate }}%</template>
          <template #cell-kind="{ row }">{{ t(`sales.${row.kind}`) }}</template>
          <template #cell-status="{ row }">{{ t(`sales.${row.status}`) }}</template>
          <template #cell-created_at="{ row }">{{ formatDateTime(row.created_at) }}</template>
          <template #cell-occurred_at="{ row }">{{ formatDateTime(row.occurred_at) }}</template>
          <template #cell-note="{ row }"><span class="inline-block max-w-64 whitespace-normal break-words">{{ row.note || '—' }}</span></template>
          <template #cell-payment_reference="{ row }"><span class="inline-block max-w-52 whitespace-normal break-all">{{ row.payment_reference || '—' }}</span></template>
          <template #cell-actions="{ row }"><div class="flex flex-wrap gap-2">
            <template v-if="tab === 'partners'"><button class="btn btn-secondary btn-sm" @click="select(row)">{{ t('sales.view') }}</button><button class="btn btn-secondary btn-sm" @click="edit(row)">{{ t('common.edit') }}</button></template>
            <button v-else-if="row.status === 'draft'" class="btn btn-secondary btn-sm" @click="openAction('confirm', row)">{{ t('sales.confirm') }}</button>
            <button v-else-if="row.status === 'confirmed'" class="btn btn-primary btn-sm" :disabled="selected?.payout_frozen" @click="openAction('pay', row)">{{ t('sales.pay') }}</button>
          </div></template>
          <template #empty><p class="py-6 text-gray-500 dark:text-dark-400">{{ t('sales.empty') }}</p></template>
        </DataTable>
        <Pagination v-if="total > 0 && !loading" :page="page" :total="total" :page-size="pageSize" @update:page="changePage" @update:page-size="value => { pageSize = value; filter() }" />
      </div>
    </div>

    <BaseDialog :show="settingsOpen" :title="t('sales.settings')" :show-close-button="!saving" :close-on-escape="!saving" @close="settingsOpen = false">
      <form id="sales-settings" class="space-y-4" @submit.prevent="save('settings')">
        <label class="flex items-center gap-2"><input v-model="settingsForm.enabled" type="checkbox" :disabled="saving" />{{ t('sales.enabled') }}</label>
        <label class="block text-sm">{{ t('sales.mainUrl') }}<input v-model.trim="settingsForm.main_frontend_url" class="input mt-1" type="url" :required="settingsForm.enabled" :disabled="saving" placeholder="https://app.example.com" /></label>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.mainUrlHint') }}</p><p v-if="dialogError" role="alert" class="text-sm text-red-500">{{ dialogError }}</p>
      </form>
      <template #footer><button class="btn btn-primary" form="sales-settings" :disabled="saving">{{ t('common.save') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="partnerOpen" :title="t(editingID ? 'sales.editPartner' : 'sales.addPartner')" :show-close-button="!saving" :close-on-escape="!saving" @close="partnerOpen = false">
      <form id="sales-partner" class="space-y-4" @submit.prevent="save('partner')">
        <label class="block text-sm">{{ t('sales.name') }}<input v-model.trim="partnerForm.name" class="input mt-1" required maxlength="100" :disabled="saving" /></label>
        <label class="block text-sm">{{ t('sales.userId') }}<input v-model.number="partnerForm.user_id" class="input mt-1" type="number" min="1" step="1" required :disabled="!!editingID || saving" /></label>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.userIdHint') }}</p>
        <label class="block text-sm">{{ t('sales.code') }}<input v-model.trim="partnerForm.code" class="input mt-1 font-mono" required maxlength="48" aria-describedby="sales-code-hint" :disabled="saving" /></label>
        <p id="sales-code-hint" class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.codeHint') }}</p>
        <label class="block text-sm">{{ t('sales.hostname') }}<input v-model.trim="partnerForm.hostname" class="input mt-1 font-mono" required maxlength="253" :disabled="saving" /></label>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.hostnameHint') }}</p>
        <label class="block text-sm">{{ t('sales.commission_rate') }} (%)<input v-model.number="partnerForm.commission_rate" class="input mt-1" type="number" min="0" max="100" step="0.0001" required :disabled="saving" /></label>
        <label v-for="key in switches" :key="key" class="flex items-center gap-2 text-sm"><input v-model="partnerForm[key]" type="checkbox" :disabled="saving" />{{ t(`sales.${key}`) }}</label>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('sales.ruleHint') }}</p><p v-if="dialogError" role="alert" class="text-sm text-red-500">{{ dialogError }}</p>
      </form>
      <template #footer><button class="btn btn-primary" form="sales-partner" :disabled="saving">{{ t('common.save') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="!!action" :title="action ? t(`sales.${action}`) : ''" :show-close-button="!saving" :close-on-escape="!saving" @close="action = null">
      <form id="sales-finance" class="space-y-4" @submit.prevent="save('finance')">
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ t(`sales.${action === 'createSettlement' ? 'settlement' : action === 'adjust' ? 'adjustment' : action}Hint`) }}</p>
        <label v-if="action === 'createSettlement'" class="block text-sm">{{ t('sales.month') }}<input v-model="finance.month" class="input mt-1" type="month" :max="lastClosedMonth" required :disabled="saving" /></label>
        <dl v-if="settlement" class="space-y-2 text-sm"><div v-for="key in (['net_commission', 'payout_amount', 'carry_amount'] as const)" :key="key" class="flex justify-between gap-4"><dt>{{ t(`sales.${key}`) }}</dt><dd class="font-mono">{{ money(settlement[key]) }}</dd></div></dl>
        <label v-if="action === 'pay'" class="block text-sm">{{ t('sales.payment_reference') }}<input v-model.trim="finance.payment_reference" class="input mt-1" required maxlength="200" :disabled="saving" /></label>
        <template v-if="action === 'adjust'">
          <label class="block text-sm">{{ t('sales.amount') }}<input v-model.number="finance.commission" class="input mt-1" type="number" step="0.00000001" required :disabled="saving" /></label>
          <label class="block text-sm">{{ t('sales.sourceLedger') }}<input v-model="finance.source_ledger_id" class="input mt-1" type="number" min="1" step="1" :disabled="saving" /></label>
          <label class="block text-sm">{{ t('sales.note') }}<textarea v-model.trim="finance.note" class="input mt-1" required maxlength="1000" :disabled="saving" /></label>
        </template>
        <p v-if="dialogError" role="alert" class="text-sm text-red-500">{{ dialogError }}</p>
      </form>
      <template #footer><button class="btn btn-primary" form="sales-finance" :disabled="saving || (action === 'adjust' && finance.commission === 0)">{{ action ? t(`sales.${action}`) : '' }}</button></template>
    </BaseDialog>
  </AppLayout>
</template>

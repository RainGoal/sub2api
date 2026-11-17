<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('profit.title') }}</h1>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('profit.description') }}</p>
        </div>
        <div class="flex flex-wrap items-end gap-2">
          <label class="text-xs text-gray-500 dark:text-gray-400">
            <span class="mb-1 block">{{ t('profit.startDate') }}</span>
            <input v-model="startDate" type="date" class="input w-36" />
          </label>
          <label class="text-xs text-gray-500 dark:text-gray-400">
            <span class="mb-1 block">{{ t('profit.endDate') }}</span>
            <input v-model="endDate" type="date" class="input w-36" />
          </label>
          <button class="btn btn-primary" :disabled="loading || !isDateRangeValid" @click="loadReport">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('profit.refresh') }}
          </button>
        </div>
      </div>

      <div class="flex flex-wrap gap-2">
        <button v-for="preset in presets" :key="preset.days" type="button" class="btn btn-secondary text-xs" @click="setPreset(preset.days)">
          {{ preset.label }}
        </button>
      </div>

      <div v-if="loading" class="card flex min-h-48 items-center justify-center"><LoadingSpinner /></div>
      <div v-else-if="error" class="card flex min-h-48 flex-col items-center justify-center gap-3 p-6 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <button class="btn btn-secondary" @click="loadReport">{{ t('common.tryAgain') }}</button>
      </div>
      <template v-else-if="report">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('profit.currencyNote') }}</p>
        <div class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200" role="note">
          {{ t('profit.historyNote') }}
        </div>
        <section class="grid grid-cols-1 gap-4 md:grid-cols-3">
          <ProfitSummaryCard :title="t('profit.balanceUsage')" :metrics="report.summary.balance" />
          <ProfitSummaryCard :title="t('profit.subscriptionUsage')" :metrics="report.summary.subscription" :show-nominal="true" />
          <ProfitSummaryCard :title="t('profit.total')" :metrics="report.summary.total" />
        </section>

        <section class="card p-4">
          <h2 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.dailyTrend') }}</h2>
          <div class="flex flex-wrap gap-x-5 gap-y-2 pb-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="inline-flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-blue-500" />{{ t('profit.balanceUsage') }}</span>
            <span class="inline-flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-violet-500" />{{ t('profit.subscriptionUsage') }}</span>
            <span class="inline-flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-emerald-500" />{{ t('profit.total') }}</span>
          </div>
          <svg v-if="report.daily_series.length" viewBox="0 0 960 220" preserveAspectRatio="none" class="h-56 w-full overflow-visible" role="img" :aria-label="t('profit.dailyTrend')">
            <line x1="0" x2="960" :y1="zeroY" :y2="zeroY" stroke="currentColor" class="text-gray-200 dark:text-dark-600" stroke-dasharray="4 5" />
            <path :d="profitPath('balance_profit_usd')" fill="none" stroke="#3b82f6" stroke-width="3" vector-effect="non-scaling-stroke" />
            <path :d="profitPath('subscription_profit_usd')" fill="none" stroke="#8b5cf6" stroke-width="3" vector-effect="non-scaling-stroke" />
            <path :d="profitPath('total_profit_usd')" fill="none" stroke="#10b981" stroke-width="3" vector-effect="non-scaling-stroke" />
          </svg>
          <div v-if="report.daily_series.length" class="flex justify-between text-[11px] text-gray-400">
            <span>{{ report.daily_series[0]?.date }}</span><span>{{ report.daily_series[report.daily_series.length - 1]?.date }}</span>
          </div>
          <div v-else class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">{{ t('profit.noData') }}</div>
        </section>

        <section class="card overflow-hidden">
          <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.dailyBreakdown') }}</h2>
          </div>
          <div v-if="!report.daily_series.length" class="p-10 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('profit.noData') }}</div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full text-left text-sm">
              <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-gray-400">
                <tr>
                  <th class="px-4 py-3">{{ t('profit.date') }}</th>
                  <th class="px-4 py-3">{{ t('profit.balanceUsage') }}</th>
                  <th class="px-4 py-3">{{ t('profit.subscriptionUsage') }}</th>
                  <th class="px-4 py-3">{{ t('profit.total') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="day in report.daily_series" :key="day.date" class="text-gray-700 dark:text-gray-300">
                  <td class="whitespace-nowrap px-4 py-3 font-medium">{{ day.date }}</td>
                  <td class="px-4 py-3"><MetricCell :revenue="day.balance_revenue_usd" :cost="day.balance_cost_usd" :profit="day.balance_profit_usd" /></td>
                  <td class="px-4 py-3"><MetricCell :revenue="day.subscription_revenue_usd" :cost="day.subscription_cost_usd" :profit="day.subscription_profit_usd" :nominal="day.subscription_nominal_usage_usd" /></td>
                  <td class="px-4 py-3"><MetricCell :revenue="day.total_revenue_usd" :cost="day.total_cost_usd" :profit="day.total_profit_usd" /></td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <section v-if="Object.keys(report.summary.subscription.payment_received_by_currency || {}).length" class="card p-4">
          <h2 class="mb-1 text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.paymentReceived') }}</h2>
          <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">{{ t('profit.paymentReceivedHint') }}</p>
          <div class="flex flex-wrap gap-3">
            <div v-for="(amount, currency) in report.summary.subscription.payment_received_by_currency" :key="currency" class="rounded-lg bg-gray-50 px-4 py-3 dark:bg-dark-800">
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ currency }}</p>
              <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ formatCurrency(amount, currency) }}</p>
            </div>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { adminPaymentAPI, type ProfitMetrics, type ProfitReport } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const report = ref<ProfitReport | null>(null)
const endDate = ref(toDateInput(new Date()))
const defaultStart = new Date()
defaultStart.setDate(defaultStart.getDate() - 29)
const startDate = ref(toDateInput(defaultStart))
const isDateRangeValid = computed(() => Boolean(startDate.value && endDate.value && startDate.value <= endDate.value))

const presets = computed(() => [
  { days: 7, label: t('profit.last7Days') },
  { days: 30, label: t('profit.last30Days') },
  { days: 90, label: t('profit.last90Days') },
])

const allProfitValues = computed(() => report.value?.daily_series.flatMap(day => [
  day.balance_profit_usd, day.subscription_profit_usd, day.total_profit_usd,
]) ?? [])
const chartRange = computed(() => {
  const values = [...allProfitValues.value, 0]
  const min = Math.min(...values)
  const max = Math.max(...values)
  return { min, span: max - min || 1 }
})
const zeroY = computed(() => 206 - ((0 - chartRange.value.min) / chartRange.value.span) * 192)

function profitPath(field: 'balance_profit_usd' | 'subscription_profit_usd' | 'total_profit_usd'): string {
  const days = report.value?.daily_series ?? []
  if (!days.length) return ''
  const { min, span } = chartRange.value
  return days.map((day, index) => {
    const x = days.length === 1 ? 480 : (index / (days.length - 1)) * 960
    const y = 206 - ((day[field] - min) / span) * 192
    return `${index ? 'L' : 'M'} ${x.toFixed(1)} ${y.toFixed(1)}`
  }).join(' ')
}

function toDateInput(date: Date): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

function setPreset(days: number) {
  const end = new Date()
  const start = new Date(end)
  start.setDate(start.getDate() - (days - 1))
  startDate.value = toDateInput(start)
  endDate.value = toDateInput(end)
  void loadReport()
}

function formatCurrency(amount: number, currency = 'USD'): string {
  try { return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount) } catch { return `${currency} ${amount.toFixed(2)}` }
}

async function loadReport() {
  if (!isDateRangeValid.value) return
  loading.value = true
  error.value = ''
  try {
    report.value = (await adminPaymentAPI.getProfitReport({ start_date: startDate.value, end_date: endDate.value })).data
  } catch (err: unknown) {
    error.value = extractI18nErrorMessage(err, t, 'profit', t('profit.loadFailed'))
  } finally { loading.value = false }
}

const ProfitSummaryCard = defineComponent({
  props: { title: { type: String, required: true }, metrics: { type: Object as () => ProfitMetrics, required: true }, showNominal: Boolean },
  setup(props) {
    return () => h('div', { class: 'card p-4' }, [
      h('p', { class: 'text-xs font-medium text-gray-500 dark:text-gray-400' }, props.title),
      h('p', { class: 'mt-2 text-2xl font-bold text-gray-900 dark:text-white' }, formatCurrency(props.metrics.profit_usd)),
      h('div', { class: 'mt-3 grid grid-cols-2 gap-2 text-xs text-gray-500 dark:text-gray-400' }, [
        h('span', `${t('profit.revenue')}: ${formatCurrency(props.metrics.revenue_usd)}`),
        h('span', `${t('profit.cost')}: ${formatCurrency(props.metrics.cost_usd)}`),
        ...(props.showNominal ? [h('span', `${t('profit.nominalUsage')}: ${formatCurrency(props.metrics.nominal_usage_usd || 0)}`)] : []),
        h('span', `${t('profit.margin')}: ${props.metrics.revenue_usd ? ((props.metrics.profit_usd / props.metrics.revenue_usd) * 100).toFixed(1) : '0.0'}%`),
      ]),
    ])
  },
})

const MetricCell = defineComponent({
  props: { revenue: Number, cost: Number, profit: Number, nominal: Number },
  setup(props) { return () => h('div', { class: 'space-y-0.5 whitespace-nowrap' }, [
    h('div', `${t('profit.revenue')}: ${formatCurrency(props.revenue || 0)}`),
    h('div', `${t('profit.cost')}: ${formatCurrency(props.cost || 0)}`),
    h('div', { class: (props.profit || 0) >= 0 ? 'font-semibold text-emerald-600 dark:text-emerald-400' : 'font-semibold text-red-600 dark:text-red-400' }, `${t('profit.profit')}: ${formatCurrency(props.profit || 0)}`),
    props.nominal !== undefined ? h('div', { class: 'text-xs text-gray-400' }, `${t('profit.nominalUsage')}: ${formatCurrency(props.nominal)}`) : null,
  ]) },
})

onMounted(() => { void loadReport() })
</script>

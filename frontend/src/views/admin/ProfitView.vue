<template>
  <AppLayout>
    <div class="space-y-6">
      <header>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('profit.title') }}</h1>
        <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('profit.description') }}</p>
      </header>

      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('profit.currencyNote') }}</p>
      <div class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200" role="note">
        {{ t('profit.historyNote') }}
      </div>

      <div v-if="overviewLoading" class="card flex min-h-48 items-center justify-center"><LoadingSpinner /></div>
      <div v-else-if="overviewError" class="card flex min-h-48 flex-col items-center justify-center gap-3 p-6 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ overviewError }}</p>
        <button class="btn btn-secondary" @click="loadOverview">{{ t('common.tryAgain') }}</button>
      </div>
      <template v-else-if="overview">
        <section class="space-y-3" aria-labelledby="today-profit-heading">
          <div>
            <h2 id="today-profit-heading" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('profit.today') }}</h2>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ todayStats?.date || overview.end_date }}</p>
          </div>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
            <ProfitSummaryCard :title="t('profit.balanceUsage')" :metrics="todayMetrics.balance" />
            <ProfitSummaryCard :title="t('profit.subscriptionUsage')" :metrics="todayMetrics.subscription" :show-nominal="true" />
            <ProfitSummaryCard :title="t('profit.total')" :metrics="todayMetrics.total" />
          </div>
        </section>

        <section class="space-y-3" aria-labelledby="month-profit-heading">
          <div>
            <h2 id="month-profit-heading" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('profit.thisMonth') }}</h2>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ overview.start_date }} — {{ overview.end_date }}</p>
          </div>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
            <ProfitSummaryCard :title="t('profit.balanceUsage')" :metrics="overview.summary.balance" />
            <ProfitSummaryCard :title="t('profit.subscriptionUsage')" :metrics="overview.summary.subscription" :show-nominal="true" />
            <ProfitSummaryCard :title="t('profit.total')" :metrics="overview.summary.total" />
          </div>
          <section v-if="Object.keys(overview.summary.subscription.payment_received_by_currency || {}).length" class="card p-4">
            <h3 class="mb-1 text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.paymentReceived') }}</h3>
            <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">{{ t('profit.paymentReceivedHint') }}</p>
            <div class="flex flex-wrap gap-3">
              <div v-for="(amount, currency) in overview.summary.subscription.payment_received_by_currency" :key="currency" class="rounded-lg bg-gray-50 px-4 py-3 dark:bg-dark-800">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ currency }}</p>
                <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ formatCurrency(amount, currency) }}</p>
              </div>
            </div>
          </section>
        </section>
      </template>

      <section class="card space-y-4 p-4" aria-labelledby="history-profit-heading">
        <div>
          <h2 id="history-profit-heading" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('profit.historyTitle') }}</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('profit.historyDescription') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <DateRangePicker
            v-model:start-date="startDate"
            v-model:end-date="endDate"
            :custom-calendar="true"
            @change="onHistoryRangeChange"
          />
          <span v-if="historyLoading" class="inline-flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400" role="status">
            <LoadingSpinner />{{ t('profit.loadingHistory') }}
          </span>
          <div v-else-if="historyError" class="flex flex-wrap items-center gap-3" role="alert">
            <p class="text-sm text-red-600 dark:text-red-400">{{ historyError }}</p>
            <button v-if="historyCanRetry" class="btn btn-secondary" type="button" @click="loadHistory()">
              {{ t('common.tryAgain') }}
            </button>
          </div>
        </div>
        <template v-if="historyReport && !historyLoading">
          <section class="grid grid-cols-1 gap-4 md:grid-cols-3">
            <ProfitSummaryCard :title="t('profit.balanceUsage')" :metrics="historyReport.summary.balance" />
            <ProfitSummaryCard :title="t('profit.subscriptionUsage')" :metrics="historyReport.summary.subscription" :show-nominal="true" />
            <ProfitSummaryCard :title="t('profit.total')" :metrics="historyReport.summary.total" />
          </section>

          <section class="card p-4">
            <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.dailyTrend') }}</h3>
            <div class="flex flex-wrap gap-x-5 gap-y-2 pb-2 text-xs text-gray-500 dark:text-gray-400">
              <span class="inline-flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-blue-500" />{{ t('profit.balanceUsage') }}</span>
              <span class="inline-flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-violet-500" />{{ t('profit.subscriptionUsage') }}</span>
              <span class="inline-flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-emerald-500" />{{ t('profit.total') }}</span>
            </div>
            <svg v-if="historyReport.daily_series.length" viewBox="0 0 960 220" preserveAspectRatio="none" class="h-56 w-full overflow-visible" role="img" :aria-label="t('profit.dailyTrend')">
              <line x1="0" x2="960" :y1="zeroY" :y2="zeroY" stroke="currentColor" class="text-gray-200 dark:text-dark-600" stroke-dasharray="4 5" />
              <path :d="profitPath('balance_profit_usd')" fill="none" stroke="#3b82f6" stroke-width="3" vector-effect="non-scaling-stroke" />
              <path :d="profitPath('subscription_profit_usd')" fill="none" stroke="#8b5cf6" stroke-width="3" vector-effect="non-scaling-stroke" />
              <path :d="profitPath('total_profit_usd')" fill="none" stroke="#10b981" stroke-width="3" vector-effect="non-scaling-stroke" />
            </svg>
            <div v-if="historyReport.daily_series.length" class="flex justify-between text-[11px] text-gray-400">
              <span>{{ historyReport.daily_series[0]?.date }}</span><span>{{ historyReport.daily_series[historyReport.daily_series.length - 1]?.date }}</span>
            </div>
            <div v-else class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">{{ t('profit.noData') }}</div>
          </section>

          <section class="card overflow-hidden">
            <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.dailyBreakdown') }}</h3>
            </div>
            <div v-if="!historyReport.daily_series.length" class="p-10 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('profit.noData') }}</div>
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
                  <tr v-for="day in historyReport.daily_series" :key="day.date" class="text-gray-700 dark:text-gray-300">
                    <td class="whitespace-nowrap px-4 py-3 font-medium">{{ day.date }}</td>
                    <td class="px-4 py-3"><MetricCell :revenue="day.balance_revenue_usd" :cost="day.balance_cost_usd" :profit="day.balance_profit_usd" /></td>
                    <td class="px-4 py-3"><MetricCell :revenue="day.subscription_revenue_usd" :cost="day.subscription_cost_usd" :profit="day.subscription_profit_usd" :nominal="day.subscription_nominal_usage_usd" /></td>
                    <td class="px-4 py-3"><MetricCell :revenue="day.total_revenue_usd" :cost="day.total_cost_usd" :profit="day.total_profit_usd" /></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
          <section v-if="Object.keys(historyReport.summary.subscription.payment_received_by_currency || {}).length" class="card p-4">
            <h3 class="mb-1 text-sm font-semibold text-gray-900 dark:text-white">{{ t('profit.paymentReceived') }}</h3>
            <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">{{ t('profit.paymentReceivedHint') }}</p>
            <div class="flex flex-wrap gap-3">
              <div v-for="(amount, currency) in historyReport.summary.subscription.payment_received_by_currency" :key="currency" class="rounded-lg bg-gray-50 px-4 py-3 dark:bg-dark-800">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ currency }}</p>
                <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ formatCurrency(amount, currency) }}</p>
              </div>
            </div>
          </section>
        </template>
        <p v-if="!historyReport && !historyError && !historyLoading" class="text-sm text-gray-500 dark:text-gray-400">{{ t('profit.historyPrompt') }}</p>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import { adminPaymentAPI, type ProfitDailyStats, type ProfitMetrics, type ProfitReport } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const overviewLoading = ref(false)
const overviewError = ref('')
const overview = ref<ProfitReport | null>(null)
const historyLoading = ref(false)
const historyError = ref('')
const historyCanRetry = ref(false)
const historyReport = ref<ProfitReport | null>(null)
const startDate = ref('')
const endDate = ref('')
const todayStats = computed(() => overview.value?.daily_series.find(day => day.date === overview.value?.end_date) ?? null)

function metricsForDay(day: ProfitDailyStats | null, kind: 'balance' | 'subscription' | 'total'): ProfitMetrics {
  if (!day) return { revenue_usd: 0, cost_usd: 0, profit_usd: 0, nominal_usage_usd: 0 }
  const prefix = `${kind}_`
  const revenue = day[`${prefix}revenue_usd` as keyof ProfitDailyStats] as number
  const cost = day[`${prefix}cost_usd` as keyof ProfitDailyStats] as number
  const profit = day[`${prefix}profit_usd` as keyof ProfitDailyStats] as number
  const nominal = kind === 'subscription' ? day.subscription_nominal_usage_usd : kind === 'balance' ? revenue : revenue
  return { revenue_usd: revenue, cost_usd: cost, profit_usd: profit, nominal_usage_usd: nominal }
}

const todayMetrics = computed(() => ({
  balance: metricsForDay(todayStats.value, 'balance'),
  subscription: metricsForDay(todayStats.value, 'subscription'),
  total: metricsForDay(todayStats.value, 'total'),
}))

const allProfitValues = computed(() => historyReport.value?.daily_series.flatMap(day => [
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
  const days = historyReport.value?.daily_series ?? []
  if (!days.length) return ''
  const { min, span } = chartRange.value
  return days.map((day, index) => {
    const x = days.length === 1 ? 480 : (index / (days.length - 1)) * 960
    const y = 206 - ((day[field] - min) / span) * 192
    return `${index ? 'L' : 'M'} ${x.toFixed(1)} ${y.toFixed(1)}`
  }).join(' ')
}

function formatCurrency(amount: number, currency = 'USD'): string {
  try { return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount) } catch { return `${currency} ${amount.toFixed(2)}` }
}

async function loadOverview() {
  overviewLoading.value = true
  overviewError.value = ''
  try {
    overview.value = (await adminPaymentAPI.getProfitReport()).data
    if (!startDate.value && !endDate.value) {
      startDate.value = overview.value.start_date
      endDate.value = overview.value.end_date
    }
  } catch (err: unknown) {
    overviewError.value = extractI18nErrorMessage(err, t, 'profit', t('profit.loadFailed'))
  } finally { overviewLoading.value = false }
}

function onHistoryRangeChange(range: { startDate: string; endDate: string }) {
  startDate.value = range.startDate
  endDate.value = range.endDate
  void loadHistory(range.startDate, range.endDate)
}

async function loadHistory(start = startDate.value, end = endDate.value) {
  if (!start || !end || start > end) return
  const firstDay = Date.parse(`${start}T00:00:00Z`)
  const lastDay = Date.parse(`${end}T00:00:00Z`)
  if (!Number.isFinite(firstDay) || !Number.isFinite(lastDay) || (lastDay - firstDay) / 86_400_000 >= 366) {
    historyReport.value = null
    historyCanRetry.value = false
    historyError.value = t('profit.rangeTooLarge')
    return
  }
  historyLoading.value = true
  historyCanRetry.value = false
  historyError.value = ''
  historyReport.value = null
  try {
    historyReport.value = (await adminPaymentAPI.getProfitReport({ start_date: start, end_date: end })).data
  } catch (err: unknown) {
    historyError.value = extractI18nErrorMessage(err, t, 'profit', t('profit.loadFailed'))
    historyCanRetry.value = true
  } finally { historyLoading.value = false }
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

onMounted(() => { void loadOverview() })
</script>

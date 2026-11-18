import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ProfitView from '../ProfitView.vue'

const { getProfitReport, showError } = vi.hoisted(() => ({
  getProfitReport: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/payment', async () => {
  const actual = await vi.importActual<typeof import('@/api/admin/payment')>('@/api/admin/payment')
  return { ...actual, adminPaymentAPI: { ...actual.adminPaymentAPI, getProfitReport } }
})
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual,
    useI18n: () => ({
    t: (key: string) => {
      const messages: Record<string, string> = {
        'profit.title': 'Profit Statistics',
        'profit.balanceUsage': 'Balance usage',
        'profit.subscriptionUsage': 'Subscription usage',
        'profit.total': 'Total',
        'profit.today': "Today's profit",
        'profit.thisMonth': "This month's profit",
        'profit.historyTitle': 'Historical profit query',
        'profit.historyDescription': 'Choose a date range',
        'profit.historyPrompt': 'Choose a start and end date',
        'profit.searchHistory': 'Query history',
        'profit.revenue': 'Revenue',
        'profit.cost': 'Cost',
        'profit.profit': 'Profit',
        'profit.margin': 'Margin',
        'profit.dailyTrend': 'Daily trend',
        'profit.dailyBreakdown': 'Daily breakdown',
        'profit.date': 'Date',
        'profit.currencyNote': 'USD consolidated',
        'profit.refresh': 'Refresh',
        'profit.startDate': 'Start date',
        'profit.endDate': 'End date',
        'profit.last7Days': 'Last 7 days',
        'profit.last30Days': 'Last 30 days',
        'profit.last90Days': 'Last 90 days',
        'profit.description': 'Revenue and cost',
        'profit.historyNote': 'Historical subscription data note',
        'common.tryAgain': 'Try again',
        'profit.loadFailed': 'Load failed',
      }
      return messages[key] ?? key
    },
    }),
  }
})
vi.mock('@/utils/apiError', () => ({ extractI18nErrorMessage: (_err: unknown, _t: unknown, _namespace: string, fallback: string) => fallback }))

const metrics = { revenue_usd: 10, cost_usd: 4, profit_usd: 6, nominal_usage_usd: 15, revenue_by_currency: { CNY: 72 } }
const report = {
  start_date: '2026-01-01',
  end_date: '2026-01-01',
  summary: { balance: metrics, subscription: metrics, total: metrics },
  daily_series: [{
    date: '2026-01-01',
    balance_revenue_usd: 10, balance_cost_usd: 4, balance_profit_usd: 6,
    subscription_revenue_usd: 10, subscription_cost_usd: 4, subscription_profit_usd: 6,
    subscription_nominal_usage_usd: 15,
    total_revenue_usd: 20, total_cost_usd: 8, total_profit_usd: 12,
  }],
}

describe('ProfitView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getProfitReport.mockResolvedValue({ data: report })
  })

  it('loads today and month-to-date overview without loading history', async () => {
    const wrapper = mount(ProfitView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true, LoadingSpinner: true } },
    })
    await flushPromises()

    expect(getProfitReport).toHaveBeenCalledWith()
    expect(getProfitReport).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain("Today's profit")
    expect(wrapper.text()).toContain("This month's profit")
    expect(wrapper.text()).toContain('Balance usage')
    expect(wrapper.text()).toContain('Subscription usage')
    expect(wrapper.text()).toContain('Historical subscription data note')
    expect(wrapper.text()).toContain('Choose a start and end date')

    const inputs = wrapper.findAll('input[type="date"]')
    await inputs[0].setValue('2026-01-01')
    await inputs[1].setValue('2026-01-01')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(getProfitReport).toHaveBeenLastCalledWith({ start_date: '2026-01-01', end_date: '2026-01-01' })
    expect(wrapper.text()).toContain('Daily breakdown')
    expect(wrapper.text()).toContain('2026-01-01')
  })
})

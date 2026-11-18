import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { ProfitReport } from '@/api/admin/payment'
import ProfitView from '../ProfitView.vue'

enableAutoUnmount(afterEach)

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
    locale: { value: 'en' },
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
        'profit.historyPrompt': 'Choose a quick range or set custom dates, then apply.',
        'profit.loadingHistory': 'Loading historical profit…',
        'profit.rangeTooLarge': 'The date range cannot exceed 366 days. Choose a shorter range.',
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

const metrics = { revenue_usd: 10, cost_usd: 4, profit_usd: 6, nominal_usage_usd: 15 }
const report: ProfitReport = {
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

const monthReport: ProfitReport = {
  ...report,
  end_date: '2026-01-16',
  summary: {
    ...report.summary,
    subscription: { ...metrics, payment_received_by_currency: { CNY: 72 } },
    total: { ...metrics, profit_usd: 42 },
  },
}

function deferredReport() {
  let resolve!: (value: { data: ProfitReport }) => void
  const promise = new Promise<{ data: ProfitReport }>((done) => { resolve = done })
  return { promise, resolve }
}

function mountProfitView() {
  return mount(ProfitView, {
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true, LoadingSpinner: true } },
  })
}

const todaySection = '[aria-labelledby="today-profit-heading"]'
const monthSection = '[aria-labelledby="month-profit-heading"]'

describe('ProfitView', () => {
  beforeEach(() => {
    getProfitReport.mockReset()
    getProfitReport.mockResolvedValue({ data: report })
  })

  it('shows today before the month is ready and queries history only after Apply', async () => {
    const today = deferredReport()
    const month = deferredReport()
    getProfitReport.mockReturnValueOnce(today.promise).mockReturnValueOnce(month.promise)
    const wrapper = mountProfitView()

    expect(getProfitReport).toHaveBeenCalledTimes(1)
    expect(getProfitReport).toHaveBeenCalledWith({ period: 'today' }, { signal: expect.any(AbortSignal) })

    today.resolve({ data: report })
    await flushPromises()

    expect(getProfitReport).toHaveBeenCalledTimes(2)
    expect(getProfitReport).toHaveBeenLastCalledWith(undefined, { signal: expect.any(AbortSignal) })
    expect(wrapper.get(todaySection).text()).toContain('Balance usage')
    expect(wrapper.get(todaySection).text()).toContain('Subscription usage')
    expect(wrapper.get(todaySection).text()).toContain('2026-01-01')
    expect(wrapper.get(todaySection).text()).toContain(new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD' }).format(12))
    expect(wrapper.get(monthSection).text()).not.toContain('Balance usage')

    month.resolve({ data: monthReport })
    await flushPromises()

    expect(wrapper.get(monthSection).text()).toContain('Balance usage')
    expect(wrapper.get(monthSection).text()).toContain('2026-01-01 — 2026-01-16')
    expect(wrapper.get(monthSection).text()).toContain('CNY')
    expect(wrapper.get(todaySection).text()).not.toContain('2026-01-16')
    expect(wrapper.text()).toContain('Historical subscription data note')
    expect(wrapper.text()).toContain('Choose a quick range or set custom dates, then apply.')

    await wrapper.find('.date-picker-trigger').trigger('click')
    expect(wrapper.findAll('input[type="date"]')).toHaveLength(0)
    await wrapper.find('[data-date="2026-01-01"]').trigger('click')
    await wrapper.find('[data-date="2026-01-05"]').trigger('click')
    expect(getProfitReport).toHaveBeenCalledTimes(2)

    await wrapper.find('.date-picker-apply').trigger('click')
    await flushPromises()

    expect(getProfitReport).toHaveBeenLastCalledWith({ start_date: '2026-01-01', end_date: '2026-01-05' })
    expect(wrapper.text()).toContain('Daily breakdown')
    expect(wrapper.text()).toContain('2026-01-01')
  })

  it.each([
    { failed: todaySection, available: monthSection, todayFails: true },
    { failed: monthSection, available: todaySection, todayFails: false },
  ])('retries only the failed report in $failed', async ({ failed, available, todayFails }) => {
    if (todayFails) {
      getProfitReport.mockRejectedValueOnce(new Error('Today failed')).mockResolvedValueOnce({ data: monthReport })
    } else {
      getProfitReport.mockResolvedValueOnce({ data: report }).mockRejectedValueOnce(new Error('Month failed'))
    }
    const wrapper = mountProfitView()
    await flushPromises()

    expect(wrapper.get(failed).get('[role="alert"]').text()).toContain('Load failed')
    expect(wrapper.get(available).text()).toContain('Balance usage')

    const retryParams = todayFails ? { period: 'today' } : undefined
    getProfitReport.mockResolvedValueOnce({ data: todayFails ? report : monthReport })
    await wrapper.get(failed).get('button').trigger('click')
    await flushPromises()

    expect(getProfitReport).toHaveBeenCalledTimes(3)
    expect(getProfitReport).toHaveBeenLastCalledWith(retryParams, { signal: expect.any(AbortSignal) })
    expect(wrapper.get(failed).text()).toContain('Balance usage')
    expect(wrapper.get(available).text()).toContain('Balance usage')
  })

  it('does not load the month after unmounting with today pending', async () => {
    const today = deferredReport()
    getProfitReport.mockReturnValueOnce(today.promise)
    const wrapper = mountProfitView()
    const signal = getProfitReport.mock.calls[0][1].signal as AbortSignal

    wrapper.unmount()
    today.resolve({ data: report })
    await flushPromises()

    expect(signal.aborted).toBe(true)
    expect(getProfitReport).toHaveBeenCalledTimes(1)
  })
})

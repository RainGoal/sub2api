import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AdminSalesView from './AdminSalesView.vue'
import { salesAPI, type SalesOverview, type SalesPartner, type SalesSettlement } from '@/api/admin/sales'
import zhSales from '@/i18n/locales/zh/sales'

vi.mock('@/api/admin/sales', () => ({ salesAPI: { settings: vi.fn(), partners: vi.fn(), overview: vi.fn(), customers: vi.fn(), ledger: vi.fn(), settlements: vi.fn(), saveSettings: vi.fn(), savePartner: vi.fn(), createSettlement: vi.fn(), confirmSettlement: vi.fn(), paySettlement: vi.fn(), adjust: vi.fn() } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({
  t: (key: string) => key.startsWith('sales.validation.') ? zhSales.validation[key.slice('sales.validation.'.length) as keyof typeof zhSales.validation] || key : key,
  te: (key: string) => key.startsWith('sales.validation.') && key.slice('sales.validation.'.length) in zhSales.validation,
  locale: { value: 'en-US' }
}) }))
const partner: SalesPartner = { id: 3, user_id: 7, name: 'Sales A', code: 'sales-a', hostname: 'sales.example.com', commission_rate: 30, promotion_enabled: true, accrual_enabled: true, payout_frozen: false, created_at: '', updated_at: '' }
const summary: SalesOverview = { partner, customer_count: 1, revenue: 10, cost: 6, profit: 4, commission: 1.2, unsettled_commission: 1.2, pending_payout: 0, paid_commission: 0, pending_events: 0 }
const bill: SalesSettlement = { id: 4, partner_id: 3, month: '2026-08', cutoff: '2026-09-01T00:00:00Z', net_commission: 1.2, payout_amount: 1.2, carry_amount: 0, status: 'confirmed', currency: 'USD', payment_reference: '', created_at: '' }
const empty = { items: [], total: 0, page: 1, page_size: 20, pages: 0 }
const wrappers: VueWrapper[] = []
async function click(wrapper: VueWrapper, key: string) { await wrapper.findAll('button').find(button => button.text() === key)!.trigger('click'); await flushPromises() }
async function create() {
  const wrapper = mount(AdminSalesView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    BaseDialog: { props: ['show', 'title'], template: '<section v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>' },
    DataTable: { props: ['data'], template: '<div><div v-for="row in data"><slot name="cell-actions" :row="row" /></div></div>' }, Pagination: true
  } } }); wrappers.push(wrapper); await flushPromises(); await click(wrapper, 'sales.view'); return wrapper
}
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(salesAPI.settings).mockResolvedValue({ enabled: true, main_frontend_url: 'https://app.example.com' })
  vi.mocked(salesAPI.partners).mockResolvedValue({ ...empty, items: [partner], total: 1, pages: 1 })
  vi.mocked(salesAPI.overview).mockResolvedValue(summary)
  vi.mocked(salesAPI.customers).mockResolvedValue(empty)
  vi.mocked(salesAPI.ledger).mockResolvedValue(empty)
  vi.mocked(salesAPI.settlements).mockResolvedValue({ ...empty, items: [bill], total: 1, pages: 1 })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.unstubAllGlobals() })
describe('sales partner validation', () => {
  it('explains a two-character code before sending a request and saves after correction', async () => {
    const wrapper = await create()
    await click(wrapper, 'sales.back'); await click(wrapper, 'sales.addPartner')
    const inputs = wrapper.findAll('#sales-partner input')
    for (const [index, value] of ['QQ sales', '7', 'qq', 'qq.yusflow.com', '50'].entries()) {
      await inputs[index].setValue(value)
    }
    await wrapper.get('#sales-partner').trigger('submit'); await flushPromises()
    expect(salesAPI.savePartner).not.toHaveBeenCalled()
    expect(wrapper.get('#sales-partner [role="alert"]').text()).toBe(zhSales.validation.code)
    expect(inputs[2].attributes('maxlength')).toBe('48')
    await inputs[2].setValue('sales-qq')
    vi.mocked(salesAPI.savePartner).mockResolvedValue({ ...partner, code: 'sales-qq', hostname: 'qq.yusflow.com', commission_rate: 50 })
    await wrapper.get('#sales-partner').trigger('submit'); await flushPromises()
    expect(salesAPI.savePartner).toHaveBeenCalledWith(expect.objectContaining({ user_id: 7, code: 'sales-qq', hostname: 'qq.yusflow.com', commission_rate: 50 }), undefined)
    expect(wrapper.find('#sales-partner').exists()).toBe(false)
  })
  it.each(['user_id', 'hostname', 'commission_rate'] as const)('shows the server validation error for %s and keeps the form open', async field => {
    const wrapper = await create(); await click(wrapper, 'sales.editPartner')
    vi.mocked(salesAPI.savePartner).mockRejectedValue({ reason: 'SALES_INVALID', metadata: { field } })
    await wrapper.get('#sales-partner').trigger('submit'); await flushPromises()
    expect(wrapper.get('#sales-partner [role="alert"]').text()).toBe(zhSales.validation[field])
    expect(wrapper.get('button[form="sales-partner"]').attributes('disabled')).toBeUndefined()
  })
  it('identifies the main URL only when that field fails settings validation', async () => {
    const wrapper = await create(); await click(wrapper, 'sales.settings')
    vi.mocked(salesAPI.saveSettings).mockRejectedValue({ reason: 'SALES_INVALID', metadata: { field: 'main_frontend_url' } })
    await wrapper.get('#sales-settings').trigger('submit'); await flushPromises()
    expect(wrapper.get('#sales-settings [role="alert"]').text()).toBe(zhSales.validation.main_frontend_url)
  })
})
describe('sales admin financial controls', () => {
  it('prevents duplicate payment submits and reuses the request key after an uncertain failure', async () => {
    vi.stubGlobal('crypto', {}) // HTTP admin origins may not expose randomUUID.
    let reject!: (reason: Error) => void
    vi.mocked(salesAPI.paySettlement).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail }))
    const wrapper = await create(); await click(wrapper, 'sales.settlements'); await click(wrapper, 'sales.pay')
    await wrapper.get('#sales-finance input').setValue('bank-reference-202609')
    await wrapper.get('#sales-finance').trigger('submit'); await wrapper.get('#sales-finance').trigger('submit')
    expect(salesAPI.paySettlement).toHaveBeenCalledTimes(1)
    expect(wrapper.get('button[form="sales-finance"]').attributes('disabled')).toBeDefined()
    const firstPayload = vi.mocked(salesAPI.paySettlement).mock.calls[0][1]
    expect(firstPayload.payment_reference).toBe('bank-reference-202609')
    reject(new Error('timeout')); await flushPromises()
    vi.mocked(salesAPI.paySettlement).mockResolvedValue({ ...bill, status: 'paid' })
    await wrapper.get('#sales-finance').trigger('submit'); await flushPromises()
    expect(salesAPI.paySettlement).toHaveBeenLastCalledWith(4, firstPayload)
    expect(wrapper.find('#sales-finance').exists()).toBe(false)
  })
  it('blocks settlement creation while usage events are pending and includes partner scope in all queries', async () => {
    vi.mocked(salesAPI.overview).mockResolvedValue({ ...summary, pending_events: 2 })
    const wrapper = await create(); await click(wrapper, 'sales.settlements')
    expect(wrapper.findAll('button').find(button => button.text() === 'sales.createSettlement')!.attributes('disabled')).toBeDefined()
    expect(salesAPI.customers).toHaveBeenCalledWith(expect.objectContaining({ partner_id: 3 }))
    expect(salesAPI.settlements).toHaveBeenCalledWith(expect.objectContaining({ partner_id: 3 }))
  })
})

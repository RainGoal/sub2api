import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UpstreamBalancesView from '../UpstreamBalancesView.vue'
import UpstreamWalletDialog from '@/components/admin/UpstreamWalletDialog.vue'
import type { UpstreamBalanceConfig, UpstreamBalanceSnapshot, UpstreamWallet } from '@/api/admin/upstreamBalances'
import en from '@/i18n/locales/en/admin/upstreamBalances'
import zh from '@/i18n/locales/zh/admin/upstreamBalances'

enableAutoUnmount(afterEach)
const { list, discover, saveConfig, refresh, showError, showSuccess, showInfo } = vi.hoisted(() => ({
  list: vi.fn(), discover: vi.fn(), saveConfig: vi.fn(), refresh: vi.fn(), showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn(),
}))
vi.mock('@/api/admin/upstreamBalances', () => ({ upstreamBalancesAPI: { list, discover, saveConfig, refresh } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError, showSuccess, showInfo }) }))
vi.mock('vue-i18n', async original => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, params?: object) => key + (params ? JSON.stringify(params) : ''), te: () => false, locale: { value: 'en' } }),
}))

const wallet = (id = 'a', overrides: Partial<UpstreamWallet> = {}): UpstreamWallet => ({ id, name: `Wallet ${id}`, site_url: 'https://upstream.example', single_account: true, account_ids: [], query_account_id: null, threshold: 20, recharge_url: '', enabled: true, ...overrides })
const snapshot = (id = 'a', overrides: Partial<UpstreamBalanceSnapshot> = {}): UpstreamBalanceSnapshot => ({ wallet_id: id, status: 'ok', kind: 'wallet', balance: 120, currency: 'USD', low_balance: false, account_count: 2, last_success_at: '2026-10-08T00:00:00Z', fresh_until: '2099-01-01T00:00:00Z', ...overrides })
const defaultConfig = (): UpstreamBalanceConfig => ({ version: 3, enabled: false, interval_seconds: 1800, interval_minutes: 30, wallets: [wallet()] })
const sites = [{ site_url: 'https://upstream.example', accounts: [{ id: 1, name: 'Key one' }, { id: 2, name: 'Key two' }] }]
const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
  Icon: true,
  DataTable: {
    props: ['columns', 'data'],
    template: '<div><div v-for="row in data" :key="row.id" data-wallet-row><template v-for="column in columns"><slot :name="`cell-${column.key}`" :row="row" /></template></div><slot v-if="!data.length" name="empty" /></div>',
  },
}
const mountView = () => mount(UpstreamBalancesView, { global: { stubs } })
const button = (wrapper: ReturnType<typeof mountView>, key: string) => wrapper.findAll('button').find(item => item.text() === key)!

describe('isolated upstream balance monitoring', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    list.mockResolvedValue({ config: defaultConfig(), items: [snapshot()] })
    discover.mockResolvedValue({ sites })
    saveConfig.mockImplementation(async (config: UpstreamBalanceConfig) => ({ ...config, version: config.version + 1, wallets: config.wallets.map(item => ({ ...item, id: item.id || 'new' })) }))
    refresh.mockResolvedValue(snapshot())
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('loads cached balances without querying upstreams or changing existing account settings', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('USD 120.00')
    expect(wrapper.get<HTMLInputElement>('[data-test="auto-enabled"]').element.checked).toBe(false)
    expect(refresh).not.toHaveBeenCalled()
    expect(saveConfig).not.toHaveBeenCalled()
    await button(wrapper, 'common.refresh').trigger('click')
    await flushPromises()
    expect(refresh).toHaveBeenCalledWith('a')
  })

  it('requires explicit shared-wallet confirmation before automatic grouping', async () => {
    list.mockResolvedValue({ config: { ...defaultConfig(), wallets: [] }, items: [] })
    const wrapper = mountView()
    await flushPromises()
    await button(wrapper, 'admin.upstreamBalances.addWallet').trigger('click')
    await wrapper.get('#wallet-site').setValue(sites[0].site_url)
    expect(wrapper.get<HTMLInputElement>('[data-test="single-account"]').element.checked).toBe(false)
    await wrapper.get('#upstream-wallet-form').trigger('submit')
    expect(saveConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="single-account"]').setValue(true)
    await wrapper.get('#upstream-wallet-form').trigger('submit')
    await flushPromises()
    expect(saveConfig).toHaveBeenCalledWith(expect.objectContaining({ version: 3, enabled: false, wallets: [expect.objectContaining({ single_account: true, account_ids: [], query_account_id: null })] }))
    expect(refresh).not.toHaveBeenCalled()
  })

  it('separates quotas, failed historical balances and fresh low balances', async () => {
    list.mockResolvedValue({
      config: { ...defaultConfig(), wallets: [wallet('quota'), wallet('old'), wallet('low')] },
      items: [snapshot('quota', { status: 'unsupported', kind: 'key_quota', balance: 999 }), snapshot('old', { status: 'failed', balance: 15, low_balance: true }), snapshot('low', { balance: 0, low_balance: true })],
    })
    const wrapper = mountView()
    await flushPromises()
    const rows = wrapper.findAll('[data-wallet-row]')
    expect(rows[0].text()).toContain('Wallet low')
    expect(rows[0].text()).toContain('USD 0.00')
    expect(wrapper.text()).not.toContain('999')
    expect(wrapper.text()).toContain('admin.upstreamBalances.keyQuota')
    expect(wrapper.text()).toContain('USD 15.00')
    expect(wrapper.text()).toContain('admin.upstreamBalances.historical')
  })

  it('marks expired data as stale even when the last query succeeded', async () => {
    list.mockResolvedValue({ config: defaultConfig(), items: [snapshot('a', { fresh_until: '2020-01-01T00:00:00Z', low_balance: true })] })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.upstreamBalances.stale')
    expect(wrapper.text()).not.toContain('admin.upstreamBalances.lowBalance')
  })

  it('saves automatic refresh settings only on submit and preserves wallets', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="auto-enabled"]').setValue(true)
    await wrapper.get('#balance-interval').setValue(60)
    expect(saveConfig).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveConfig).toHaveBeenCalledWith({ ...defaultConfig(), enabled: true, interval_seconds: 60 })
    expect(refresh).not.toHaveBeenCalled()
  })

  it('loads legacy minute settings as seconds without automatically saving', async () => {
    const legacy = defaultConfig()
    delete legacy.interval_seconds
    list.mockResolvedValue({ config: legacy, items: [snapshot()] })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('#balance-interval').element.value).toBe('1800')
    expect(button(wrapper, 'common.save').attributes('disabled')).toBeDefined()
    expect(saveConfig).not.toHaveBeenCalled()
  })

  it('prefers the seconds field over the legacy minute field', async () => {
    list.mockResolvedValue({ config: { ...defaultConfig(), interval_seconds: 15 }, items: [snapshot()] })
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get<HTMLInputElement>('#balance-interval')
    expect(input.element.value).toBe('15')
    expect(input.attributes()).toMatchObject({ min: '10', max: '86400', step: '1' })
    expect(button(wrapper, 'common.save').attributes('disabled')).toBeDefined()
  })

  it.each([10, 15, 30, 86400])('saves %i seconds without changing wallet settings', async seconds => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('#balance-interval').setValue(seconds)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveConfig).toHaveBeenCalledWith({ ...defaultConfig(), interval_seconds: seconds })
    expect(button(wrapper, 'common.save').attributes('disabled')).toBeDefined()
    expect(refresh).not.toHaveBeenCalled()
  })

  it.each([0, 9, 86401, 10.5, ''])('rejects invalid second intervals (%s)', async seconds => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('#balance-interval').setValue(seconds)
    await wrapper.get('form').trigger('submit')
    expect(saveConfig).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.upstreamBalances.validationInterval')
  })

  it.each([
    { enabled: true, interval_seconds: 15, wait: 15000 },
    { enabled: true, interval_seconds: 1800, wait: 60000 },
    { enabled: false, interval_seconds: 10, wait: 60000 },
  ])('polls only cached balances at a bounded interval ($wait ms)', async ({ enabled, interval_seconds, wait }) => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    list.mockResolvedValue({ config: { ...defaultConfig(), enabled, interval_seconds }, items: [snapshot()] })
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(wait - 1)
    expect(list).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(list).toHaveBeenCalledTimes(2)
    expect(refresh).not.toHaveBeenCalled()
    expect(saveConfig).not.toHaveBeenCalled()
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(wait * 2)
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('keeps unsaved settings while updating cached attempt status and balances', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    const saved = { ...defaultConfig(), enabled: true, interval_seconds: 15 }
    list.mockResolvedValueOnce({ config: saved, items: [snapshot()] })
    list.mockResolvedValue({ config: saved, items: [snapshot('a', { balance: 75, status: 'failed', last_attempt_at: '2026-10-09T00:00:00Z' })] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('#balance-interval').setValue(30)
    await wrapper.get('[data-test="auto-enabled"]').setValue(false)
    await vi.advanceTimersByTimeAsync(15000)
    expect(wrapper.text()).toContain('USD 75.00')
    expect(wrapper.text()).toContain('admin.upstreamBalances.lastAttempt')
    expect(wrapper.get<HTMLInputElement>('#balance-interval').element.value).toBe('30')
    expect(wrapper.get<HTMLInputElement>('[data-test="auto-enabled"]').element.checked).toBe(false)
    expect(button(wrapper, 'common.save').attributes('disabled')).toBeUndefined()
    expect(refresh).not.toHaveBeenCalled()
  })

  it('does not overlap cache reads and stops scheduling after unmount during a request', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    const saved = { ...defaultConfig(), enabled: true, interval_seconds: 10 }
    let resolveList!: (result: { config: UpstreamBalanceConfig; items: UpstreamBalanceSnapshot[] }) => void
    list.mockResolvedValueOnce({ config: saved, items: [snapshot()] })
    list.mockReturnValue(new Promise(resolve => { resolveList = resolve }))
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)
    await vi.advanceTimersByTimeAsync(60000)
    expect(list).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    resolveList({ config: saved, items: [snapshot()] })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(60000)
    expect(list).toHaveBeenCalledTimes(2)
    expect(refresh).not.toHaveBeenCalled()
  })

  it('pauses cache polling while hidden and resumes on visibility', async () => {
    vi.useFakeTimers()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    list.mockResolvedValue({ config: { ...defaultConfig(), enabled: true, interval_seconds: 10 }, items: [snapshot()] })
    const wrapper = mountView()
    await flushPromises()
    hidden.mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(60000)
    expect(list).toHaveBeenCalledTimes(1)
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(10000)
    expect(list).toHaveBeenCalledTimes(2)
    expect(refresh).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('marks second-based balances stale promptly even if a cache read fails', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    list.mockResolvedValueOnce({ config: { ...defaultConfig(), enabled: true, interval_seconds: 15 }, items: [snapshot('a', { fresh_until: new Date(Date.now() + 14999).toISOString() })] })
    list.mockRejectedValue(new Error('offline'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.upstreamBalances.stale')
    await vi.advanceTimersByTimeAsync(15000)
    expect(wrapper.text()).toContain('admin.upstreamBalances.stale')
    expect(wrapper.text()).toContain('USD 120.00')
    expect(refresh).not.toHaveBeenCalled()
  })

  it('does not overwrite dirty settings when another administrator saves a new version', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    list.mockResolvedValueOnce({ config: { ...defaultConfig(), enabled: true, interval_seconds: 15 }, items: [snapshot()] })
    list.mockResolvedValue({ config: { ...defaultConfig(), version: 4, interval_seconds: 10 }, items: [snapshot()] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('#balance-interval').setValue(30)
    await vi.advanceTimersByTimeAsync(15000)
    expect(wrapper.get<HTMLInputElement>('#balance-interval').element.value).toBe('30')
    expect(wrapper.get<HTMLInputElement>('[data-test="auto-enabled"]').element.checked).toBe(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveConfig).toHaveBeenCalledWith(expect.objectContaining({ version: 3, enabled: true, interval_seconds: 30 }))
  })

  it('observes remotely saved settings through the low-frequency cache read when no edits are pending', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    list.mockResolvedValueOnce({ config: defaultConfig(), items: [snapshot()] })
    list.mockResolvedValue({ config: { ...defaultConfig(), version: 4, enabled: true, interval_seconds: 10 }, items: [snapshot()] })
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(60000)
    expect(wrapper.get<HTMLInputElement>('#balance-interval').element.value).toBe('10')
    expect(wrapper.get<HTMLInputElement>('[data-test="auto-enabled"]').element.checked).toBe(true)
    expect(button(wrapper, 'common.save').attributes('disabled')).toBeDefined()
    expect(saveConfig).not.toHaveBeenCalled()
    expect(refresh).not.toHaveBeenCalled()
  })

  it('keeps edits on a version conflict and prevents duplicate saves', async () => {
    let rejectSave!: (error: unknown) => void
    saveConfig.mockReturnValue(new Promise((_, reject) => { rejectSave = reject }))
    const wrapper = mountView()
    await flushPromises()
    await button(wrapper, 'common.edit').trigger('click')
    await wrapper.get('#wallet-name').setValue('My edited wallet')
    await wrapper.get('#upstream-wallet-form').trigger('submit')
    await wrapper.get('#upstream-wallet-form').trigger('submit')
    expect(saveConfig).toHaveBeenCalledTimes(1)
    rejectSave({ status: 409 })
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('#wallet-name').element.value).toBe('My edited wallet')
    expect(showError).toHaveBeenCalledWith('admin.upstreamBalances.conflict')
  })

  it('limits refresh-all concurrency and stops scheduling when leaving the page', async () => {
    list.mockResolvedValue({ config: { ...defaultConfig(), wallets: [wallet('a'), wallet('b'), wallet('c')] }, items: [] })
    const resolvers: ((value: UpstreamBalanceSnapshot) => void)[] = []
    refresh.mockImplementation(() => new Promise(resolve => resolvers.push(resolve)))
    const wrapper = mountView()
    await flushPromises()
    await button(wrapper, 'admin.upstreamBalances.refreshAll').trigger('click')
    expect(refresh).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    resolvers[0](snapshot('a'))
    resolvers[1](snapshot('b'))
    await flushPromises()
    expect(refresh).toHaveBeenCalledTimes(2)
    expect(showInfo).not.toHaveBeenCalled()
  })

  it('preserves cached balances when site discovery or a manual query fails', async () => {
    discover.mockRejectedValue(new Error('offline'))
    refresh.mockRejectedValue({ status: 502 })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.upstreamBalances.discoveryFailed')
    expect(button(wrapper, 'common.edit').attributes('disabled')).toBeDefined()
    await button(wrapper, 'common.refresh').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('USD 120.00')
    expect(showError).toHaveBeenCalled()
  })

  it('shows a permission state without discovery or query requests on 403', async () => {
    list.mockRejectedValue({ status: 403 })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.upstreamBalances.forbidden')
    expect(discover).not.toHaveBeenCalled()
    expect(refresh).not.toHaveBeenCalled()
  })
})

describe('wallet membership boundaries', () => {
  it('prevents automatic merging and reusing Keys already assigned to another wallet', async () => {
    const existing = wallet('other', { single_account: false, account_ids: [1] })
    const wrapper = mount(UpstreamWalletDialog, { props: { show: true, saving: false, wallet: null, wallets: [existing], sites }, global: { stubs } })
    await wrapper.get('#wallet-site').setValue(sites[0].site_url)
    expect(wrapper.get('[data-test="single-account"]').attributes('disabled')).toBeDefined()
    const accounts = wrapper.findAll('fieldset input')
    expect(accounts[0].attributes('disabled')).toBeDefined()
    await accounts[1].setValue(true)
    await wrapper.get('#wallet-query').setValue(2)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]).toEqual([expect.objectContaining({ single_account: false, account_ids: [2], query_account_id: 2 })])
  })

  it('allows splitting an automatic group into a manually chosen wallet', async () => {
    const existing = wallet()
    const wrapper = mount(UpstreamWalletDialog, { props: { show: true, saving: false, wallet: existing, wallets: [existing], sites }, global: { stubs } })
    await wrapper.get('[data-test="single-account"]').setValue(false)
    await wrapper.findAll('fieldset input')[0].setValue(true)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]).toEqual([expect.objectContaining({ id: 'a', single_account: false, account_ids: [1] })])
  })

  it('preserves an unavailable explicitly selected query Key during unrelated edits', async () => {
    const existing = wallet('a', { query_account_id: 42 })
    const wrapper = mount(UpstreamWalletDialog, { props: { show: true, saving: false, wallet: existing, wallets: [existing], sites }, global: { stubs } })
    await flushPromises()
    await wrapper.get('#wallet-threshold').setValue(50)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]).toEqual([expect.objectContaining({ query_account_id: 42, threshold: 50 })])
  })

  it('rejects unsafe top-up URLs without saving', async () => {
    const existing = wallet()
    const wrapper = mount(UpstreamWalletDialog, { props: { show: true, saving: false, wallet: existing, wallets: [existing], sites }, global: { stubs } })
    await wrapper.get('#wallet-recharge').setValue('javascript:alert(1)')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.text()).toContain('admin.upstreamBalances.validationURL')
  })

  it('provides matching Chinese and English labels', () => {
    const keys = (value: Record<string, unknown>, prefix = ''): string[] => Object.entries(value).flatMap(([key, child]) => typeof child === 'object' && child ? keys(child as Record<string, unknown>, `${prefix}${key}.`) : `${prefix}${key}`)
    expect(keys(en).sort()).toEqual(keys(zh).sort())
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const { listAccounts } = vi.hoisted(() => ({
  listAccounts: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag: vi.fn(),
      getBatchTodayStats: vi.fn().mockResolvedValue({ stats: {} }),
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: vi.fn().mockResolvedValue([]) },
    groups: { getAll: vi.fn().mockResolvedValue([]) }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const DataTableStub = {
  props: ['columns', 'data'],
  emits: ['sort'],
  template: `
    <div data-test="data-table">
      <span v-for="column in columns" :key="column.key" :data-column="column.key">
        {{ column.sortable ? 'sortable' : 'fixed' }}
      </span>
      <div v-for="row in data" :key="row.id" data-test="group-priority-row">
        <slot name="cell-group_priority" :row="row" />
      </div>
      <button data-test="sort-priority" @click="$emit('sort', 'priority', 'desc')" />
    </div>
  `
}

function mountView() {
  return mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: DataTableStub,
        AccountTableActions: { template: '<div><slot name="after" /></div>' },
        AccountTableFilters: {
          props: ['filters'],
          emits: ['update:filters', 'change'],
          template: `
            <div>
              <button data-test="select-group" @click="$emit('update:filters', { ...filters, group: '7' }); $emit('change')" />
              <button data-test="select-all-groups" @click="$emit('update:filters', { ...filters, group: '' }); $emit('change')" />
              <button data-test="select-ungrouped" @click="$emit('update:filters', { ...filters, group: 'ungrouped' }); $emit('change')" />
            </div>
          `
        },
        AccountBulkActionsBar: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        HelpTooltip: true,
        Icon: true,
        Teleport: true
      }
    }
  })
}

describe('admin AccountsView priority column preferences', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
      pages: 0
    })
  })

  it('shows priority as a sortable column for fresh preferences', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-column="priority"]').text()).toBe('sortable')
    expect(wrapper.find('[data-column="group_priority"]').exists()).toBe(false)

    await wrapper.get('[data-test="sort-priority"]').trigger('click')
    await flushPromises()

    expect(listAccounts).toHaveBeenLastCalledWith(
      1,
      20,
      expect.objectContaining({ sort_by: 'priority', sort_order: 'desc' }),
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })

  it('shows a separate non-sortable group priority for a specific group filter', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-group"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-column="group_priority"]').text()).toBe('fixed')

    await wrapper.get('[data-test="select-all-groups"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-column="group_priority"]').exists()).toBe(false)

    await wrapper.get('[data-test="select-group"]').trigger('click')
    await wrapper.get('[data-test="select-ungrouped"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-column="group_priority"]').exists()).toBe(false)
  })

  it('does not offer the previous group value while the next group list is loading', async () => {
    vi.useFakeTimers()
    const page = (groupPriority: number, groupPriorityGroupID: number) => ({
      items: [{
        id: 7,
        name: 'Account',
        priority: 11,
        group_ids: [5, 7],
        group_priority: groupPriority,
        group_priority_group_id: groupPriorityGroupID
      }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    let resolveGroupPage: ((value: ReturnType<typeof page>) => void) | undefined
    listAccounts.mockReset()
      .mockResolvedValueOnce(page(3, 5))
      .mockImplementationOnce(() => new Promise(resolve => { resolveGroupPage = resolve }))

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="select-group"]').trigger('click')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()

    expect(wrapper.find('[data-testid="account-priority-cell"]').exists()).toBe(false)

    expect(listAccounts).toHaveBeenCalledTimes(2)
    resolveGroupPage?.(page(8, 7))
    await flushPromises()

    expect(wrapper.find('[data-testid="account-priority-cell"]').exists()).toBe(true)
    wrapper.unmount()
    vi.useRealTimers()
  })

  it('preserves an existing preference that explicitly hides priority', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['priority', 'today_stats']))
    localStorage.setItem('account-hidden-columns-version', 'scheduler-score-hidden-by-default')

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-column="priority"]').exists()).toBe(false)
    expect(JSON.parse(localStorage.getItem('account-hidden-columns') || '[]')).toEqual([
      'priority',
      'today_stats'
    ])
  })

  it('keeps priority visible while migrating older saved preferences', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['today_stats']))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-column="priority"]').text()).toBe('sortable')
    expect(JSON.parse(localStorage.getItem('account-hidden-columns') || '[]')).toEqual(
      expect.arrayContaining(['today_stats', 'scheduler_score'])
    )
    expect(JSON.parse(localStorage.getItem('account-hidden-columns') || '[]')).not.toContain('priority')
  })
})

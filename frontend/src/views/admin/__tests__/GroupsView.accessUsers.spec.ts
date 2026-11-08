import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupsView from '@/views/admin/GroupsView.vue'
import type { AdminGroup } from '@/types'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), update: vi.fn(), getGroupRateMultipliers: vi.fn(), getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(), getLiveCapability: vi.fn(), getModelAllowlistCandidates: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn()
}))
const authState = vi.hoisted(() => ({ isSimpleMode: false }))
vi.mock('@/api/admin', () => ({ adminAPI: {
  groups: { ...mocks, getAll: vi.fn(), updateSortOrder: vi.fn() },
  accounts: { list: vi.fn(), getById: vi.fn() }
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => authState }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: () => false }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
enableAutoUnmount(afterEach)

const group = {
  id: 42, name: 'Primary', description: null, platform: 'openai', rate_multiplier: 1,
  is_exclusive: false, status: 'active', subscription_type: 'standard',
  daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null,
  image_price_1k: null, image_price_2k: null, image_price_4k: null,
  video_price_480p: null, video_price_720p: null, video_price_1080p: null,
  fallback_group_id: null, fallback_group_id_on_invalid_request: null,
  model_routing: null, model_routing_enabled: false, supported_model_scopes: [], sort_order: 0,
} as AdminGroup
const candidates = [
  { user_id: 1, user_name: 'One', user_email: 'one@example.com', user_notes: '', user_status: 'active', rate_multiplier: 0, has_explicit_access: false },
  { user_id: 2, user_name: 'Two', user_email: 'two@example.com', user_notes: '', user_status: 'active', rate_multiplier: 1, has_explicit_access: false },
  { user_id: 3, user_name: 'Three', user_email: 'three@example.com', user_notes: '', user_status: 'active', rate_multiplier: 0.5, has_explicit_access: true },
]
const BaseDialogStub = defineComponent({
  props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})
function mountView(value = group) {
  mocks.list.mockResolvedValue({ items: [value], total: 1, page: 1, page_size: 20, pages: 1 })
  return mount(GroupsView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
    DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' },
    BaseDialog: BaseDialogStub, ConfirmDialog: true, EmptyState: true, Select: true,
    Icon: true, PlatformIcon: true, Pagination: true, GroupCapacityBadge: true,
    GroupRateMultipliersModal: true, GroupRPMOverridesModal: true, VueDraggable: true,
  } } })
}
async function edit(wrapper: VueWrapper) {
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
  await flushPromises()
}
async function makeExclusive(wrapper: VueWrapper) {
  await wrapper.get('[data-testid="edit-group-exclusive"]').trigger('click')
  await flushPromises()
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('GroupsView exclusive access selection', () => {
  beforeEach(() => {
    localStorage.clear()
    authState.isSimpleMode = false
    Object.values(mocks).forEach(mock => mock.mockReset())
    mocks.getGroupRateMultipliers.mockResolvedValue(candidates)
    mocks.getModelAllowlistCandidates.mockResolvedValue([])
    mocks.getUsageSummary.mockResolvedValue([])
    mocks.getCapacitySummary.mockResolvedValue([])
    mocks.getLiveCapability.mockResolvedValue({ supported: false })
    mocks.update.mockResolvedValue(group)
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => vi.restoreAllMocks())

  it('submits the exclusive switch and only newly selected grants in a single request', async () => {
    const wrapper = mountView()
    await edit(wrapper)
    expect(mocks.getGroupRateMultipliers).not.toHaveBeenCalled()
    await makeExclusive(wrapper)
    await wrapper.get('[data-user-id="2"] input').setValue(false)
    expect(wrapper.get('[form="edit-group-form"]').text()).toBe('admin.groups.accessUsers.saveAndGrant')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ is_exclusive: true, grant_user_ids: [1] }))
  })

  it('allows already exclusive groups to add access without preselecting new users', async () => {
    const wrapper = mountView({ ...group, is_exclusive: true })
    await edit(wrapper)
    expect(wrapper.get<HTMLInputElement>('[data-user-id="1"] input').element.checked).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[data-user-id="3"] input').element.checked).toBe(true)
    await wrapper.get('[data-user-id="2"] input').setValue(true)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ is_exclusive: true, grant_user_ids: [2] }))
  })

  it('blocks saving while loading or failed, then permits retry', async () => {
    const request = deferred<typeof candidates>()
    mocks.getGroupRateMultipliers.mockReturnValueOnce(request.promise)
    const wrapper = mountView()
    await edit(wrapper)
    await makeExclusive(wrapper)
    expect(wrapper.get('[form="edit-group-form"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#edit-group-form').trigger('submit')
    expect(mocks.update).not.toHaveBeenCalled()
    request.reject(new Error('offline'))
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.get('[form="edit-group-form"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.get('[form="edit-group-form"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ grant_user_ids: [1, 2] }))
  })

  it('does not grant on cancel and resets the draft when reopened', async () => {
    const wrapper = mountView()
    await edit(wrapper)
    await makeExclusive(wrapper)
    await wrapper.get('[data-user-id="1"] input').setValue(false)
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    expect(mocks.update).not.toHaveBeenCalled()
    await edit(wrapper)
    expect(wrapper.find('[data-testid="group-access-users"]').exists()).toBe(false)
    await makeExclusive(wrapper)
    expect(wrapper.get<HTMLInputElement>('[data-user-id="1"] input').element.checked).toBe(true)
  })

  it('omits access changes when switched back to public', async () => {
    const wrapper = mountView()
    await edit(wrapper)
    await makeExclusive(wrapper)
    await makeExclusive(wrapper)
    expect(wrapper.find('[data-testid="group-access-users"]').exists()).toBe(false)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update.mock.calls[0][1]).not.toHaveProperty('grant_user_ids')
    expect(mocks.update.mock.calls[0][1].is_exclusive).toBe(false)
  })

  it('preserves subscription handling without loading or submitting explicit grants', async () => {
    const wrapper = mountView({ ...group, is_exclusive: true, subscription_type: 'subscription' })
    await edit(wrapper)
    expect(wrapper.find('[data-testid="group-access-users"]').exists()).toBe(false)
    expect(mocks.getGroupRateMultipliers).not.toHaveBeenCalled()
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update.mock.calls[0][1]).not.toHaveProperty('grant_user_ids')
  })

  it('blocks duplicate submission and retains selections after save failure', async () => {
    const request = deferred<AdminGroup>()
    mocks.update.mockReturnValueOnce(request.promise)
    const wrapper = mountView()
    await edit(wrapper)
    await makeExclusive(wrapper)
    await wrapper.get('#edit-group-form').trigger('submit')
    await wrapper.get('#edit-group-form').trigger('submit')
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(wrapper.get<HTMLInputElement>('[data-user-id="1"] input').element.disabled).toBe(true)
    request.reject(new Error('save failed'))
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-user-id="1"] input').element.checked).toBe(true)
    expect(wrapper.get('[form="edit-group-form"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update.mock.calls[1][1].grant_user_ids).toEqual([1, 2])
  })
})

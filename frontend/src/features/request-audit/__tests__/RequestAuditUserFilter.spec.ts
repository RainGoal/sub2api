import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import Select from '@/components/common/Select.vue'
import RequestAuditUserFilter from '../RequestAuditUserFilter.vue'

const searchUsers = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/usage', () => ({ searchUsers }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const alice = { id: 3, email: 'alice@example.com', deleted: false }
const createFilter = () => mount(RequestAuditUserFilter, { props: { modelValue: '' }, global: { stubs: { Select: true } } })

describe('RequestAuditUserFilter', () => {
  beforeEach(() => { searchUsers.mockReset() })

  it('searches by keyword and preserves the selected user while another search runs', async () => {
    searchUsers.mockResolvedValue([alice])
    const wrapper = createFilter()
    const select = wrapper.getComponent(Select)
    select.vm.$emit('search', ' alice ')
    await flushPromises()
    expect(searchUsers).toHaveBeenCalledWith('alice')
    const option = select.props('options')[0]
    expect(option).toEqual({ value: '3', label: 'alice@example.com · #3' })
    select.vm.$emit('change', '3', option)
    await wrapper.setProps({ modelValue: '3', label: 'alice@example.com · #3' })
    searchUsers.mockResolvedValue([])
    select.vm.$emit('search', 'missing')
    await flushPromises()
    expect(select.props('options')).toContainEqual(option)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['3'])
    expect(wrapper.emitted('update:label')?.[0]).toEqual(['alice@example.com · #3'])
    select.vm.$emit('change', null, null)
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual([''])
    wrapper.unmount()
  })

  it('ignores older results and errors after a newer keyword or reset', async () => {
    let resolveOld!: (users: unknown[]) => void
    searchUsers.mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
    const wrapper = createFilter()
    const select = wrapper.getComponent(Select)
    select.vm.$emit('search', 'old')
    searchUsers.mockResolvedValueOnce([{ ...alice, deleted: true }])
    select.vm.$emit('search', 'alice')
    await flushPromises()
    resolveOld([{ id: 9, email: 'old@example.com', deleted: false }])
    await flushPromises()
    expect(select.props('options')).toEqual([{ value: '3', label: 'alice@example.com · #3 · admin.requestAudit.deletedUser' }])
    await wrapper.setProps({ modelValue: '3' })
    let rejectOld!: (error: Error) => void
    searchUsers.mockReturnValueOnce(new Promise((_resolve, reject) => { rejectOld = reject }))
    select.vm.$emit('search', 'old')
    await wrapper.setProps({ modelValue: '' })
    rejectOld(new Error('late failure'))
    await flushPromises()
    expect(select.props('options')).toEqual([])
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reports search failures and recovers when a new query succeeds', async () => {
    searchUsers.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = createFilter()
    const select = wrapper.getComponent(Select)
    select.vm.$emit('search', 'alice')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.requestAudit.userSearchFailed')
    searchUsers.mockResolvedValueOnce([alice])
    select.vm.$emit('search', 'alice@example.com')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(select.props('options')).toHaveLength(1)
    wrapper.unmount()
  })

  it('displays a restored selection and falls back to its ID when the label is unavailable', async () => {
    const wrapper = createFilter()
    await wrapper.setProps({ modelValue: '3', label: 'alice@example.com · #3' })
    expect(wrapper.getComponent(Select).props('options')).toEqual([{ value: '3', label: 'alice@example.com · #3' }])
    await wrapper.setProps({ modelValue: '4', label: '' })
    expect(wrapper.getComponent(Select).props('options')).toEqual([{ value: '4', label: 'admin.requestAudit.userFallback' }])
    wrapper.unmount()
  })
})

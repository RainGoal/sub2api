import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import Select from '@/components/common/Select.vue'
import RequestAuditGroupFilter from '../RequestAuditGroupFilter.vue'

const getGroups = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: getGroups }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const createFilter = () => mount(RequestAuditGroupFilter, { props: { modelValue: '' }, global: { stubs: { Select: true } } })

describe('RequestAuditGroupFilter', () => {
  beforeEach(() => { getGroups.mockReset() })

  it('loads searchable groups including inactive ones and emits the selected ID and name', async () => {
    getGroups.mockResolvedValue([{ id: 7, name: 'Claude Pro', status: 'active' }, { id: 8, name: 'Retired group', status: 'inactive' }])
    const wrapper = createFilter()
    await flushPromises()
    expect(getGroups).toHaveBeenCalledOnce()
    const select = wrapper.getComponent(Select)
    expect(select.props('searchable')).toBe(true)
    expect(select.props('options')).toEqual([{ value: '7', label: 'Claude Pro' }, { value: '8', label: 'Retired group' }])
    select.vm.$emit('change', '8', select.props('options')[1])
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['8'])
    expect(wrapper.emitted('update:label')?.[0]).toEqual(['Retired group'])
    select.vm.$emit('change', null, null)
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual([''])
    expect(wrapper.emitted('update:label')?.[1]).toEqual([''])
    wrapper.unmount()
  })

  it('shows a retry action after loading fails and recovers without changing the selection', async () => {
    getGroups.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = createFilter()
    await wrapper.setProps({ modelValue: '7', label: 'Claude Pro' })
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.requestAudit.groupLoadFailed')
    expect(wrapper.getComponent(Select).props('options')).toEqual([{ value: '7', label: 'Claude Pro' }])
    getGroups.mockResolvedValueOnce([{ id: 7, name: 'Claude Pro' }])
    await wrapper.get('[data-test="retry-groups"]').trigger('click')
    await flushPromises()
    expect(getGroups).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.getComponent(Select).props('modelValue')).toBe('7')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })

})

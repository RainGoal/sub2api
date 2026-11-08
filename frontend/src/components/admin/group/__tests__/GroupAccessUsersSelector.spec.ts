import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import GroupAccessUsersSelector from '../GroupAccessUsersSelector.vue'
import type { GroupRateMultiplierEntry } from '@/api/admin/groups'

const { getGroupRateMultipliers } = vi.hoisted(() => ({ getGroupRateMultipliers: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getGroupRateMultipliers } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

function user(id: number, overrides: Partial<GroupRateMultiplierEntry> = {}): GroupRateMultiplierEntry {
  return { user_id: id, user_name: `User ${id}`, user_email: `user${id}@example.com`, user_notes: '',
    user_status: 'active', rate_multiplier: 0.5, has_explicit_access: false, ...overrides }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
function open(selectByDefault = true) {
  return mount(GroupAccessUsersSelector, { props: { groupId: 1, selectByDefault }, global: { stubs: {
    Pagination: { props: ['page'], emits: ['update:page'], template: '<button type="button" data-testid="next-page" @click="$emit(\'update:page\', page + 1)">Next</button>' }
  } } })
}
function lastSelection(wrapper: VueWrapper) {
  return wrapper.emitted('selection-change')!.at(-1)![0] as number[]
}
async function clickAction(wrapper: VueWrapper, action: string) {
  await wrapper.findAll('button').find(button => button.text() === `admin.groups.accessUsers.${action}`)!.trigger('click')
}

describe('GroupAccessUsersSelector', () => {
  beforeEach(() => { getGroupRateMultipliers.mockReset().mockResolvedValue([]) })

  it('includes zero and one multipliers, excludes RPM-only rows, and preserves existing access', async () => {
    getGroupRateMultipliers.mockResolvedValue([
      user(1, { rate_multiplier: 0 }), user(2, { rate_multiplier: 1 }),
      user(3, { rate_multiplier: null, rpm_override: 60 }),
      user(4, { has_explicit_access: true }), user(5, { user_status: 'inactive' })
    ])
    const wrapper = open()
    await flushPromises()
    expect(lastSelection(wrapper)).toEqual([1, 2])
    expect(wrapper.find('[data-user-id="3"]').exists()).toBe(false)
    const authorized = wrapper.get<HTMLInputElement>('[data-user-id="4"] input')
    expect(authorized.element.checked).toBe(true)
    expect(authorized.element.disabled).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-user-id="5"] input').element.disabled).toBe(true)
    await clickAction(wrapper, 'clearSelection')
    expect(lastSelection(wrapper)).toEqual([])
    expect(authorized.element.checked).toBe(true)
    expect(wrapper.emitted('ready-change')).toEqual([[false], [true]])
  })

  it('does not add grants by default when editing an already exclusive group', async () => {
    getGroupRateMultipliers.mockResolvedValue([user(1), user(2, { has_explicit_access: true })])
    const wrapper = open(false)
    await flushPromises()
    expect(lastSelection(wrapper)).toEqual([])
    await wrapper.get('[data-user-id="1"] input').setValue(true)
    expect(lastSelection(wrapper)).toEqual([1])
  })

  it('retains selection across pages and searches and clearly selects the full candidate set', async () => {
    getGroupRateMultipliers.mockResolvedValue(Array.from({ length: 21 }, (_, index) => user(index + 1, { user_notes: index === 16 ? 'priority' : '' })))
    const wrapper = open(false)
    await flushPromises()
    await clickAction(wrapper, 'selectPage')
    expect(lastSelection(wrapper)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10])
    await wrapper.get('[data-testid="next-page"]').trigger('click')
    expect(wrapper.find('[data-user-id="11"]').exists()).toBe(true)
    await wrapper.get('[data-user-id="11"] input').setValue(true)
    await wrapper.get('input[type="search"]').setValue('priority')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    await clickAction(wrapper, 'selectPage')
    expect(lastSelection(wrapper)).toContain(17)
    expect(lastSelection(wrapper)).toContain(11)
    await clickAction(wrapper, 'selectAll')
    expect(lastSelection(wrapper)).toHaveLength(21)
    await clickAction(wrapper, 'clearSelection')
    expect(lastSelection(wrapper)).toEqual([])
  })

  it('blocks readiness after a failure and can retry an empty candidate list', async () => {
    getGroupRateMultipliers.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([])
    const wrapper = open()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.groups.accessUsers.loadFailed')
    expect(wrapper.emitted('ready-change')).toEqual([[false]])
    await clickAction(wrapper, 'retry')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.groups.accessUsers.empty')
    expect(wrapper.emitted('ready-change')!.at(-1)).toEqual([true])
  })

  it('prevents embedded pagination buttons from submitting the group form', async () => {
    getGroupRateMultipliers.mockResolvedValue(Array.from({ length: 11 }, (_, index) => user(index + 1)))
    const wrapper = mount(GroupAccessUsersSelector, {
      props: { groupId: 1, selectByDefault: false }, global: { stubs: { Icon: true } }
    })
    await flushPromises()
    const click = new MouseEvent('click', { bubbles: true, cancelable: true })
    wrapper.get('nav button[aria-label="pagination.next"]').element.dispatchEvent(click)
    await wrapper.vm.$nextTick()
    expect(click.defaultPrevented).toBe(true)
    expect(wrapper.find('[data-user-id="11"]').exists()).toBe(true)
  })

  it('discards old group responses and resets selections immediately when the group changes', async () => {
    const previous = deferred<GroupRateMultiplierEntry[]>()
    getGroupRateMultipliers.mockReturnValueOnce(previous.promise).mockResolvedValueOnce([user(2)])
    const wrapper = open()
    await wrapper.setProps({ groupId: 2 })
    await flushPromises()
    expect(lastSelection(wrapper)).toEqual([2])
    previous.resolve([user(1)])
    await flushPromises()
    expect(lastSelection(wrapper)).toEqual([2])
    expect(wrapper.find('[data-user-id="1"]').exists()).toBe(false)
    expect(getGroupRateMultipliers).toHaveBeenLastCalledWith(2)
  })

  it('ignores responses after unmount and disables selection during saving', async () => {
    getGroupRateMultipliers.mockResolvedValueOnce([user(1)])
    const wrapper = open(false)
    await flushPromises()
    await wrapper.setProps({ disabled: true })
    expect(wrapper.get<HTMLInputElement>('[data-user-id="1"] input').element.disabled).toBe(true)
    await clickAction(wrapper, 'selectAll')
    expect(lastSelection(wrapper)).toEqual([])
    const request = deferred<GroupRateMultiplierEntry[]>()
    getGroupRateMultipliers.mockReturnValueOnce(request.promise)
    const readyChanged = vi.fn()
    const pending = mount(GroupAccessUsersSelector, { props: { groupId: 1, selectByDefault: true, onReadyChange: readyChanged } })
    const eventsBeforeUnmount = readyChanged.mock.calls.length
    pending.unmount()
    request.resolve([user(3)])
    await flushPromises()
    expect(readyChanged).toHaveBeenCalledTimes(eventsBeforeUnmount)
  })
})

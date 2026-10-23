import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import DateRangePicker from '../DateRangePicker.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(2026, 9, 22, 23, 59, 12))
})
afterEach(() => vi.useRealTimers())

const mountPicker = (startDate = '2026-10-22', endDate = '2026-10-22') => mount(DateRangePicker, {
  props: { startDate, endDate, enableTime: true },
  global: { stubs: { Icon: true } },
})

describe('DateRangePicker precise ranges', () => {
  it('selects exactly 24 hours at 23:59, preserving seconds and timezone', async () => {
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    await wrapper.findAll('.date-picker-preset').find(b => b.text() === 'dates.last24Hours')!.trigger('click')
    const inputs = wrapper.findAll<HTMLInputElement>('input[type="datetime-local"]')
    expect(inputs).toHaveLength(2)
    expect(inputs[0].attributes('step')).toBe('1')
    expect(new Date(inputs[1].element.value).getTime() - new Date(inputs[0].element.value).getTime()).toBe(86400000)
    await wrapper.get('.date-picker-apply').trigger('click')
    const range = wrapper.emitted('change')![0][0] as { startDate: string; endDate: string; preset: string }
    expect(range).toEqual({
      startDate: new Date(2026, 9, 21, 23, 59, 12).toISOString(),
      endDate: new Date(2026, 9, 22, 23, 59, 12).toISOString(),
      preset: 'last24Hours',
    })
  })

  it('keeps legacy date ranges as full calendar days in the time inputs', async () => {
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    const values = wrapper.findAll<HTMLInputElement>('input').map(input => new Date(input.element.value).getTime())
    expect(values).toEqual([new Date(2026, 9, 22).getTime(), new Date(2026, 9, 23).getTime()])
    await wrapper.get('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')![0][0]).toMatchObject({
      startDate: new Date(2026, 9, 22).toISOString(),
      endDate: new Date(2026, 9, 23).toISOString(),
      preset: 'today',
    })
  })

  it('submits custom second-level bounds and displays both times', async () => {
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('2026-10-22T12:30:01')
    await inputs[1].setValue('2026-10-22T12:30:09')
    await wrapper.get('.date-picker-apply').trigger('click')
    const range = wrapper.emitted('change')![0][0] as { startDate: string; endDate: string }
    expect(range).toMatchObject({
      startDate: new Date(2026, 9, 22, 12, 30, 1).toISOString(),
      endDate: new Date(2026, 9, 22, 12, 30, 9).toISOString(),
      preset: null,
    })
    await wrapper.setProps(range)
    expect(wrapper.get('.date-picker-trigger').text()).toContain('12:30:01')
    expect(wrapper.get('.date-picker-trigger').text()).toContain('12:30:09')
  })

  it.each(['2026-10-22T12:30:01', '2026-10-22T12:29:59', ''])('blocks invalid end %s', async (end) => {
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('2026-10-22T12:30:01')
    await inputs[1].setValue(end)
    expect(wrapper.get('.date-picker-apply').attributes('disabled')).toBeDefined()
    await wrapper.get('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')).toBeUndefined()
  })
})

import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ModelPlazaMonitorSettings from '../ModelPlazaMonitorSettings.vue'
import zh from '@/i18n/locales/zh/admin/settings'
import en from '@/i18n/locales/en/admin/settings'
import type { PlazaMonitorConfig } from '@/api/admin/modelPlazaMonitor'

const api = vi.hoisted(() => ({ config: vi.fn(), candidates: vi.fn(), save: vi.fn(), run: vi.fn(), status: vi.fn() }))
vi.mock('@/api/admin/modelPlazaMonitor', () => ({ modelPlazaMonitorAPI: api }))

const initial: PlazaMonitorConfig = {
  version: 3, enabled: true, default_interval_seconds: 300,
  groups: [{ group_id: 1, enabled: true, interval_seconds: 120, probe_api_key_id: 11,
    models: [{ platform: 'openai', name: 'model-a', interval_seconds: 0, api_mode: 'chat_completions' }] }]
}
const options = {
  groups: [
    { id: 1, name: 'Group A', platform: 'openai', models: [
      { platform: 'openai', name: 'model-a', supported: true },
      { platform: 'openai', name: 'model-b', supported: true },
      { platform: 'openai', name: 'image-model', supported: false }
    ] },
    { id: 2, name: 'Group B', platform: 'openai', models: [
      { platform: 'openai', name: 'model-a', supported: true },
      { platform: 'openai', name: 'model-b', supported: true }
    ] }
  ],
  api_keys: [{ id: 11, name: 'Probe A', group_id: 1 }, { id: 22, name: 'Probe B', group_id: 2 }]
}
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value))
async function open(locale = 'zh') {
  const wrapper = mount(ModelPlazaMonitorSettings, { global: {
    plugins: [createI18n({ legacy: false, locale, messages: { zh: { admin: zh }, en: { admin: en } } })],
    stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } }
  } })
  await wrapper.get('[data-test="monitor-open"]').trigger('click')
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  api.config.mockResolvedValue(clone(initial))
  api.candidates.mockResolvedValue(clone(options))
  api.status.mockResolvedValue({ enabled: true, computed_at: '', items: [] })
  api.run.mockResolvedValue({ enabled: true, computed_at: '', items: [] })
  api.save.mockImplementation(async (config: PlazaMonitorConfig) => ({ ...clone(config), version: config.version + 1 }))
})

describe('Model Plaza monitoring settings', () => {
  afterEach(() => vi.useRealTimers())

  it('refreshes asynchronous check results for a bounded period without sending more probes', async () => {
    vi.useFakeTimers()
    const wrapper = await open()
    await wrapper.get('[data-test="monitor-run"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('已提交到期模型')
    expect(wrapper.text()).not.toContain('上次检测')
    api.status.mockResolvedValue({ enabled: true, computed_at: '', items: [{
      group_id: 1, platform: 'openai', model: 'model-a', interval_seconds: 120,
      status: 'operational', last_checked_at: '2026-11-10T01:00:00Z',
      next_check_at: '2026-11-10T01:02:00Z', timeline: []
    }] })
    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.text()).toContain('上次检测')
    expect(api.run).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(34000)
    const calls = api.status.mock.calls.length
    await vi.advanceTimersByTimeAsync(60000)
    expect(api.status).toHaveBeenCalledTimes(calls)
    wrapper.unmount()
  })

  it('cancels pending check refreshes when switching groups or closing the dialog', async () => {
    vi.useFakeTimers()
    const wrapper = await open()
    await wrapper.get('[data-test="monitor-run"]').trigger('click')
    await flushPromises()
    const signal = api.status.mock.calls[0][1] as AbortSignal
    await wrapper.get('[data-test="monitor-group-2"]').trigger('click')
    expect(signal.aborted).toBe(true)
    const calls = api.status.mock.calls.length
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.status).toHaveBeenCalledTimes(calls)
    await wrapper.get('[data-test="monitor-group-1"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="monitor-run"]').trigger('click')
    await flushPromises()
    const close = wrapper.findAll('button').find(button => button.text() === '关闭')!
    await close.trigger('click')
    const afterClose = api.status.mock.calls.length
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.status).toHaveBeenCalledTimes(afterClose)
    wrapper.unmount()
  })

  it('keeps model selections and second overrides separate for each group', async () => {
    const wrapper = await open()
    expect(wrapper.get('[data-test="monitor-interval-openai-model-a"]').attributes('placeholder')).toBe('继承 120 秒')
    expect(wrapper.get('[data-test="monitor-run"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-test="monitor-interval-openai-model-a"]').setValue('45')
    expect(wrapper.get('[data-test="monitor-run"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="monitor-group-2"]').trigger('click')
    expect(wrapper.get('[data-test="monitor-model-openai-model-a"]').element).toHaveProperty('checked', false)
    expect(wrapper.get('[data-test="monitor-key"]').text()).not.toContain('Probe A')
    await wrapper.get('[data-test="monitor-group-enabled"]').trigger('click')
    await wrapper.get('[data-test="monitor-key"]').setValue('22')
    await wrapper.get('[data-test="monitor-model-openai-model-b"]').setValue(true)
    await wrapper.get('[data-test="monitor-save"]').trigger('click')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ version: 3, groups: [
      expect.objectContaining({ group_id: 1, models: [expect.objectContaining({ name: 'model-a', interval_seconds: 45 })] }),
      expect.objectContaining({ group_id: 2, probe_api_key_id: 22, models: [expect.objectContaining({ name: 'model-b', interval_seconds: 0 })] })
    ] }))
    expect(wrapper.get('[data-test="monitor-run"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('preserves edits on a conflict and never executes unsaved configuration', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="monitor-interval-openai-model-a"]').setValue('17')
    api.save.mockRejectedValueOnce({ status: 409 })
    await wrapper.get('[data-test="monitor-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('其他管理员')
    expect(wrapper.get('[data-test="monitor-interval-openai-model-a"]').element).toHaveProperty('value', '17')
    await wrapper.get('[data-test="monitor-run"]').trigger('click')
    expect(api.run).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('clearing an override restores inheritance and rejects invalid seconds', async () => {
    const config = clone(initial)
    config.groups[0].models[0].interval_seconds = 60
    api.config.mockResolvedValueOnce(config)
    const wrapper = await open()
    const interval = wrapper.get('[data-test="monitor-interval-openai-model-a"]')
    await interval.setValue('1.5')
    await wrapper.get('[data-test="monitor-save"]').trigger('click')
    expect(wrapper.get('[role="alert"]').text()).toContain('整数秒')
    expect(api.save).not.toHaveBeenCalled()
    await interval.setValue('')
    await wrapper.get('[data-test="monitor-save"]').trigger('click')
    await flushPromises()
    expect(api.save.mock.calls[0][0].groups[0].models[0].interval_seconds).toBe(0)
    wrapper.unmount()
  })

  it('blocks unsupported models and stops manual checks while globally disabled', async () => {
    const wrapper = await open()
    expect(wrapper.get('[data-test="monitor-model-openai-image-model"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="monitor-enabled"]').trigger('click')
    await wrapper.get('[data-test="monitor-save"]').trigger('click')
    await flushPromises()
    expect(api.save.mock.calls[0][0].enabled).toBe(false)
    expect(wrapper.get('[data-test="monitor-run"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('sends only the saved group ID and blocks duplicate checks', async () => {
    const wrapper = await open()
    let finish!: (value: unknown) => void
    api.run.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await wrapper.get('[data-test="monitor-run"]').trigger('click')
    await wrapper.get('[data-test="monitor-run"]').trigger('click')
    expect(api.run).toHaveBeenCalledTimes(1)
    expect(api.run).toHaveBeenCalledWith(1)
    finish({ enabled: true, items: [] })
    await flushPromises()
    wrapper.unmount()
  })

  it('can turn monitoring off after a saved probe key becomes unavailable', async () => {
    api.candidates.mockResolvedValueOnce({ ...clone(options), api_keys: [] })
    const wrapper = await open()
    await wrapper.get('[data-test="monitor-enabled"]').trigger('click')
    await wrapper.get('[data-test="monitor-save"]').trigger('click')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ enabled: false }))
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows permission errors without enabling the editor', async () => {
    api.config.mockRejectedValueOnce({ status: 403 })
    const wrapper = await open()
    expect(wrapper.get('[role="alert"]').text()).toContain('权限')
    expect(wrapper.get('[data-test="monitor-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="monitor-enabled"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps new English and Chinese keys aligned and renders English controls', async () => {
    expect(Object.keys(zh.settings.features.modelPlaza.monitor).sort()).toEqual(Object.keys(en.settings.features.modelPlaza.monitor).sort())
    const wrapper = await open('en')
    expect(wrapper.text()).toContain('Group interval (seconds)')
    expect(wrapper.text()).toContain('Each probe sends only hi')
    expect(wrapper.text()).not.toMatch(/[\u3400-\u9fff]/)
    wrapper.unmount()
  })
})

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import OpenAICollaborationSettings from '../OpenAICollaborationSettings.vue'
import zh from '@/i18n/locales/zh/admin/settings'
import en from '@/i18n/locales/en/admin/settings'
import type { OpenAICollaborationSettings as Settings } from '@/api/admin/settings'

const api = vi.hoisted(() => ({ getSettings: vi.fn(), updateSettings: vi.fn(), getAll: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { settings: api, groups: { getAll: api.getAll } } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: api.showSuccess, showError: api.showError }) }))

const enabled: Settings = {
  enabled: true, plaintext_enabled: true, legacy_cipher_recovery_enabled: true,
  blank_prelude_wait_seconds: 10,
  group_ids: [7], api_key_ids: [12]
}
const clone = (config: Settings): Settings => ({ ...config, group_ids: [...config.group_ids], api_key_ids: [...config.api_key_ids] })
async function open(locale = 'zh', active = true) {
  const wrapper = mount(OpenAICollaborationSettings, {
    props: { active },
    global: { plugins: [createI18n({ legacy: false, locale, messages: { zh: { admin: zh }, en: { admin: en } } })] }
  })
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  api.getSettings.mockResolvedValue({ openai_collaboration_settings: clone(enabled) })
  api.getAll.mockResolvedValue([
    { id: 7, name: 'OpenAI group', platform: 'openai' },
    { id: 8, name: 'Composite group', platform: 'composite' },
    { id: 9, name: 'Other group', platform: 'anthropic' }
  ])
  api.updateSettings.mockImplementation(async payload => ({ openai_collaboration_settings: payload.openai_collaboration_settings }))
})

describe('OpenAI collaboration settings', () => {
  it('preserves edits on failure and blocks duplicate saves', async () => {
    let reject!: (reason: Error) => void
    api.updateSettings.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail }))
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-key-ids"]').setValue('42')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    expect(api.updateSettings).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="collaboration-stop"]').attributes('disabled')).toBeDefined()
    reject(new Error('unavailable'))
    await flushPromises()
    expect(wrapper.get('[data-test="collaboration-key-ids"]').element).toHaveProperty('value', '42')
    expect(wrapper.get('[data-test="collaboration-status"]').text()).toContain('开启')
    expect(api.showError).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
  it('does not report success if the server cannot confirm the saved config', async () => {
    api.updateSettings.mockResolvedValueOnce({})
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-stop"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('无法确认')
    expect(wrapper.get('[data-test="collaboration-save"]').attributes('disabled')).toBeDefined()
    expect(api.showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('keeps translations aligned and renders the English controls without Chinese', async () => {
    expect(Object.keys(zh.settings.openaiCollaboration).sort()).toEqual(Object.keys(en.settings.openaiCollaboration).sort())
    const wrapper = await open('en')
    expect(wrapper.text()).toContain('Disable compatibility handling')
    expect(wrapper.text()).toContain('No restart is required')
    expect(wrapper.text()).not.toMatch(/[\u3400-\u9fff]/)
    wrapper.unmount()
  })
  it.each([undefined, null, { enabled: true }])('never saves a missing or incomplete setting: %s', async value => {
    api.getSettings.mockResolvedValueOnce({ openai_collaboration_settings: value })
    const wrapper = await open()
    expect(wrapper.get('[role="alert"]').text()).toContain('禁止保存')
    expect(wrapper.get('[data-test="collaboration-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="collaboration-enabled"]').exists()).toBe(false)
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    expect(api.updateSettings).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('reports permission failure and permits a later reload', async () => {
    api.getSettings.mockRejectedValueOnce({ response: { status: 403 } })
    const wrapper = await open()
    expect(wrapper.get('[role="alert"]').text()).toContain('权限')
    await wrapper.get('[data-test="collaboration-reload"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="collaboration-enabled"]').exists()).toBe(true)
    wrapper.unmount()
  })
  it('can stop immediately despite group loading failure and invalid unsaved input', async () => {
    api.getAll.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-key-ids"]').setValue('not-an-id')
    await wrapper.get('[data-test="collaboration-blank-wait"]').setValue('')
    await wrapper.get('[data-test="collaboration-plaintext"]').trigger('click')
    await wrapper.get('[data-test="collaboration-stop"]').trigger('click')
    await flushPromises()
    expect(api.updateSettings).toHaveBeenCalledWith({ openai_collaboration_settings: { ...enabled, enabled: false } })
    expect(wrapper.get('[data-test="collaboration-status"]').text()).toContain('关闭')
    wrapper.unmount()
  })
  it('loads older settings without a wait limit as 2 seconds', async () => {
    api.getSettings.mockResolvedValueOnce({ openai_collaboration_settings: { ...enabled, blank_prelude_wait_seconds: undefined } })
    const wrapper = await open()
    expect(wrapper.get('[data-test="collaboration-blank-wait"]').element).toHaveProperty('value', '2')
    expect(wrapper.get('[data-test="collaboration-save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="collaboration-key-ids"]').setValue('42')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    await flushPromises()
    expect(api.updateSettings).toHaveBeenCalledWith({ openai_collaboration_settings: {
      ...enabled, blank_prelude_wait_seconds: 2, api_key_ids: [42]
    } })
    wrapper.unmount()
  })
  it.each(['', '1', '0', '-1', '2.5', '16'])('rejects an invalid wait limit without saving: %s', async value => {
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-blank-wait"]').setValue(value)
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    expect(wrapper.get('[role="alert"]').text()).toContain('2–15 秒之间的整数')
    expect(api.updateSettings).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('saves the wait limit and preserves it when shutting down', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-blank-wait"]').setValue('15')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    await flushPromises()
    expect(api.updateSettings).toHaveBeenLastCalledWith({ openai_collaboration_settings: {
      ...enabled, blank_prelude_wait_seconds: 15
    } })
    await wrapper.get('[data-test="collaboration-blank-wait"]').setValue('99')
    await wrapper.get('[data-test="collaboration-stop"]').trigger('click')
    await flushPromises()
    expect(api.updateSettings).toHaveBeenLastCalledWith({ openai_collaboration_settings: {
      ...enabled, blank_prelude_wait_seconds: 15, enabled: false
    } })
    expect(wrapper.get('[data-test="collaboration-blank-wait"]').element).toHaveProperty('value', '15')
    wrapper.unmount()
  })
  it('does not confirm a larger wait when an older backend omits it', async () => {
    api.updateSettings.mockResolvedValueOnce({ openai_collaboration_settings: { ...enabled, blank_prelude_wait_seconds: undefined } })
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-blank-wait"]').setValue('15')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('无法确认')
    expect(api.showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it.each([0, 1, 16, 2.5, '10', null])('does not load an invalid server wait limit: %s', async value => {
    api.getSettings.mockResolvedValueOnce({ openai_collaboration_settings: { ...enabled, blank_prelude_wait_seconds: value } })
    const wrapper = await open()
    expect(wrapper.get('[role="alert"]').text()).toContain('禁止保存')
    expect(wrapper.find('[data-test="collaboration-blank-wait"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('requires a selected group and valid key IDs before enabling', async () => {
    api.getSettings.mockResolvedValueOnce({ openai_collaboration_settings: { ...enabled, enabled: false, group_ids: [], api_key_ids: [] } })
    const wrapper = await open()
    await wrapper.get('[data-test="collaboration-enabled"]').trigger('click')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    expect(wrapper.get('[role="alert"]').text()).toContain('至少选择一个分组')
    await wrapper.get('[data-test="collaboration-group-7"]').setValue(true)
    await wrapper.get('[data-test="collaboration-key-ids"]').setValue('1.5')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    expect(wrapper.get('[role="alert"]').text()).toContain('正整数')
    expect(api.updateSettings).not.toHaveBeenCalled()
    await wrapper.get('[data-test="collaboration-key-ids"]').setValue('')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    await flushPromises()
    expect(api.updateSettings.mock.calls[0][0].openai_collaboration_settings.api_key_ids).toEqual([])
    wrapper.unmount()
  })
  it('loads only when the gateway tab is active and saves only its own field', async () => {
    const wrapper = await open('zh', false)
    expect(api.getSettings).not.toHaveBeenCalled()
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(wrapper.find('[data-test="collaboration-group-9"]').exists()).toBe(false)
    await wrapper.get('[data-test="collaboration-group-8"]').setValue(true)
    await wrapper.get('[data-test="collaboration-key-ids"]').setValue('12, 34 34')
    await wrapper.get('[data-test="collaboration-save"]').trigger('click')
    await flushPromises()
    expect(api.updateSettings).toHaveBeenCalledWith({ openai_collaboration_settings: {
      ...enabled, group_ids: [7, 8], api_key_ids: [12, 34]
    } })
    expect(api.showSuccess).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
})

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { templatesMock, discoverMock } = vi.hoisted(() => ({ templatesMock: vi.fn(), discoverMock: vi.fn() }))
vi.mock('@/api/admin/videoTasks', () => ({ getVideoModelTemplates: templatesMock, discoverVideoModels: discoverMock }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

import VideoTasksAccountConfig from '../VideoTasksAccountConfig.vue'

const capability = { model: 'Seedance-2.0', profile: 'bblabu_official' as const, enabled: true, resolutions: ['480p'], billing_unit: 'per_second' as const }
const catalog = { protocol: 'video_tasks_json_v1', version: 'test', preset: { name: 'BBLabu', base_url: 'https://api.bblabu.ai/v1' }, models: [capability] }
const baseProps = { id: 'test', modelValue: [], capabilities: [], supplierName: '', baseUrl: 'https://api.example/v1', apiKey: 'test-key' }

describe('VideoTasksAccountConfig', () => {
  beforeEach(() => { templatesMock.mockReset(); templatesMock.mockResolvedValue(catalog); discoverMock.mockReset(); discoverMock.mockResolvedValue(['Brand/NewVideo', 'Seedance-2.0']) })

  it('loads server templates but discovery never grants model authorization', async () => {
    const wrapper = mount(VideoTasksAccountConfig, { props: baseProps })
    await flushPromises()
    expect(templatesMock).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-testid="video-model-Seedance-2.0"]').exists()).toBe(true)
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="video-discovered-models"]').text()).toContain('Brand/NewVideo')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    await wrapper.get('[data-testid="video-model-Seedance-2.0"]').setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([['Seedance-2.0']])
    expect(wrapper.emitted('update:capabilities')?.at(-1)).toEqual([[capability]])
  })

  it('applies connection preset without enabling models', async () => {
    const wrapper = mount(VideoTasksAccountConfig, { props: baseProps })
    await flushPromises()
    await wrapper.findAll('button')[0]!.trigger('click')
    expect(wrapper.emitted('update:baseUrl')?.at(-1)).toEqual(['https://api.bblabu.ai/v1'])
    expect(wrapper.emitted('update:supplierName')?.at(-1)).toEqual(['BBLabu'])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('reports invalid JSON and preserves the previous capability configuration', async () => {
    const wrapper = mount(VideoTasksAccountConfig, { props: { ...baseProps, capabilities: [capability] } })
    await flushPromises()
    await wrapper.get('[data-testid="video-capability-json"]').setValue('{broken')
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    const custom = { ...capability, model: 'Custom/Video' }
    await wrapper.get('[data-testid="video-capability-json"]').setValue(JSON.stringify([custom]))
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([true])
    expect(wrapper.emitted('update:capabilities')?.at(-1)).toEqual([[custom]])
  })

  it('requires saving a changed connection before reusing a stored key', async () => {
    const wrapper = mount(VideoTasksAccountConfig, { props: { ...baseProps, apiKey: '', accountId: 7, savedBaseUrl: 'https://old.example/v1', savedProtocol: 'video_tasks_json_v1' } })
    await flushPromises()
    expect(wrapper.get('[data-testid="video-discover-models"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.videoTasks.saveBeforeDiscovery')
    expect(discoverMock).not.toHaveBeenCalled()
  })

  it('shows template failures and supports retry', async () => {
    templatesMock.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(VideoTasksAccountConfig, { props: baseProps })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.videoTasks.templatesFailed')
    await wrapper.findAll('button').find((button) => button.text() === 'admin.videoTasks.retry')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="video-model-Seedance-2.0"]').exists()).toBe(true)
  })
})

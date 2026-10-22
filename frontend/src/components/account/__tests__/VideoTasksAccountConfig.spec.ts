import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { templatesMock, discoverMock } = vi.hoisted(() => ({ templatesMock: vi.fn(), discoverMock: vi.fn() }))
vi.mock('@/api/admin/videoTasks', () => ({ getVideoModelTemplates: templatesMock, discoverVideoModels: discoverMock }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

import VideoTasksAccountConfig from '../VideoTasksAccountConfig.vue'
import VideoModelCapabilityEditor from '../VideoModelCapabilityEditor.vue'
import type { VideoModelCapability } from '@/api/admin/videoTasks'

const capability: VideoModelCapability = {
  model: 'Seedance-2.0', profile: 'bblabu_official', enabled: true,
  duration_mode: 'controlled', min_duration: 4, max_duration: 15, default_duration: 4,
  resolutions: ['720p'], default_resolution: '720p', resolution_control: true,
  ratios: ['16:9'], default_ratio: '16:9', max_images: 0, max_videos: 0, max_audios: 0,
  max_total_assets: 0, frames: false, allow_first_only: false, allow_frames_with_references: false,
  generate_audio: false, audio_requires_image: false, frames_require_auto_ratio: false,
  billing_unit: 'per_video', billing_policy: 'output_only', metering_policy: 'per_video', metering_trusted: false,
  max_reference_video_seconds: 0, require_reference_video_durations: false, max_combined_video_seconds: 0,
  supports_cancel: false, content_mode: 'content_endpoint'
}
const catalog = { protocol: 'video_tasks_json_v1', version: 'test', preset: { name: 'BBLabu', base_url: 'https://api.bblabu.ai/v1' }, models: [capability] }
const baseProps = { id: 'test', modelValue: [], capabilities: [], supplierName: '', baseUrl: 'https://api.example/v1', apiKey: 'test-key' }
const snapshot = {
  models: ['Seedance-2.0'], synced_at: '2026-10-20T00:00:00.000Z',
  base_url: baseProps.baseUrl, proxy_id: null, protocol: 'video_tasks_json_v1' as const
}
function mountConfig(props: Partial<InstanceType<typeof VideoTasksAccountConfig>['$props']> = {}) {
  return mount(VideoTasksAccountConfig, { props: { ...baseProps, ...props }, global: { stubs: { VideoModelCapabilityEditor: true } } })
}

describe('VideoTasksAccountConfig', () => {
  beforeEach(() => { templatesMock.mockReset(); templatesMock.mockResolvedValue(catalog); discoverMock.mockReset(); discoverMock.mockResolvedValue(['Brand/NewVideo', 'Seedance-2.0']) })

  it('loads the Zhiqi catalog, validates aliases against discovered upstream IDs, and saves protocol-scoped capabilities', async () => {
    templatesMock.mockResolvedValue({ ...catalog, protocol: 'zhiqi_v1', preset: { name: 'Zhiqi', base_url: 'https://zhiqiapi.com/v1' }, models: [] })
    const aliased = { ...capability, model: 'Public/Video', upstream_model: 'Supplier/Video', profile: 'zhiqi_v1' as const }
    const wrapper = mountConfig({ protocol: 'zhiqi_v1', capabilities: [aliased], discovery: { ...snapshot, protocol: 'zhiqi_v1', models: ['Supplier/Video'] } })
    await flushPromises()
    expect(templatesMock).toHaveBeenLastCalledWith('zhiqi_v1')
    expect(wrapper.text()).toContain('admin.videoTasks.zhiqiPreset')
    expect(wrapper.get('[data-testid="video-model-row-Public/Video"]').attributes('data-status')).toBe('available')
    await wrapper.get('[data-testid="video-model-Public/Video"]').setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([['Public/Video']])
    await wrapper.get('[data-testid="video-configure-Public/Video"]').trigger('click')
    const editor = wrapper.getComponent(VideoModelCapabilityEditor)
    expect(editor.props('protocol')).toBe('zhiqi_v1')
    editor.vm.$emit('save', { ...aliased, profile: 'bblabu_official' })
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    editor.vm.$emit('save', aliased)
    expect(wrapper.emitted('update:capabilities')?.at(-1)).toEqual([[aliased]])
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(discoverMock).toHaveBeenLastCalledWith(expect.objectContaining({ protocol: 'zhiqi_v1' }))
    expect(wrapper.emitted('update:discovery')?.at(-1)?.[0]).toMatchObject({ protocol: 'zhiqi_v1' })
  })

  it('rejects discovery, templates, and capability JSON belonging to another protocol', async () => {
    const wrapper = mountConfig({ protocol: 'zhiqi_v1', discovery: snapshot })
    await flushPromises()
    expect(wrapper.find('[data-testid="video-model-Seedance-2.0"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.videoTasks.templatesFailed')
    expect(wrapper.findAll('button')[0]!.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="video-capability-json"]').setValue(JSON.stringify([capability]))
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    await wrapper.get('[data-testid="video-capability-json"]').setValue(JSON.stringify([{ ...capability, profile: 'zhiqi_v1', supports_cancel: true }]))
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    await wrapper.get('[data-testid="video-capability-json"]').setValue(JSON.stringify([{ ...capability, profile: 'zhiqi_v1', billing_unit: 'per_second', billing_policy: 'output_and_reference', metering_policy: 'output_and_reference_seconds', max_reference_video_seconds: 10 }]))
    expect(wrapper.emitted('validity')?.at(-1)).toEqual([false])
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
  })

  it.each(['preset', 'override'])('shows a saved public alias using its upstream %s without enabling other templates', async source => {
    if (source === 'override') templatesMock.mockResolvedValue({ ...catalog, models: [] })
    const wrapper = mountConfig({ modelValue: ['Public/Stable'], modelMapping: { 'Public/Stable': capability.model }, discovery: snapshot, capabilities: source === 'override' ? [capability] : [] })
    await flushPromises()
    expect(wrapper.get('[data-testid="video-model-row-Public/Stable"]').attributes('data-status')).toBe('available')
    await wrapper.get('[data-testid="video-configure-Public/Stable"]').trigger('click')
    expect(wrapper.getComponent(VideoModelCapabilityEditor).props('capability')).toMatchObject({ model: 'Public/Stable', upstream_model: capability.model })
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    await wrapper.get('[data-testid="video-model-Public/Stable"]').setValue(false)
    await wrapper.setProps({ modelValue: [] })
    expect(wrapper.get('[data-testid="video-model-Public/Stable"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="video-model-Seedance-2.0"]').attributes('disabled')).toBe(source === 'override' ? undefined : '')
    await wrapper.setProps({ capabilities: [{ ...capability, model: 'Public/Stable' }] })
    expect(wrapper.get('[data-testid="video-model-row-Public/Stable"]').attributes('data-status')).toBe('missing')
  })

  it('discards pending discovery and catalogs when the selected protocol changes', async () => {
    let resolve!: (models: string[]) => void
    discoverMock.mockReturnValue(new Promise<string[]>(done => { resolve = done }))
    const wrapper = mountConfig()
    await flushPromises()
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    templatesMock.mockResolvedValue({ ...catalog, protocol: 'zhiqi_v1', models: [] })
    await wrapper.setProps({ protocol: 'zhiqi_v1' })
    resolve(['Previous/Protocol'])
    await flushPromises()
    expect(wrapper.find('[data-testid="video-model-Previous/Protocol"]').exists()).toBe(false)
    expect(wrapper.emitted('update:discovery')?.at(-1)).toEqual([null])
    expect(templatesMock).toHaveBeenLastCalledWith('zhiqi_v1')
  })

  it('uses upstream candidates and never lists or authorizes static templates automatically', async () => {
    const wrapper = mountConfig()
    await flushPromises()
    expect(templatesMock).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-testid="video-model-Seedance-2.0"]').exists()).toBe(false)
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="video-model-Brand/NewVideo"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="video-model-Seedance-2.0"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    expect(wrapper.emitted('update:discovery')?.at(-1)?.[0]).toMatchObject({ ...snapshot, models: ['Brand/NewVideo', 'Seedance-2.0'], synced_at: expect.any(String) })
    await wrapper.setProps({ capabilities: [capability] })
    await wrapper.get('[data-testid="video-model-Seedance-2.0"]').setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([['Seedance-2.0']])
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
  })

  it('applies connection preset without enabling models', async () => {
    const wrapper = mount(VideoTasksAccountConfig, { props: baseProps })
    await flushPromises()
    await wrapper.findAll('button')[0]!.trigger('click')
    expect(wrapper.emitted('update:baseUrl')?.at(-1)).toEqual(['https://api.bblabu.ai/v1'])
    expect(wrapper.emitted('update:supplierName')?.at(-1)).toEqual(['BBLabu'])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('requires explicit capability configuration before a discovered new model can be selected', async () => {
    const wrapper = mountConfig()
    await flushPromises()
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="video-model-Brand/NewVideo"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="video-configure-Brand/NewVideo"]').trigger('click')
    const editor = wrapper.getComponent(VideoModelCapabilityEditor)
    expect(editor.props('capability')).toBeUndefined()
    const custom = { ...capability, model: 'Brand/NewVideo' }
    editor.vm.$emit('save', custom)
    await flushPromises()
    expect(wrapper.emitted('update:capabilities')?.at(-1)).toEqual([[custom]])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.setProps({ capabilities: [custom] })
    await wrapper.get('[data-testid="video-model-Brand/NewVideo"]').setValue(true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([['Brand/NewVideo']])
  })

  it('marks missing old models without deleting their capabilities or authorization', async () => {
    discoverMock.mockResolvedValue(['Brand/NewVideo'])
    const wrapper = mountConfig({ modelValue: ['Seedance-2.0'], capabilities: [capability], discovery: snapshot })
    await flushPromises()
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="video-model-row-Seedance-2.0"]').attributes('data-status')).toBe('missing')
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-model-Seedance-2.0"]').element.checked).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
    await wrapper.get('[data-testid="video-model-Seedance-2.0"]').setValue(false)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[]])
    await wrapper.setProps({ modelValue: [] })
    expect(wrapper.get('[data-testid="video-model-Seedance-2.0"]').attributes('disabled')).toBeDefined()
  })

  it('restores a saved catalog and preserves it when a later sync fails', async () => {
    const wrapper = mountConfig({ capabilities: [capability], discovery: snapshot })
    await flushPromises()
    expect(wrapper.get('[data-testid="video-model-row-Seedance-2.0"]').attributes('data-status')).toBe('available')
    discoverMock.mockRejectedValue(new Error('upstream unavailable'))
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.videoTasks.discoveryFailed')
    expect(wrapper.get('[data-testid="video-model-row-Seedance-2.0"]').attributes('data-status')).toBe('available')
    expect(wrapper.emitted('update:discovery')).toBeUndefined()
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

  it('accepts an authoritative empty catalog without clearing old settings', async () => {
    discoverMock.mockResolvedValue([])
    const wrapper = mountConfig({ modelValue: ['Seedance-2.0'], capabilities: [capability], discovery: snapshot })
    await flushPromises()
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.videoTasks.noDiscoveredModels')
    expect(wrapper.get('[data-testid="video-model-row-Seedance-2.0"]').attributes('data-status')).toBe('missing')
    expect(wrapper.emitted('update:discovery')?.at(-1)?.[0]).toMatchObject({ models: [] })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it.each(['baseUrl', 'apiKey', 'proxyId', 'accountId'] as const)('discards responses from the previous %s', async (field) => {
    let resolve!: (models: string[]) => void
    discoverMock.mockReturnValue(new Promise<string[]>((done) => { resolve = done }))
    const wrapper = mountConfig()
    await flushPromises()
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await wrapper.setProps({ [field]: field === 'baseUrl' ? 'https://new.example/v1' : field === 'apiKey' ? 'new-key' : 9 })
    resolve(['Previous/Supplier'])
    await flushPromises()
    expect(wrapper.find('[data-testid="video-model-Previous/Supplier"]').exists()).toBe(false)
    expect(wrapper.emitted('update:discovery')?.every(([value]) => value === null)).toBe(true)
  })

  it('ignores a saved catalog belonging to a different upstream or proxy', async () => {
    const wrapper = mountConfig({ discovery: { ...snapshot, proxy_id: 7 } })
    await flushPromises()
    expect(wrapper.find('[data-testid="video-model-Seedance-2.0"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.videoTasks.syncFirst')
  })

  it('preserves exact upstream model casing instead of authorizing a different route', async () => {
    discoverMock.mockResolvedValue(['seedance-2.0'])
    const wrapper = mountConfig({ capabilities: [capability], modelValue: ['Seedance-2.0'] })
    await flushPromises()
    await wrapper.get('[data-testid="video-discover-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="video-model-row-Seedance-2.0"]').attributes('data-status')).toBe('missing')
    expect(wrapper.get('[data-testid="video-model-row-seedance-2.0"]').attributes('data-status')).toBe('available')
    await wrapper.get('[data-testid="video-configure-seedance-2.0"]').trigger('click')
    wrapper.getComponent(VideoModelCapabilityEditor).vm.$emit('save', { ...capability, model: 'seedance-2.0' })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.videoTasks.modelCaseConflict')
    expect(wrapper.emitted('update:capabilities')).toBeUndefined()
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
    expect(wrapper.find('[data-testid="video-model-Seedance-2.0"]').exists()).toBe(false)
  })
})

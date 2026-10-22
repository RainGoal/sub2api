import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import VideoModelCapabilityEditor from '../VideoModelCapabilityEditor.vue'
import { buildVideoCapability, createVideoCapabilityForm } from '../videoModelCapabilityForm'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const template = buildVideoCapability('Legacy/Old-Video', {
  ...createVideoCapabilityForm(), profile: 'bblabu_official', enabled: true,
  duration_mode: 'fixed', min_duration: '4', max_duration: '4', default_duration: '4',
  billing_unit: 'per_video', billing_policy: 'output_only', metering_policy: 'per_video'
})

describe('VideoModelCapabilityEditor', () => {
  it('starts Zhiqi models with the matching field profile and hides BBLabu templates', async () => {
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: 'Public/Stable', protocol: 'zhiqi_v1', templates: [template] } })
    expect(wrapper.find('[data-testid="capability-template"]').exists()).toBe(false)
    expect(wrapper.get<HTMLSelectElement>('[data-testid="capability-profile"]').element.value).toBe('zhiqi_v1')
    expect(wrapper.find('[data-testid="capability-supports_cancel"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="capability-metering-policy"] option[value="output_and_reference_seconds"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="capability-billing-policy"] option[value="output_and_reference"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="capability-profile"]').findAll('option').map(option => option.attributes('value'))).not.toContain('bblabu_official')
    await wrapper.get('[data-testid="capability-upstream-model"]').setValue('Supplier/Actual')
    await wrapper.get('[data-testid="capability-duration-mode"]').setValue('unknown')
    await wrapper.get('[data-testid="capability-billing-unit"]').setValue('per_video')
    await wrapper.get('[data-testid="capability-metering-policy"]').setValue('per_video')
    await wrapper.get('[data-testid="capability-billing-policy"]').setValue('output_only')
    await wrapper.get('[data-testid="capability-enabled"]').setValue(true)
    await wrapper.get('[data-testid="capability-save"]').trigger('click')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ model: 'Public/Stable', upstream_model: 'Supplier/Actual', profile: 'zhiqi_v1' })
    await wrapper.setProps({ protocol: 'video_tasks_json_v1' })
    expect(wrapper.get<HTMLSelectElement>('[data-testid="capability-profile"]').element.value).toBe('')
    expect(wrapper.find('[data-testid="capability-supports_cancel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="capability-metering-policy"] option[value="output_and_reference_seconds"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="capability-billing-policy"] option[value="output_and_reference"]').exists()).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-testid="capability-upstream-model"]').element.value).toBe('')
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeDefined()
  })

  it('does not infer a new model capability from historical templates or authorize it', () => {
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: 'New/Exact-Case', templates: [template] } })
    expect(wrapper.get('[data-testid="capability-model"]').text()).toBe('New/Exact-Case')
    expect((wrapper.get('[data-testid="capability-profile"]').element as HTMLSelectElement).value).toBe('')
    expect((wrapper.get('[data-testid="capability-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('save')).toBeUndefined()
  })

  it('copies a template only on explicit action, retaining the upstream model and current enabled state', async () => {
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: 'New/Exact-Case', templates: [template] } })
    await wrapper.get('[data-testid="capability-template"]').setValue(template.model)
    expect((wrapper.get('[data-testid="capability-profile"]').element as HTMLSelectElement).value).toBe('')
    await wrapper.get('[data-testid="capability-copy-template"]').trigger('click')
    expect((wrapper.get('[data-testid="capability-enabled"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[data-testid="capability-enabled"]').setValue(true)
    await wrapper.get('[data-testid="capability-save"]').trigger('click')
    const saved = wrapper.emitted('save')?.[0]?.[0]
    expect(saved).toMatchObject({ model: 'New/Exact-Case', enabled: true, profile: 'bblabu_official', billing_unit: 'per_video', min_duration: 4 })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(template.model).toBe('Legacy/Old-Video')
  })

  it('configures a newly discovered model manually without any template', async () => {
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: 'New/Manual', templates: [] } })
    await wrapper.get('[data-testid="capability-profile"]').setValue('bblabu_route')
    await wrapper.get('[data-testid="capability-duration-mode"]').setValue('unknown')
    await wrapper.get('[data-testid="capability-billing-unit"]').setValue('per_video')
    await wrapper.get('[data-testid="capability-metering-policy"]').setValue('per_video')
    await wrapper.get('[data-testid="capability-billing-policy"]').setValue('output_only')
    await wrapper.get('[data-testid="capability-enabled"]').setValue(true)
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="capability-save"]').trigger('click')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ model: 'New/Manual', enabled: true, profile: 'bblabu_route', duration_mode: 'unknown', billing_unit: 'per_video' })
  })

  it('saves a copied controlled-duration template as fixed without carrying hidden duration options', async () => {
    const controlled = { ...template, duration_mode: 'controlled' as const, max_duration: 12, allowed_durations: [4, 8, 12] }
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: 'New/Fixed', templates: [controlled] } })
    await wrapper.get('[data-testid="capability-template"]').setValue(controlled.model)
    await wrapper.get('[data-testid="capability-copy-template"]').trigger('click')
    await wrapper.get('[data-testid="capability-duration-mode"]').setValue('fixed')
    await wrapper.get('[data-testid="capability-max_duration"]').setValue('4')
    await wrapper.get('[data-testid="capability-enabled"]').setValue(true)
    expect(wrapper.find('[data-testid="capability-allowed-durations"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="capability-save"]').trigger('click')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ model: 'New/Fixed', enabled: true, duration_mode: 'fixed', min_duration: 4, max_duration: 4, default_duration: 4, allowed_durations: [] })
    expect(controlled.allowed_durations).toEqual([4, 8, 12])
  })

  it('blocks incompatible billing and duplicate resolution overrides while preserving unknown fields', async () => {
    const capability = { ...template, model: 'Exact/Configured', resolutions: ['720p'], default_resolution: '720p', max_images: 4, future_contract: { version: 2 } }
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: capability.model, capability, templates: [] } })
    await wrapper.get('[data-testid="capability-metering-policy"]').setValue('output_seconds')
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="capability-metering-policy"]').setValue('per_video')
    for (let index = 0; index < 2; index++) {
      await wrapper.get('[data-testid="capability-add-image-limit"]').trigger('click')
      await wrapper.get(`[data-testid="capability-image-resolution-${index}"]`).setValue('720p')
    }
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeDefined()
    const remove = wrapper.findAll('button').find((button) => button.text() === 'admin.videoTasks.capabilityEditor.remove')!
    await remove.trigger('click')
    await wrapper.get('[data-testid="capability-save"]').trigger('click')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({ future_contract: { version: 2 }, max_images_by_resolution: { '720p': 0 } })
    expect(capability.max_images_by_resolution).toEqual({})
  })

  it('resets when another model is opened and cancels without changing the parent', async () => {
    const wrapper = mount(VideoModelCapabilityEditor, { props: { model: template.model, capability: template, templates: [] } })
    await wrapper.setProps({ model: 'Another/Model', capability: undefined })
    expect(wrapper.get('[data-testid="capability-save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="capability-cancel"]').trigger('click')
    expect(wrapper.emitted('cancel')).toEqual([[]])
    expect(wrapper.emitted('save')).toBeUndefined()
  })
})

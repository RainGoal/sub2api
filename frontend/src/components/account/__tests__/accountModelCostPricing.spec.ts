import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AccountModelCostPricing from '../AccountModelCostPricing.vue'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import ModelTagInput from '@/components/admin/channel/ModelTagInput.vue'
import IntervalRow from '@/components/admin/channel/IntervalRow.vue'
import Select from '@/components/common/Select.vue'
import {
  ACCOUNT_MODEL_COST_KEY, accountModelCostPricingToAPI, createAccountModelCostEntry,
  readAccountModelCostPricing, validateAccountModelCostPricing,
} from '../accountModelCostPricing'
import type { PricingFormEntry } from '@/components/admin/channel/types'

const { defaultPricingMock } = vi.hoisted(() => ({ defaultPricingMock: vi.fn() }))
vi.mock('@/api/admin/channels', () => ({ default: { getModelDefaultPricing: defaultPricingMock } }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

function mountVideoCostEditor(entry: PricingFormEntry) {
  return mount(AccountModelCostPricing, {
    props: { modelValue: [entry], platform: 'seedance' },
    global: { stubs: { teleport: true } },
  })
}

type VideoCostEditor = ReturnType<typeof mountVideoCostEditor>

async function applyCostUpdate(wrapper: VideoCostEditor) {
  const entries = wrapper.emitted('update:modelValue')!.at(-1)![0] as PricingFormEntry[]
  await wrapper.setProps({ modelValue: entries })
  return entries
}

async function selectVideoUnit(wrapper: VideoCostEditor, unit: 'per_video' | 'per_second') {
  const select = wrapper.getComponent(PricingEntryCard).getComponent(Select)
  await select.get('button').trigger('click')
  const label = unit === 'per_video' ? 'admin.channels.form.videoPerVideo' : 'admin.channels.form.videoPerSecond'
  const option = select.findAll('[role="option"]').find(candidate => candidate.text() === label)
  expect(option).toBeDefined()
  await option!.trigger('click')
  return applyCostUpdate(wrapper)
}

async function addVideoTier(wrapper: VideoCostEditor, resolution: string, price: string) {
  const card = wrapper.getComponent(PricingEntryCard)
  const addTier = card.findAll('button').find(button => button.text().includes('admin.channels.form.addTier'))!
  await addTier.trigger('click')
  await applyCostUpdate(wrapper)
  let row = card.findAllComponents(IntervalRow).at(-1)!
  await row.get('input[type="text"]').setValue(resolution)
  await applyCostUpdate(wrapper)
  row = card.findAllComponents(IntervalRow).at(-1)!
  await row.get('input[type="number"]').setValue(price)
  return applyCostUpdate(wrapper)
}

describe('AccountModelCostPricing', () => {
  it('configures per-video resolution costs through the billing selector and preserves them across saving and unit changes', async () => {
    const wrapper = mountVideoCostEditor({
      ...createAccountModelCostEntry('seedance'), models: ['Niulai-Pro'], per_request_price: 0.4,
    })
    await selectVideoUnit(wrapper, 'per_video')
    await addVideoTier(wrapper, '720p', '0.3')
    await addVideoTier(wrapper, '768p', '0')
    const entries = await addVideoTier(wrapper, '2k', '0.8')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(entries[0]).toMatchObject({
      models: ['Niulai-Pro'], billing_mode: 'video', video_pricing_version: 1, video_billing_unit: 'per_video',
      per_request_price: 0.4,
    })
    for (const row of wrapper.findAllComponents(IntervalRow)) {
      expect(row.text()).toContain('$/video')
      expect(row.findAll('input')).toHaveLength(2)
    }

    const saved = accountModelCostPricingToAPI(entries, 'seedance')
    expect(saved[0].intervals).toMatchObject([
      { tier_label: '720p', per_request_price: 0.3 },
      { tier_label: '768p', per_request_price: 0 },
      { tier_label: '2k', per_request_price: 0.8 },
    ])
    const restored = readAccountModelCostPricing({ [ACCOUNT_MODEL_COST_KEY]: saved })
    wrapper.unmount()
    const reopened = mountVideoCostEditor(restored[0])
    expect(reopened.getComponent(Select).props('modelValue')).toBe('per_video')
    expect(reopened.props('modelValue')[0]).toMatchObject({
      models: ['Niulai-Pro'], video_billing_unit: 'per_video', per_request_price: 0.4,
    })
    expect(reopened.findAllComponents(IntervalRow).map(row => row.props('interval').per_request_price))
      .toEqual([0.3, 0, 0.8])
    const switched = await selectVideoUnit(reopened, 'per_second')
    expect(switched[0]).toMatchObject({ video_billing_unit: 'per_second', per_request_price: 0.4 })
    expect(switched[0].intervals).toEqual(restored[0].intervals)
    expect(reopened.find('[role="alert"]').exists()).toBe(false)
    expect(reopened.findAllComponents(IntervalRow).every(row => row.text().includes('$/s'))).toBe(true)
    reopened.unmount()
  })

  it('keeps an untouched legacy fixed task cost unchanged when editing and saving the account', () => {
    const entry: PricingFormEntry = {
      ...createAccountModelCostEntry('seedance'), models: ['seedance-2.0'],
      billing_mode: 'per_request', per_request_price: 0,
    }
    const wrapper = mountVideoCostEditor(entry)
    expect(wrapper.getComponent(Select).props('modelValue')).toBe('per_video')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    const saved = accountModelCostPricingToAPI(wrapper.props('modelValue'), 'seedance')
    expect(saved[0]).toMatchObject({ billing_mode: 'per_request', per_request_price: 0, intervals: [] })
    expect(saved[0]).not.toHaveProperty('video_pricing_version')
    expect(readAccountModelCostPricing({ [ACCOUNT_MODEL_COST_KEY]: saved })[0].billing_mode).toBe('per_request')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([
    ['Seedance-2.0', 'seedance-2.0'],
    ['bytedance/seedance-2.5', 'seedance-2.5'],
  ])('upgrades legacy task cost %s only when adding a resolution tier', async (model, canonical) => {
    const wrapper = mountVideoCostEditor({
      ...createAccountModelCostEntry('seedance'), models: [model],
      billing_mode: 'per_request', per_request_price: 0.4,
    })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    const entries = await addVideoTier(wrapper, '720p', '0')
    expect(entries[0]).toMatchObject({
      models: [canonical], billing_mode: 'video', video_pricing_version: 1,
      video_billing_unit: 'per_video', per_request_price: 0.4,
    })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    const saved = accountModelCostPricingToAPI(entries, 'seedance')
    expect(saved[0].intervals[0]).toMatchObject({ tier_label: '720p', per_request_price: 0 })
    expect(readAccountModelCostPricing({ [ACCOUNT_MODEL_COST_KEY]: saved })[0]).toMatchObject({
      models: [canonical], video_billing_unit: 'per_video', per_request_price: 0.4,
    })
    wrapper.unmount()
  })

  it('preserves exact v1 custom model IDs when changing units', async () => {
    const wrapper = mountVideoCostEditor({
      ...createAccountModelCostEntry('seedance'), models: ['bytedance/Seedance-2.5-Custom'],
      per_request_price: 0.5, video_pricing_version: 1, video_billing_unit: 'per_second',
      video_quantity_policy: 'output_seconds',
    })
    const entries = await selectVideoUnit(wrapper, 'per_video')
    expect(accountModelCostPricingToAPI(entries, 'seedance')[0].models).toEqual(['bytedance/Seedance-2.5-Custom'])
    expect(validateAccountModelCostPricing(entries, 'seedance', key => key)).toBeNull()
    wrapper.unmount()
  })

  it('rejects a legacy wildcard when resolution pricing requires an exact video model', async () => {
    const wrapper = mountVideoCostEditor({
      ...createAccountModelCostEntry('seedance'), models: ['seedance-*'],
      billing_mode: 'per_request', per_request_price: 0.4,
    })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    const entries = await addVideoTier(wrapper, '720p', '0.3')
    expect(entries[0].models).toEqual(['seedance-*'])
    expect(entries[0].video_billing_unit).toBe('per_video')
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.accounts.modelCostPricing.invalidEntry')
    expect(validateAccountModelCostPricing(entries, 'seedance', key => key)).not.toBeNull()
    wrapper.unmount()
  })

  it('adds a Seedance video cost, reports incomplete pricing and explicitly removes it', async () => {
    const wrapper = mount(AccountModelCostPricing, { props: { modelValue: [], platform: 'seedance' } })
    expect(wrapper.text()).toContain('admin.accounts.modelCostPricing.empty')
    await wrapper.get('[data-testid="add-account-model-cost"]').trigger('click')
    let entries = wrapper.emitted('update:modelValue')!.at(-1)![0] as PricingFormEntry[]
    expect(entries[0].billing_mode).toBe('video')
    await wrapper.setProps({ modelValue: entries })
    expect(wrapper.get('[role="alert"]').exists()).toBe(true)
    wrapper.getComponent(PricingEntryCard).vm.$emit('update', { ...entries[0], models: ['seedance-2.0'], per_request_price: 0 })
    entries = wrapper.emitted('update:modelValue')!.at(-1)![0] as PricingFormEntry[]
    await wrapper.setProps({ modelValue: entries })
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.getComponent(PricingEntryCard).vm.$emit('remove')
    expect(wrapper.emitted('update:modelValue')!.at(-1)![0]).toEqual([])
  })

  it('does not use channel default sale prices as supplier purchase prices', async () => {
    defaultPricingMock.mockReset().mockResolvedValue({ found: true, input_price: 0.1 })
    const entry = createAccountModelCostEntry('openai')
    const wrapper = mount(AccountModelCostPricing, { props: { modelValue: [entry], platform: 'openai' } })
    wrapper.getComponent(ModelTagInput).vm.$emit('update:models', ['vendor-model'])
    await flushPromises()
    expect(defaultPricingMock).not.toHaveBeenCalled()
    const entries = wrapper.emitted('update:modelValue')!.at(-1)![0] as PricingFormEntry[]
    expect(entries[0].models).toEqual(['vendor-model'])
    expect(entries[0].input_price).toBeNull()
  })

  it('disables changes while the parent account is submitting', async () => {
    const entry = { ...createAccountModelCostEntry('seedance'), models: ['seedance-2.0'], per_request_price: 0.04 }
    const wrapper = mount(AccountModelCostPricing, { props: { modelValue: [entry], platform: 'seedance', disabled: true } })
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
    wrapper.getComponent(PricingEntryCard).vm.$emit('remove')
    await wrapper.get('[data-testid="add-account-model-cost"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})

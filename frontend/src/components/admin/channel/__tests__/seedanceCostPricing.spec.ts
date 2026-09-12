import { mount, shallowMount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PricingEntryCard from '../PricingEntryCard.vue'
import IntervalRow from '../IntervalRow.vue'
import ModelTagInput from '../ModelTagInput.vue'
import { isValidSeedanceCostEntry } from '../seedanceCostPricing'
import { createDefaultTimePricingForm, formIntervalsToAPI, type PricingFormEntry } from '../types'

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

function entry(model = 'seedance-2.0'): PricingFormEntry {
  return {
    models: [model], billing_mode: 'video', input_price: null, output_price: null,
    cache_write_price: null, cache_read_price: null, image_input_price: null,
    image_output_price: null, per_request_price: null, intervals: [],
    time_pricing: createDefaultTimePricingForm(),
  }
}

describe('Seedance account cost pricing', () => {
  it('offers video and fixed task pricing, with model-specific resolution tiers', async () => {
    const wrapper = shallowMount(PricingEntryCard, { props: { platform: 'seedance', entry: entry('seedance-2.5') } })
    expect(wrapper.findComponent({ name: 'Select' }).props('options').map((option: { value: string }) => option.value))
      .toEqual(['per_request', 'video'])
    const addTier = wrapper.findAll('button').find(button => button.text().includes('admin.channels.form.addTier'))!
    await addTier.trigger('click')
    let updated = wrapper.emitted('update')!.at(-1)![0] as PricingFormEntry
    expect(updated.intervals[0].tier_label).toBe('480p')
    updated.intervals[0].per_request_price = '0.03'
    await wrapper.setProps({ entry: updated })
    await addTier.trigger('click')
    updated = wrapper.emitted('update')!.at(-1)![0] as PricingFormEntry
    expect(updated.intervals[1].tier_label).toBe('720p')
    updated.intervals[1].per_request_price = '0'
    await wrapper.setProps({ entry: updated })
    expect(addTier.attributes('disabled')).toBeDefined()
    expect(isValidSeedanceCostEntry(updated)).toBe(true)
    expect(formIntervalsToAPI(updated.intervals).map(tier => tier.per_request_price)).toEqual([0.03, 0])
    expect(wrapper.text()).toContain('$/s')
  })

  it('shows resolution and USD/second instead of token bounds for video tiers', () => {
    const wrapper = mount(IntervalRow, { props: { mode: 'video', interval: {
      min_tokens: 0, max_tokens: null, tier_label: '720p', per_request_price: '0.05',
      input_price: null, output_price: null, cache_write_price: null, cache_read_price: null,
      input_multiplier: null, output_multiplier: null, cache_write_multiplier: null,
      cache_read_multiplier: null, sort_order: 0,
    } } })
    expect(wrapper.findAll('input')).toHaveLength(2)
    expect(wrapper.text()).toContain('admin.channels.form.resolution')
    expect(wrapper.text()).toContain('$/s')
    expect(wrapper.text()).not.toContain('admin.channels.form.minTokens')
  })

  it('adds Seedance model IDs using the built-in catalog and recognizes aliases', async () => {
    const wrapper = mount(ModelTagInput, { props: { platform: 'seedance', models: ['bytedance/seedance-2.5'] } })
    const buttons = wrapper.findAll('button')
    expect(buttons.find(button => button.text() === 'Seedance-2.5')?.attributes('disabled')).toBeDefined()
    await buttons.find(button => button.text() === 'Seedance-2.0 Fast')!.trigger('click')
    expect(wrapper.emitted('update:models')?.[0]?.[0]).toEqual(['bytedance/seedance-2.5', 'seedance-2.0-fast'])
  })
})

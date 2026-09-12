import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AccountModelCostPricing from '../AccountModelCostPricing.vue'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import ModelTagInput from '@/components/admin/channel/ModelTagInput.vue'
import { createAccountModelCostEntry } from '../accountModelCostPricing'
import type { PricingFormEntry } from '@/components/admin/channel/types'

const { defaultPricingMock } = vi.hoisted(() => ({ defaultPricingMock: vi.fn() }))
vi.mock('@/api/admin/channels', () => ({ default: { getModelDefaultPricing: defaultPricingMock } }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('AccountModelCostPricing', () => {
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

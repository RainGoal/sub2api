import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelsView from '../ChannelsView.vue'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import { accountModelCostPricingToAPI, createAccountModelCostEntry } from '@/components/account/accountModelCostPricing'
import type { Channel } from '@/api/admin/channels'

const { listChannels, createChannel, updateChannel, previewImport, showError, showSuccess } = vi.hoisted(() => ({
  listChannels: vi.fn(), createChannel: vi.fn(), updateChannel: vi.fn(), previewImport: vi.fn(), showError: vi.fn(), showSuccess: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: {
  channels: { list: listChannels, create: createChannel, update: updateChannel, previewSeedanceSalesImport: previewImport },
  groups: { getAll: vi.fn().mockResolvedValue([{ id: 9, name: 'Video group', platform: 'seedance', rate_multiplier: 1 }]) },
  settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }) },
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }),
}))
const Layout = defineComponent({ template: '<div><slot /><slot name="filters" /><slot name="table" /></div>' })
const Dialog = defineComponent({ props: ['show'], emits: ['close'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' })
const Table = defineComponent({ props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' })
const wrappers: ReturnType<typeof mount>[] = []
function mountView(renderPricing = false) {
  const wrapper = mount(ChannelsView, { global: { stubs: {
    AppLayout: Layout, TablePageLayout: Layout, BaseDialog: Dialog, DataTable: Table,
    Pagination: true, Select: true, Icon: true, PlatformIcon: true, Toggle: true, ConfirmDialog: true,
    PricingEntryCard: !renderPricing,
  } } })
  wrappers.push(wrapper)
  return wrapper
}
function button(wrapper: ReturnType<typeof mount>, key: string) {
  const result = wrapper.findAll('button').find(node => node.text().includes(key))
  if (!result) throw new Error(`Button not found: ${key}`)
  return result
}
async function openSeedance(wrapper: ReturnType<typeof mount>) {
  await button(wrapper, 'admin.channels.createChannel').trigger('click')
  await flushPromises()
  await wrapper.get('#channel-form input[type="text"]').setValue('Video sales')
  const platform = wrapper.findAll('label').find(node => node.text().trim() === 'admin.groups.platforms.seedance')!
  await platform.get('input').setValue(true)
  const group = wrapper.findAll('label').find(node => node.text().includes('Video group'))!
  await group.get('input').setValue(true)
}
function importedPrice() {
  const entry = { ...createAccountModelCostEntry('seedance'), models: ['seedance-2.0'], per_request_price: 0.08 }
  return { model_pricing: accountModelCostPricingToAPI([entry], 'seedance') }
}

function legacyChannel(mode: 'token' | 'per_request' = 'token'): Channel {
  const price = { ...importedPrice().model_pricing[0], id: 41, billing_mode: mode,
    input_price: mode === 'token' ? 0.000001234567891234 : null,
    per_request_price: mode === 'per_request' ? 0.1234567891234 : null,
  }
  return {
    id: 12, name: 'Legacy video channel', description: '', status: 'active', billing_model_source: 'channel_mapped',
    restrict_models: false, group_ids: [9], model_pricing: [price], model_mapping: {},
    apply_pricing_to_account_stats: false, created_at: '', updated_at: '',
    account_stats_pricing_rules: [{ id: 20, name: 'Legacy cost', group_ids: [9], account_ids: [],
      pricing: [{ ...price, id: 42, fast_multiplier: 1.75, flex_multiplier: 0.75 }],
    }],
  }
}

async function openLegacyChannel(channel = legacyChannel()) {
  listChannels.mockResolvedValue({ items: [channel], total: 1 })
  const wrapper = mountView(true)
  await flushPromises()
  await button(wrapper, 'common.edit').trigger('click')
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  listChannels.mockResolvedValue({ items: [], total: 0 })
  createChannel.mockResolvedValue({})
  updateChannel.mockResolvedValue({})
  previewImport.mockResolvedValue(importedPrice())
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('Seedance channel sales pricing', () => {
  it.each(['token', 'per_request'] as const)('preserves untouched legacy %s prices and hidden statistics rules exactly', async mode => {
    const channel = legacyChannel(mode)
    const wrapper = await openLegacyChannel(channel)
    expect(wrapper.getComponent(PricingEntryCard).props('entry').billing_mode).toBe(mode)
    expect(wrapper.getComponent(PricingEntryCard).emitted('update')).toBeUndefined()
    await wrapper.get('#channel-form input[type="text"]').setValue('Renamed channel')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(updateChannel).toHaveBeenCalledWith(channel.id, expect.objectContaining({
      name: 'Renamed channel', model_pricing: channel.model_pricing,
      account_stats_pricing_rules: channel.account_stats_pricing_rules,
    }))
  })

  it('rejects changes to old non-video prices until the Seedance sales are converted', async () => {
    const wrapper = await openLegacyChannel()
    const card = wrapper.getComponent(PricingEntryCard)
    card.vm.$emit('update', { ...card.props('entry'), input_price: 2 })
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.channels.form.invalidSeedanceSales')
  })

  it('accepts an explicit replacement of legacy prices with USD/second video prices', async () => {
    const channel = legacyChannel()
    const wrapper = await openLegacyChannel(channel)
    wrapper.getComponent(PricingEntryCard).vm.$emit('update', {
      ...createAccountModelCostEntry('seedance'), models: ['seedance-2.0'], per_request_price: 0.09,
    })
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel).toHaveBeenCalledWith(channel.id, expect.objectContaining({
      model_pricing: [expect.objectContaining({ platform: 'seedance', billing_mode: 'video', per_request_price: 0.09 })],
      account_stats_pricing_rules: channel.account_stats_pricing_rules,
    }))
  })

  it('imports legacy prices into video sales entries and sends USD/second in the channel payload', async () => {
    const wrapper = mountView()
    await openSeedance(wrapper)
    await button(wrapper, 'admin.channels.form.importSeedanceSales').trigger('click')
    await flushPromises()
    expect(previewImport).toHaveBeenCalledWith([9])
    expect(wrapper.getComponent(PricingEntryCard).props('allowedBillingModes')).toEqual(['video'])
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(createChannel).toHaveBeenCalledWith(expect.objectContaining({
      group_ids: [9], model_pricing: [expect.objectContaining({
        platform: 'seedance', models: ['seedance-2.0'], billing_mode: 'video', per_request_price: 0.08,
      })], account_stats_pricing_rules: [],
    }))
  })

  it('reports conflicting legacy prices without modifying the draft', async () => {
    previewImport.mockRejectedValueOnce({ status: 409, message: 'Groups 9 and 10 have conflicting prices' })
    const wrapper = mountView()
    await openSeedance(wrapper)
    await button(wrapper, 'admin.channels.form.importSeedanceSales').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('Groups 9 and 10 have conflicting prices')
    expect(wrapper.findComponent(PricingEntryCard).exists()).toBe(false)
    expect(createChannel).not.toHaveBeenCalled()
  })

  it('blocks saving during import and ignores the result after closing and reopening', async () => {
    let resolve!: (value: ReturnType<typeof importedPrice>) => void
    previewImport.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountView()
    await openSeedance(wrapper)
    await button(wrapper, 'admin.channels.form.importSeedanceSales').trigger('click')
    expect(wrapper.get('button[form="channel-form"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#channel-form').trigger('submit')
    expect(createChannel).not.toHaveBeenCalled()
    await button(wrapper, 'common.cancel').trigger('click')
    await openSeedance(wrapper)
    resolve(importedPrice())
    await flushPromises()
    expect(wrapper.findComponent(PricingEntryCard).exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.get('button[form="channel-form"]').attributes('disabled')).toBeUndefined()
  })

  it('drops import results when the associated groups change before the response', async () => {
    let resolve!: (value: ReturnType<typeof importedPrice>) => void
    previewImport.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountView()
    await openSeedance(wrapper)
    await button(wrapper, 'admin.channels.form.importSeedanceSales').trigger('click')
    await wrapper.findAll('label').find(node => node.text().includes('Video group'))!.get('input').setValue(false)
    resolve(importedPrice())
    await flushPromises()
    expect(wrapper.findComponent(PricingEntryCard).exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
  })
})

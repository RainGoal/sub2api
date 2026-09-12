import { describe, expect, it } from 'vitest'
import {
  ACCOUNT_MODEL_COST_KEY, accountModelCostPricingToAPI, createAccountModelCostEntry,
  readAccountModelCostPricing, validateAccountModelCostPricing,
} from '../accountModelCostPricing'
import type { PricingFormEntry } from '@/components/admin/channel/types'

const t = (key: string) => key

function videoEntry(price: number | null = 0.04): PricingFormEntry {
  return { ...createAccountModelCostEntry('seedance'), models: ['seedance-2.0'], per_request_price: price }
}

describe('account model purchase price form', () => {
  it('round trips token and cache prices in USD/MTok without applying account multipliers', () => {
    const entry = {
      ...createAccountModelCostEntry('openai'), models: ['vendor-model'], input_price: '2.5',
      output_price: '10', cache_write_price: '3', cache_write_1h_price: '5',
      cache_read_price: '0', image_input_price: '0.5', image_output_price: '20', fast_multiplier: '1.5',
    }
    const prices = accountModelCostPricingToAPI([entry], 'openai')
    expect(prices[0]).toMatchObject({
      input_price: 0.0000025, output_price: 0.00001, cache_write_1h_price: 0.000005,
      cache_read_price: 0, image_input_price: 0.0000005, image_output_price: 0.00002, fast_multiplier: 1.5,
    })
    const restored = readAccountModelCostPricing({ [ACCOUNT_MODEL_COST_KEY]: prices })
    expect(restored[0]).toMatchObject({ input_price: 2.5, output_price: 10, cache_read_price: 0, fast_multiplier: 1.5 })
    restored[0].models.push('another-model')
    expect(prices[0].models).toEqual(['vendor-model'])
    expect(validateAccountModelCostPricing(restored, 'openai', t)).toBeNull()
  })

  it('preserves USD/second video tiers and normalizes the legacy model alias', () => {
    const entry = videoEntry(null)
    entry.models = ['bytedance/seedance-2.5']
    entry.intervals = [{
      min_tokens: 0, max_tokens: null, tier_label: '720p', per_request_price: '0.05',
      input_price: null, output_price: null, cache_write_price: null, cache_read_price: null,
      input_multiplier: null, output_multiplier: null, cache_write_multiplier: null, cache_read_multiplier: null,
      sort_order: 0,
    }]
    const prices = accountModelCostPricingToAPI([entry], 'seedance')
    expect(prices[0].models).toEqual(['seedance-2.5'])
    expect(prices[0].intervals[0].per_request_price).toBe(0.05)
    expect(readAccountModelCostPricing({ [ACCOUNT_MODEL_COST_KEY]: prices })[0].intervals[0].per_request_price).toBe(0.05)
    expect(validateAccountModelCostPricing([entry], 'seedance', t)).toBeNull()
    entry.intervals[0].tier_label = '1080p'
    expect(validateAccountModelCostPricing([entry], 'seedance', t)).not.toBeNull()
  })

  it('distinguishes no configuration, explicit zero and incomplete pricing', () => {
    expect(readAccountModelCostPricing({ unrelated: true })).toEqual([])
    expect(accountModelCostPricingToAPI([], 'seedance')).toEqual([])
    expect(validateAccountModelCostPricing([videoEntry(0)], 'seedance', t)).toBeNull()
    expect(validateAccountModelCostPricing([videoEntry(null)], 'seedance', t)).not.toBeNull()
    expect(validateAccountModelCostPricing([videoEntry(-1)], 'seedance', t)).not.toBeNull()
    expect(validateAccountModelCostPricing([videoEntry(Infinity)], 'seedance', t)).not.toBeNull()
  })

  it('rejects duplicate model matches including aliases and case variants', () => {
    expect(validateAccountModelCostPricing([videoEntry(), videoEntry()], 'seedance', t)).toBe('admin.channels.modelConflict')
    const alias = { ...videoEntry(), models: ['bytedance/seedance-2.5'] }
    const canonical = { ...videoEntry(), models: ['seedance-2.5'] }
    expect(validateAccountModelCostPricing([alias, canonical], 'seedance', t)).toBe('admin.channels.modelConflict')
    expect(validateAccountModelCostPricing([
      { ...createAccountModelCostEntry('openai'), models: ['vendor-*'], input_price: 1 },
      { ...createAccountModelCostEntry('openai'), models: ['VENDOR-MODEL'], output_price: 2 },
    ], 'openai', t)).toBe('admin.channels.modelConflict')
  })

  it('requires ordinary account default prices even when tier prices exist', () => {
    const interval = {
      min_tokens: 0, max_tokens: 100000, tier_label: '', per_request_price: null,
      input_price: 2, output_price: null, cache_write_price: null, cache_read_price: null,
      input_multiplier: null, output_multiplier: null, cache_write_multiplier: null, cache_read_multiplier: null,
      sort_order: 0,
    }
    const token = { ...createAccountModelCostEntry('openai'), models: ['vendor-model'], intervals: [interval] }
    expect(validateAccountModelCostPricing([token], 'openai', t)).toBe('admin.accounts.modelCostPricing.defaultPriceRequired')
    token.input_price = 0
    expect(validateAccountModelCostPricing([token], 'openai', t)).toBeNull()
    for (const mode of ['image', 'per_request', 'video'] as const) {
      const media = {
        ...createAccountModelCostEntry('openai'), models: ['vendor-model'], billing_mode: mode,
        intervals: [{ ...interval, input_price: null, tier_label: mode === 'video' ? '720p' : '1K', per_request_price: 0.05 }],
      }
      expect(validateAccountModelCostPricing([media], 'openai', t)).toBe('admin.accounts.modelCostPricing.defaultPriceRequired')
      media.per_request_price = 0
      expect(validateAccountModelCostPricing([media], 'openai', t)).toBeNull()
    }
  })
})

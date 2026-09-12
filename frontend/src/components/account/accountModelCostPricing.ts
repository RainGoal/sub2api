import type { ChannelModelPricing } from '@/api/admin/channels'
import { normalizeSeedanceModelID } from '@/constants/videoProviders'
import { isValidWildcardPattern } from '@/composables/useModelWhitelist'
import {
  apiIntervalsToForm, createDefaultTimePricingForm, findModelConflict,
  formIntervalsToAPI, isValidPositiveMultiplier, mTokToPerToken, perTokenToMTok,
  toNullableNumber, validateIntervals, type PricingFormEntry,
} from '@/components/admin/channel/types'
import { isValidSeedanceCostEntry } from '@/components/admin/channel/seedanceCostPricing'

export const ACCOUNT_MODEL_COST_KEY = 'model_cost_pricing'

const tokenPriceFields = [
  'input_price', 'output_price', 'cache_write_price', 'cache_write_1h_price',
  'cache_read_price', 'image_input_price', 'image_output_price',
] as const
const multiplierFields = ['fast_multiplier', 'flex_multiplier', 'max_reasoning_effort_multiplier'] as const

export function createAccountModelCostEntry(platform: string): PricingFormEntry {
  return {
    models: [], billing_mode: platform === 'seedance' ? 'video' : 'token',
    input_price: null, output_price: null, cache_write_price: null, cache_write_1h_price: null,
    cache_read_price: null, image_input_price: null, image_output_price: null,
    per_request_price: null, intervals: [], time_pricing: createDefaultTimePricingForm(),
  }
}

export function readAccountModelCostPricing(extra: Record<string, unknown> | undefined): PricingFormEntry[] {
  const prices = extra?.[ACCOUNT_MODEL_COST_KEY]
  if (!Array.isArray(prices)) return []
  return (prices as ChannelModelPricing[]).map(price => {
    const entry: PricingFormEntry = {
      ...createAccountModelCostEntry(price.platform), models: [...price.models],
      billing_mode: price.billing_mode, per_request_price: price.per_request_price,
      intervals: apiIntervalsToForm(price.intervals),
    }
    for (const field of tokenPriceFields) entry[field] = perTokenToMTok(price[field])
    for (const field of multiplierFields) entry[field] = price[field] ?? null
    return entry
  })
}

export function accountModelCostPricingToAPI(entries: PricingFormEntry[], platform: string): ChannelModelPricing[] {
  return entries.map(entry => {
    const price: ChannelModelPricing = {
      platform, models: entry.models.map(model => platform === 'seedance'
        ? normalizeSeedanceModelID(model.trim()) : model.trim()),
      billing_mode: entry.billing_mode, input_price: null, output_price: null,
      cache_write_price: null, cache_read_price: null, image_input_price: null, image_output_price: null,
      per_request_price: toNullableNumber(entry.per_request_price),
      intervals: formIntervalsToAPI(entry.intervals), time_pricing: null,
    }
    for (const field of tokenPriceFields) price[field] = mTokToPerToken(entry[field])
    for (const field of multiplierFields) price[field] = toNullableNumber(entry[field])
    return price
  })
}

export function validateAccountModelCostPricing(
  entries: PricingFormEntry[], platform: string,
  t: (key: string, params?: Record<string, unknown>) => string,
): string | null {
  const models = entries.flatMap(entry => entry.models.map(model => platform === 'seedance'
    ? normalizeSeedanceModelID(model.trim()) : model.trim()))
  const conflict = findModelConflict(models)
  if (conflict) return t('admin.channels.modelConflict', { model1: conflict[0], model2: conflict[1] })
  const hasPrice = (value: unknown) => value !== null && value !== undefined && value !== ''
  const validPrice = (value: unknown) => !hasPrice(value) || (Number.isFinite(Number(value)) && Number(value) >= 0)
  for (const [index, entry] of entries.entries()) {
    const invalid = () => t('admin.accounts.modelCostPricing.invalidEntry', { index: index + 1 })
    if (entry.models.length === 0 || entry.models.some(model =>
      !model.trim() || /\s/.test(model.trim()) || !isValidWildcardPattern(model.trim()))) return invalid()
    if (!['token', 'per_request', 'image', 'video'].includes(entry.billing_mode)) return invalid()
    if ([...tokenPriceFields, 'per_request_price']
      .some(field => !validPrice(entry[field as keyof PricingFormEntry]))) return invalid()
    if (multiplierFields.some(field => !isValidPositiveMultiplier(entry[field]))) return invalid()
    if (platform === 'seedance' && !isValidSeedanceCostEntry(entry)) return invalid()
    const intervalError = validateIntervals(entry.intervals, entry.billing_mode, t)
    if (intervalError) return intervalError
    if (entry.billing_mode === 'token') {
      if (!tokenPriceFields.some(field => hasPrice(entry[field]))) {
        return t('admin.accounts.modelCostPricing.defaultPriceRequired', { index: index + 1 })
      }
      for (const interval of entry.intervals) {
        const fields = tokenPriceFields.filter(field => field !== 'image_input_price' && field !== 'image_output_price')
        const multipliers = ['input_multiplier', 'output_multiplier', 'cache_write_multiplier', 'cache_read_multiplier'] as const
        if (interval.tier_label || hasPrice(interval.per_request_price) ||
          fields.some(field => !validPrice(interval[field])) ||
          (!fields.some(field => hasPrice(interval[field])) && !multipliers.some(field => hasPrice(interval[field])))) return invalid()
      }
    } else {
      if (platform !== 'seedance' && !hasPrice(entry.per_request_price)) {
        return t('admin.accounts.modelCostPricing.defaultPriceRequired', { index: index + 1 })
      }
      if (!hasPrice(entry.per_request_price) && !entry.intervals.length) return invalid()
      const tiers = new Set<string>()
      for (const interval of entry.intervals) {
        const tier = interval.tier_label.trim().toLowerCase()
        if (!tier || tiers.has(tier) || !hasPrice(interval.per_request_price) || !validPrice(interval.per_request_price)) return invalid()
        if (entry.billing_mode === 'video' && !['480p', '720p', '1080p', '4k'].includes(tier)) return invalid()
        tiers.add(tier)
      }
    }
  }
  return null
}

import { SEEDANCE_MODEL_OPTIONS, normalizeSeedanceModelID } from '@/constants/videoProviders'
import type { PricingFormEntry } from './types'

export function seedanceCostResolutions(models: string[]): string[] {
  const selected = models.map(normalizeSeedanceModelID)
  const known = SEEDANCE_MODEL_OPTIONS.filter(model => selected.includes(model.id))
  return ['480p', '720p', '1080p', '4k'].filter(resolution =>
    known.length === 0 || known.every(model => model.resolutions.some(value => value === resolution)))
}

export function isValidSeedanceCostEntry(entry: PricingFormEntry): boolean {
  if (entry.models.length === 0) return false
  const models = entry.models.map(model => entry.video_pricing_version ? model.trim() : normalizeSeedanceModelID(model))
  if (new Set(models).size !== models.length) return false
  const hasPrice = (value: unknown) => value !== null && value !== undefined && value !== ''
  const validPrice = (value: unknown) => hasPrice(value) && Number.isFinite(Number(value)) && Number(value) >= 0
  if (entry.video_pricing_version) {
    if (entry.video_pricing_version !== 1 || entry.billing_mode !== 'video' ||
      !['per_second', 'per_video'].includes(entry.video_billing_unit || '') ||
      (!(entry.video_billing_unit === 'per_video' && !entry.video_quantity_policy) && !['requested_seconds', 'output_seconds', 'output_and_reference_seconds'].includes(entry.video_quantity_policy || '')) ||
      !Number.isFinite(Number(entry.video_max_billable_seconds || 0)) || Number(entry.video_max_billable_seconds || 0) < 0 ||
      entry.models.some(model => !model.trim() || /[\s*]/.test(model))) return false
    if (hasPrice(entry.per_request_price) && !validPrice(entry.per_request_price)) return false
    if (!hasPrice(entry.per_request_price) && entry.intervals.length === 0) return false
    const tiers = new Set<string>()
    for (const interval of entry.intervals) {
      const tier = interval.tier_label.trim().toLowerCase()
      if (!tier || tiers.has(tier) || !validPrice(interval.per_request_price)) return false
      tiers.add(tier)
    }
    return true
  }
  if (entry.video_billing_unit || entry.video_quantity_policy || Number(entry.video_max_billable_seconds || 0)) return false
  if (entry.billing_mode === 'per_request') {
    return validPrice(entry.per_request_price) && entry.intervals.length === 0
  }
  if (entry.billing_mode !== 'video') return false
  if (hasPrice(entry.per_request_price) && !validPrice(entry.per_request_price)) return false
  if (!hasPrice(entry.per_request_price) && entry.intervals.length === 0) return false
  const resolutions = seedanceCostResolutions(entry.models)
  const seen = new Set<string>()
  for (const interval of entry.intervals) {
    const tier = interval.tier_label.trim().toLowerCase()
    if (!resolutions.includes(tier) || seen.has(tier) || !validPrice(interval.per_request_price)) return false
    seen.add(tier)
  }
  return true
}

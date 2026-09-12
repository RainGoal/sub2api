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
  const models = entry.models.map(normalizeSeedanceModelID)
  if (new Set(models).size !== models.length) return false
  const hasPrice = (value: unknown) => value !== null && value !== undefined && value !== ''
  const validPrice = (value: unknown) => hasPrice(value) && Number.isFinite(Number(value)) && Number(value) >= 0
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

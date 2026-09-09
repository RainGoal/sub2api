import { SEEDANCE_MODEL_OPTIONS } from '@/constants/videoProviders'

export const grokVideoPriceResolutions = [
  { key: '480p', label: '480p' },
  { key: '720p', label: '720p' },
  { key: '1080p', label: '1080p' }
] as const

type VideoPricingPlatform = 'grok' | 'seedance'
type ResolutionOption = { key: string; label: string }
type FamilyOption = { key: string; label: string; resolutions: readonly ResolutionOption[] }

const platformFamilies: Record<VideoPricingPlatform, readonly FamilyOption[]> = {
  grok: [
    { key: 'grok-imagine-video', label: 'grok-imagine-video', resolutions: grokVideoPriceResolutions },
    { key: 'grok-imagine-video-1.5', label: 'grok-imagine-video-1.5', resolutions: grokVideoPriceResolutions }
  ],
  seedance: SEEDANCE_MODEL_OPTIONS.map(({ id, label, resolutions }) => ({
    key: id,
    label,
    resolutions: resolutions.map((key) => ({ key, label: key === '4k' ? '4K' : key }))
  }))
}

export type VideoModelPrices = Record<string, Record<string, number>>
export type VideoModelPricesForm = Record<string, Record<string, number | string | null>>

function pricingPlatform(platform: string): VideoPricingPlatform {
  return platform === 'seedance' ? 'seedance' : 'grok'
}

function normalizeFamily(value: string): string {
  const family = value.trim().toLowerCase()
  return family === 'bytedance/seedance-2.5' ? 'seedance-2.5' : family
}

function normalizePrice(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const price = Number(value)
  return Number.isFinite(price) && price >= 0 ? price : null
}

function emptyTiers(resolutions: readonly ResolutionOption[]): Record<string, number | string | null> {
  return Object.fromEntries(resolutions.map(({ key }) => [key, null]))
}

function combinedResolutions(catalog: readonly FamilyOption[]): ResolutionOption[] {
  const resolutions = new Map<string, ResolutionOption>()
  for (const family of catalog) {
    for (const resolution of family.resolutions) resolutions.set(resolution.key, resolution)
  }
  return [...resolutions.values()]
}

export function createVideoModelPricesForm(
  prices?: VideoModelPrices | null,
  platform = 'grok'
): VideoModelPricesForm {
  const form: VideoModelPricesForm = {}
  const catalog = platformFamilies[pricingPlatform(platform)]
  const fallbackResolutions = combinedResolutions(catalog)

  for (const rawFamily of Object.keys(prices ?? {}).sort()) {
    const rawTiers = prices?.[rawFamily]
    const family = normalizeFamily(rawFamily)
    if (!family || !rawTiers || typeof rawTiers !== 'object') continue
    const known = catalog.find(({ key }) => key === family)
    form[family] ??= emptyTiers(known?.resolutions ?? fallbackResolutions)
    for (const [rawResolution, rawPrice] of Object.entries(rawTiers)) {
      const price = normalizePrice(rawPrice)
      if (price !== null) form[family][rawResolution.trim().toLowerCase()] = price
    }
  }

  for (const family of catalog) {
    form[family.key] ??= emptyTiers(family.resolutions)
  }
  return form
}

export function serializeVideoModelPrices(form: VideoModelPricesForm): VideoModelPrices {
  const result: VideoModelPrices = {}
  for (const rawFamily of Object.keys(form).sort()) {
    const tiers = form[rawFamily]
    const family = normalizeFamily(rawFamily)
    if (!family || !tiers || typeof tiers !== 'object') continue

    const normalizedTiers: Record<string, number> = result[family] ?? {}
    for (const [rawResolution, rawPrice] of Object.entries(tiers)) {
      const resolution = rawResolution.trim().toLowerCase()
      const price = normalizePrice(rawPrice)
      if (resolution && price !== null) normalizedTiers[resolution] = price
    }
    if (Object.keys(normalizedTiers).length > 0) result[family] = normalizedTiers
  }
  return result
}

export function videoModelPriceFamilyRows(form: VideoModelPricesForm, platform = 'grok') {
  const catalog = platformFamilies[pricingPlatform(platform)]
  const known = new Set<string>(catalog.map(({ key }) => key))
  const fallbackResolutions = combinedResolutions(catalog)
  const extra = Object.keys(form)
    .map(normalizeFamily)
    .filter((family) => family && !known.has(family))
    .sort()
    .map((key) => ({ key, label: key, resolutions: fallbackResolutions }))
  return [...catalog, ...extra]
}

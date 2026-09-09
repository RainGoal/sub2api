export const VIDEO_PROVIDER_OPTIONS = [
  { id: 'bblabu_v1', label: 'bblabu V1', defaultBaseUrl: 'https://api.bblabu.ai/v1' },
  { id: 'fflink_v1', label: 'fflink V1', defaultBaseUrl: 'https://api.fflink.top/v1' }
] as const

export type VideoProviderID = (typeof VIDEO_PROVIDER_OPTIONS)[number]['id']

export const DEFAULT_VIDEO_PROVIDER_ID: VideoProviderID = 'bblabu_v1'

export interface SeedanceModelOption {
  id: string
  label: string
  resolutions: string[]
}

export const SEEDANCE_MODEL_OPTIONS: SeedanceModelOption[] = [
  { id: 'seedance-2.0', label: 'Seedance-2.0', resolutions: ['480p', '720p', '1080p', '4k'] },
  { id: 'seedance-2.0-fast', label: 'Seedance-2.0 Fast', resolutions: ['480p', '720p'] },
  { id: 'seedance-2.0-mini', label: 'Seedance-2.0 Mini', resolutions: ['480p', '720p', '1080p'] },
  { id: 'seedance-2.5', label: 'Seedance-2.5', resolutions: ['480p', '720p'] }
]

export function normalizeSeedanceModelID(model: string): string {
  const normalized = model.trim().toLowerCase()
  return normalized === 'bytedance/seedance-2.5' ? 'seedance-2.5' : normalized
}

export function videoProviderModelOptions(providerID: VideoProviderID): SeedanceModelOption[] {
  return providerID === 'fflink_v1'
    ? SEEDANCE_MODEL_OPTIONS
    : SEEDANCE_MODEL_OPTIONS.filter(({ id }) => id === 'seedance-2.0' || id === 'seedance-2.5')
}

export function normalizeVideoProviderID(value: unknown): VideoProviderID {
  return VIDEO_PROVIDER_OPTIONS.some((provider) => provider.id === value)
    ? (value as VideoProviderID)
    : DEFAULT_VIDEO_PROVIDER_ID
}

export function videoProviderDefaultBaseUrl(providerID: VideoProviderID): string {
  return (
    VIDEO_PROVIDER_OPTIONS.find((provider) => provider.id === providerID)?.defaultBaseUrl ??
    VIDEO_PROVIDER_OPTIONS[0].defaultBaseUrl
  )
}

export function videoProviderDisplayName(providerID: VideoProviderID): string {
  return (
    VIDEO_PROVIDER_OPTIONS.find((provider) => provider.id === providerID)?.label ??
    VIDEO_PROVIDER_OPTIONS[0].label
  )
}

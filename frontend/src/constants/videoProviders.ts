export const VIDEO_PROVIDER_OPTIONS = [
  { id: 'bblabu_v1', label: 'bblabu V1', defaultBaseUrl: 'https://api.bblabu.ai/v1' },
  { id: 'fflink_v1', label: 'fflink V1', defaultBaseUrl: 'https://api.fflink.top/v1' }
] as const

export type VideoProviderID = (typeof VIDEO_PROVIDER_OPTIONS)[number]['id']

export const DEFAULT_VIDEO_PROVIDER_ID: VideoProviderID = 'bblabu_v1'

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

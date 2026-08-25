import { describe, expect, it } from 'vitest'
import {
  DEFAULT_VIDEO_PROVIDER_ID,
  VIDEO_PROVIDER_OPTIONS,
  normalizeVideoProviderID,
  videoProviderDefaultBaseUrl,
  videoProviderDisplayName
} from '../videoProviders'

describe('video provider catalog', () => {
  it('keeps protocol metadata in one ordered catalog', () => {
    expect(VIDEO_PROVIDER_OPTIONS.map((provider) => provider.id)).toEqual([
      'bblabu_v1',
      'fflink_v1'
    ])
    expect(videoProviderDefaultBaseUrl('fflink_v1')).toBe('https://api.fflink.top/v1')
    expect(videoProviderDisplayName('bblabu_v1')).toBe('bblabu V1')
  })

  it('falls back to the backward-compatible default for missing and unknown values', () => {
    expect(normalizeVideoProviderID(undefined)).toBe(DEFAULT_VIDEO_PROVIDER_ID)
    expect(normalizeVideoProviderID('unknown_v1')).toBe(DEFAULT_VIDEO_PROVIDER_ID)
  })
})

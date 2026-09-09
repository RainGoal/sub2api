import { describe, expect, it } from 'vitest'
import {
  DEFAULT_VIDEO_PROVIDER_ID,
  SEEDANCE_MODEL_OPTIONS,
  VIDEO_PROVIDER_OPTIONS,
  normalizeSeedanceModelID,
  normalizeVideoProviderID,
  videoProviderDefaultBaseUrl,
  videoProviderDisplayName,
  videoProviderModelOptions
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

  it('limits each protocol to its built-in Seedance models and resolutions', () => {
    expect(videoProviderModelOptions('bblabu_v1').map(({ id }) => id)).toEqual([
      'seedance-2.0', 'seedance-2.5'
    ])
    expect(videoProviderModelOptions('fflink_v1')).toEqual(SEEDANCE_MODEL_OPTIONS)
    expect(SEEDANCE_MODEL_OPTIONS.map(({ id, resolutions }) => [id, resolutions])).toEqual([
      ['seedance-2.0', ['480p', '720p', '1080p', '4k']],
      ['seedance-2.0-fast', ['480p', '720p']],
      ['seedance-2.0-mini', ['480p', '720p', '1080p']],
      ['seedance-2.5', ['480p', '720p']]
    ])
    expect(normalizeSeedanceModelID(' Bytedance/Seedance-2.5 ')).toBe('seedance-2.5')
  })
})

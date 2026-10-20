import { describe, expect, it } from 'vitest'
import { buildVideoCapability, createVideoCapabilityForm, validateVideoModelCapability } from '../videoModelCapabilityForm'
import type { VideoModelCapability } from '@/api/admin/videoTasks'

function validCapability(overrides: Record<string, unknown> = {}): VideoModelCapability {
  return buildVideoCapability('Supplier/New-Video', {
    ...createVideoCapabilityForm(), profile: 'bblabu_route', enabled: true,
    duration_mode: 'controlled', min_duration: '4', max_duration: '12', default_duration: '4',
    resolutions: '720p, 1080p', default_resolution: '720p', resolution_control: true,
    ratios: '16:9, auto', default_ratio: '16:9',
    billing_unit: 'per_video', metering_policy: 'per_video', billing_policy: 'output_only',
    ...overrides
  })
}

describe('video model capability configuration', () => {
  it('requires explicit profile, duration, and billing rules for an unknown model', () => {
    const form = createVideoCapabilityForm()
    expect(form.enabled).toBe(false)
    expect(form.profile).toBe('')
    expect(form.duration_mode).toBe('')
    expect(form.billing_unit).toBe('')
    expect(validateVideoModelCapability(buildVideoCapability('Supplier/New-Video', form))).not.toBeNull()
    expect(validateVideoModelCapability(validCapability())).toBeNull()
  })

  it('preserves exact upstream names, unknown fields, and source immutability', () => {
    const original = { ...validCapability(), future_contract: { revision: 3 }, max_images: 2, max_images_by_resolution: { '720p': 1 } }
    const before = JSON.stringify(original)
    const form = createVideoCapabilityForm(original)
    form.imageLimits[0]!.limit = '2'
    const result = buildVideoCapability('Exact/UPSTREAM-v2', form)
    expect(result.model).toBe('Exact/UPSTREAM-v2')
    expect(result.future_contract).toEqual({ revision: 3 })
    expect(result.max_images_by_resolution).toEqual({ '720p': 2 })
    expect(JSON.stringify(original)).toBe(before)
  })

  it.each([
    [{ duration_mode: 'fixed', min_duration: 4, max_duration: 8 }, 'durationBounds'],
    [{ allowed_durations: [8, 12] }, 'allowedDurations'],
    [{ resolutions: ['720p', '720P'] }, 'options'],
    [{ default_resolution: '4k' }, 'resolution'],
    [{ resolutions: [], resolution_control: true }, 'resolution'],
    [{ default_ratio: '4:3' }, 'ratio'],
    [{ max_images: -1 }, 'limits'],
    [{ max_images: 2, max_images_by_resolution: { '1080p': 3 } }, 'imageLimits'],
    [{ frames: false, allow_first_only: true }, 'frames'],
    [{ frames: true, frames_require_auto_ratio: true, ratios: ['16:9'] }, 'frameRatio'],
    [{ billing_unit: 'per_video', metering_policy: 'output_seconds' }, 'perVideo'],
    [{ billing_unit: 'per_second', metering_policy: 'output_and_reference_seconds', billing_policy: 'output_and_reference', max_reference_video_seconds: 0 }, 'referenceMetering'],
    [{ billing_unit: 'per_second', metering_policy: 'output_seconds', billing_policy: 'output_and_reference' }, 'billingPolicy'],
    [{ content_mode: 'arbitrary_url' }, 'content'],
    [{ unverified_combinations: ['unknown_combination'] }, 'unverified'],
    [{ enabled: 'yes' }, 'schema'],
    [{ resolutions: '720p' }, 'schema'],
    [{ max_images_by_resolution: [] }, 'schema']
  ])('rejects invalid capability combinations: %j', (overrides, error) => {
    expect(validateVideoModelCapability({ ...validCapability(), ...overrides })).toBe(error)
  })

  it('allows per-video billing when upstream decides duration and clears obsolete duration values', () => {
    const form = createVideoCapabilityForm(validCapability())
    form.duration_mode = 'unknown'
    form.allowed_durations = '4, 8'
    const result = buildVideoCapability('Supplier/New-Video', form)
    expect(result).toMatchObject({ min_duration: 0, max_duration: 0, default_duration: 0, allowed_durations: [] })
    expect(validateVideoModelCapability(result)).toBeNull()
    expect(validateVideoModelCapability({ ...result, billing_unit: 'per_second', metering_policy: 'output_seconds' })).toBe('unknownSecond')
  })

  it('drops controlled duration options when switching a copied capability to fixed duration', () => {
    const form = createVideoCapabilityForm({ ...validCapability(), allowed_durations: [4, 8, 12] })
    form.duration_mode = 'fixed'
    form.min_duration = form.max_duration = form.default_duration = '4'
    const fixed = buildVideoCapability('Supplier/New-Video', form)
    expect(fixed.allowed_durations).toEqual([])
    expect(validateVideoModelCapability(fixed)).toBeNull()
    form.duration_mode = 'controlled'
    form.max_duration = '12'
    expect(buildVideoCapability('Supplier/New-Video', form).allowed_durations).toEqual([4, 8, 12])
  })

  it('accepts explicit reference-second billing only with a finite reference duration limit', () => {
    const capability = { ...validCapability(), billing_unit: 'per_second', metering_policy: 'output_and_reference_seconds', billing_policy: 'output_and_reference', max_reference_video_seconds: 15 }
    expect(validateVideoModelCapability(capability)).toBeNull()
  })
})

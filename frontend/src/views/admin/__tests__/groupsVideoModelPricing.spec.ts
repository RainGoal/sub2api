import { describe, expect, it } from 'vitest'

import {
  createVideoModelPricesForm,
  serializeVideoModelPrices,
  videoModelPriceFamilyRows
} from '../groupsVideoModelPricing'

describe('Grok video model pricing form', () => {
  it('provides editable rows for both canonical Grok video families', () => {
    const form = createVideoModelPricesForm()

    expect(videoModelPriceFamilyRows(form).map(({ key }) => key)).toEqual([
      'grok-imagine-video',
      'grok-imagine-video-1.5'
    ])
    expect(form['grok-imagine-video']['480p']).toBeNull()
    expect(form['grok-imagine-video-1.5']['1080p']).toBeNull()
  })

  it('serializes only finite non-negative prices and preserves future families', () => {
    const form = createVideoModelPricesForm({
      'grok-imagine-video-2': { '1080p': 0.4 }
    })
    form['grok-imagine-video']['480p'] = 0.05
    form['grok-imagine-video']['720p'] = ''
    form['grok-imagine-video-1.5']['1080p'] = -1

    expect(serializeVideoModelPrices(form)).toEqual({
      'grok-imagine-video': { '480p': 0.05 },
      'grok-imagine-video-2': { '1080p': 0.4 }
    })
  })

  it('round-trips unknown model families so editing does not discard them', () => {
    const form = createVideoModelPricesForm({
      'grok-imagine-video-2': { '480p': 0.2 }
    })

    expect(videoModelPriceFamilyRows(form).map(({ key }) => key)).toContain(
      'grok-imagine-video-2'
    )
    expect(serializeVideoModelPrices(form)).toMatchObject({
      'grok-imagine-video-2': { '480p': 0.2 }
    })
    const futureRow = videoModelPriceFamilyRows(form).find(({ key }) => key === 'grok-imagine-video-2')
    expect(futureRow?.resolutions.map(({ key }) => key)).toEqual(['480p', '720p', '1080p'])
  })
})

describe('Seedance video model pricing form', () => {
  it('exposes only the documented resolution tiers for each model', () => {
    const form = createVideoModelPricesForm(undefined, 'seedance')
    const rows = videoModelPriceFamilyRows(form, 'seedance')

    expect(rows.map(({ key }) => key)).toEqual(['seedance-2.0', 'seedance-2.5'])
    expect(rows[0].resolutions.map(({ key }) => key)).toEqual(['480p', '720p', '1080p', '4k'])
    expect(rows[1].resolutions.map(({ key }) => key)).toEqual(['480p', '720p'])
    expect(form['seedance-2.0']['4k']).toBeNull()
    expect(form['seedance-2.5']['1080p']).toBeUndefined()
  })

  it('normalizes Seedance model names for backend lookup', () => {
    const form = createVideoModelPricesForm({
      'Seedance-2.5': { '720P': 0.3 }
    }, 'seedance')

    expect(serializeVideoModelPrices(form)).toMatchObject({
      'seedance-2.5': { '720p': 0.3 }
    })
  })

  it('renders each fallback resolution once for future Seedance models', () => {
    const form = createVideoModelPricesForm({ 'Seedance-3.0': { '720p': 0.5 } }, 'seedance')
    const futureRow = videoModelPriceFamilyRows(form, 'seedance').find(
      ({ key }) => key === 'seedance-3.0'
    )

    expect(futureRow?.resolutions.map(({ key }) => key)).toEqual([
      '480p',
      '720p',
      '1080p',
      '4k'
    ])
  })
})

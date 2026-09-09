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

    expect(rows.map(({ key, resolutions }) => [key, resolutions.map(({ key }) => key)])).toEqual([
      ['seedance-2.0', ['480p', '720p', '1080p', '4k']],
      ['seedance-2.0-fast', ['480p', '720p']],
      ['seedance-2.0-mini', ['480p', '720p', '1080p']],
      ['seedance-2.5', ['480p', '720p']]
    ])
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

  it('merges the 2.5 alias without mixing prices between model variants', () => {
    const prices = {
      'seedance-2.5': { '720p': 0.3 },
      'bytedance/seedance-2.5': { '480p': 0.2, '720p': 0.9 },
      'seedance-2.0': { '720p': 0.1, '4k': 0.4 },
      'seedance-2.0-fast': { '720p': 0.05 },
      'seedance-2.0-mini': { '1080p': 0.08 }
    }
    const form = createVideoModelPricesForm(prices, 'seedance')
    const expected = {
      'seedance-2.5': { '480p': 0.2, '720p': 0.3 },
      'seedance-2.0': { '720p': 0.1, '4k': 0.4 },
      'seedance-2.0-fast': { '720p': 0.05 },
      'seedance-2.0-mini': { '1080p': 0.08 }
    }

    expect(serializeVideoModelPrices(form)).toEqual(expected)
    expect(serializeVideoModelPrices(prices)).toEqual(expected)
    expect(videoModelPriceFamilyRows(form, 'seedance')).toHaveLength(4)
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

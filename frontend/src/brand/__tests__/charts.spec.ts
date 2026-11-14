import { afterEach, describe, expect, it } from 'vitest'
import { adminChartColor, adminChartFill, adminChartPalette, adminChartTooltip } from '../charts'

afterEach(() => {
  delete document.documentElement.dataset.adminBrand
  document.documentElement.style.removeProperty('--ws-chart-1')
  document.documentElement.style.removeProperty('--ws-panel')
})

describe('admin chart scope', () => {
  it('preserves user chart colors and tooltip defaults outside the admin scope', () => {
    document.documentElement.style.setProperty('--ws-chart-1', '#123456')
    const original = ['#001122', '#334455']
    expect(adminChartColor('accent', '#001122')).toBe('#001122')
    expect(adminChartPalette(original)).toEqual(original)
    expect(adminChartFill('#123456', 'rgba(0, 17, 34, 0.2)')).toBe('rgba(0, 17, 34, 0.2)')
    expect(adminChartTooltip()).toEqual({})
  })

  it('reads the active admin tokens without changing the caller palette', () => {
    document.documentElement.dataset.adminBrand = 'wsapi'
    document.documentElement.style.setProperty('--ws-chart-1', '#123456')
    document.documentElement.style.setProperty('--ws-panel', '#654321')
    const original = ['#001122', '#334455', '#667788']
    const themed = adminChartPalette(original)
    expect(themed[0]).toBe('#123456')
    expect(new Set(themed).size).toBe(original.length)
    expect(original).toEqual(['#001122', '#334455', '#667788'])
    expect(adminChartTooltip().backgroundColor).toBe('#654321')
  })

  it('stops applying admin overrides after the route leaves the scope', () => {
    document.documentElement.dataset.adminBrand = 'wsapi'
    document.documentElement.style.setProperty('--ws-chart-1', '#123456')
    expect(adminChartColor('accent', '#001122')).toBe('#123456')
    delete document.documentElement.dataset.adminBrand
    expect(adminChartColor('accent', '#001122')).toBe('#001122')
    expect(adminChartTooltip()).toEqual({})
  })
})

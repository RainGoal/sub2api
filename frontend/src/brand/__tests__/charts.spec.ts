import { afterEach, describe, expect, it } from 'vitest'
import { computed } from 'vue'
import { adminChartColor, adminChartFill, adminChartPalette, adminChartTooltip } from '../charts'
import { setAdminBrand, setThemePreference } from '../theme'

afterEach(() => {
  delete document.documentElement.dataset.adminBrand
  document.documentElement.classList.remove('dark')
  localStorage.removeItem('theme')
  localStorage.removeItem('ws_theme')
  document.documentElement.style.removeProperty('--ws-chart-1')
  document.documentElement.style.removeProperty('--ws-panel')
})

describe('admin chart scope', () => {
  it('updates cached chart colors and tooltip options when the theme changes', () => {
    localStorage.setItem('ws_theme', 'dark')
    setAdminBrand(true)
    document.documentElement.style.setProperty('--ws-chart-1', '#ee6a43')
    document.documentElement.style.setProperty('--ws-panel', '#252523')
    const palette = computed(() => adminChartPalette(['#001122', '#334455', '#667788']))
    const tooltip = computed(() => adminChartTooltip())
    const darkCategory = palette.value[2]
    expect(palette.value[0]).toBe('#ee6a43')
    expect(tooltip.value.backgroundColor).toBe('#252523')

    document.documentElement.style.setProperty('--ws-chart-1', '#b03a1a')
    document.documentElement.style.setProperty('--ws-panel', '#ffffff')
    setThemePreference('light')
    expect(palette.value[0]).toBe('#b03a1a')
    expect(palette.value[2]).not.toBe(darkCategory)
    expect(tooltip.value.backgroundColor).toBe('#ffffff')
    setAdminBrand(false)
    expect(palette.value).toEqual(['#001122', '#334455', '#667788'])
    expect(tooltip.value).toEqual({})
  })

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

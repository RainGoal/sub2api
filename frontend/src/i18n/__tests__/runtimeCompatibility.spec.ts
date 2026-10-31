import { afterEach, describe, expect, it, vi } from 'vitest'

import { getLocale, i18n, initI18n, loadLocaleMessages } from '..'

const originalLocale = i18n.global.locale.value
const originalLanguage = document.documentElement.lang

afterEach(() => {
  vi.restoreAllMocks()
  i18n.global.locale.value = originalLocale
  document.documentElement.lang = originalLanguage
})

describe('production i18n runtime compatibility', () => {
  it.each([
    ['en', 'Loading...', 'Peak rate: 15m'],
    ['zh', '加载中...', '高峰倍率：15m']
  ] as const)('loads and interpolates %s messages', async (locale, loading, peakRate) => {
    i18n.global.locale.value = locale
    await initI18n()

    expect(getLocale()).toBe(locale)
    expect(document.documentElement.lang).toBe(locale)
    expect(i18n.global.t('common.loading')).toBe(loading)
    expect(i18n.global.t('common.peakRateTooltip', { window: '15m' })).toBe(peakRate)
  })

  it('switches between cached locales without losing numeric interpolation', async () => {
    await Promise.all([loadLocaleMessages('en'), loadLocaleMessages('zh')])
    i18n.global.locale.value = 'zh'
    expect(i18n.global.t('common.selectedCount', { count: 3 })).toBe('（已选 3 个）')

    i18n.global.locale.value = 'en'
    expect(i18n.global.t('common.selectedCount', { count: 3 })).toBe('(3 selected)')
  })

  it('interpolates runtime messages without requiring unsafe-eval', async () => {
    await loadLocaleMessages('en')
    i18n.global.locale.value = 'en'
    const dynamicFunction = vi.spyOn(globalThis, 'Function').mockImplementation(() => {
      throw new Error('CSP blocks unsafe-eval')
    })

    expect(i18n.global.t('common.selectedFile', { name: 'example.png' })).toBe('Selected: example.png')
    expect(dynamicFunction).not.toHaveBeenCalled()
  })
})

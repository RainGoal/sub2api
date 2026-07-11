export type ThemeMode = 'dark' | 'light'

const THEME_STORAGE_KEY = 'theme'
const LEGACY_FORCED_DARK_MIGRATION_KEY = 'theme-system-default-migrated'
const SYSTEM_THEME_MEDIA_QUERY = '(prefers-color-scheme: dark)'

let systemThemeListenerBound = false

function getSystemTheme(): ThemeMode {
  if (typeof window === 'undefined' || !window.matchMedia) {
    return 'dark'
  }
  return window.matchMedia(SYSTEM_THEME_MEDIA_QUERY).matches ? 'dark' : 'light'
}

function readStoredTheme(): ThemeMode | null {
  try {
    const theme = localStorage.getItem(THEME_STORAGE_KEY)
    const migrationComplete = localStorage.getItem(LEGACY_FORCED_DARK_MIGRATION_KEY) === 'true'

    if (theme === 'dark' && !migrationComplete) {
      localStorage.removeItem(THEME_STORAGE_KEY)
      localStorage.setItem(LEGACY_FORCED_DARK_MIGRATION_KEY, 'true')
      return null
    }

    if (!migrationComplete) {
      localStorage.setItem(LEGACY_FORCED_DARK_MIGRATION_KEY, 'true')
    }

    return theme === 'dark' || theme === 'light' ? theme : null
  } catch {
    return null
  }
}

function persistTheme(theme: ThemeMode) {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme)
    localStorage.setItem(LEGACY_FORCED_DARK_MIGRATION_KEY, 'true')
  } catch {
    // Theme persistence is best effort; class application still works.
  }
}

function applyTheme(theme: ThemeMode) {
  const root = document.documentElement
  root.classList.toggle('dark', theme === 'dark')
  root.classList.toggle('light', theme === 'light')
}

function dispatchThemeChange(theme: ThemeMode) {
  window.dispatchEvent(new CustomEvent<ThemeMode>('themechange', { detail: theme }))
}

function applyAndNotify(theme: ThemeMode): ThemeMode {
  applyTheme(theme)
  dispatchThemeChange(theme)
  return theme
}

function bindSystemThemeListener() {
  if (systemThemeListenerBound || typeof window === 'undefined' || !window.matchMedia) {
    return
  }

  const mediaQuery = window.matchMedia(SYSTEM_THEME_MEDIA_QUERY)
  mediaQuery.addEventListener('change', (event) => {
    if (readStoredTheme()) return
    applyAndNotify(event.matches ? 'dark' : 'light')
  })
  systemThemeListenerBound = true
}

export function initTheme(): ThemeMode {
  const theme = readStoredTheme() ?? getSystemTheme()
  applyTheme(theme)
  bindSystemThemeListener()
  return theme
}

export function getCurrentTheme(): ThemeMode {
  if (document.documentElement.classList.contains('light')) return 'light'
  if (document.documentElement.classList.contains('dark')) return 'dark'
  return getSystemTheme()
}

export function setTheme(theme: ThemeMode): ThemeMode {
  persistTheme(theme)
  return applyAndNotify(theme)
}

export function toggleTheme(): ThemeMode {
  return setTheme(getCurrentTheme() === 'dark' ? 'light' : 'dark')
}

export function forceDarkTheme() {
  setTheme('dark')
}

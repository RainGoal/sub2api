import { onScopeDispose, readonly, ref, watch } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

type BrandRoute = Pick<RouteLocationNormalizedLoaded, 'path' | 'meta'>
const themeRevision = ref(0)
export const adminThemeRevision = readonly(themeRevision)
const DARK_QUERY = '(prefers-color-scheme: dark)'

export function isAdminBrandRoute(route: BrandRoute, backendMode: boolean, isAdmin = false): boolean {
  // Keep the administrator's personal workspace in the same shell; the
  // standalone OAuth consent page retains its original authentication layout.
  const personalWorkspace = route.meta.requiresAuth === true && route.path !== '/oauth/mcp/authorize'
  return route.meta.requiresAdmin === true ||
    (isAdmin && personalWorkspace) ||
    (backendMode && route.path === '/login')
}

export function applyPreferredTheme(adminBrand = document.documentElement.dataset.adminBrand === 'wsapi'): boolean {
  const sharedPreference = adminBrand ? localStorage.getItem('ws_theme') : null
  const preference = ['light', 'dark', 'system'].includes(sharedPreference ?? '')
    ? sharedPreference
    : localStorage.getItem('theme')
  const dark = preference === 'dark' ||
    (preference !== 'light' && window.matchMedia(DARK_QUERY).matches)
  document.documentElement.classList.toggle('dark', dark)
  // Chart options read CSS tokens and must be recalculated after the root changes.
  themeRevision.value += 1
  return dark
}

export function setThemePreference(preference: 'light' | 'dark'): boolean {
  const brandActive = document.documentElement.dataset.adminBrand === 'wsapi'
  localStorage.setItem(brandActive ? 'ws_theme' : 'theme', preference)
  return applyPreferredTheme(brandActive)
}

export function setAdminBrand(active: boolean): void {
  const root = document.documentElement
  if (active) {
    root.dataset.adminBrand = 'wsapi'
    applyPreferredTheme(true)
  } else if (root.dataset.adminBrand === 'wsapi') {
    delete root.dataset.adminBrand
    // Restore the user's preference without ever overwriting it for the brand.
    applyPreferredTheme(false)
  }
}

/** Apply before child setup, including charts and Teleports outside AppLayout. */
export function useAdminBrand(
  route: BrandRoute,
  backendMode: () => boolean,
  isAdmin: () => boolean = () => false
): void {
  watch(
    () => isAdminBrandRoute(route, backendMode(), isAdmin()),
    setAdminBrand,
    { immediate: true, flush: 'sync' }
  )
  const media = window.matchMedia(DARK_QUERY)
  const updateBrandTheme = () => {
    if (document.documentElement.dataset.adminBrand === 'wsapi') applyPreferredTheme(true)
  }
  const onStorage = (event: StorageEvent) => {
    if (event.storageArea && event.storageArea !== localStorage) return
    if (event.key === null || event.key === 'ws_theme' || event.key === 'theme') updateBrandTheme()
  }
  media.addEventListener('change', updateBrandTheme)
  window.addEventListener('storage', onStorage)
  onScopeDispose(() => {
    media.removeEventListener('change', updateBrandTheme)
    window.removeEventListener('storage', onStorage)
    setAdminBrand(false)
  })
}

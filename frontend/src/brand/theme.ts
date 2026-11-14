import { onScopeDispose, watch } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

type BrandRoute = Pick<RouteLocationNormalizedLoaded, 'path' | 'meta'>

export function isAdminBrandRoute(route: BrandRoute, backendMode: boolean, isAdmin = false): boolean {
  // Keep the administrator's personal workspace in the same shell; the
  // standalone OAuth consent page retains its original authentication layout.
  const personalWorkspace = route.meta.requiresAuth === true && route.path !== '/oauth/mcp/authorize'
  return route.meta.requiresAdmin === true ||
    (isAdmin && personalWorkspace) ||
    (backendMode && route.path === '/login')
}

function preferredDarkMode(): boolean {
  const preference = localStorage.getItem('theme')
  return preference === 'dark' ||
    (!preference && window.matchMedia('(prefers-color-scheme: dark)').matches)
}

export function setAdminBrand(active: boolean): void {
  const root = document.documentElement
  if (active) {
    root.dataset.adminBrand = 'wsapi'
    root.classList.add('dark')
  } else if (root.dataset.adminBrand === 'wsapi') {
    delete root.dataset.adminBrand
    // Restore the user's preference without ever overwriting it for the brand.
    root.classList.toggle('dark', preferredDarkMode())
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
  onScopeDispose(() => setAdminBrand(false))
}

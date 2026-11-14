import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, effectScope, h, reactive, ref, Teleport } from 'vue'
import { isAdminBrandRoute, setAdminBrand, useAdminBrand } from '../theme'

afterEach(() => {
  delete document.documentElement.dataset.adminBrand
  document.documentElement.classList.remove('dark')
  localStorage.removeItem('theme')
  vi.restoreAllMocks()
})

describe('admin brand lifecycle', () => {
  it('uses route metadata and the existing backend-only login setting', () => {
    expect(isAdminBrandRoute({ path: '/admin/users', meta: { requiresAdmin: true } }, false)).toBe(true)
    expect(isAdminBrandRoute({ path: '/profile', meta: {} }, true)).toBe(false)
    expect(isAdminBrandRoute({ path: '/login', meta: {} }, true)).toBe(true)
    expect(isAdminBrandRoute({ path: '/login', meta: {} }, false)).toBe(false)
    expect(isAdminBrandRoute({ path: '/auth/callback', meta: {} }, true)).toBe(false)
    expect(isAdminBrandRoute({ path: '/profile', meta: { requiresAuth: true } }, false, false)).toBe(false)
    expect(isAdminBrandRoute({ path: '/oauth/mcp/authorize', meta: { requiresAuth: true } }, true, true)).toBe(false)
    expect(isAdminBrandRoute({ path: '/payment/result', meta: { requiresAuth: false } }, true, true)).toBe(false)
  })

  it('enters dark mode synchronously and restores a saved light preference when leaving', () => {
    localStorage.setItem('theme', 'light')
    const route = reactive({ path: '/admin/users', meta: { requiresAdmin: true } })
    const scope = effectScope()
    scope.run(() => useAdminBrand(route, () => false))
    expect(document.documentElement.dataset.adminBrand).toBe('wsapi')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('light')

    route.meta.requiresAdmin = false
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    scope.stop()
  })

  it('keeps an administrator in the brand across management and personal account pages', () => {
    localStorage.setItem('theme', 'light')
    const route = reactive({ path: '/admin/users', meta: { requiresAuth: true, requiresAdmin: true } })
    const scope = effectScope()
    scope.run(() => useAdminBrand(route, () => false, () => true))

    for (const path of ['/keys', '/usage', '/available-channels', '/monitor', '/subscriptions', '/purchase', '/orders', '/redeem', '/affiliate', '/profile']) {
      route.path = path
      route.meta.requiresAdmin = false
      expect(document.documentElement.dataset.adminBrand).toBe('wsapi')
      expect(document.documentElement.classList.contains('dark')).toBe(true)
      expect(localStorage.getItem('theme')).toBe('light')
    }

    route.path = '/home'
    route.meta.requiresAuth = false
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    scope.stop()
  })

  it('reacts to administrator restoration and logout on a personal deep link', () => {
    localStorage.setItem('theme', 'light')
    const isAdmin = ref(false)
    const scope = effectScope()
    scope.run(() => useAdminBrand(
      { path: '/profile', meta: { requiresAuth: true } }, () => false, () => isAdmin.value
    ))
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    isAdmin.value = true
    expect(document.documentElement.dataset.adminBrand).toBe('wsapi')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    isAdmin.value = false
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    scope.stop()
  })

  it('restores a saved dark preference on disposal', () => {
    localStorage.setItem('theme', 'dark')
    const scope = effectScope()
    scope.run(() => useAdminBrand({ path: '/admin/ops', meta: { requiresAdmin: true } }, () => false))
    scope.stop()
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('uses the current system preference on exit when no preference was saved', () => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    vi.spyOn(window, 'matchMedia').mockReturnValue({ ...media, matches: false })
    setAdminBrand(true)
    setAdminBrand(false)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBeNull()
  })

  it('responds to delayed public settings without changing non-login public pages', () => {
    const route = reactive({ path: '/login', meta: {} })
    const backendMode = ref(false)
    const scope = effectScope()
    scope.run(() => useAdminBrand(route, () => backendMode.value))
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    backendMode.value = true
    expect(document.documentElement.dataset.adminBrand).toBe('wsapi')
    route.path = '/home'
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    scope.stop()
  })

  it('leaves ordinary page theme choices alone when the brand was never active', () => {
    localStorage.setItem('theme', 'light')
    document.documentElement.classList.add('dark')
    setAdminBrand(false)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('sets the root scope before child setup and includes a body Teleport', () => {
    localStorage.setItem('theme', 'light')
    let brandAtChildSetup: string | undefined
    const Child = defineComponent({
      setup() {
        brandAtChildSetup = document.documentElement.dataset.adminBrand
        return () => h(Teleport, { to: 'body' }, h('div', { id: 'brand-test-dialog' }, 'Dialog'))
      }
    })
    const Host = defineComponent({
      setup() {
        useAdminBrand({ path: '/admin/users', meta: { requiresAdmin: true } }, () => false)
        return () => h(Child)
      }
    })
    const wrapper = mount(Host)
    try {
      const dialog = document.getElementById('brand-test-dialog')
      expect(brandAtChildSetup).toBe('wsapi')
      expect(dialog?.parentElement).toBe(document.body)
      expect(dialog?.closest('[data-admin-brand="wsapi"]')).toBe(document.documentElement)
    } finally {
      wrapper.unmount()
    }
    expect(document.getElementById('brand-test-dialog')).toBeNull()
    expect(document.documentElement.dataset.adminBrand).toBeUndefined()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})

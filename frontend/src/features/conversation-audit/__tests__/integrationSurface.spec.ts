import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

const here = dirname(fileURLToPath(import.meta.url))
const read = (path: string) => readFileSync(resolve(here, path), 'utf8')

function shape(value: unknown): unknown {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return typeof value
  return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, shape(child)]))
}

describe('Conversation Audit integration surface', () => {
  it('registers a dedicated admin route without coupling it to risk control', () => {
    const router = read('../../../router/index.ts')
    const start = router.indexOf("path: '/admin/conversation-audit'")
    const route = router.slice(start, router.indexOf("path: '/admin/usage'", start))
    expect(route).toContain('requiresAuth: true')
    expect(route).toContain('requiresAdmin: true')
    expect(route).not.toContain('requiresRiskControl')
  })

  it('places the page beside operational audit logs in the admin sidebar', () => {
    const sidebar = read('../../../components/layout/AppSidebar.vue')
    const conversation = sidebar.indexOf("path: '/admin/conversation-audit'")
    const operations = sidebar.indexOf("path: '/admin/audit-logs'")
    expect(conversation).toBeGreaterThan(0)
    expect(operations).toBeGreaterThan(conversation)
  })

  it('keeps locale trees symmetric and preserves two-step filtered deletion', () => {
    expect(shape(zh.admin.conversationAudit)).toEqual(shape(en.admin.conversationAudit))
    expect(zh.nav.conversationAudit).toBeTruthy()
    expect(en.nav.conversationAudit).toBeTruthy()
    const api = read('../api.ts')
    expect(api).toContain('/delete-preview')
    expect(api).toContain('confirmation_token: confirmationToken')
    expect(api).not.toContain('keyword')
  })
})

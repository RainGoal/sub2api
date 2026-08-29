import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defaultConversationAuditFilters } from '../viewModel'
import type { ConversationAuditRecord } from '../types'

const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import conversationAuditAPI from '../api'

describe('Conversation Audit API', () => {
  beforeEach(() => Object.values(client).forEach((mock) => mock.mockReset()))

  it('uses the isolated admin namespace', async () => {
    client.get.mockResolvedValue({ data: { config_version: 1 } })
    await conversationAuditAPI.getConfig()
    expect(client.get).toHaveBeenCalledWith('/admin/conversation-audit/config')

    client.get.mockResolvedValue({ data: { lifecycle: 'running' } })
    await conversationAuditAPI.getRuntime()
    expect(client.get).toHaveBeenCalledWith('/admin/conversation-audit/runtime')
  })

  it('lists metadata with cursor filters and never sends a content-search field', async () => {
    client.get.mockResolvedValue({ data: { items: [] } })
    const filters = defaultConversationAuditFilters(new Date('2026-08-29T12:00:00Z'))
    filters.session_id = 'session-1'
    await conversationAuditAPI.listRecords(filters, 'opaque-cursor', 50)

    const request = client.get.mock.calls[0]
    expect(request[0]).toBe('/admin/conversation-audit/records')
    expect(request[1].params).toMatchObject({ session_id: 'session-1', cursor: 'opaque-cursor', limit: 50 })
    expect(request[1].params).not.toHaveProperty('content')
    expect(request[1].params).not.toHaveProperty('keyword')
  })

  it('uses the UTC partition date and opaque confirmation token contracts', async () => {
    const record = {
      audit_id: '00000000-0000-4000-8000-000000000001',
      created_at: '2026-08-29T23:30:00-07:00',
    } as ConversationAuditRecord
    client.get.mockResolvedValue({ data: { metadata: record } })
    await conversationAuditAPI.getRecord(record)
    expect(client.get).toHaveBeenCalledWith('/admin/conversation-audit/records/2026-08-30/00000000-0000-4000-8000-000000000001')

    client.post.mockResolvedValue({ data: { deleted_records: 2 } })
    await conversationAuditAPI.deleteByFilter('opaque-token')
    expect(client.post).toHaveBeenCalledWith('/admin/conversation-audit/delete-by-filter', {
      confirmation_token: 'opaque-token',
      confirm: true,
    })
  })
})

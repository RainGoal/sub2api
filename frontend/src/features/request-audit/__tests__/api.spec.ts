import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import api from '../api'

describe('Request audit API', () => {
  beforeEach(() => Object.values(client).forEach(mock => mock.mockReset()))

  it('sends only editable config and its concurrency version', async () => {
    client.put.mockResolvedValue({ data: { version: 5 } })
    await api.updateConfig({ enabled: false, retention_days: 3, session_ban_enabled: true, device_ban_enabled: false, version: 4, updated_by: 999 })
    expect(client.put).toHaveBeenCalledWith('/admin/request-audits/config', {
      enabled: false, retention_days: 3, session_ban_enabled: true, device_ban_enabled: false, expected_version: 4,
    })
  })

  it('requires explicit sensitive reveal and partial reading flags', async () => {
    client.get.mockResolvedValue({ data: { content: '{}' } })
    await api.payload('audit-id', 'headers')
    expect(client.get).toHaveBeenCalledWith('/admin/request-audits/audit-id/payload', { params: {
      part: 'headers', mode: 'preview', offset: 0, limit: 65536, reveal_sensitive: false, allow_partial: false,
    } })
  })

  it('rejects truncated download bytes even when the HTTP client resolves', async () => {
    client.get.mockResolvedValue({ data: new TextEncoder().encode('short').buffer, headers: { 'content-length': '10' } })
    await expect(api.download('id', 'body')).rejects.toMatchObject({ reason: 'AUDIT_DOWNLOAD_INCOMPLETE' })
    client.get.mockResolvedValue({ data: new TextEncoder().encode('whole').buffer, headers: { 'content-length': '5' } })
    expect((await api.download('id', 'body')).size).toBe(5)
  })

  it('decodes binary error responses before shared API error handling', async () => {
    client.get.mockResolvedValue({ data: new ArrayBuffer(0), headers: { 'content-length': '0' } })
    await api.download('id', 'body')
    const transform = client.get.mock.calls[0][1].transformResponse[0]
    const data = new TextEncoder().encode(JSON.stringify({ code: 409, reason: 'AUDIT_PAYLOAD_INCOMPLETE' })).buffer
    expect(transform(data, {}, 409)).toEqual({ code: 409, reason: 'AUDIT_PAYLOAD_INCOMPLETE' })
    expect(transform(data, {}, 200)).toBe(data)
  })

  it('rejects a download without a declared byte length', async () => {
    client.get.mockResolvedValue({ data: new ArrayBuffer(0), headers: {} })
    await expect(api.download('id', 'headers')).rejects.toMatchObject({ reason: 'AUDIT_DOWNLOAD_INCOMPLETE' })
  })

  it('supports confirming an uncertain ban via primary-backed read routes', async () => {
    client.get.mockResolvedValue({ data: { id: 'ban-id' } })
    await api.getBan('ban-id')
    expect(client.get).toHaveBeenCalledWith('/admin/session-bans/ban-id')
    await api.listBans({ source_audit_id: 'audit-id' })
    expect(client.get).toHaveBeenCalledWith('/admin/session-bans', { params: { source_audit_id: 'audit-id' } })
  })

  it('supports device ban creation, exact identity filtering, result checks and revocation', async () => {
    const input = { source_audit_id: 'audit-id', public_reason: 'Reason', internal_note: 'Note', expires_at: null }
    client.post.mockResolvedValue({ data: { id: 'device/ban' } })
    client.get.mockResolvedValue({ data: { id: 'device/ban' } })
    await api.createDeviceBan(input)
    expect(client.post).toHaveBeenCalledWith('/admin/device-bans', input)
    await api.listDeviceBans({ device_type: 'codex', device_id: ' raw-id ', source_audit_id: 'audit-id', state: 'active' })
    expect(client.get).toHaveBeenCalledWith('/admin/device-bans', { params: { device_type: 'codex', device_id: ' raw-id ', source_audit_id: 'audit-id', state: 'active' } })
    await api.getDeviceBan('device/ban')
    expect(client.get).toHaveBeenCalledWith('/admin/device-bans/device%2Fban')
    await api.revokeDeviceBan('device/ban')
    expect(client.post).toHaveBeenCalledWith('/admin/device-bans/device%2Fban/revoke', {})
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import api from '../api'

function readBlob(blob: Blob, format: 'text' | 'arraybuffer') {
  return new Promise<string | ArrayBuffer>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result!)
    reader.onerror = () => reject(reader.error)
    if (format === 'text') reader.readAsText(blob)
    else reader.readAsArrayBuffer(blob)
  })
}

describe('Request audit API', () => {
  beforeEach(() => Object.values(client).forEach(mock => mock.mockReset()))

  it('sends only editable config and its concurrency version', async () => {
    client.put.mockResolvedValue({ data: { version: 5 } })
    await api.updateConfig({ enabled: false, retention_days: 3, session_ban_enabled: true, device_ban_enabled: false, version: 4, updated_by: 999 })
    expect(client.put).toHaveBeenCalledWith('/admin/request-audits/config', {
      enabled: false, retention_days: 3, session_ban_enabled: true, device_ban_enabled: false, expected_version: 4,
    })
  })

  it('sends exact minutes when given a minute value', async () => {
    client.put.mockResolvedValue({ data: { version: 5 } })
    await api.updateConfig({ enabled: true, retention_days: 1, retention_minutes: 15, session_ban_enabled: false, device_ban_enabled: false, version: 4 })
    expect(client.put).toHaveBeenCalledWith('/admin/request-audits/config', {
      enabled: true, retention_minutes: 15, session_ban_enabled: false, device_ban_enabled: false, expected_version: 4,
    })
  })

  it('allows reading the original prefix of a selected upstream attempt part', async () => {
    client.get.mockResolvedValue({ data: { content: 'prefix' } })
    await api.attemptPayload('audit/id', 2, 'response_body', 65536)
    expect(client.get).toHaveBeenCalledWith('/admin/request-audits/audit%2Fid/attempts/2/payload', {
      params: { part: 'response_body', mode: 'preview', offset: 65536, limit: 65536, allow_partial: true },
    })
  })

  it('uses versioned group policy endpoints and includes every capture switch', async () => {
    const policy = {
      group_only: true,
      version: 6,
      rules: [{
        group_id: 9,
        inbound_headers: true,
        inbound_body: false,
        upstream_request_headers: false,
        upstream_request_body: true,
        upstream_response_headers: false,
        upstream_response_body: false,
      }],
    }
    client.get.mockResolvedValue({ data: policy })
    expect(await api.getGroupPolicies()).toEqual(policy)
    expect(client.get).toHaveBeenCalledWith('/admin/request-audits/group-policies')
    client.put.mockResolvedValue({ data: { ...policy, version: 7 } })
    await api.updateGroupPolicies(policy)
    expect(client.put).toHaveBeenCalledWith('/admin/request-audits/group-policies', {
      group_only: true, rules: policy.rules, expected_version: 6,
    })
  })

  it('lists upstream attempts with a raw part payload route', async () => {
    client.get.mockResolvedValue({ data: { items: [{ attempt_no: 1 }] } })
    await api.listAttempts('audit/id')
    expect(client.get).toHaveBeenCalledWith('/admin/request-audits/audit%2Fid/attempts')
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

  it('indents upstream JSON downloads without changing numeric tokens, keys or string escapes', async () => {
    const raw = String.raw`{"id":9007199254740993,"id":1e+20,"text":" \u0061,{}[]\" ","items":[[],{},true],"__proto__":null}`
    const data = new TextEncoder().encode(raw).buffer
    client.get.mockResolvedValue({ data, headers: { 'content-length': String(data.byteLength) } })
    const file = await api.downloadAttempt('audit/id', 2, 'response_body')
    expect(client.get).toHaveBeenCalledWith('/admin/request-audits/audit%2Fid/attempts/2/payload', expect.objectContaining({
      params: { part: 'response_body', mode: 'raw', allow_partial: true },
    }))
    expect(file).toMatchObject({ extension: 'json', partial: false })
    expect(await readBlob(file.blob, 'text')).toBe([
      '{', '  "id": 9007199254740993,', '  "id": 1e+20,', String.raw`  "text": " \u0061,{}[]\" ",`,
      '  "items": [', '    [],', '    {},', '    true', '  ],', '  "__proto__": null', '}', '',
    ].join('\n'))
  })

  it.each([
    { data: new TextEncoder().encode('data: {"text":"你好"}\n\n'), partial: false, extension: 'txt' },
    { data: new TextEncoder().encode('{"id":1}'), partial: true, extension: 'txt' },
    { data: Uint8Array.of(0xff, 0x01), partial: false, extension: 'bin' },
    { data: Uint8Array.of(0, 0), partial: false, extension: 'bin' },
  ])('preserves the bytes of upstream $extension downloads (partial=$partial)', async ({ data, partial, extension }) => {
    client.get.mockResolvedValue({ data: data.buffer, headers: { 'content-length': String(data.byteLength), 'x-audit-partial': partial ? 'true' : undefined } })
    const file = await api.downloadAttempt('id', 1, 'response_body')
    expect(file).toMatchObject({ partial, extension })
    const downloaded = await readBlob(file.blob, 'arraybuffer') as ArrayBuffer
    expect(Array.from(new Uint8Array(downloaded))).toEqual(Array.from(data))
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

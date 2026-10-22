import { describe, expect, it, vi } from 'vitest'
import { discoverVideoModels, getVideoModelTemplates, readVideoModelDiscovery } from '@/api/admin/videoTasks'

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get: getMock, post: postMock } }))

const snapshot = {
  models: ['Seedance-2.0'], synced_at: '2026-10-20T01:00:00.000Z',
  base_url: 'https://supplier.example/v1', proxy_id: null, protocol: 'video_tasks_json_v1',
}

describe('video model discovery snapshot', () => {
  it('keeps snapshots isolated by protocol even when the upstream URL is identical', () => {
    const zhiqiSnapshot = { ...snapshot, protocol: 'zhiqi_v1' }
    expect(readVideoModelDiscovery(zhiqiSnapshot)).toBeNull()
    expect(readVideoModelDiscovery(snapshot, 'zhiqi_v1')).toBeNull()
    expect(readVideoModelDiscovery(zhiqiSnapshot, 'zhiqi_v1')).toEqual(zhiqiSnapshot)
  })

  it('requests the selected catalog and sends its protocol during discovery', async () => {
    getMock.mockResolvedValue({ data: { protocol: 'zhiqi_v1', models: [] } })
    postMock.mockResolvedValue({ data: { models: ['Zhiqi/Video'] } })
    await getVideoModelTemplates('zhiqi_v1')
    expect(getMock).toHaveBeenLastCalledWith('/admin/accounts/video-model-templates', { params: { protocol: 'zhiqi_v1' } })
    await getVideoModelTemplates()
    expect(getMock).toHaveBeenLastCalledWith('/admin/accounts/video-model-templates')
    await expect(discoverVideoModels({ baseUrl: 'https://zhiqiapi.com/v1', apiKey: 'test-key', protocol: 'zhiqi_v1' })).resolves.toEqual(['Zhiqi/Video'])
    expect(postMock).toHaveBeenLastCalledWith('/admin/accounts/models/sync-upstream-preview', expect.objectContaining({ video_provider: 'zhiqi_v1' }))
  })

  it('trims and deduplicates exact IDs without folding model names or preserving credentials', () => {
    expect(readVideoModelDiscovery({
      ...snapshot, models: [' Seedance-2.0 ', 'seedance-2.0', 'Seedance-2.0'],
      base_url: ' https://supplier.example/v1 ', api_key: 'must-not-be-saved',
    })).toEqual({ ...snapshot, models: ['Seedance-2.0', 'seedance-2.0'] })
  })

  it('preserves a successful empty list so removed upstream models remain distinguishable from a failed sync', () => {
    expect(readVideoModelDiscovery({ ...snapshot, models: [], proxy_id: 7 }))
      .toEqual({ ...snapshot, models: [], proxy_id: 7 })
  })

  it.each([
    null, [], 'invalid', {},
    { ...snapshot, models: [''] }, { ...snapshot, models: ['   '] },
    { ...snapshot, models: ['model\nname'] }, { ...snapshot, models: [42] },
    { ...snapshot, models: 'model' }, { ...snapshot, models: ['valid', {}] },
    { ...snapshot, protocol: 'bblabu_v1' }, { ...snapshot, synced_at: 'not-a-date' },
    { ...snapshot, synced_at: '2026-13-20T01:00:00Z' }, { ...snapshot, synced_at: 123 },
    { ...snapshot, proxy_id: 0 }, { ...snapshot, proxy_id: -1 },
    { ...snapshot, proxy_id: 1.5 }, { ...snapshot, proxy_id: '7' },
    { ...snapshot, proxy_id: undefined }, { ...snapshot, base_url: 'invalid' },
    { ...snapshot, base_url: 'file:///tmp/models' },
    { ...snapshot, base_url: 'https://user:secret@supplier.example/v1' },
    { ...snapshot, base_url: 'https://supplier.example/v1?key=secret' },
    { ...snapshot, base_url: 'https://supplier.example/v1#secret' },
  ])('rejects malformed or credential-bearing snapshot %#', value => {
    expect(readVideoModelDiscovery(value)).toBeNull()
  })
})

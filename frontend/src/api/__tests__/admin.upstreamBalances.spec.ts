import { beforeEach, describe, expect, it, vi } from 'vitest'
import { upstreamBalancesAPI } from '../admin/upstreamBalances'

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put, post } }))

describe('upstream balance API boundaries', () => {
  beforeEach(() => vi.clearAllMocks())

  it('reads snapshots and discovery from their dedicated admin endpoints', async () => {
    get.mockResolvedValueOnce({ data: { config: {}, items: [] } }).mockResolvedValueOnce({ data: { sites: [] } })
    await upstreamBalancesAPI.list()
    await upstreamBalancesAPI.discover()
    expect(get).toHaveBeenNthCalledWith(1, '/admin/upstream-balances')
    expect(get).toHaveBeenNthCalledWith(2, '/admin/upstream-balances/discovery')
    expect(post).not.toHaveBeenCalled()
    expect(put).not.toHaveBeenCalled()
  })

  it('saves only isolated configuration and explicitly refreshes a wallet', async () => {
    const config = { version: 8, enabled: false, interval_minutes: 30, wallets: [] }
    put.mockResolvedValue({ data: { ...config, version: 9 } })
    post.mockResolvedValue({ data: { wallet_id: 'a/b', balance: null } })
    await expect(upstreamBalancesAPI.saveConfig(config)).resolves.toMatchObject({ version: 9 })
    await upstreamBalancesAPI.refresh('a/b')
    expect(put).toHaveBeenCalledWith('/admin/upstream-balances/config', config)
    expect(post).toHaveBeenCalledWith('/admin/upstream-balances/a%2Fb/refresh', undefined, { timeout: 60000 })
  })
})

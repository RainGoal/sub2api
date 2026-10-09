import { apiClient } from '../client'

export interface UpstreamWallet {
  id: string
  name: string
  site_url: string
  single_account: boolean
  account_ids: number[]
  query_account_id: number | null
  threshold: number
  recharge_url: string
  enabled: boolean
}

export interface UpstreamBalanceConfig {
  version: number
  enabled: boolean
  interval_seconds?: number
  interval_minutes?: number
  wallets: UpstreamWallet[]
}

export interface UpstreamBalanceSnapshot {
  wallet_id: string
  status: 'ok' | 'failed' | 'unsupported' | 'unconfigured'
  kind: 'wallet' | 'key_quota' | 'subscription' | 'unknown'
  balance: number | null
  currency: string
  queried_account_id?: number | null
  last_success_at?: string | null
  last_attempt_at?: string | null
  next_refresh_at?: string | null
  fresh_until?: string | null
  error_code?: string
  low_balance: boolean
  account_count: number
}

export interface UpstreamBalanceSite {
  site_url: string
  accounts: { id: number; name: string }[]
}

export interface UpstreamBalanceOverview {
  config: UpstreamBalanceConfig
  items: UpstreamBalanceSnapshot[]
}

export const upstreamBalancesAPI = {
  async list(): Promise<UpstreamBalanceOverview> {
    const { data } = await apiClient.get<UpstreamBalanceOverview>('/admin/upstream-balances')
    return data
  },
  async discover(): Promise<{ sites: UpstreamBalanceSite[] }> {
    const { data } = await apiClient.get<{ sites: UpstreamBalanceSite[] }>('/admin/upstream-balances/discovery')
    return data
  },
  async saveConfig(config: UpstreamBalanceConfig): Promise<UpstreamBalanceConfig> {
    const { data } = await apiClient.put<UpstreamBalanceConfig>('/admin/upstream-balances/config', config)
    return data
  },
  async refresh(id: string): Promise<UpstreamBalanceSnapshot> {
    const { data } = await apiClient.post<UpstreamBalanceSnapshot>(`/admin/upstream-balances/${encodeURIComponent(id)}/refresh`, undefined, { timeout: 60000 })
    return data
  },
}

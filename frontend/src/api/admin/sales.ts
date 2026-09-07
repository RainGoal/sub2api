import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface SalesSettings { enabled: boolean; main_frontend_url: string }
export interface SalesPartnerInput {
  user_id: number; name: string; code: string; hostname: string; commission_rate: number
  promotion_enabled: boolean; accrual_enabled: boolean; payout_frozen: boolean
}
export interface SalesPartner extends SalesPartnerInput { id: number; created_at: string; updated_at: string }
export interface SalesOverview {
  partner: SalesPartner; customer_count: number; revenue: number; cost: number; profit: number
  commission: number; unsettled_commission: number; pending_payout: number; paid_commission: number; pending_events: number
}
export interface SalesCustomer {
  user_id: number; partner_id: number; email: string; created_at: string; revenue: number; profit: number; commission: number
}
export interface SalesLedger {
  id: number; partner_id: number; customer_user_id?: number; event_id?: number; kind: 'usage' | 'adjustment' | 'carry'
  revenue: number; cost: number; profit: number; commission_rate: number; commission: number; currency: string
  note: string; occurred_at: string; settlement_id?: number
}
export interface SalesSettlement {
  id: number; partner_id: number; month: string; cutoff: string; net_commission: number; payout_amount: number
  carry_amount: number; status: 'draft' | 'confirmed' | 'paid'; currency: string; payment_reference: string
  created_at: string; confirmed_at?: string; paid_at?: string
}
export interface SalesQuery { page?: number; page_size?: number; partner_id?: number; search?: string; start_date?: string; end_date?: string }

export const salesAPI = {
  async settings() { return (await apiClient.get<SalesSettings>('/admin/sales/settings')).data },
  async saveSettings(input: SalesSettings) { return (await apiClient.put<SalesSettings>('/admin/sales/settings', input)).data },
  async partners(params: SalesQuery) { return (await apiClient.get<PaginatedResponse<SalesPartner>>('/admin/sales/partners', { params })).data },
  async savePartner(input: SalesPartnerInput, id?: number) {
    return id ? (await apiClient.put<SalesPartner>(`/admin/sales/partners/${id}`, input)).data
      : (await apiClient.post<SalesPartner>('/admin/sales/partners', input)).data
  },
  async overview(id: number, params: SalesQuery) { return (await apiClient.get<SalesOverview>(`/admin/sales/partners/${id}/overview`, { params })).data },
  async customers(params: SalesQuery) { return (await apiClient.get<PaginatedResponse<SalesCustomer>>('/admin/sales/customers', { params })).data },
  async ledger(params: SalesQuery) { return (await apiClient.get<PaginatedResponse<SalesLedger>>('/admin/sales/ledger', { params })).data },
  async settlements(params: SalesQuery) { return (await apiClient.get<PaginatedResponse<SalesSettlement>>('/admin/sales/settlements', { params })).data },
  async createSettlement(input: { partner_id: number; month: string; request_key: string }) {
    return (await apiClient.post<SalesSettlement>('/admin/sales/settlements', input)).data
  },
  async confirmSettlement(id: number, request_key: string) { return (await apiClient.post<SalesSettlement>(`/admin/sales/settlements/${id}/confirm`, { request_key })).data },
  async paySettlement(id: number, input: { request_key: string; payment_reference: string }) {
    return (await apiClient.post<SalesSettlement>(`/admin/sales/settlements/${id}/pay`, input)).data
  },
  async adjust(input: { partner_id: number; source_ledger_id?: number; commission: number; note: string; request_key: string }) {
    return (await apiClient.post<SalesLedger>('/admin/sales/adjustments', input)).data
  }
}

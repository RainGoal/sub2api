import { apiClient } from '@/api/client'
import type {
  CreateRequestAuditBan,
  RequestAuditBan,
  RequestAuditBanFilters,
  RequestAuditBanPage,
  RequestAuditConfig,
  RequestAuditFilters,
  RequestAuditPage,
  RequestAuditPayload,
  RequestAuditRecord,
  RequestAuditStatus,
} from './types'

const audits = '/admin/request-audits'
const bans = '/admin/session-bans'
const segment = (id: string) => encodeURIComponent(id)

export const requestAuditAPI = {
  async list(filters: RequestAuditFilters = {}): Promise<RequestAuditPage> {
    const { data } = await apiClient.get<RequestAuditPage>(audits, { params: filters })
    return data
  },
  async get(id: string): Promise<RequestAuditRecord> {
    const { data } = await apiClient.get<RequestAuditRecord>(`${audits}/${segment(id)}`)
    return data
  },
  async payload(id: string, part: 'headers' | 'body' | 'trailers', offset = 0, limit = 65536, revealSensitive = false, allowPartial = false): Promise<RequestAuditPayload> {
    const { data } = await apiClient.get<RequestAuditPayload>(`${audits}/${segment(id)}/payload`, {
      params: { part, mode: 'preview', offset, limit, reveal_sensitive: revealSensitive, allow_partial: allowPartial },
    })
    return data
  },
  async download(id: string, part: 'headers' | 'body' | 'trailers', allowPartial = false): Promise<Blob> {
    const { data, headers } = await apiClient.get<ArrayBuffer>(`${audits}/${segment(id)}/payload`, {
      params: { part, mode: 'raw', allow_partial: allowPartial },
      responseType: 'arraybuffer',
      // Decode error envelopes before the shared authentication/error interceptor runs.
      transformResponse: [(data: ArrayBuffer, _headers, status) => {
        if (status && status >= 400) {
          try { return JSON.parse(new TextDecoder().decode(data)) } catch { return data }
        }
        return data
      }],
      timeout: 120000,
    })
    const declared = headers['content-length']
    const expected = Number(declared)
    if (declared == null || declared === '' || !Number.isSafeInteger(expected) || expected < 0 || data.byteLength !== expected) {
      throw { reason: 'AUDIT_DOWNLOAD_INCOMPLETE', message: 'Payload download did not match its declared length' }
    }
    return new Blob([data], { type: 'application/octet-stream' })
  },
  async getConfig(): Promise<RequestAuditConfig> {
    const { data } = await apiClient.get<RequestAuditConfig>(`${audits}/config`)
    return data
  },
  async updateConfig(config: RequestAuditConfig): Promise<RequestAuditConfig> {
    const { data } = await apiClient.put<RequestAuditConfig>(`${audits}/config`, {
      enabled: config.enabled,
      retention_days: config.retention_days,
      session_ban_enabled: config.session_ban_enabled,
      expected_version: config.version,
    })
    return data
  },
  async status(): Promise<RequestAuditStatus> {
    const { data } = await apiClient.get<RequestAuditStatus>(`${audits}/status`)
    return data
  },
  async listBans(filters: RequestAuditBanFilters = {}): Promise<RequestAuditBanPage> {
    const { data } = await apiClient.get<RequestAuditBanPage>(bans, { params: filters })
    return data
  },
  async getBan(id: string): Promise<RequestAuditBan> {
    const { data } = await apiClient.get<RequestAuditBan>(`${bans}/${segment(id)}`)
    return data
  },
  async createBan(input: CreateRequestAuditBan): Promise<RequestAuditBan> {
    const { data } = await apiClient.post<RequestAuditBan>(bans, input)
    return data
  },
  async revokeBan(id: string): Promise<RequestAuditBan> {
    const { data } = await apiClient.post<RequestAuditBan>(`${bans}/${segment(id)}/revoke`, {})
    return data
  },
}

export default requestAuditAPI

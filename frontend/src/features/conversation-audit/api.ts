import { apiClient } from '@/api/client'
import type {
  ConversationAuditConfig,
  ConversationAuditDeletePreview,
  ConversationAuditDeleteResult,
  ConversationAuditFilters,
  ConversationAuditRecord,
  ConversationAuditRecordDetail,
  ConversationAuditRecordPage,
  ConversationAuditRuntime,
  ConversationAuditUpdateRequest,
} from './types'
import { conversationAuditFilterPayload, conversationAuditQueryParams } from './viewModel'

const basePath = '/admin/conversation-audit'

export async function getConfig(): Promise<ConversationAuditConfig> {
  const { data } = await apiClient.get<ConversationAuditConfig>(`${basePath}/config`)
  return data
}

export async function updateConfig(payload: ConversationAuditUpdateRequest): Promise<ConversationAuditConfig> {
  const { data } = await apiClient.put<ConversationAuditConfig>(`${basePath}/config`, payload)
  return data
}

export async function getRuntime(): Promise<ConversationAuditRuntime> {
  const { data } = await apiClient.get<ConversationAuditRuntime>(`${basePath}/runtime`)
  return data
}

export async function listRecords(
  filters: ConversationAuditFilters,
  cursor: string,
  limit: number,
): Promise<ConversationAuditRecordPage> {
  const { data } = await apiClient.get<ConversationAuditRecordPage>(`${basePath}/records`, {
    params: conversationAuditQueryParams(filters, cursor, limit),
  })
  return data
}

function recordPath(record: ConversationAuditRecord): string {
  const day = new Date(record.created_at).toISOString().slice(0, 10)
  return `${basePath}/records/${day}/${encodeURIComponent(record.audit_id)}`
}

export async function getRecord(record: ConversationAuditRecord): Promise<ConversationAuditRecordDetail> {
  const { data } = await apiClient.get<ConversationAuditRecordDetail>(recordPath(record))
  return data
}

export async function deleteRecord(record: ConversationAuditRecord): Promise<void> {
  await apiClient.delete(recordPath(record))
}

export async function previewDelete(
  filters: ConversationAuditFilters,
): Promise<ConversationAuditDeletePreview> {
  const { data } = await apiClient.post<ConversationAuditDeletePreview>(
    `${basePath}/delete-preview`,
    conversationAuditFilterPayload(filters),
  )
  return data
}

export async function deleteByFilter(confirmationToken: string): Promise<ConversationAuditDeleteResult> {
  const { data } = await apiClient.post<ConversationAuditDeleteResult>(`${basePath}/delete-by-filter`, {
    confirmation_token: confirmationToken,
    confirm: true,
  })
  return data
}

export const conversationAuditAPI = {
  getConfig,
  updateConfig,
  getRuntime,
  listRecords,
  getRecord,
  deleteRecord,
  previewDelete,
  deleteByFilter,
}

export default conversationAuditAPI

import type {
  ConversationAuditConfig,
  ConversationAuditFilters,
  ConversationAuditUpdateRequest,
} from './types'

export const MAX_LIST_WINDOW_MS = 31 * 24 * 60 * 60 * 1000
export const MAX_UNINDEXED_WINDOW_MS = 24 * 60 * 60 * 1000

export function toDatetimeLocal(value: Date): string {
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}T${pad(value.getHours())}:${pad(value.getMinutes())}`
}

export function defaultConversationAuditFilters(now = new Date()): ConversationAuditFilters {
  return {
    start: toDatetimeLocal(new Date(now.getTime() - MAX_UNINDEXED_WINDOW_MS)),
    end: toDatetimeLocal(now),
    user_id: '',
    group_id: '',
    api_key_id: '',
    session_id: '',
    request_id: '',
    outcome_status: '',
    capture_status: '',
    protocol: '',
    inbound_endpoint: '',
    requested_model: '',
  }
}

export function cloneConversationAuditFilters(filters: ConversationAuditFilters): ConversationAuditFilters {
  return { ...filters }
}

function positiveID(value: string): number | undefined {
  const normalized = value.trim()
  if (!normalized) return undefined
  const parsed = Number(normalized)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined
}

function normalizedTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toISOString()
}

export function filterValidationCode(filters: ConversationAuditFilters): string {
  const start = new Date(filters.start)
  const end = new Date(filters.end)
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || end <= start) {
    return 'invalidTimeRange'
  }
  const window = end.getTime() - start.getTime()
  if (window > MAX_LIST_WINDOW_MS) return 'windowTooLarge'
  if (
    window > MAX_UNINDEXED_WINDOW_MS &&
    (filters.protocol.trim() || filters.inbound_endpoint.trim() || filters.requested_model.trim())
  ) {
    return 'unindexedWindowTooLarge'
  }
  for (const value of [filters.user_id, filters.group_id, filters.api_key_id]) {
    if (value.trim() && positiveID(value) === undefined) return 'invalidID'
  }
  return ''
}

export function conversationAuditFilterPayload(filters: ConversationAuditFilters): Record<string, unknown> {
  return {
    start: normalizedTime(filters.start),
    end: normalizedTime(filters.end),
    user_id: positiveID(filters.user_id),
    group_id: positiveID(filters.group_id),
    api_key_id: positiveID(filters.api_key_id),
    session_id: filters.session_id.trim() || undefined,
    request_id: filters.request_id.trim() || undefined,
    outcome_status: filters.outcome_status || undefined,
    capture_status: filters.capture_status || undefined,
    protocol: filters.protocol.trim() || undefined,
    inbound_endpoint: filters.inbound_endpoint.trim() || undefined,
    requested_model: filters.requested_model.trim() || undefined,
  }
}

export function conversationAuditQueryParams(
  filters: ConversationAuditFilters,
  cursor: string,
  limit: number,
): Record<string, unknown> {
  return {
    ...conversationAuditFilterPayload(filters),
    cursor: cursor || undefined,
    limit,
  }
}

export function buildConversationAuditUpdate(config: ConversationAuditConfig): ConversationAuditUpdateRequest {
  return {
    expected_config_version: config.config_version,
    enabled: config.enabled,
    retention_days: config.retention_days,
    request_max_bytes: config.request_max_bytes,
    response_max_bytes: config.response_max_bytes,
    memory_budget_bytes: config.memory_budget_bytes,
    worker_count: config.worker_count,
    queue_capacity: config.queue_capacity,
  }
}

export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit++
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

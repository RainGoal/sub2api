export type ConversationAuditOutcome =
  | 'completed'
  | 'error'
  | 'timeout'
  | 'partial'
  | 'cancelled'
  | 'unknown'

export type ConversationAuditCaptureStatus =
  | 'complete'
  | 'truncated'
  | 'metadata_only'
  | 'degraded'

export interface ConversationAuditConfig {
  enabled: boolean
  effective_enabled: boolean
  lifecycle: string
  encryption_ready: boolean
  retention_days: number
  request_max_bytes: number
  response_max_bytes: number
  memory_budget_bytes: number
  worker_count: number
  queue_capacity: number
  config_version: number
  updated_at: string
  updated_by: number
}

export interface ConversationAuditUpdateRequest {
  expected_config_version: number
  enabled: boolean
  retention_days: number
  request_max_bytes: number
  response_max_bytes: number
  memory_budget_bytes: number
  worker_count: number
  queue_capacity: number
}

export interface ConversationAuditRuntime {
  enabled: boolean
  lifecycle: string
  config_version: number
  active_captures: number
  buffered_bytes: number
  memory_budget_bytes: number
  payload_queue_depth: number
  payload_queue_capacity: number
  metadata_queue_depth: number
  metadata_queue_capacity: number
  workers_active: number
  queue_full: number
  budget_full: number
  encode_failed: number
  write_failed: number
  last_error: string
  last_error_at?: string
}

export interface ConversationAuditRecord {
  audit_id: string
  created_at: string
  completed_at?: string
  updated_at: string
  mutable_until?: string
  lease_expires_at?: string
  request_id: string
  session_id?: string
  user_id: number
  user_name: string
  api_key_id: number
  api_key_name: string
  group_id?: number
  group_name: string
  account_id?: number
  account_name: string
  protocol: string
  inbound_endpoint: string
  requested_model: string
  effective_model: string
  transport_mode: string
  http_status?: number
  error_code: string
  record_state: string
  outcome_status?: ConversationAuditOutcome
  capture_status: ConversationAuditCaptureStatus
  degraded_reason: string
  request_original_bytes: number
  request_stored_bytes: number
  request_compressed_bytes: number
  request_encrypted_bytes: number
  request_truncated: boolean
  request_omitted_messages: number
  request_omitted_bytes: number
  response_original_bytes: number
  response_stored_bytes: number
  response_compressed_bytes: number
  response_encrypted_bytes: number
  response_truncated: boolean
  response_omitted_messages: number
  response_omitted_bytes: number
}

export interface ConversationAuditContentItem {
  type: string
  text?: string
  name?: string
  arguments?: string
  content?: string
  media_type?: string
  encoded_bytes?: number
  resource_type?: string
  id?: string
  url?: string
  omitted_bytes?: number
}

export interface ConversationAuditMessage {
  role: string
  content: ConversationAuditContentItem[]
}

export interface CanonicalConversation {
  version: number
  messages: ConversationAuditMessage[]
  error?: { code: string; message: string }
  omitted_messages?: number
  omitted_bytes?: number
  truncated: boolean
}

export interface ConversationAuditPayloadDetail {
  available: boolean
  error_code?: string
  payload?: CanonicalConversation
}

export interface ConversationAuditRecordDetail {
  metadata: ConversationAuditRecord
  request: ConversationAuditPayloadDetail
  response: ConversationAuditPayloadDetail
}

export interface ConversationAuditRecordPage {
  items: ConversationAuditRecord[]
  next_cursor?: string
}

export interface ConversationAuditFilters {
  start: string
  end: string
  user_id: string
  group_id: string
  api_key_id: string
  session_id: string
  request_id: string
  outcome_status: string
  capture_status: string
  protocol: string
  inbound_endpoint: string
  requested_model: string
}

export interface ConversationAuditDeletePreview {
  matched_count: number
  has_more: boolean
  operation_type: string
  eligibility_cutoff: string
  expires_at: string
  filter_hash: string
  confirmation_token?: string
}

export interface ConversationAuditDeleteResult {
  deleted_records: number
}

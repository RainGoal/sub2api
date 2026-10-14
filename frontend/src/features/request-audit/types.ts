export const REQUEST_AUDIT_KINDS = ['http_request', 'websocket_handshake', 'websocket_message'] as const

export interface RequestAuditRecord {
  id: string
  received_at: string
  kind: typeof REQUEST_AUDIT_KINDS[number]
  api_key_id?: number | null
  user_id?: number | null
  user_username?: string
  user_email?: string
  api_key_name?: string
  group_id?: number | null
  group_name?: string
  group_platform?: string
  session_id?: string | null
  session_source?: string | null
  connection_id?: string | null
  client_request_id?: string
  instance_id?: string
  protocol?: string
  message_seq?: number
  message_type?: string
  session_resolution?: string
  upstream_summary?: string
  method?: string | null
  route_pattern?: string | null
  model?: string | null
  http_status?: number | null
  duration_ms?: number | null
  outcome?: string | null
  capture_state: string
  capture_reason?: string | null
  observed_body_bytes?: number
  stored_body_bytes?: number
  terminal_manifest?: Partial<Record<'headers' | 'body' | 'trailers', RequestAuditManifest>>
}

export interface RequestAuditManifest {
  expected_chunks: number
  expected_bytes: number
  complete: boolean
  reason?: string
}

export interface RequestAuditPage {
  items: RequestAuditRecord[]
  next_cursor: string
  has_more: boolean
}

export interface RequestAuditFilters {
  from?: string
  to?: string
  user_id?: number
  api_key_id?: number
  session_id?: string
  kind?: string
  capture_state?: string
  http_status?: number
  cursor?: string
  limit?: number
}

export interface RequestAuditConfig {
  enabled: boolean
  retention_days: number
  session_ban_enabled: boolean
  version: number
  updated_at?: string
  updated_by?: number
}

export interface RequestAuditStatus {
  instance_id: string
  running: boolean
  config_known: boolean
  config: RequestAuditConfig
  accepted_requests: number
  dropped_requests: number
  dropped_events: number
  write_failures: number
  written_events: number
  ban_check_failures: number
  queue_bytes: number
  buffer_bytes: number
  queue_events: number
  last_write_at?: string
  last_cleanup_at?: string
  cleanup_deleted: number
  cleanup_failures: number
  writer_backoff_until?: string
}

export interface RequestAuditPayload {
  content: string
  encoding: 'utf8' | 'base64'
  offset: number
  returned_bytes: number
  part_bytes: number
  has_more: boolean
  capture_state: string
  partial: boolean
  redacted: boolean
}

export interface RequestAuditBan {
  id: string
  api_key_id: number
  session_id: string
  source_audit_id: string
  public_reason: string
  internal_note?: string | null
  created_at: string
  expires_at?: string | null
  revoked_at?: string | null
  created_by?: number
  revoked_by?: number | null
  state?: string
  [key: string]: unknown
}

export interface RequestAuditBanPage {
  items: RequestAuditBan[]
  next_cursor: string
  has_more: boolean
}

export interface RequestAuditBanFilters {
  api_key_id?: number
  session_id?: string
  source_audit_id?: string
  state?: 'active' | 'expired' | 'revoked'
  cursor?: string
  limit?: number
}

export interface CreateRequestAuditBan {
  source_audit_id: string
  public_reason: string
  internal_note?: string
  expires_at?: string | null
}

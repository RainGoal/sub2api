import { apiClient } from '../client'

// The backend validates the full schema. Preserve additional contract fields
// when editing JSON so new server capability versions are not truncated.
export interface VideoModelCapability {
  model: string
  profile: 'bblabu_official' | 'bblabu_route'
  enabled: boolean
  resolutions?: string[]
  billing_unit?: 'per_second' | 'per_video'
  duration_mode?: 'controlled' | 'fixed' | 'unknown'
  min_duration?: number
  max_duration?: number
  default_duration?: number
  [key: string]: unknown
}

export interface VideoModelTemplates {
  protocol: 'video_tasks_json_v1'
  version: string
  preset: { name: string; base_url: string }
  models: VideoModelCapability[]
}

// A successful discovery is an account-scoped display snapshot, never authorization.
export interface VideoModelDiscovery {
  models: string[]
  synced_at: string
  base_url: string
  proxy_id: number | null
  protocol: 'video_tasks_json_v1'
}

export function readVideoModelDiscovery(value: unknown): VideoModelDiscovery | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const snapshot = value as Record<string, unknown>
  if (snapshot.protocol !== 'video_tasks_json_v1' || !Array.isArray(snapshot.models) ||
    typeof snapshot.base_url !== 'string' || typeof snapshot.synced_at !== 'string' ||
    (snapshot.proxy_id !== null && (typeof snapshot.proxy_id !== 'number' || !Number.isSafeInteger(snapshot.proxy_id) || snapshot.proxy_id <= 0))) return null
  const baseUrl = snapshot.base_url.trim()
  const syncedAt = snapshot.synced_at.trim()
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(syncedAt) || !Number.isFinite(Date.parse(syncedAt))) return null
  try {
    const parsed = new URL(baseUrl)
    if (!['https:', 'http:'].includes(parsed.protocol) || parsed.username || parsed.password || parsed.search || parsed.hash) return null
  } catch {
    return null
  }
  const models: string[] = []
  for (const model of snapshot.models) {
    if (typeof model !== 'string' || !model.trim() || [...model].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)) return null
    const name = model.trim()
    if (!models.includes(name)) models.push(name)
  }
  // Explicitly select fields so credentials and unrelated data cannot be persisted.
  return { models, synced_at: syncedAt, base_url: baseUrl, proxy_id: snapshot.proxy_id, protocol: snapshot.protocol }
}

export async function getVideoModelTemplates(): Promise<VideoModelTemplates> {
  const { data } = await apiClient.get<VideoModelTemplates>('/admin/accounts/video-model-templates')
  return data
}

export async function discoverVideoModels(connection: {
  baseUrl: string; apiKey: string; accountId?: number; proxyId?: number | null
}): Promise<string[]> {
  if (!connection.apiKey.trim() && !connection.accountId) throw new Error('A key or saved account is required')
  const { data } = connection.apiKey.trim()
    ? await apiClient.post<{ models: string[] }>('/admin/accounts/models/sync-upstream-preview', {
      platform: 'seedance', type: 'apikey', video_provider: 'video_tasks_json_v1',
      base_url: connection.baseUrl.trim(), api_key: connection.apiKey.trim(),
      ...(connection.proxyId ? { proxy_id: connection.proxyId } : {})
    })
    : await apiClient.post<{ models: string[] }>(`/admin/accounts/${connection.accountId}/models/sync-upstream`)
  return data.models
}

export function readVideoCapabilities(value: unknown): VideoModelCapability[] {
  return Array.isArray(value)
    ? value.filter((item): item is VideoModelCapability => !!item && typeof item === 'object' && typeof item.model === 'string')
    : []
}

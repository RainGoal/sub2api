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

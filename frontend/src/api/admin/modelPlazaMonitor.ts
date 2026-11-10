import { apiClient } from '../client'

export type PlazaMonitorStatus = 'operational' | 'degraded' | 'unavailable' | 'unknown'
export type PlazaMonitorAPIMode = 'chat_completions' | 'responses'

export interface PlazaMonitorModelConfig {
  platform: string
  name: string
  interval_seconds?: number
  api_mode?: PlazaMonitorAPIMode
}

export interface PlazaMonitorGroupConfig {
  group_id: number
  enabled: boolean
  interval_seconds: number
  probe_api_key_id: number
  models: PlazaMonitorModelConfig[]
}

export interface PlazaMonitorConfig {
  version: number
  enabled: boolean
  default_interval_seconds: number
  groups: PlazaMonitorGroupConfig[]
}

export interface PlazaMonitorCandidateModel {
  name: string
  platform: string
  supported: boolean
}

export interface PlazaMonitorCandidates {
  groups: Array<{
    id: number
    name: string
    platform: string
    models: PlazaMonitorCandidateModel[]
  }>
  api_keys: Array<{ id: number; name: string; group_id: number }>
}

export interface PlazaMonitorStatusResponse {
  enabled: boolean
  computed_at: string
  items: Array<{
    group_id: number
    platform: string
    model: string
    interval_seconds: number
    status: PlazaMonitorStatus
    last_checked_at: string | null
    next_check_at: string | null
    timeline: Array<{ status: PlazaMonitorStatus; checked_at: string; latency_ms?: number | null }>
  }>
}

const base = '/admin/model-plaza-monitor'

export const modelPlazaMonitorAPI = {
  async config(signal?: AbortSignal): Promise<PlazaMonitorConfig> {
    return (await apiClient.get<PlazaMonitorConfig>(base + '/config', { signal })).data
  },
  async save(input: PlazaMonitorConfig): Promise<PlazaMonitorConfig> {
    return (await apiClient.put<PlazaMonitorConfig>(base + '/config', input)).data
  },
  async candidates(signal?: AbortSignal): Promise<PlazaMonitorCandidates> {
    return (await apiClient.get<PlazaMonitorCandidates>(base + '/candidates', { signal })).data
  },
  async run(groupID: number): Promise<PlazaMonitorStatusResponse> {
    return (await apiClient.post<PlazaMonitorStatusResponse>(base + '/run', { group_id: groupID })).data
  },
  async status(groupID: number, signal?: AbortSignal): Promise<PlazaMonitorStatusResponse> {
    return (await apiClient.get<PlazaMonitorStatusResponse>(base + '/status', {
      params: { group_id: groupID }, signal
    })).data
  }
}

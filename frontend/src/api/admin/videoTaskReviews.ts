import { apiClient } from '../client'

export type VideoTaskReviewAction = 'retry_poll' | 'confirmed_failed' | 'confirmed_canceled'

export interface VideoTaskReview {
  state_id: string
  public_id: string
  upstream_id: string
  user_id: number
  api_key_id: number
  account_id: number
  model: string
  resolution: string
  duration: number
  upstream_status: string
  settlement_status: string
  reason: string
  requested_at: string | null
  deadline: string | null
  decisions: { actor_id: number; action: VideoTaskReviewAction; reason: string; decided_at: string }[] | null
  sales_snapshot: Record<string, unknown> | null
  account_cost: Record<string, unknown> | null
}

export async function listVideoTaskReviews(limit = 20, offset = 0): Promise<VideoTaskReview[]> {
  const { data } = await apiClient.get<{ items: VideoTaskReview[] }>('/admin/video-tasks/reviews', {
    params: { limit, offset }
  })
  return data.items
}

export async function resolveVideoTaskReview(
  stateID: string, action: VideoTaskReviewAction, reason: string
): Promise<VideoTaskReview> {
  const { data } = await apiClient.post<VideoTaskReview>(
    `/admin/video-tasks/reviews/${encodeURIComponent(stateID)}/resolve`, { action, reason }
  )
  return data
}

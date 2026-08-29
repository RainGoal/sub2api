import { describe, expect, it } from 'vitest'
import {
  MAX_UNINDEXED_WINDOW_MS,
  MAX_LIST_WINDOW_MS,
  defaultConversationAuditFilters,
  filterValidationCode,
  formatBytes,
} from '../viewModel'

describe('Conversation Audit view model', () => {
  it('defaults to an indexed 24-hour window', () => {
    const now = new Date(2026, 7, 29, 18, 30)
    const filters = defaultConversationAuditFilters(now)
    expect(new Date(filters.end).getTime() - new Date(filters.start).getTime()).toBe(MAX_UNINDEXED_WINDOW_MS)
    expect(filterValidationCode(filters)).toBe('')
  })

  it('enforces server query windows before sending requests', () => {
    const filters = defaultConversationAuditFilters(new Date(2026, 7, 29, 18, 30))
    filters.start = '2026-07-01T00:00'
    filters.end = '2026-08-02T00:00'
    expect(new Date(filters.end).getTime() - new Date(filters.start).getTime()).toBeGreaterThan(MAX_LIST_WINDOW_MS)
    expect(filterValidationCode(filters)).toBe('windowTooLarge')

    filters.start = '2026-08-01T00:00'
    filters.end = '2026-08-02T12:00'
    filters.protocol = 'openai_responses'
    expect(filterValidationCode(filters)).toBe('unindexedWindowTooLarge')
  })

  it('rejects malformed IDs and formats compressed storage sizes', () => {
    const filters = defaultConversationAuditFilters()
    filters.api_key_id = '-1'
    expect(filterValidationCode(filters)).toBe('invalidID')
    expect(formatBytes(1536)).toBe('1.5 KiB')
  })
})

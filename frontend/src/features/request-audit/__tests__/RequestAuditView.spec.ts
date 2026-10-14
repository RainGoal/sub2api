import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import RequestAuditView from '../RequestAuditView.vue'

const mocks = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), payload: vi.fn(), download: vi.fn(), getConfig: vi.fn(), updateConfig: vi.fn(), status: vi.fn(), listBans: vi.fn(), getBan: vi.fn(), createBan: vi.fn(), revokeBan: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }) }))

const record = { id: 'audit-one', received_at: '2026-10-14T00:00:00Z', kind: 'http', api_key_id: 12, user_id: 3, session_id: 'session-1', method: 'POST', route_pattern: '/v1/messages', capture_state: 'complete' }
const activeBan = { id: 'ban-one', api_key_id: 12, session_id: 'session-1', source_audit_id: record.id, public_reason: 'Reason', created_at: '2026-10-14T00:00:00Z' }
function createView() {
  return mount(RequestAuditView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    ConfirmDialog: { props: ['show'], emits: ['confirm'], template: '<div v-if="show"><button data-test="confirm" @click="$emit(\'confirm\')">confirm</button></div>' },
  } } })
}

async function createUncertainBan(hasBanID: boolean) {
  mocks.createBan.mockRejectedValueOnce(hasBanID
    ? { reason: 'BAN_RESULT_UNKNOWN', metadata: { ban_id: activeBan.id } }
    : { status: 503, reason: 'AUDIT_STORAGE_UNAVAILABLE' })
  const wrapper = createView()
  await flushPromises()
  await wrapper.get('[data-test="detail-audit-one"]').trigger('click')
  await flushPromises()
  await wrapper.get('[data-test="open-ban"]').trigger('click')
  await wrapper.get('textarea[maxlength="500"]').setValue('Reason')
  await wrapper.get('textarea[maxlength="4000"]').setValue('Keep this note')
  await wrapper.get('#request-audit-ban input[type="datetime-local"]').setValue('2099-01-01T12:00')
  await wrapper.get('#request-audit-ban').trigger('submit')
  await flushPromises()
  return wrapper
}

describe('RequestAuditView', () => {
  beforeEach(() => {
    Object.values(mocks).forEach(mock => mock.mockReset())
    mocks.getConfig.mockResolvedValue({ enabled: true, retention_days: 3, session_ban_enabled: false, version: 2 })
    mocks.status.mockResolvedValue({ instance_id: 'instance-one', running: true, config_known: true, config: { enabled: true }, dropped_requests: 0, dropped_events: 0, write_failures: 0, ban_check_failures: 0 })
    mocks.list.mockResolvedValue({ items: [record], has_more: false, next_cursor: '' })
    mocks.get.mockResolvedValue(record)
    mocks.listBans.mockResolvedValue({ items: [], has_more: false, next_cursor: '' })
    mocks.payload.mockResolvedValue({ content: '<script>alert(1)</script>', encoding: 'utf8', offset: 0, returned_bytes: 25, part_bytes: 25, has_more: false, capture_state: 'complete', redacted: false })
  })

  it('loads status and records independently and keeps the sensitive notice visible', async () => {
    mocks.status.mockRejectedValue(new Error('status unavailable'))
    const wrapper = createView()
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('admin.requestAudit.sensitiveNotice')
    expect(wrapper.find('[data-test="detail-audit-one"]').exists()).toBe(true)
    expect(mocks.payload).not.toHaveBeenCalled()
  })

  it('loads payload on demand and displays HTML as text', async () => {
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="detail-audit-one"]').trigger('click')
    await flushPromises()
    expect(mocks.payload).not.toHaveBeenCalled()
    await wrapper.get('[data-test="load-payload"]').trigger('click')
    await flushPromises()
    expect(mocks.payload).toHaveBeenCalledWith('audit-one', 'body', 0, 65536, false, false)
    expect(wrapper.get('[data-test="payload-content"]').text()).toBe('<script>alert(1)</script>')
    expect(wrapper.get('[data-test="payload-content"]').find('script').exists()).toBe(false)
  })

  it('requires confirmation before shortening retention and preserves separate ban toggle', async () => {
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="tab-settings"]').trigger('click')
    await wrapper.get('input[min="1"][max="30"]').setValue('1')
    await wrapper.get('[data-test="save-config"]').trigger('submit')
    expect(mocks.updateConfig).not.toHaveBeenCalled()
    mocks.updateConfig.mockResolvedValue({ enabled: true, retention_days: 1, session_ban_enabled: false, version: 3 })
    await wrapper.get('[data-test="confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ retention_days: 1, session_ban_enabled: false, version: 2 }))
  })

  it('never marks an uncertain ban write as successful and offers verification', async () => {
    mocks.createBan.mockRejectedValue({ reason: 'BAN_RESULT_UNKNOWN', metadata: { ban_id: 'unknown-id' } })
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="detail-audit-one"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="open-ban"]').trigger('click')
    await wrapper.get('textarea[maxlength="500"]').setValue('Reason')
    await wrapper.get('#request-audit-ban').trigger('submit')
    await flushPromises()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.requestAudit.resultUnknown')
    expect(wrapper.get('[data-test="create-ban"]').attributes('disabled')).toBeDefined()
  })

  it('shows degraded instance health beside the request list', async () => {
    mocks.status.mockResolvedValue({ running: true, config_known: true, dropped_requests: 2, dropped_events: 1, write_failures: 3 })
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.get('[data-test="health-summary"]').text()).toContain('admin.requestAudit.health.degraded')
  })

  it('does not report healthy capture when retention cleanup has failed', async () => {
    mocks.status.mockResolvedValue({ running: true, config_known: true, config: { enabled: true }, cleanup_failures: 1 })
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.get('[data-test="health-summary"]').text()).toContain('admin.requestAudit.health.degraded')
  })

  it('submits HTTP status filters and preserves the raw session ID', async () => {
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="http-status-filter"]').setValue('403')
    await wrapper.findAll('input[type="number"]')[0].setValue('12')
    await wrapper.get('input[maxlength="255"]').setValue(' session-1 ')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.list).toHaveBeenLastCalledWith(expect.objectContaining({ api_key_id: 12, http_status: 403, session_id: ' session-1 ' }))
  })

  it('keeps newer query results when a prior request finishes late', async () => {
    let resolveOld!: (value: unknown) => void
    mocks.list.mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
    const wrapper = createView()
    await wrapper.get('[data-test="http-status-filter"]').setValue('403')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    resolveOld({ items: [{ ...record, id: 'stale' }], has_more: false, next_cursor: '' })
    await flushPromises()
    expect(wrapper.find('[data-test="detail-audit-one"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="detail-stale"]').exists()).toBe(false)
  })

  it('blocks repeated submission after a network-uncertain ban write', async () => {
    mocks.createBan.mockRejectedValue({ status: 0, code: 'ERR_NETWORK' })
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="detail-audit-one"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="open-ban"]').trigger('click')
    await wrapper.get('textarea[maxlength="500"]').setValue('Reason')
    await wrapper.get('#request-audit-ban').trigger('submit')
    await flushPromises()
    await wrapper.get('#request-audit-ban').trigger('submit')
    expect(mocks.createBan).toHaveBeenCalledOnce()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.requestAudit.resultUnknown')
  })

  it.each([true, false])('preserves the draft and enables retry after confirmed absence (ban ID: %s)', async (hasBanID) => {
    const wrapper = await createUncertainBan(hasBanID)
    if (hasBanID) mocks.getBan.mockRejectedValueOnce({ status: 404, reason: 'BAN_NOT_FOUND' })
    else mocks.listBans.mockResolvedValueOnce({ items: [], has_more: false, next_cursor: '' })
    await wrapper.get('[data-test="check-ban-result"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="check-ban-result"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="create-ban"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get<HTMLTextAreaElement>('textarea[maxlength="500"]').element.value).toBe('Reason')
    expect(wrapper.get<HTMLTextAreaElement>('textarea[maxlength="4000"]').element.value).toBe('Keep this note')
    expect(wrapper.get<HTMLInputElement>('#request-audit-ban input[type="datetime-local"]').element.value).toBe('2099-01-01T12:00')
    expect(wrapper.text()).toContain('admin.requestAudit.banRetryReady')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    if (hasBanID) expect(mocks.getBan).toHaveBeenCalledWith(activeBan.id)
    else expect(mocks.listBans).toHaveBeenLastCalledWith({ source_audit_id: record.id, state: 'active', limit: 1 })

    mocks.createBan.mockResolvedValueOnce(activeBan)
    await wrapper.get('#request-audit-ban').trigger('submit')
    await flushPromises()
    expect(mocks.createBan).toHaveBeenCalledTimes(2)
    expect(mocks.createBan.mock.calls[1][0]).toEqual(mocks.createBan.mock.calls[0][0])
    expect(wrapper.find('#request-audit-ban').exists()).toBe(false)
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.requestAudit.banned')
    expect(mocks.listBans).toHaveBeenLastCalledWith(expect.objectContaining({ limit: 50 }))
  })

  it.each([
    [true, { status: 503, reason: 'AUDIT_STORAGE_UNAVAILABLE' }],
    [true, { status: 404, message: 'Route unavailable' }],
    [false, { status: 0, code: 'ERR_NETWORK' }],
    [false, { status: 404, reason: 'BAN_NOT_FOUND' }],
  ])('keeps a genuinely unknown result blocked (ban ID: %s, error: %j)', async (hasBanID, error) => {
    const wrapper = await createUncertainBan(hasBanID)
    if (hasBanID) mocks.getBan.mockRejectedValueOnce(error)
    else mocks.listBans.mockRejectedValueOnce(error)
    await wrapper.get('[data-test="check-ban-result"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="check-ban-result"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="create-ban"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#request-audit-ban').trigger('submit')
    expect(mocks.createBan).toHaveBeenCalledOnce()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })

  it.each([true, false])('refreshes the ban list after confirming successful creation (ban ID: %s)', async (hasBanID) => {
    const wrapper = await createUncertainBan(hasBanID)
    if (hasBanID) mocks.getBan.mockResolvedValueOnce(activeBan)
    else mocks.listBans.mockResolvedValueOnce({ items: [activeBan], has_more: false, next_cursor: '' })
    await wrapper.get('[data-test="check-ban-result"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('#request-audit-ban').exists()).toBe(false)
    expect(wrapper.get('[data-test="open-ban"]').attributes('disabled')).toBeDefined()
    expect(mocks.createBan).toHaveBeenCalledOnce()
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.requestAudit.banned')
    expect(mocks.listBans).toHaveBeenLastCalledWith(expect.objectContaining({ limit: 50 }))
  })
})

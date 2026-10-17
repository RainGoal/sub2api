import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { listMock, resolveMock, successMock } = vi.hoisted(() => ({ listMock: vi.fn(), resolveMock: vi.fn(), successMock: vi.fn() }))
vi.mock('@/api/admin/videoTaskReviews', () => ({ listVideoTaskReviews: listMock, resolveVideoTaskReview: resolveMock }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: successMock }) }))

import VideoTaskReviewsView from '../VideoTaskReviewsView.vue'

const task = {
  state_id: 'vid_00112233445566778899aabbccddeeff', public_id: 'vid_00112233445566778899aabbccddeeff',
  upstream_id: '', user_id: 7, api_key_id: 8, account_id: 9, model: 'test-video',
  resolution: '720p', duration: 10, upstream_status: '', settlement_status: 'needs_review',
  reason: 'submission_outcome_unknown', requested_at: null, deadline: null,
  decisions: [], sales_snapshot: null, account_cost: null
}
const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
  DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.state_id"><slot name="cell-actions" :row="row" /></div><slot v-if="!data.length" name="empty" /></div>' },
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
}

describe('VideoTaskReviewsView', () => {
  beforeEach(() => {
    listMock.mockReset()
    listMock.mockResolvedValue([task])
    resolveMock.mockReset()
    successMock.mockReset()
  })

  it('shows a permission error without exposing a task action', async () => {
    listMock.mockRejectedValue({ status: 403 })
    const wrapper = mount(VideoTaskReviewsView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.videoTaskReviews.forbidden')
    expect(wrapper.find('[data-testid^="review-vid_"]').exists()).toBe(false)
  })

  it('requires evidence and release confirmation and blocks duplicate submission', async () => {
    let finish: (value: unknown) => void = () => {}
    resolveMock.mockReturnValue(new Promise((resolve) => { finish = resolve }))
    const wrapper = mount(VideoTaskReviewsView, { global: { stubs } })
    await flushPromises()
    await wrapper.get(`[data-testid="review-${task.state_id}"]`).trigger('click')
    expect(wrapper.get('[data-testid="review-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#review-reason').setValue('Supplier billing confirms no completed output')
    expect(wrapper.get('[data-testid="review-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="review-confirm-release"]').setValue(true)
    await wrapper.get('[data-testid="review-submit"]').trigger('click')
    await wrapper.get('[data-testid="review-submit"]').trigger('click')
    expect(resolveMock).toHaveBeenCalledOnce()
    expect(resolveMock).toHaveBeenCalledWith(task.state_id, 'confirmed_failed', 'Supplier billing confirms no completed output')
    expect(wrapper.get('#review-reason').attributes('disabled')).toBeDefined()
    finish(task)
    await flushPromises()
    expect(successMock).toHaveBeenCalledWith('admin.videoTaskReviews.resolved')
  })
})

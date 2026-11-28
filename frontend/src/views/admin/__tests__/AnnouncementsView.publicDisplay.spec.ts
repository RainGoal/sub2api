import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AnnouncementsView from '../AnnouncementsView.vue'
import type { Announcement } from '@/types'

const { list, update } = vi.hoisted(() => ({ list: vi.fn(), update: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    announcements: { list, update },
    groups: { getAll: vi.fn().mockResolvedValue([]) },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }),
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('AnnouncementsView public display', () => {
  it('clears targeting in the same save request as enabling public display', async () => {
    const announcement: Announcement = {
      id: 12,
      title: 'Targeted announcement',
      content: 'Announcement body',
      status: 'active',
      notify_mode: 'silent',
      targeting: { any_of: [{ all_of: [{ type: 'balance', operator: 'gte', value: 10 }] }] },
      created_at: '2026-11-28T10:00:00Z',
      updated_at: '2026-11-28T10:00:00Z',
    }
    list.mockResolvedValue({ items: [announcement], total: 1, page: 1, page_size: 20, pages: 1 })
    update.mockResolvedValue({ ...announcement, is_public: true, targeting: {} })

    const wrapper = mount(AnnouncementsView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          TablePageLayout: { template: '<section><slot name="table" /></section>' },
          DataTable: {
            inheritAttrs: false,
            props: ['data'],
            template: '<div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div>',
          },
          BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
          ConfirmDialog: true,
          Select: true,
          Icon: true,
          AnnouncementTargetingEditor: true,
          AnnouncementReadStatusDialog: true,
          AnnouncementPopup: true,
        },
      },
    })
    await flushPromises()

    await wrapper.get('button[title="common.edit"]').trigger('click')
    const publicToggle = wrapper.get('#announcement-is-public')
    expect(publicToggle.attributes('aria-checked')).toBe('false')
    expect(wrapper.find('announcement-targeting-editor-stub').exists()).toBe(true)

    await publicToggle.trigger('click')
    expect(publicToggle.attributes('aria-checked')).toBe('true')
    expect(wrapper.find('announcement-targeting-editor-stub').exists()).toBe(false)
    await wrapper.get('#announcement-form').trigger('submit')

    expect(update).toHaveBeenCalledWith(12, { is_public: true, targeting: { any_of: [] } })
    await flushPromises()
    wrapper.unmount()
  })
})

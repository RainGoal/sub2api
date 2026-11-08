import { defineComponent } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DocsEditorView from '../DocsEditorView.vue'
import type { DocAdminNode } from '@/api/admin/docs'

const { page, tree, saveDraft, publish, unpublish, update, showSuccess, showError } = vi.hoisted(() => ({
  page: vi.fn(), tree: vi.fn(), saveDraft: vi.fn(), publish: vi.fn(), unpublish: vi.fn(), update: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/admin/docs', () => ({ docsAPI: { page, tree, saveDraft, publish, unpublish, update } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

function documentNode(): DocAdminNode {
  return {
    id: 1, kind: 'page', parent_id: null, slug: 'quick-start', title_zh: 'Start', title_en: '', sort_order: 0, revision: 1,
    locales: { 'zh-CN': { title: 'Start', summary: 'Summary', content_md: '## Setup\nOriginal', revision: 2, published_revision: 2, published_at: '2026-11-08T00:00:00Z' } }
  }
}
const ConfirmStub = defineComponent({
  props: ['show', 'title', 'confirmText'], emits: ['confirm', 'cancel'],
  template: '<section v-if="show" :data-dialog="title"><button class="dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button><button class="dialog-cancel" @click="$emit(\'cancel\')">Cancel</button></section>'
})
async function setup() {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/admin/docs/:id', component: DocsEditorView },
    { path: '/admin/docs', component: { template: '<div>List</div>' } }
  ] })
  await router.push('/admin/docs/1')
  await router.isReady()
  const wrapper = mount(defineComponent({ template: '<RouterView />' }), { global: {
    plugins: [router], stubs: { AppLayout: { template: '<main><slot /></main>' }, ConfirmDialog: ConfirmStub, Icon: true, DocsMarkdownPreview: true }
  } })
  await flushPromises()
  return { wrapper, router }
}
function button(wrapper: VueWrapper, key: string) { return wrapper.findAll('button').find(item => item.text() === key)! }

describe('documentation draft editor', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    page.mockResolvedValue(documentNode())
    tree.mockResolvedValue([documentNode()])
    saveDraft.mockImplementation(async (_id, input) => ({ ...documentNode(), locales: { 'zh-CN': { ...documentNode().locales['zh-CN'], ...input, revision: 3 } } }))
    publish.mockResolvedValue({ ...documentNode(), locales: { 'zh-CN': { ...documentNode().locales['zh-CN'], revision: 3, published_revision: 3 } } })
  })

  it('keeps the new article when an earlier request arrives after route navigation', async () => {
    let finishFirst!: (value: DocAdminNode) => void
    page.mockReturnValueOnce(new Promise(resolve => { finishFirst = resolve }))
    const { wrapper, router } = await setup()
    const second = { ...documentNode(), id: 2, slug: 'second', locales: { 'zh-CN': { ...documentNode().locales['zh-CN']!, title: 'Second article' } } }
    page.mockResolvedValueOnce(second)
    await router.push('/admin/docs/2')
    await flushPromises()
    finishFirst(documentNode())
    await flushPromises()
    expect((wrapper.get('#doc-page-title').element as HTMLInputElement).value).toBe('Second article')
    await wrapper.get('textarea.docs-markdown-input').setValue('Changed second article')
    await button(wrapper, 'docsAdmin.saveDraft').trigger('click')
    await flushPromises()
    expect(saveDraft).toHaveBeenCalledWith(2, expect.any(Object))
    wrapper.unmount()
  })

  it('keeps the slug locked after every language has been unpublished', async () => {
    page.mockResolvedValue({ ...documentNode(), slug_locked: true, locales: { 'zh-CN': { ...documentNode().locales['zh-CN']!, published_revision: null, published_at: null } } })
    const { wrapper } = await setup()
    expect(wrapper.get('#doc-page-slug').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('saves changes to the draft without publishing and requires a separate publish confirmation', async () => {
    const { wrapper } = await setup()
    await wrapper.get('textarea.docs-markdown-input').setValue('## Setup\nChanged')
    expect(button(wrapper, 'docsAdmin.publish').attributes('disabled')).toBeDefined()
    await button(wrapper, 'docsAdmin.saveDraft').trigger('click')
    await flushPromises()
    expect(saveDraft).toHaveBeenCalledWith(1, expect.objectContaining({ locale: 'zh-CN', revision: 2, content_md: '## Setup\nChanged' }))
    expect(publish).not.toHaveBeenCalled()
    await button(wrapper, 'docsAdmin.publish').trigger('click')
    expect(publish).not.toHaveBeenCalled()
    await wrapper.get('.dialog-confirm').trigger('click')
    await flushPromises()
    expect(publish).toHaveBeenCalledWith(1, 'zh-CN', 3)
    wrapper.unmount()
  })

  it('keeps unsaved changes when a language switch is cancelled', async () => {
    const { wrapper } = await setup()
    await wrapper.get('textarea.docs-markdown-input').setValue('Unsaved text')
    await wrapper.get('select[aria-label="docsAdmin.languages"]').setValue('en-US')
    await wrapper.get('.dialog-cancel').trigger('click')
    await flushPromises()
    expect((wrapper.get('textarea.docs-markdown-input').element as HTMLTextAreaElement).value).toBe('Unsaved text')
    expect((wrapper.get('select[aria-label="docsAdmin.languages"]').element as HTMLSelectElement).value).toBe('zh-CN')
    expect(saveDraft).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the current Markdown on revision conflicts and never publishes it', async () => {
    saveDraft.mockRejectedValue({ status: 409, message: 'Conflict' })
    const { wrapper } = await setup()
    await wrapper.get('textarea.docs-markdown-input').setValue('My draft')
    await button(wrapper, 'docsAdmin.saveDraft').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('docsAdmin.conflict')
    expect((wrapper.get('textarea.docs-markdown-input').element as HTMLTextAreaElement).value).toBe('My draft')
    expect(publish).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import DocsMarkdownPreview from '@/components/admin/docs/DocsMarkdownPreview.vue'
import { docsAPI, type DocAdminNode, type DocLocale } from '@/api/admin/docs'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { documentErrorKey } from '@/utils/docsError'
import { DOC_MAX_CONTENT_BYTES, documentByteLength, documentImageToDataURL, importedMarkdown } from '@/utils/docsMarkdown'

const { t } = useI18n()
const app = useAppStore()
const route = useRoute()
const node = ref<DocAdminNode | null>(null)
const groups = ref<DocAdminNode[]>([])
const loading = ref(false)
const busy = ref(false)
const imageBusy = ref(false)
const error = ref('')
const language = ref<DocLocale>('zh-CN')
const mode = ref<'edit' | 'split' | 'preview'>(window.matchMedia('(max-width: 1023px)').matches ? 'edit' : 'split')
const form = reactive({ title: '', summary: '', content_md: '', slug: '', parent: '' })
const savedForm = ref('')
const textarea = ref<HTMLTextAreaElement | null>(null)
const imageInput = ref<HTMLInputElement | null>(null)
const importInput = ref<HTMLInputElement | null>(null)
const previewContent = ref('')
const action = ref<'publish' | 'unpublish' | null>(null)
const leaveOpen = ref(false)
let resolveLeave: ((value: boolean) => void) | undefined
let previewTimer: ReturnType<typeof setTimeout> | undefined
let loadSequence = 0

const current = computed(() => node.value?.locales?.[language.value])
const dirty = computed(() => Boolean(node.value) && JSON.stringify(form) !== savedForm.value)
const contentBytes = computed(() => documentByteLength(form.content_md))
const slugLocked = computed(() => node.value?.slug_locked || Object.values(node.value?.locales || {}).some(content => content?.published_revision || content?.published_at))
const isPublished = computed(() => Boolean(current.value?.published_revision))
const stateLabel = computed(() => {
  if (dirty.value) return t('docsAdmin.unsaved')
  if (!isPublished.value) return t('docsAdmin.draft')
  return t(current.value?.revision === current.value?.published_revision ? 'docsAdmin.published' : 'docsAdmin.changed')
})
const languageLabel = computed(() => t(language.value === 'zh-CN' ? 'docsAdmin.chinese' : 'docsAdmin.english'))

function report(cause: unknown): string {
  return t(documentErrorKey(cause))
}
function fillForm() {
  if (!node.value) return
  const content = node.value.locales?.[language.value]
  Object.assign(form, {
    title: content?.title || (language.value === 'zh-CN' ? node.value.title_zh : node.value.title_en) || '',
    summary: content?.summary || '', content_md: content?.content_md || '',
    slug: node.value.slug, parent: node.value.parent_id ? String(node.value.parent_id) : ''
  })
  savedForm.value = JSON.stringify(form)
  previewContent.value = form.content_md
}
async function load() {
  const sequence = ++loadSequence
  const id = Number(route.params.id)
  node.value = null
  action.value = null
  if (!Number.isSafeInteger(id) || id <= 0) { loading.value = false; error.value = t('docsAdmin.loadFailed'); return }
  loading.value = true
  error.value = ''
  try {
    const [page, tree] = await Promise.all([docsAPI.page(id), docsAPI.tree()])
    if (sequence !== loadSequence) return
    node.value = page
    groups.value = tree.filter(item => item.kind === 'group').sort((a, b) => a.sort_order - b.sort_order)
    fillForm()
  } catch (cause) { if (sequence === loadSequence) error.value = report(cause) }
  finally { if (sequence === loadSequence) loading.value = false }
}
function confirmLeave(): Promise<boolean> {
  leaveOpen.value = true
  return new Promise(resolve => { resolveLeave = resolve })
}
function finishLeave(allow: boolean) {
  leaveOpen.value = false
  resolveLeave?.(allow)
  resolveLeave = undefined
}
async function requestReload() {
  if (!dirty.value || await confirmLeave()) await load()
}
async function changeLanguage(event: Event) {
  const select = event.target as HTMLSelectElement
  const next = select.value as DocLocale
  if (dirty.value && !await confirmLeave()) { select.value = language.value; return }
  language.value = next
  fillForm()
}
async function saveDraft() {
  if (!node.value || busy.value || imageBusy.value) return
  if (!form.title.trim() || !form.content_md.trim()) { app.showError(t('docsAdmin.emptyContent')); return }
  if (contentBytes.value > DOC_MAX_CONTENT_BYTES) { app.showError(t('docsAdmin.contentSize')); return }
  if (form.slug.length > 96 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(form.slug)) { app.showError(t('docsAdmin.invalidSlug')); return }
  busy.value = true
  try {
    const id = node.value.id
    node.value = await docsAPI.saveDraft(id, { locale: language.value, title: form.title.trim(), summary: form.summary.trim(), content_md: form.content_md, revision: current.value?.revision || 0 })
    if (form.slug !== node.value.slug || (form.parent ? Number(form.parent) : null) !== node.value.parent_id) {
      node.value = await docsAPI.update(id, { slug: form.slug, parent_id: form.parent ? Number(form.parent) : null })
    }
    fillForm()
    app.showSuccess(t('docsAdmin.saved'))
  } catch (cause) { app.showError(report(cause)) }
  finally { busy.value = false }
}
async function runAction() {
  if (!node.value || !action.value || busy.value || dirty.value) return
  busy.value = true
  try {
    const operation = action.value
    node.value = operation === 'publish'
      ? await docsAPI.publish(node.value.id, language.value, current.value?.revision || 0)
      : await docsAPI.unpublish(node.value.id, language.value, current.value?.revision || 0)
    fillForm()
    action.value = null
    app.showSuccess(t(operation === 'publish' ? 'docsAdmin.publishedSuccess' : 'docsAdmin.unpublishedSuccess'))
  } catch (cause) { app.showError(report(cause)) }
  finally { busy.value = false }
}
async function replaceSelection(before: string, after = '', placeholder = '') {
  if (busy.value || imageBusy.value) return
  const field = textarea.value
  const start = field?.selectionStart ?? form.content_md.length
  const end = field?.selectionEnd ?? start
  const selected = form.content_md.slice(start, end) || placeholder
  const replacement = before + selected + after
  const next = form.content_md.slice(0, start) + replacement + form.content_md.slice(end)
  if (documentByteLength(next) > DOC_MAX_CONTENT_BYTES) { app.showError(t('docsAdmin.contentSize')); return }
  form.content_md = next
  await nextTick()
  textarea.value?.focus()
  textarea.value?.setSelectionRange(start + before.length, start + before.length + selected.length)
}
function toolbar(kind: string) {
  const fence = String.fromCharCode(96).repeat(3)
  const text = t('docsAdmin.selectedText')
  if (kind === 'heading') void replaceSelection('\n## ', '\n', text)
  if (kind === 'bold') void replaceSelection('**', '**', text)
  if (kind === 'list') void replaceSelection('\n- ', '\n', text)
  if (kind === 'numberedList') void replaceSelection('\n1. ', '\n', text)
  if (kind === 'link') void replaceSelection('[', '](https://)', t('docsAdmin.linkText'))
  if (kind === 'quote') void replaceSelection('\n> ', '\n', text)
  if (kind === 'code') void replaceSelection('\n' + fence + 'bash\n', '\n' + fence + '\n', text)
  if (kind === 'table') void replaceSelection('\n| ' + t('docsAdmin.tableColumn') + ' | ' + t('docsAdmin.tableColumn') + ' |\n| --- | --- |\n| ', ' | |\n', text)
}
async function insertImage(file?: File) {
  if (!file || imageBusy.value || busy.value) return
  imageBusy.value = true
  try {
    const dataURL = await documentImageToDataURL(file)
    imageBusy.value = false
    await replaceSelection('\n![' + t('docsAdmin.imageAlt') + '](' + dataURL + ')\n')
  } catch (cause) {
    const code = (cause as Error).message
    app.showError(t(['imageType', 'imageSize', 'imageRead'].includes(code) ? 'docsAdmin.' + code : 'docsAdmin.imageRead'))
  } finally {
    imageBusy.value = false
    if (imageInput.value) imageInput.value.value = ''
  }
}
function pasteImage(event: ClipboardEvent) {
  const file = Array.from(event.clipboardData?.files || []).find(item => item.type.startsWith('image/'))
  if (file) { event.preventDefault(); void insertImage(file) }
}
function dropImage(event: DragEvent) {
  const file = Array.from(event.dataTransfer?.files || []).find(item => item.type.startsWith('image/'))
  if (file) { event.preventDefault(); void insertImage(file) }
}
async function importFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file || busy.value) return
  try {
    if (file.size > DOC_MAX_CONTENT_BYTES) { app.showError(t('docsAdmin.importSize')); return }
    if (dirty.value && !await confirmLeave()) return
    const result = importedMarkdown(await file.text(), form.title)
    if (documentByteLength(result.content) > DOC_MAX_CONTENT_BYTES) { app.showError(t('docsAdmin.contentSize')); return }
    form.title = result.title
    form.content_md = result.content
    app.showSuccess(t('docsAdmin.imported'))
  } catch { app.showError(t('docsAdmin.importFailed')) }
  finally { input.value = '' }
}
function exportFile() {
  const file = new Blob(['# ' + form.title + '\n\n' + form.content_md], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(file)
  const link = document.createElement('a')
  link.href = url
  link.download = (form.slug || 'document') + '.' + language.value + '.md'
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
async function copyPath() {
  try { await navigator.clipboard.writeText('/docs/' + form.slug); app.showSuccess(t('docsAdmin.copiedPath')) }
  catch { app.showError(t('docsAdmin.saveFailed')) }
}
function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value) { event.preventDefault(); event.returnValue = '' }
}
function tab(event: KeyboardEvent) {
  if (event.key === 'Tab' && !event.shiftKey) { event.preventDefault(); void replaceSelection('  ') }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') { event.preventDefault(); void saveDraft() }
}
watch(() => form.content_md, content => {
  clearTimeout(previewTimer)
  previewTimer = setTimeout(() => { previewContent.value = content }, 250)
})
watch(() => route.params.id, load)
onBeforeRouteLeave(() => dirty.value ? confirmLeave() : true)
onBeforeRouteUpdate(() => dirty.value ? confirmLeave() : true)
onMounted(() => { void load(); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { loadSequence++; clearTimeout(previewTimer); window.removeEventListener('beforeunload', beforeUnload); resolveLeave?.(false) })
</script>

<template>
  <AppLayout>
    <div class="space-y-5">
      <RouterLink to="/admin/docs" class="inline-flex items-center gap-2 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white"><Icon name="arrowLeft" size="sm" />{{ t('docsAdmin.back') }}</RouterLink>
      <div v-if="loading" class="py-16 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</div>
      <div v-else-if="error" class="rounded-md border border-red-200 p-6 dark:border-red-900" role="alert"><p class="mb-4 text-sm text-red-600 dark:text-red-400">{{ error }}</p><button class="btn btn-secondary" @click="load">{{ t('docsAdmin.retry') }}</button></div>
      <template v-else-if="node">
        <header class="flex flex-wrap items-center justify-between gap-4">
          <div class="min-w-0"><h2 class="break-words text-2xl font-semibold text-gray-900 dark:text-white">{{ form.title || t('docsAdmin.editPage') }}</h2><p class="mt-2 text-xs" :class="dirty ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500 dark:text-dark-400'" role="status">{{ imageBusy ? t('docsAdmin.imageBusy') : stateLabel }}</p></div>
          <div class="flex flex-wrap items-center gap-2">
            <select :value="language" class="input w-28" :aria-label="t('docsAdmin.languages')" :disabled="busy || imageBusy" @change="changeLanguage"><option value="zh-CN">{{ t('docsAdmin.chinese') }}</option><option value="en-US">{{ t('docsAdmin.english') }}</option></select>
            <button class="btn btn-secondary" :disabled="busy || imageBusy" :title="t('docsAdmin.reload')" @click="requestReload"><Icon name="refresh" size="sm" /><span class="sr-only">{{ t('docsAdmin.reload') }}</span></button>
            <button v-if="isPublished" class="btn btn-secondary" :disabled="busy || dirty || imageBusy" @click="action = 'unpublish'">{{ t('docsAdmin.unpublish') }}</button>
            <button class="btn btn-secondary" :disabled="busy || imageBusy || !dirty" @click="saveDraft">{{ t(busy ? 'docsAdmin.saving' : 'docsAdmin.saveDraft') }}</button>
            <button class="btn btn-primary" :disabled="busy || imageBusy || dirty || !current?.revision || current.revision === current.published_revision" :title="dirty ? t('docsAdmin.saveBeforePublish') : undefined" @click="action = 'publish'">{{ t('docsAdmin.publish') }}</button>
          </div>
        </header>
        <details class="rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800" open>
          <summary class="cursor-pointer px-4 py-3 text-sm font-medium text-gray-900 dark:text-white">{{ t('docsAdmin.settings') }}</summary>
          <div class="grid gap-4 border-t border-gray-200 p-4 md:grid-cols-2 dark:border-dark-700">
            <div><label for="doc-page-title" class="input-label">{{ t('docsAdmin.titleLabel') }}</label><input id="doc-page-title" v-model="form.title" class="input" maxlength="200" :disabled="busy" /></div>
            <div><label for="doc-page-parent" class="input-label">{{ t('docsAdmin.parent') }}</label><select id="doc-page-parent" v-model="form.parent" class="input" :disabled="busy"><option value="">{{ t('docsAdmin.rootDocuments') }}</option><option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.title_zh || group.title_en }}</option></select></div>
            <div><label for="doc-page-slug" class="input-label">{{ t('docsAdmin.slug') }}</label><div class="flex gap-2"><input id="doc-page-slug" v-model="form.slug" class="input font-mono" maxlength="96" :disabled="slugLocked || busy" /><button class="btn btn-secondary" :aria-label="t('docsAdmin.copyPath')" @click="copyPath"><Icon name="link" size="sm" /></button></div><p class="input-hint">{{ t('docsAdmin.slugHint') }}</p></div>
            <div><label for="doc-page-summary" class="input-label">{{ t('docsAdmin.summary') }}</label><textarea id="doc-page-summary" v-model="form.summary" class="input" rows="2" maxlength="1000" :disabled="busy"></textarea></div>
          </div>
        </details>
        <div class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 p-3 dark:border-dark-700">
            <div class="flex gap-1" role="group" :aria-label="t('docsAdmin.editor')"><button v-for="item in (['edit', 'split', 'preview'] as const)" :key="item" class="docs-mode" :class="{ active: mode === item, 'hidden lg:inline-flex': item === 'split' }" :aria-pressed="mode === item" @click="mode = item">{{ t(item === 'edit' ? 'docsAdmin.editor' : item === 'split' ? 'docsAdmin.split' : 'docsAdmin.preview') }}</button></div>
            <div class="flex gap-2"><button class="btn btn-secondary text-xs" :disabled="busy || imageBusy" @click="importInput?.click()">{{ t('docsAdmin.import') }}</button><button class="btn btn-secondary text-xs" @click="exportFile">{{ t('docsAdmin.export') }}</button></div>
          </div>
          <div class="docs-editor-body" :class="{ 'is-split': mode === 'split' }">
            <section v-show="mode !== 'preview'" class="min-w-0" :aria-label="t('docsAdmin.markdown')">
              <div class="flex flex-wrap gap-1 border-b border-gray-200 px-3 py-2 dark:border-dark-700" role="toolbar" :aria-label="t('docsAdmin.markdown')"><button v-for="kind in ['heading', 'bold', 'list', 'numberedList', 'link', 'quote', 'code', 'table']" :key="kind" class="docs-tool" :disabled="busy || imageBusy" @click="toolbar(kind)">{{ t('docsAdmin.' + kind) }}</button><button class="docs-tool" :disabled="busy || imageBusy" @click="imageInput?.click()">{{ t('docsAdmin.image') }}</button></div>
              <textarea ref="textarea" v-model="form.content_md" class="docs-markdown-input" :aria-label="t('docsAdmin.markdown')" :placeholder="t('docsAdmin.markdownHint')" :disabled="busy || imageBusy" spellcheck="false" @keydown="tab" @paste="pasteImage" @drop="dropImage" @dragover.prevent></textarea>
            </section>
            <section v-show="mode !== 'edit'" class="docs-preview min-w-0 bg-white dark:bg-dark-900" :aria-label="t('docsAdmin.preview')">
              <DocsMarkdownPreview v-if="form.title || previewContent" :title="form.title" :summary="form.summary" :content="previewContent" />
              <p v-else class="p-8 text-sm text-gray-500 dark:text-dark-400">{{ t('docsAdmin.previewEmpty') }}</p>
            </section>
          </div>
          <footer class="flex flex-wrap justify-between gap-2 border-t border-gray-200 px-4 py-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400"><span :class="{ 'text-red-500': contentBytes > DOC_MAX_CONTENT_BYTES }">{{ t('docsAdmin.documentSize', { size: (contentBytes / 1024 / 1024).toFixed(2) }) }}</span><span v-if="current?.published_at">{{ t('docsAdmin.publishedAt', { time: formatDateTime(current.published_at) }) }}</span></footer>
        </div>
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('docsAdmin.fallback') }}</p>
      </template>
    </div>
    <input ref="imageInput" type="file" accept="image/png,image/jpeg,image/webp" class="hidden" :aria-label="t('docsAdmin.image')" @change="insertImage(($event.target as HTMLInputElement).files?.[0])" />
    <input ref="importInput" type="file" accept=".md,.markdown,text/markdown,text/plain" class="hidden" :aria-label="t('docsAdmin.import')" @change="importFile" />
    <ConfirmDialog :show="Boolean(action)" :title="t(action === 'unpublish' ? 'docsAdmin.unpublishTitle' : 'docsAdmin.publishTitle')" :message="t(action === 'unpublish' ? 'docsAdmin.unpublishMessage' : 'docsAdmin.publishMessage', { language: languageLabel })" :confirm-text="t(action === 'unpublish' ? 'docsAdmin.unpublish' : 'docsAdmin.publish')" :danger="action === 'unpublish'" @confirm="runAction" @cancel="!busy && (action = null)" />
    <ConfirmDialog :show="leaveOpen" :title="t('docsAdmin.discardTitle')" :message="t('docsAdmin.discardMessage')" :confirm-text="t('docsAdmin.discard')" :cancel-text="t('docsAdmin.stay')" @confirm="finishLeave(true)" @cancel="finishLeave(false)" />
  </AppLayout>
</template>

<style scoped>
.docs-editor-body { display: grid; grid-template-columns: minmax(0, 1fr); }
.docs-editor-body.is-split { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
.docs-editor-body.is-split > section + section { @apply border-l border-gray-200 dark:border-dark-700; }
.docs-markdown-input { @apply block w-full resize-y border-0 bg-transparent p-4 font-mono text-sm leading-7 text-gray-800 outline-none focus:ring-2 focus:ring-inset focus:ring-primary-500/30 disabled:opacity-60 dark:text-gray-200; min-height: 640px; tab-size: 2; }
.docs-tool { @apply rounded px-2 py-1 text-xs text-gray-500 hover:bg-gray-100 hover:text-gray-900 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 disabled:opacity-40 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-white; }
.docs-mode { @apply items-center rounded-md px-3 py-1.5 text-sm text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-700; }
.docs-mode.active { @apply bg-gray-100 font-medium text-gray-900 dark:bg-dark-700 dark:text-white; }
@media (max-width: 1023px) { .docs-editor-body.is-split { grid-template-columns: 1fr; } .is-split .docs-preview { display: none; } .docs-markdown-input { min-height: 420px; font-size: 16px; } }
</style>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { docsAPI, type DocAdminNode, type DocContent, type DocLocale } from '@/api/admin/docs'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { documentErrorKey } from '@/utils/docsError'

const { t, locale } = useI18n()
const app = useAppStore()
const router = useRouter()
const nodes = ref<DocAdminNode[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const search = ref('')
const selectedGroup = ref<number | 'all' | 'root'>('all')
const originalTree = ref('[]')
const draggedID = ref<number | null>(null)
const deleting = ref<DocAdminNode | null>(null)
const dialogOpen = ref(false)
const editingGroup = ref<DocAdminNode | null>(null)
const form = reactive({ kind: 'page' as 'group' | 'page', title_zh: '', title_en: '', slug: '', parent: '' })
const leaveOpen = ref(false)
let resolveLeave: ((value: boolean) => void) | undefined

const treeItems = computed(() => nodes.value.map(node => ({ id: node.id, parent_id: node.parent_id, sort_order: node.sort_order })))
const directoryDirty = computed(() => JSON.stringify(treeItems.value) !== originalTree.value)
const groups = computed(() => nodes.value.filter(node => node.kind === 'group').sort(byOrder))
const pages = computed(() => nodes.value.filter(node => node.kind === 'page'))
const selectedNode = computed(() => groups.value.find(group => group.id === selectedGroup.value))
const displayPages = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return pages.value.filter(page => {
    if (selectedGroup.value === 'root' && page.parent_id !== null) return false
    if (typeof selectedGroup.value === 'number' && page.parent_id !== selectedGroup.value) return false
    return !query || [page.slug, page.title_zh, page.title_en, page.locales?.['zh-CN']?.title, page.locales?.['en-US']?.title]
      .some(value => value?.toLocaleLowerCase().includes(query))
  }).sort((a, b) => (a.parent_id || 0) - (b.parent_id || 0) || byOrder(a, b))
})
const columns = computed(() => [
  { key: 'title', label: t('docsAdmin.page') },
  { key: 'locales', label: t('docsAdmin.languages') },
  { key: 'updated_at', label: t('docsAdmin.modified') },
  { key: 'actions', label: t('docsAdmin.actions') }
])

function byOrder(a: DocAdminNode, b: DocAdminNode) { return a.sort_order - b.sort_order || a.id - b.id }
function nodeTitle(node: DocAdminNode): string {
  return locale.value.startsWith('en')
    ? node.locales?.['en-US']?.title || node.title_en || node.locales?.['zh-CN']?.title || node.title_zh
    : node.locales?.['zh-CN']?.title || node.title_zh || node.title_en
}
function state(content?: DocContent): string {
  if (!content?.published_revision) return t('docsAdmin.unpublished')
  return content.revision === content.published_revision ? t('docsAdmin.published') : t('docsAdmin.changed')
}
function report(cause: unknown): string {
  return t(documentErrorKey(cause))
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    nodes.value = (await docsAPI.tree()).map(node => ({ ...node, locales: node.locales || {} }))
    originalTree.value = JSON.stringify(treeItems.value)
    if (typeof selectedGroup.value === 'number' && !nodes.value.some(node => node.id === selectedGroup.value)) selectedGroup.value = 'all'
  } catch (cause) { error.value = report(cause) }
  finally { loading.value = false }
}
async function saveDirectory() {
  if (busy.value) return
  busy.value = true
  try {
    await docsAPI.saveTree(treeItems.value)
    originalTree.value = JSON.stringify(treeItems.value)
    app.showSuccess(t('docsAdmin.directorySaved'))
  } catch (cause) { app.showError(report(cause)) }
  finally { busy.value = false }
}
function reorder(source: DocAdminNode, target: DocAdminNode) {
  if (busy.value || loading.value) return
  if (source.kind !== target.kind || source.parent_id !== target.parent_id || source.id === target.id) return
  const siblings = nodes.value.filter(node => node.kind === source.kind && node.parent_id === source.parent_id).sort(byOrder)
  const from = siblings.findIndex(node => node.id === source.id)
  const to = siblings.findIndex(node => node.id === target.id)
  siblings.splice(to, 0, siblings.splice(from, 1)[0])
  siblings.forEach((node, index) => { node.sort_order = index })
}
function drop(target: DocAdminNode) {
  const source = nodes.value.find(node => node.id === draggedID.value)
  if (source) reorder(source, target)
  draggedID.value = null
}
function move(node: DocAdminNode, direction: number) {
  const siblings = nodes.value.filter(item => item.kind === node.kind && item.parent_id === node.parent_id).sort(byOrder)
  const target = siblings[siblings.findIndex(item => item.id === node.id) + direction]
  if (target) reorder(node, target)
}
function canMove(node: DocAdminNode, direction: number): boolean {
  const siblings = nodes.value.filter(item => item.kind === node.kind && item.parent_id === node.parent_id).sort(byOrder)
  return Boolean(siblings[siblings.findIndex(item => item.id === node.id) + direction])
}
function moveToGroup(node: DocAdminNode, event: Event) {
  if (busy.value || loading.value) return
  const value = (event.target as HTMLSelectElement).value
  node.parent_id = value ? Number(value) : null
  node.sort_order = Math.max(0, ...nodes.value.filter(item => item.id !== node.id && item.parent_id === node.parent_id).map(item => item.sort_order)) + 1
}
function create(kind: 'page' | 'group') {
  if (busy.value || loading.value || directoryDirty.value) return
  editingGroup.value = null
  Object.assign(form, { kind, title_zh: '', title_en: '', slug: '', parent: typeof selectedGroup.value === 'number' ? String(selectedGroup.value) : '' })
  dialogOpen.value = true
}
function editGroup() {
  if (!selectedNode.value) return
  editingGroup.value = selectedNode.value
  Object.assign(form, { kind: 'group', title_zh: selectedNode.value.title_zh, title_en: selectedNode.value.title_en, slug: selectedNode.value.slug, parent: '' })
  dialogOpen.value = true
}
async function submitNode() {
  if (busy.value) return
  if (!form.title_zh.trim()) { app.showError(t('docsAdmin.missingTitle')); return }
  const slug = form.kind === 'group' ? form.slug || 'group-' + Date.now().toString(36) : form.slug.trim()
  if (slug.length > 96 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug)) { app.showError(t('docsAdmin.invalidSlug')); return }
  busy.value = true
  try {
    const node = editingGroup.value
      ? await docsAPI.update(editingGroup.value.id, { title_zh: form.title_zh.trim(), title_en: form.title_en.trim() })
      : await docsAPI.create({ kind: form.kind, title_zh: form.title_zh.trim(), title_en: form.title_en.trim(), slug, parent_id: form.kind === 'page' && form.parent ? Number(form.parent) : null })
    dialogOpen.value = false
    app.showSuccess(t(editingGroup.value ? 'docsAdmin.updated' : 'docsAdmin.created'))
    await load()
    if (node.kind === 'page') await router.push('/admin/docs/' + node.id)
  } catch (cause) { app.showError(report(cause)) }
  finally { busy.value = false }
}
async function remove() {
  if (!deleting.value || busy.value) return
  busy.value = true
  try {
    await docsAPI.remove(deleting.value.id)
    deleting.value = null
    app.showSuccess(t('docsAdmin.deleted'))
    await load()
  } catch (cause) { app.showError(report(cause)) }
  finally { busy.value = false }
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
function beforeUnload(event: BeforeUnloadEvent) {
  if (directoryDirty.value) { event.preventDefault(); event.returnValue = '' }
}
onBeforeRouteLeave(() => directoryDirty.value ? confirmLeave() : true)
onMounted(() => { void load(); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { window.removeEventListener('beforeunload', beforeUnload); resolveLeave?.(false) })
</script>

<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('docsAdmin.description') }}</p>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-secondary" :disabled="loading || busy || directoryDirty" @click="load"><Icon name="refresh" size="sm" />{{ t('docsAdmin.refresh') }}</button>
          <button class="btn btn-secondary" :disabled="loading || busy || directoryDirty" @click="create('group')"><Icon name="plus" size="sm" />{{ t('docsAdmin.newGroup') }}</button>
          <button class="btn btn-primary" :disabled="loading || busy || directoryDirty" @click="create('page')"><Icon name="plus" size="sm" />{{ t('docsAdmin.newPage') }}</button>
        </div>
      </div>
      <div v-if="error" class="rounded-md border border-red-200 p-5 dark:border-red-900" role="alert">
        <p class="mb-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <button class="btn btn-secondary" @click="load">{{ t('docsAdmin.retry') }}</button>
      </div>
      <div v-else class="docs-management-grid">
        <aside class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <h2 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('docsAdmin.directory') }}</h2>
          <div class="space-y-1">
            <button class="docs-folder" :class="{ selected: selectedGroup === 'all' }" @click="selectedGroup = 'all'">{{ t('docsAdmin.allDocuments') }}<span>{{ pages.length }}</span></button>
            <button class="docs-folder" :class="{ selected: selectedGroup === 'root' }" @click="selectedGroup = 'root'">{{ t('docsAdmin.rootDocuments') }}</button>
            <div v-for="group in groups" :key="group.id" class="flex items-center gap-1" @dragover.prevent @drop.prevent="drop(group)">
              <button class="docs-folder min-w-0 flex-1" :class="{ selected: selectedGroup === group.id }" draggable="true" :title="t('docsAdmin.drag')" @dragstart="draggedID = group.id" @dragend="draggedID = null" @click="selectedGroup = group.id"><span class="truncate">{{ nodeTitle(group) }}</span></button>
              <button class="docs-icon" :disabled="!canMove(group, -1) || busy" :aria-label="t('docsAdmin.moveUp')" @click="move(group, -1)"><Icon name="arrowUp" size="xs" /></button>
              <button class="docs-icon" :disabled="!canMove(group, 1) || busy" :aria-label="t('docsAdmin.moveDown')" @click="move(group, 1)"><Icon name="arrowDown" size="xs" /></button>
            </div>
          </div>
          <p class="mt-5 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('docsAdmin.directoryHint') }}</p>
          <button class="btn btn-secondary mt-3 w-full" :disabled="!directoryDirty || busy" @click="saveDirectory">{{ t('docsAdmin.saveDirectory') }}</button>
          <p v-if="directoryDirty" class="mt-2 text-xs text-amber-600 dark:text-amber-400" role="status">{{ t('docsAdmin.directoryChanged') }}</p>
        </aside>
        <section class="min-w-0 space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <input v-model="search" class="input max-w-sm" :placeholder="t('docsAdmin.search')" :aria-label="t('docsAdmin.search')" />
            <div v-if="selectedNode" class="flex gap-2">
              <button class="btn btn-secondary" :disabled="busy || directoryDirty" @click="editGroup">{{ t('docsAdmin.editGroup') }}</button>
              <button class="docs-icon" :disabled="busy || directoryDirty" :aria-label="t('docsAdmin.removeGroup')" @click="deleting = selectedNode || null"><Icon name="trash" size="sm" /></button>
            </div>
          </div>
          <DataTable :columns="columns" :data="displayPages" :loading="loading">
            <template #cell-title="{ row }">
              <div class="flex items-start gap-2" @dragover.prevent @drop.prevent="drop(row)">
                <button class="docs-icon mt-0.5" draggable="true" :title="t('docsAdmin.drag')" :aria-label="t('docsAdmin.drag')" @dragstart="draggedID = row.id" @dragend="draggedID = null"><Icon name="more" size="sm" /></button>
                <div class="min-w-0">
                  <RouterLink :to="'/admin/docs/' + row.id" class="break-words font-medium text-gray-900 hover:underline dark:text-white">{{ nodeTitle(row) }}</RouterLink>
                  <p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-dark-400">/docs/{{ row.slug }}</p>
                  <select class="input mt-2 max-w-48 py-1 text-xs" :value="row.parent_id || ''" :aria-label="t('docsAdmin.parent')" :disabled="busy" @change="moveToGroup(row, $event)"><option value="">{{ t('docsAdmin.rootDocuments') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ nodeTitle(group) }}</option></select>
                </div>
              </div>
            </template>
            <template #cell-locales="{ row }">
              <div class="space-y-2 text-xs">
                <div v-for="language in (['zh-CN', 'en-US'] as DocLocale[])" :key="language" class="flex flex-wrap items-center gap-2"><span class="w-12 text-gray-500 dark:text-dark-400">{{ t(language === 'zh-CN' ? 'docsAdmin.chinese' : 'docsAdmin.english') }}</span><span class="rounded border border-gray-200 px-2 py-0.5 dark:border-dark-600" :class="row.locales?.[language]?.published_revision ? 'text-emerald-700 dark:text-emerald-400' : 'text-gray-500 dark:text-dark-400'">{{ state(row.locales?.[language]) }}</span></div>
              </div>
            </template>
            <template #cell-updated_at="{ row }"><span class="text-xs text-gray-500 dark:text-dark-400">{{ row.updated_at ? formatDateTime(row.updated_at) : '—' }}</span></template>
            <template #cell-actions="{ row }">
              <div class="flex flex-wrap items-center gap-1">
                <button class="docs-icon" :disabled="!canMove(row, -1) || busy" :aria-label="t('docsAdmin.moveUp')" @click="move(row, -1)"><Icon name="arrowUp" size="sm" /></button>
                <button class="docs-icon" :disabled="!canMove(row, 1) || busy" :aria-label="t('docsAdmin.moveDown')" @click="move(row, 1)"><Icon name="arrowDown" size="sm" /></button>
                <RouterLink class="docs-icon" :to="'/admin/docs/' + row.id" :aria-label="t('docsAdmin.editPage')"><Icon name="edit" size="sm" /></RouterLink>
                <button class="docs-icon" :disabled="busy || directoryDirty" :aria-label="t('common.delete')" @click="deleting = row"><Icon name="trash" size="sm" /></button>
              </div>
            </template>
            <template #empty><EmptyState :title="t(pages.length ? 'docsAdmin.noResults' : 'docsAdmin.empty')" :description="t(pages.length ? 'docsAdmin.noResultsHint' : 'docsAdmin.emptyHint')" :action-text="t('docsAdmin.newPage')" @action="create('page')" /></template>
          </DataTable>
          <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('docsAdmin.fallback') }}</p>
        </section>
      </div>
    </div>
    <BaseDialog :show="dialogOpen" :title="t(editingGroup ? 'docsAdmin.editGroup' : form.kind === 'group' ? 'docsAdmin.newGroup' : 'docsAdmin.newPage')" :close-on-escape="!busy" @close="!busy && (dialogOpen = false)">
      <form id="docs-node-form" class="space-y-4" @submit.prevent="submitNode">
        <div><label for="docs-title-zh" class="input-label">{{ t('docsAdmin.chineseTitle') }}</label><input id="docs-title-zh" v-model="form.title_zh" class="input" maxlength="200" required /></div>
        <div><label for="docs-title-en" class="input-label">{{ t('docsAdmin.englishTitle') }}</label><input id="docs-title-en" v-model="form.title_en" class="input" maxlength="200" /></div>
        <template v-if="form.kind === 'page'">
          <div><label for="docs-slug" class="input-label">{{ t('docsAdmin.slug') }}</label><input id="docs-slug" v-model="form.slug" class="input font-mono" maxlength="96" pattern="[a-z0-9]+(-[a-z0-9]+)*" required /><p class="input-hint">{{ t('docsAdmin.slugHint') }}</p></div>
          <div><label for="docs-parent" class="input-label">{{ t('docsAdmin.parent') }}</label><select id="docs-parent" v-model="form.parent" class="input"><option value="">{{ t('docsAdmin.rootDocuments') }}</option><option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ nodeTitle(group) }}</option></select></div>
        </template>
      </form>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="dialogOpen = false">{{ t('common.cancel') }}</button><button form="docs-node-form" class="btn btn-primary" :disabled="busy">{{ t(busy ? 'docsAdmin.saving' : 'docsAdmin.save') }}</button></div></template>
    </BaseDialog>
    <ConfirmDialog :show="Boolean(deleting)" :title="t('docsAdmin.deleteTitle')" :message="t('docsAdmin.deleteMessage', { title: deleting ? nodeTitle(deleting) : '' })" danger @confirm="remove" @cancel="!busy && (deleting = null)" />
    <ConfirmDialog :show="leaveOpen" :title="t('docsAdmin.discardTitle')" :message="t('docsAdmin.directoryDiscard')" :confirm-text="t('docsAdmin.discard')" :cancel-text="t('docsAdmin.stay')" @confirm="finishLeave(true)" @cancel="finishLeave(false)" />
  </AppLayout>
</template>

<style scoped>
.docs-management-grid { display: grid; grid-template-columns: 248px minmax(0, 1fr); align-items: start; gap: 20px; }
.docs-folder { @apply flex min-h-9 w-full items-center justify-between gap-2 rounded-md px-3 py-2 text-left text-sm text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700; }
.docs-folder.selected { @apply bg-gray-100 font-medium text-gray-900 dark:bg-dark-700 dark:text-white; }
.docs-icon { @apply inline-flex h-8 w-8 flex-none items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-30 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-white; }
@media (max-width: 1100px) { .docs-management-grid { grid-template-columns: 1fr; } }
</style>

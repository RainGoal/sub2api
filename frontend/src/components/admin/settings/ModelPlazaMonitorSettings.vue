<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import {
  modelPlazaMonitorAPI,
  type PlazaMonitorCandidates,
  type PlazaMonitorConfig,
  type PlazaMonitorModelConfig,
  type PlazaMonitorCandidateModel,
  type PlazaMonitorStatusResponse
} from '@/api/admin/modelPlazaMonitor'

const { t, locale } = useI18n()
const prefix = 'admin.settings.features.modelPlaza.monitor.'
const show = ref(false)
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const running = ref(false)
const error = ref('')
const notice = ref('')
const candidates = ref<PlazaMonitorCandidates>({ groups: [], api_keys: [] })
const draft = ref<PlazaMonitorConfig>({ version: 0, enabled: false, default_interval_seconds: 300, groups: [] })
const snapshot = ref('')
const activeID = ref<number | null>(null)
const search = ref('')
const results = ref<PlazaMonitorStatusResponse['items']>([])
let controller: AbortController | undefined
let statusController: AbortController | undefined
let statusTimer: ReturnType<typeof setTimeout> | undefined
let refreshGeneration = 0
const busy = computed(() => saving.value || running.value)
const active = computed(() => draft.value.groups.find(group => group.group_id === activeID.value))
const availableGroups = computed(() => {
  const groups = [...candidates.value.groups]
  for (const group of draft.value.groups) {
    if (!groups.some(item => item.id === group.group_id)) {
      groups.push({ id: group.group_id, name: t(prefix + 'missingGroup', { id: group.group_id }), platform: '', models: [] })
    }
  }
  return groups
})
const activeCandidate = computed(() => candidates.value.groups.find(group => group.id === activeID.value))
const availableKeys = computed(() => candidates.value.api_keys.filter(key => key.group_id === activeID.value))
const activeModels = computed(() => {
  const models = [...(activeCandidate.value?.models ?? [])]
  for (const model of active.value?.models ?? []) {
    if (!models.some(item => sameModel(item, model))) models.push({ ...model, supported: false })
  }
  const term = search.value.trim().toLowerCase()
  return models.filter(model => (model.name + ' ' + model.platform).toLowerCase().includes(term))
})

function payload(): PlazaMonitorConfig {
  return {
    ...draft.value,
    groups: draft.value.groups.filter(group => group.enabled || group.models.length > 0 || group.probe_api_key_id > 0)
  }
}
const dirty = computed(() => loaded.value && JSON.stringify(payload()) !== snapshot.value)
const dailyRequests = computed(() => {
  if (!draft.value.enabled) return 0
  return draft.value.groups.filter(group => group.enabled).reduce((total, group) => total + group.models.reduce((count, model) => {
    const interval = model.interval_seconds || group.interval_seconds || draft.value.default_interval_seconds
    return count + (validInterval(interval) ? 86400 / interval : 0)
  }, 0), 0)
})
const canRun = computed(() => loaded.value && !dirty.value && !busy.value && draft.value.enabled &&
  !!active.value?.enabled && !!active.value.models.length && !!activeCandidate.value)

function sameModel(left: { platform: string; name: string }, right: { platform: string; name: string }): boolean {
  return left.platform === right.platform && left.name === right.name
}
function selected(model: PlazaMonitorCandidateModel): PlazaMonitorModelConfig | undefined {
  return active.value?.models.find(item => sameModel(item, model))
}
function validInterval(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 86400
}
function selectGroup(id: number): void {
  stopStatusRefresh()
  results.value = []
  activeID.value = id
  search.value = ''
  if (!draft.value.groups.some(group => group.group_id === id)) {
    draft.value.groups.push({ group_id: id, enabled: false, interval_seconds: draft.value.default_interval_seconds, probe_api_key_id: 0, models: [] })
  }
  if (!dirty.value && draft.value.enabled && active.value?.enabled) void loadStatus(id)
}
function selectModel(model: PlazaMonitorCandidateModel, checked: boolean): void {
  if (!active.value) return
  if (checked && model.supported && !selected(model)) {
    active.value.models.push({ platform: model.platform, name: model.name, interval_seconds: 0, api_mode: 'chat_completions' })
  } else if (!checked) {
    active.value.models = active.value.models.filter(item => !sameModel(item, model))
  }
}
function setModelInterval(model: PlazaMonitorCandidateModel, value: string): void {
  const item = selected(model)
  if (item) item.interval_seconds = value === '' ? 0 : Number(value)
}
function formatTime(value: string | null | undefined): string {
  if (!value || !Number.isFinite(Date.parse(value))) return t(prefix + 'notChecked')
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value))
}
function modelResult(model: PlazaMonitorCandidateModel) {
  return results.value.find(item => item.platform === model.platform && item.model === model.name && item.group_id === activeID.value)
}
function failure(cause: unknown, fallback: string): string {
  const status = cause && typeof cause === 'object' ? (cause as { status?: number; response?: { status?: number } }) : undefined
  if (status?.status === 403 || status?.response?.status === 403) return t(prefix + 'forbidden')
  if (status?.status === 409 || status?.response?.status === 409) return t(prefix + 'conflict')
  return t(prefix + fallback)
}

async function load(): Promise<void> {
  stopStatusRefresh()
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  loaded.value = false
  error.value = ''
  notice.value = ''
  try {
    const [config, options] = await Promise.all([
      modelPlazaMonitorAPI.config(controller.signal), modelPlazaMonitorAPI.candidates(controller.signal)
    ])
    if (controller.signal.aborted) return
    candidates.value = { groups: options.groups ?? [], api_keys: options.api_keys ?? [] }
    draft.value = { ...config, groups: (config.groups ?? []).map(group => ({ ...group, models: group.models ?? [] })) }
    snapshot.value = JSON.stringify(payload())
    loaded.value = true
    const first = availableGroups.value.find(group => group.id === activeID.value) ?? availableGroups.value[0]
    if (first) selectGroup(first.id)
    else activeID.value = null
  } catch (cause) {
    if (!controller.signal.aborted) error.value = failure(cause, 'loadFailed')
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}
async function loadStatus(groupID: number): Promise<void> {
  statusController?.abort()
  const request = new AbortController()
  statusController = request
  try {
    const status = await modelPlazaMonitorAPI.status(groupID, request.signal)
    if (!request.signal.aborted && activeID.value === groupID) results.value = status.items ?? []
  } catch {
    // The editable configuration remains available when status refresh fails.
  }
}
function stopStatusRefresh(): void {
  refreshGeneration++
  clearTimeout(statusTimer)
  statusTimer = undefined
  statusController?.abort()
}
function refreshRunStatus(groupID: number, deadline: number, generation: number): void {
  if (generation !== refreshGeneration || !show.value || dirty.value ||
    activeID.value !== groupID || !draft.value.enabled || !active.value?.enabled || Date.now() >= deadline) return
  statusTimer = setTimeout(async () => {
    if (generation !== refreshGeneration || !show.value || dirty.value || activeID.value !== groupID || Date.now() >= deadline) return
    await loadStatus(groupID)
    refreshRunStatus(groupID, deadline, generation)
  }, 2000)
}
function validationError(): string {
  if (!validInterval(draft.value.default_interval_seconds)) return t(prefix + 'invalidInterval')
  for (const group of draft.value.groups) {
    if (!validInterval(group.interval_seconds)) return t(prefix + 'invalidInterval')
    if (group.models.some(model => model.interval_seconds != null && model.interval_seconds !== 0 && !validInterval(model.interval_seconds))) return t(prefix + 'invalidInterval')
    if (!draft.value.enabled || !group.enabled) continue
    const candidate = candidates.value.groups.find(item => item.id === group.group_id)
    if (!candidate) return t(prefix + 'invalidGroup')
    if (!group.models.length) return t(prefix + 'selectModels')
    if (!candidates.value.api_keys.some(key => key.id === group.probe_api_key_id && key.group_id === group.group_id)) return t(prefix + 'selectKey')
    if (group.models.some(model => !candidate.models.some(item => item.supported && sameModel(item, model)))) return t(prefix + 'unsupportedSelection')
  }
  return ''
}
async function save(): Promise<void> {
  if (busy.value || !loaded.value) return
  error.value = validationError()
  notice.value = ''
  if (error.value) return
  saving.value = true
  try {
    const saved = await modelPlazaMonitorAPI.save(JSON.parse(JSON.stringify(payload())))
    draft.value = { ...saved, groups: (saved.groups ?? []).map(group => ({ ...group, models: group.models ?? [] })) }
    snapshot.value = JSON.stringify(payload())
    if (activeID.value != null) selectGroup(activeID.value)
    notice.value = t(prefix + 'saved')
  } catch (cause) {
    error.value = failure(cause, 'saveFailed')
  } finally {
    saving.value = false
  }
}
async function run(): Promise<void> {
  if (!canRun.value || activeID.value == null) return
  stopStatusRefresh()
  const groupID = activeID.value
  running.value = true
  error.value = ''
  notice.value = ''
  try {
    const response = await modelPlazaMonitorAPI.run(groupID)
    if (activeID.value === groupID) results.value = response.items ?? []
    notice.value = t(prefix + 'runQueued')
    refreshRunStatus(groupID, Date.now() + 35000, refreshGeneration)
  } catch (cause) {
    error.value = failure(cause, 'runFailed')
  } finally {
    running.value = false
  }
}
function open(): void {
  show.value = true
  if (!loaded.value) void load()
  else if (activeID.value != null) selectGroup(activeID.value)
}
function close(): void {
  if (!busy.value) {
    show.value = false
    stopStatusRefresh()
  }
}
onBeforeUnmount(() => { controller?.abort(); stopStatusRefresh() })
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 pt-5 dark:border-dark-700">
    <div>
      <p class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t(prefix + 'title') }}</p>
      <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t(prefix + 'description') }}</p>
    </div>
    <button type="button" data-test="monitor-open" class="btn btn-secondary btn-sm" @click="open">{{ t(prefix + 'configure') }}</button>
  </div>
  <BaseDialog :show="show" :title="t(prefix + 'title')" width="extra-wide" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
    <div class="space-y-4">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'probeHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-green-700 dark:text-green-400">{{ notice }}</p>
      <p v-if="loading" role="status" class="py-6 text-center text-sm text-gray-500">{{ t(prefix + 'loading') }}</p>
      <fieldset v-else-if="loaded" :disabled="busy" class="min-w-0 space-y-5">
        <div class="flex flex-wrap items-center gap-x-8 gap-y-4">
          <label class="flex items-center gap-3 text-sm font-medium"><span>{{ t(prefix + 'enabled') }}</span><Toggle v-model="draft.enabled" data-test="monitor-enabled" :aria-label="t(prefix + 'enabled')" /></label>
          <label class="flex items-center gap-2 text-sm">{{ t(prefix + 'defaultInterval') }}<input v-model.number="draft.default_interval_seconds" data-test="monitor-default-interval" class="input w-28" type="number" min="1" max="86400" step="1" :aria-label="t(prefix + 'defaultInterval')" /></label>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(prefix + 'dailyRequests', { count: Math.ceil(dailyRequests).toLocaleString(locale) }) }}</p>
        </div>
        <p v-if="!availableGroups.length" class="py-6 text-center text-sm text-gray-500">{{ t(prefix + 'emptyGroups') }}</p>
        <div v-else class="grid min-w-0 gap-5 md:grid-cols-[180px_minmax(0,1fr)]">
          <nav class="flex gap-1 overflow-x-auto md:max-h-[55vh] md:flex-col md:overflow-y-auto" :aria-label="t(prefix + 'groups')">
            <button v-for="group in availableGroups" :key="group.id" type="button" :data-test="`monitor-group-${group.id}`" class="flex min-w-[140px] items-center justify-between gap-2 rounded-md px-3 py-2 text-left text-sm md:min-w-0" :class="activeID === group.id ? 'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" :aria-pressed="activeID === group.id" @click="selectGroup(group.id)">
              <span class="min-w-0 break-words">{{ group.name }}</span><span class="shrink-0 font-mono text-xs">{{ draft.groups.find(item => item.group_id === group.id)?.models.length ?? 0 }}</span>
            </button>
          </nav>
          <div v-if="active" class="min-w-0 space-y-4">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <label class="flex items-center gap-3 text-sm font-medium"><span>{{ t(prefix + 'groupEnabled') }}</span><Toggle v-model="active.enabled" data-test="monitor-group-enabled" :aria-label="t(prefix + 'groupEnabled')" /></label>
              <button type="button" data-test="monitor-run" class="btn btn-secondary btn-sm" :disabled="!canRun" @click="run">{{ running ? t(prefix + 'running') : t(prefix + 'run') }}</button>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block text-sm">{{ t(prefix + 'groupInterval') }}<input v-model.number="active.interval_seconds" data-test="monitor-group-interval" class="input mt-1" type="number" min="1" max="86400" step="1" /></label>
              <label class="block min-w-0 text-sm">{{ t(prefix + 'key') }}<select v-model.number="active.probe_api_key_id" data-test="monitor-key" class="input mt-1 min-w-0"><option :value="0">{{ t(prefix + 'selectKey') }}</option><option v-if="active.probe_api_key_id && !availableKeys.some(key => key.id === active?.probe_api_key_id)" :value="active.probe_api_key_id" disabled>{{ t(prefix + 'missingKey', { id: active.probe_api_key_id }) }}</option><option v-for="key in availableKeys" :key="key.id" :value="key.id">{{ key.name }} · #{{ key.id }}</option></select></label>
            </div>
            <p v-if="!availableKeys.length" class="text-xs text-amber-700 dark:text-amber-300">{{ t(prefix + 'noKeys') }}</p>
            <input v-model="search" data-test="monitor-search" type="search" class="input" :placeholder="t(prefix + 'search')" :aria-label="t(prefix + 'search')" />
            <p v-if="!activeModels.length" class="py-4 text-center text-sm text-gray-500">{{ t(prefix + 'emptyModels') }}</p>
            <div v-else class="max-h-[42vh] overflow-y-auto divide-y divide-gray-100 border-y border-gray-100 dark:divide-dark-700 dark:border-dark-700">
              <div v-for="model in activeModels" :key="model.platform + ':' + model.name" class="grid min-w-0 gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_130px] sm:gap-4">
                <div class="min-w-0">
                  <label class="flex items-start gap-2 text-sm"><input type="checkbox" :data-test="`monitor-model-${model.platform}-${model.name}`" class="mt-1 shrink-0" :checked="!!selected(model)" :disabled="!model.supported && !selected(model)" @change="selectModel(model, ($event.target as HTMLInputElement).checked)" /><span class="min-w-0 break-all font-mono">{{ model.name }}<small class="ml-2 text-xs text-gray-500">{{ model.platform }}</small></span></label>
                  <p v-if="!model.supported" class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ t(prefix + 'unsupported') }}</p>
                  <p v-if="selected(model) && modelResult(model)" class="mt-1 break-words text-xs text-gray-500">{{ t(prefix + 'lastCheck', { time: formatTime(modelResult(model)?.last_checked_at) }) }} · {{ t(prefix + 'nextCheck', { time: formatTime(modelResult(model)?.next_check_at) }) }}</p>
                  <label v-if="selected(model) && model.platform === 'openai'" class="mt-2 flex flex-wrap items-center gap-2 text-xs text-gray-500">{{ t(prefix + 'protocol') }}<select v-model="selected(model)!.api_mode" class="input w-auto py-1 text-xs" :aria-label="t(prefix + 'protocol')"><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label>
                </div>
                <label v-if="selected(model)" class="block text-xs text-gray-500">{{ t(prefix + 'modelInterval') }}<input :value="selected(model)?.interval_seconds || ''" :data-test="`monitor-interval-${model.platform}-${model.name}`" class="input mt-1" type="number" min="1" max="86400" step="1" :placeholder="t(prefix + 'inherit', { seconds: active.interval_seconds })" @input="setModelInterval(model, ($event.target as HTMLInputElement).value)" /></label>
              </div>
            </div>
          </div>
        </div>
      </fieldset>
    </div>
    <template #footer>
      <p v-if="dirty" class="mr-auto text-xs text-amber-700 dark:text-amber-300">{{ t(prefix + 'unsaved') }}</p>
      <button type="button" data-test="monitor-reload" class="btn btn-secondary" :disabled="loading || busy" @click="load">{{ t(prefix + 'reload') }}</button>
      <button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ t(prefix + 'close') }}</button>
      <button type="button" data-test="monitor-save" class="btn btn-primary" :disabled="!loaded || loading || busy || !dirty" @click="save">{{ saving ? t(prefix + 'saving') : t(prefix + 'save') }}</button>
    </template>
  </BaseDialog>
</template>

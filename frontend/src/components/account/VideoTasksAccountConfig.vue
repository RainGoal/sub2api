<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="video-tasks-account-config">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <span class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.videoTasks.connection') }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="!templates || loading" @click="applyPreset">
        {{ t('admin.videoTasks.bblabuPreset') }}
      </button>
    </div>
    <div>
      <label class="input-label" :for="`${id}-supplier`">{{ t('admin.videoTasks.supplierName') }}</label>
      <input :id="`${id}-supplier`" :value="supplierName" class="input" maxlength="100" data-testid="video-supplier-name"
        @input="emit('update:supplierName', ($event.target as HTMLInputElement).value)" />
      <p class="input-hint">{{ t('admin.videoTasks.explicitBaseUrl') }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <button type="button" class="btn btn-secondary btn-sm" :disabled="discovering || !canDiscover" data-testid="video-discover-models" @click="discover">
        {{ discovering ? t('admin.videoTasks.discovering') : t('admin.videoTasks.discover') }}
      </button>
      <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.videoTasks.discoveryHint') }}</span>
    </div>
    <p v-if="savedConnectionChanged" class="input-hint">{{ t('admin.videoTasks.saveBeforeDiscovery') }}</p>
    <p v-if="discoveryError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ discoveryError }}</p>
    <div v-if="discovered !== null" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="video-discovered-models">
      <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.videoTasks.discoveredCount', { count: discovered.length }) }}</p>
      <div class="flex max-h-28 flex-wrap gap-1 overflow-y-auto">
        <span v-for="model in discovered" :key="model" class="rounded bg-gray-100 px-2 py-1 font-mono text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-200">{{ model }}</span>
      </div>
      <p v-if="discovered.length === 0" class="input-hint">{{ t('admin.videoTasks.noDiscoveredModels') }}</p>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <label class="input-label mb-0" :for="`${id}-search`">{{ t('admin.videoTasks.enabledModels') }}</label>
      <span class="font-mono text-xs text-gray-500">{{ modelValue?.length || 0 }} / {{ models.length }}</span>
    </div>
    <input :id="`${id}-search`" v-model="search" type="search" class="input" :placeholder="t('admin.videoTasks.search')" />
    <p v-if="loading" class="input-hint" role="status">{{ t('admin.videoTasks.loadingTemplates') }}</p>
    <div v-else-if="templateError" class="space-y-2" role="alert">
      <p class="text-sm text-red-600 dark:text-red-400">{{ templateError }}</p>
      <button type="button" class="btn btn-secondary btn-sm" @click="loadTemplates">{{ t('admin.videoTasks.retry') }}</button>
    </div>
    <div v-else class="grid max-h-80 gap-2 overflow-y-auto sm:grid-cols-2" data-testid="video-capability-models">
      <label v-for="model in filteredModels" :key="model.model" class="flex items-start gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
        <input type="checkbox" class="checkbox mt-0.5" :disabled="!model.enabled" :checked="isSelected(model.model)"
          :data-testid="`video-model-${model.model}`" @change="toggleModel(model)" />
        <span class="min-w-0">
          <span class="block break-all font-mono text-xs text-gray-800 dark:text-gray-100">{{ model.model }}</span>
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ modelSummary(model) }}</span>
        </span>
      </label>
      <p v-if="filteredModels.length === 0" class="input-hint">{{ t('admin.videoTasks.noMatchingModels') }}</p>
    </div>
    <p v-if="!modelValue?.length" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.videoTasks.chooseModels') }}</p>
    <details class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
      <summary class="cursor-pointer text-sm text-gray-700 dark:text-gray-200">{{ t('admin.videoTasks.capabilityJSON') }}</summary>
      <p class="input-hint mb-2">{{ t('admin.videoTasks.capabilityHint') }}</p>
      <textarea :value="draft" rows="12" class="input font-mono text-xs" spellcheck="false" :aria-label="t('admin.videoTasks.capabilityJSON')"
        data-testid="video-capability-json" @focus="editing = true" @input="updateDraft" @blur="editing = false" />
      <p v-if="!jsonValid" class="mt-2 text-sm text-red-600 dark:text-red-400" role="alert">{{ t('admin.videoTasks.invalidJSON') }}</p>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { discoverVideoModels, getVideoModelTemplates, type VideoModelCapability, type VideoModelTemplates } from '@/api/admin/videoTasks'
import type { SeedanceModelSelection } from './seedanceModelRestriction'

const props = defineProps<{
  id: string; modelValue: SeedanceModelSelection; capabilities: VideoModelCapability[]; supplierName: string
  baseUrl: string; apiKey: string; accountId?: number; proxyId?: number | null; savedBaseUrl?: string; savedProtocol?: string; savedProxyId?: number | null
}>()
const emit = defineEmits<{
  'update:modelValue': [value: SeedanceModelSelection]
  'update:capabilities': [value: VideoModelCapability[]]
  'update:supplierName': [value: string]
  'update:baseUrl': [value: string]
  validity: [value: boolean]
}>()
const { t } = useI18n()
const templates = ref<VideoModelTemplates | null>(null)
const loading = ref(false)
const templateError = ref('')
const discoveryError = ref('')
const discovering = ref(false)
const discovered = ref<string[] | null>(null)
const search = ref('')
const draft = ref(JSON.stringify(props.capabilities, null, 2))
const editing = ref(false)
const jsonValid = ref(true)
const models = computed(() => {
  const merged = new Map((templates.value?.models || []).map((model) => [model.model.toLowerCase(), model]))
  for (const model of props.capabilities) merged.set(model.model.toLowerCase(), model)
  return [...merged.values()]
})
const filteredModels = computed(() => models.value.filter((model) => model.model.toLowerCase().includes(search.value.trim().toLowerCase())))
const savedConnectionChanged = computed(() => !!props.accountId && !props.apiKey.trim() && (
  props.baseUrl.trim().replace(/\/$/, '') !== (props.savedBaseUrl || '').replace(/\/$/, '') ||
  props.savedProtocol !== 'video_tasks_json_v1' || (props.proxyId || null) !== (props.savedProxyId || null)
))
const canDiscover = computed(() => !!props.baseUrl.trim() && (!!props.apiKey.trim() || !!props.accountId) && !savedConnectionChanged.value)

watch(() => props.capabilities, (value) => { if (!editing.value) { draft.value = JSON.stringify(value, null, 2); jsonValid.value = true; emit('validity', true) } }, { deep: true })
watch(() => [props.baseUrl, props.apiKey, props.accountId], () => { discovered.value = null; discoveryError.value = '' })

async function loadTemplates() {
  loading.value = true; templateError.value = ''
  try { templates.value = await getVideoModelTemplates() }
  catch { templateError.value = t('admin.videoTasks.templatesFailed') }
  finally { loading.value = false }
}

function applyPreset() {
  if (!templates.value) return
  emit('update:supplierName', templates.value.preset.name)
  emit('update:baseUrl', templates.value.preset.base_url)
}

async function discover() {
  if (!canDiscover.value || discovering.value) return
  discovering.value = true; discoveryError.value = ''
  const connection = JSON.stringify([props.baseUrl, props.apiKey, props.accountId])
  try {
    const result = await discoverVideoModels({ baseUrl: props.baseUrl, apiKey: props.apiKey, accountId: props.accountId, proxyId: props.proxyId })
    if (connection === JSON.stringify([props.baseUrl, props.apiKey, props.accountId])) discovered.value = result
  } catch { discoveryError.value = t('admin.videoTasks.discoveryFailed') }
  finally { discovering.value = false }
}

function isSelected(model: string): boolean {
  return props.modelValue?.some((value) => value.toLowerCase() === model.toLowerCase()) || false
}

function toggleModel(model: VideoModelCapability) {
  if (!model.enabled) return
  const selected = (props.modelValue || []).filter((value) => value.toLowerCase() !== model.model.toLowerCase())
  if (!isSelected(model.model)) {
    selected.push(model.model)
    if (!props.capabilities.some((value) => value.model.toLowerCase() === model.model.toLowerCase())) {
      emit('update:capabilities', [...props.capabilities, { ...model }])
    }
  }
  emit('update:modelValue', selected)
}

function modelSummary(model: VideoModelCapability): string {
  const resolution = model.resolutions?.join(' / ') || t('admin.videoTasks.fixedSpecification')
  const unit = model.billing_unit === 'per_video' ? t('admin.videoTasks.perVideo') : t('admin.videoTasks.perSecond')
  return `${resolution} · ${unit}`
}

function updateDraft(event: Event) {
  draft.value = (event.target as HTMLTextAreaElement).value
  try {
    const parsed: unknown = JSON.parse(draft.value)
    if (!Array.isArray(parsed) || parsed.some((model) => !model || typeof model !== 'object' || typeof model.model !== 'string')) throw new Error('invalid')
    jsonValid.value = true
    emit('update:capabilities', parsed as VideoModelCapability[])
  } catch { jsonValid.value = false }
  emit('validity', jsonValid.value)
}

onMounted(() => {
  if (props.modelValue === null) emit('update:modelValue', [])
  void loadTemplates()
})
</script>

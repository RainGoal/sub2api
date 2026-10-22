<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="video-tasks-account-config">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <span class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.videoTasks.connection') }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="!templates || loading" @click="applyPreset">
        {{ t(protocol === 'zhiqi_v1' ? 'admin.videoTasks.zhiqiPreset' : 'admin.videoTasks.bblabuPreset') }}
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
    <div v-if="currentDiscovery" class="space-y-1 text-xs text-gray-500 dark:text-gray-400" data-testid="video-discovered-models">
      <p>{{ t('admin.videoTasks.discoveredCount', { count: currentDiscovery.models.length }) }}</p>
      <p>{{ t('admin.videoTasks.lastSync', { time: new Date(currentDiscovery.synced_at).toLocaleString() }) }}</p>
      <p>{{ t('admin.videoTasks.discoverySaveHint') }}</p>
      <p v-if="!currentDiscovery.models.length" class="input-hint">{{ t('admin.videoTasks.noDiscoveredModels') }}</p>
    </div>
    <p v-else class="input-hint">{{ t('admin.videoTasks.syncFirst') }}</p>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <label class="input-label mb-0" :for="`${id}-search`">{{ t('admin.videoTasks.enabledModels') }}</label>
      <span class="font-mono text-xs text-gray-500">{{ modelValue?.length || 0 }} / {{ models.length }}</span>
    </div>
    <input :id="`${id}-search`" v-model="search" type="search" class="input" :placeholder="t('admin.videoTasks.search')" />
    <p v-if="loading" class="input-hint" role="status">{{ t('admin.videoTasks.loadingTemplates') }}</p>
    <div v-if="templateError" class="space-y-2" role="alert">
      <p class="text-sm text-red-600 dark:text-red-400">{{ templateError }}</p>
      <button type="button" class="btn btn-secondary btn-sm" @click="loadTemplates">{{ t('admin.videoTasks.retry') }}</button>
    </div>
    <div class="grid max-h-80 gap-2 overflow-y-auto sm:grid-cols-2" data-testid="video-capability-models">
      <div v-for="model in filteredModels" :key="model.name" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600"
        :data-testid="`video-model-row-${model.name}`" :data-status="model.status">
        <label class="flex items-start gap-2">
          <input type="checkbox" class="checkbox mt-0.5" :disabled="!isSelected(model.name) && !canSelect(model)" :checked="isSelected(model.name)"
            :data-testid="`video-model-${model.name}`" @change="toggleModel(model)" />
          <span class="min-w-0">
            <span class="block break-all font-mono text-xs text-gray-800 dark:text-gray-100">{{ model.name }}</span>
            <span v-if="model.status !== 'available'" class="mt-1 block text-xs text-amber-600 dark:text-amber-400">
              {{ t(model.status === 'missing' ? 'admin.videoTasks.missingUpstream' : 'admin.videoTasks.notVerified') }}
            </span>
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
              {{ model.capability ? modelSummary(model.capability) : t('admin.videoTasks.capabilityRequired') }}
            </span>
            <span v-if="model.capability && !model.capability.enabled" class="block text-xs text-amber-600 dark:text-amber-400">{{ t('admin.videoTasks.capabilityDisabled') }}</span>
          </span>
        </label>
        <button type="button" class="btn btn-secondary btn-sm" :data-testid="`video-configure-${model.name}`"
          @click="editingModel = model.name">
          {{ t(model.capability ? 'admin.videoTasks.editCapability' : 'admin.videoTasks.configure') }}
        </button>
      </div>
      <p v-if="filteredModels.length === 0" class="input-hint">{{ t('admin.videoTasks.noMatchingModels') }}</p>
    </div>
    <VideoModelCapabilityEditor v-if="editingModel" :key="editingModel" :model="editingModel"
      :capability="configuredCapability(editingModel)" :templates="templates?.models || []" :protocol="protocol"
      @save="saveCapability" @cancel="editingModel = ''" />
    <p v-if="capabilityError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ capabilityError }}</p>
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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { discoverVideoModels, getVideoModelTemplates, type VideoModelCapability, type VideoModelTemplates, type VideoModelDiscovery } from '@/api/admin/videoTasks'
import type { SeedanceModelSelection } from './seedanceModelRestriction'
import VideoModelCapabilityEditor from './VideoModelCapabilityEditor.vue'
import { validateVideoModelCapability } from './videoModelCapabilityForm'
import type { CapabilityVideoProviderID } from '@/constants/videoProviders'

const props = withDefaults(defineProps<{
  id: string; modelValue: SeedanceModelSelection; capabilities: VideoModelCapability[]; supplierName: string
  baseUrl: string; apiKey: string; accountId?: number; proxyId?: number | null; savedBaseUrl?: string; savedProtocol?: string; savedProxyId?: number | null
  discovery?: VideoModelDiscovery | null
  protocol?: CapabilityVideoProviderID
  modelMapping?: Record<string, string>
}>(), { protocol: 'video_tasks_json_v1' })
const emit = defineEmits<{
  'update:modelValue': [value: SeedanceModelSelection]
  'update:capabilities': [value: VideoModelCapability[]]
  'update:supplierName': [value: string]
  'update:baseUrl': [value: string]
  'update:discovery': [value: VideoModelDiscovery | null]
  validity: [value: boolean]
}>()
const { t } = useI18n()
const templates = ref<VideoModelTemplates | null>(null)
const loading = ref(false)
const templateError = ref('')
const discoveryError = ref('')
const discovering = ref(false)
const currentDiscovery = ref<VideoModelDiscovery | null>(matchingDiscovery(props.discovery))
const editingModel = ref('')
const capabilityError = ref('')
let requestSequence = 0
let templateSequence = 0
const search = ref('')
const draft = ref(JSON.stringify(props.capabilities, null, 2))
const editing = ref(false)
const jsonValid = ref(validCapabilities(props.capabilities))
interface ModelCandidate {
  name: string
  status: 'available' | 'missing' | 'unverified'
  capability?: VideoModelCapability
}
const models = computed<ModelCandidate[]>(() => {
  const available = new Set(currentDiscovery.value?.models || [])
  const names = new Set([...available, ...(props.modelValue || []), ...Object.keys(props.modelMapping || {}), ...props.capabilities.map((model) => model.model)])
  return [...names].map((name) => ({
    name, capability: configuredCapability(name),
    status: available.has(configuredModelTarget(name)) ? 'available' : currentDiscovery.value ? 'missing' : 'unverified'
  }))
})
const filteredModels = computed(() => models.value.filter((model) => model.name.toLowerCase().includes(search.value.trim().toLowerCase())))
const savedConnectionChanged = computed(() => !!props.accountId && !props.apiKey.trim() && (
  props.baseUrl.trim().replace(/\/$/, '') !== (props.savedBaseUrl || '').replace(/\/$/, '') ||
  props.savedProtocol !== props.protocol || (props.proxyId || null) !== (props.savedProxyId || null)
))
const canDiscover = computed(() => !!props.baseUrl.trim() && (!!props.apiKey.trim() || !!props.accountId) && !savedConnectionChanged.value)

watch(() => props.capabilities, (value) => {
  if (!editing.value) {
    draft.value = JSON.stringify(value, null, 2)
    jsonValid.value = validCapabilities(value)
  }
}, { deep: true })
watch(jsonValid, (value) => emit('validity', value), { immediate: true })
watch(() => props.discovery, (value) => { currentDiscovery.value = matchingDiscovery(value) })
watch(() => [props.baseUrl, props.apiKey, props.accountId, props.proxyId, props.savedProtocol, props.protocol], () => {
  requestSequence++
  discovering.value = false
  currentDiscovery.value = null
  discoveryError.value = ''
  editingModel.value = ''
  capabilityError.value = ''
  emit('update:discovery', null)
})
watch(() => props.protocol, () => {
  templates.value = null
  jsonValid.value = validCapabilities(props.capabilities)
  void loadTemplates()
})

function matchingDiscovery(value: VideoModelDiscovery | null | undefined): VideoModelDiscovery | null {
  return value?.protocol === props.protocol && value.base_url.replace(/\/+$/, '') === props.baseUrl.trim().replace(/\/+$/, '') &&
    value.proxy_id === (props.proxyId || null) ? value : null
}

function configuredCapability(model: string): VideoModelCapability | undefined {
  const direct = props.capabilities.find((capability) => capability.model.toLowerCase() === model.toLowerCase())
  if (direct) return direct.model === model ? direct : { ...direct, model }
  const target = props.modelMapping?.[model]
  if (!target) return undefined
  const inherited = [...props.capabilities, ...(templates.value?.models || [])].find((capability) => capability.model.toLowerCase() === target.toLowerCase())
  return inherited ? { ...inherited, model, upstream_model: inherited.upstream_model || target } : undefined
}

function configuredModelTarget(model: string): string {
  const capability = configuredCapability(model)
  return capability ? capability.upstream_model || model : props.modelMapping?.[model] || model
}

async function loadTemplates() {
  const sequence = ++templateSequence
  loading.value = true; templateError.value = ''
  try {
    const result = await getVideoModelTemplates(props.protocol)
    if (sequence !== templateSequence) return
    if (result.protocol !== props.protocol) throw new Error('Mismatched video protocol catalog')
    templates.value = result
  }
  catch { if (sequence === templateSequence) templateError.value = t('admin.videoTasks.templatesFailed') }
  finally { if (sequence === templateSequence) loading.value = false }
}

function applyPreset() {
  if (!templates.value) return
  emit('update:supplierName', templates.value.preset.name)
  emit('update:baseUrl', templates.value.preset.base_url)
}

async function discover() {
  if (!canDiscover.value || discovering.value) return
  discovering.value = true; discoveryError.value = ''
  const sequence = ++requestSequence
  try {
    const result = await discoverVideoModels({ baseUrl: props.baseUrl, apiKey: props.apiKey, accountId: props.accountId, proxyId: props.proxyId, protocol: props.protocol })
    if (sequence !== requestSequence) return
    const snapshot: VideoModelDiscovery = {
      models: [...new Set(result)], synced_at: new Date().toISOString(),
      base_url: props.baseUrl.trim().replace(/\/+$/, ''), proxy_id: props.proxyId || null, protocol: props.protocol
    }
    currentDiscovery.value = snapshot
    emit('update:discovery', snapshot)
  } catch { if (sequence === requestSequence) discoveryError.value = t('admin.videoTasks.discoveryFailed') }
  finally { if (sequence === requestSequence) discovering.value = false }
}

function isSelected(model: string): boolean {
  return props.modelValue?.includes(model) || false
}

function canSelect(model: ModelCandidate): boolean {
  return model.status === 'available' && !!model.capability?.enabled && validateVideoModelCapability(model.capability, props.protocol) === null
}

function toggleModel(model: ModelCandidate) {
  if (!isSelected(model.name) && !canSelect(model)) return
  const selected = (props.modelValue || []).filter((value) => value !== model.name)
  if (!isSelected(model.name)) selected.push(model.name)
  emit('update:modelValue', selected)
}

function saveCapability(capability: VideoModelCapability) {
  capabilityError.value = ''
  if (capability.model !== editingModel.value || validateVideoModelCapability(capability, props.protocol) !== null) return
  const key = capability.model.toLowerCase()
  if (props.modelValue?.some((model) => model !== capability.model && model.toLowerCase() === key)) {
    capabilityError.value = t('admin.videoTasks.modelCaseConflict')
    return
  }
  emit('update:capabilities', [...props.capabilities.filter((model) => model.model.toLowerCase() !== key), capability])
  editingModel.value = ''
}

function modelSummary(model: VideoModelCapability): string {
  if (validateVideoModelCapability(model, props.protocol) !== null) return t('admin.videoTasks.capabilityRequired')
  const resolution = model.resolutions?.join(' / ') || t('admin.videoTasks.fixedSpecification')
  const unit = model.billing_unit === 'per_video' ? t('admin.videoTasks.perVideo') : t('admin.videoTasks.perSecond')
  return `${resolution} · ${unit}`
}

function updateDraft(event: Event) {
  draft.value = (event.target as HTMLTextAreaElement).value
  try {
    const parsed: unknown = JSON.parse(draft.value)
    if (!validCapabilities(parsed)) throw new Error('invalid')
    jsonValid.value = true
    emit('update:capabilities', parsed as VideoModelCapability[])
  } catch { jsonValid.value = false }
}

function validCapabilities(value: unknown): value is VideoModelCapability[] {
  if (!Array.isArray(value) || value.length > 512 || value.some((model) => validateVideoModelCapability(model, props.protocol) !== null)) return false
  return new Set(value.map((model: VideoModelCapability) => model.model.toLowerCase())).size === value.length
}

onMounted(() => {
  if (props.modelValue === null) emit('update:modelValue', [])
  void loadTemplates()
})
onBeforeUnmount(() => { requestSequence++; templateSequence++ })
</script>

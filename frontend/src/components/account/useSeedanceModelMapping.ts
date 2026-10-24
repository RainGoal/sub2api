import { computed, ref, type Ref } from 'vue'
import type { VideoModelCapability, VideoModelDiscovery } from '@/api/admin/videoTasks'
import { isCapabilityVideoProvider, normalizeSeedanceModelID, videoProviderModelOptions, type VideoProviderID } from '@/constants/videoProviders'
import { buildModelMappingObject, type ModelMappingEntry } from '@/composables/useModelWhitelist'
import { buildSeedanceModelMapping, readSeedanceVideoModelMapping, type SeedanceModelSelection } from './seedanceModelRestriction'

export function validSeedanceModelMappings(entries: ModelMappingEntry[]): boolean {
  const ids = entries.flatMap(entry => [entry.from.trim(), entry.to.trim()])
  return ids.every(id => !!id && new TextEncoder().encode(id).length <= 128 && !/[\s*]/.test(id) &&
    ![...id].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)) &&
    new Set(entries.map(entry => entry.from.trim().toLowerCase())).size === entries.length
}

function legacyUpstreamModel(provider: VideoProviderID, model: string): string {
  if (isCapabilityVideoProvider(provider)) return model
  const canonical = normalizeSeedanceModelID(model)
  if (provider === 'bblabu_v1') return canonical === 'seedance-2.0' ? 'Seedance-2.0' : canonical === 'seedance-2.5' ? 'Seedance-2.5' : model
  return model.trim().toLowerCase() === 'bytedance/seedance-2.5' ? 'bytedance/seedance-2.5' : canonical
}

// The mapping is the forwarding source of truth; public capability overrides
// follow edits to it, while capabilities stored under supplier IDs stay intact.
export function useSeedanceModelMapping(
  selection: Ref<SeedanceModelSelection>, capabilities: Ref<VideoModelCapability[]>,
  discovery: Ref<VideoModelDiscovery | null>, provider: Ref<VideoProviderID>,
) {
  const entries = ref<ModelMappingEntry[]>([])
  let appliedEntries: ModelMappingEntry[] = []
  const savedMapping = ref<Record<string, string>>({})
  const aliases = computed(() => buildModelMappingObject('mapping', [], entries.value) || {})
  const mapping = computed(() => buildSeedanceModelMapping(selection.value, capabilities.value, {
    ...savedMapping.value, ...aliases.value,
  }))
  const contextMapping = computed(() => ({ ...mapping.value, ...aliases.value }))
  const valid = computed(() => validSeedanceModelMappings(entries.value))
  const upstreamModels = computed(() => [...new Set([
    ...Object.entries(contextMapping.value).map(([from, to]) => from === to ? legacyUpstreamModel(provider.value, to) : to),
    ...(discovery.value?.models || []),
    ...(isCapabilityVideoProvider(provider.value) ? capabilities.value.map(capability => capability.upstream_model || contextMapping.value[capability.model] || capability.model) : []),
    ...(!discovery.value && Object.keys(contextMapping.value).length === 0 ? videoProviderModelOptions(provider.value).map(model => legacyUpstreamModel(provider.value, model.id)) : []),
  ])])

  function load(value?: unknown) {
    savedMapping.value = readSeedanceVideoModelMapping(value)
    entries.value = Object.entries(savedMapping.value).filter(([from, to]) => from !== to).map(([from, to]) => ({ from, to }))
    appliedEntries = entries.value.map(entry => ({ ...entry }))
  }

  function updateEntries(next: ModelMappingEntry[]) {
    const previous = appliedEntries
    entries.value = next
    if (!validSeedanceModelMappings(next)) return
    if (selection.value === null && next.length === 0 && previous.length === 0) return
    const removed = previous.filter(entry => !next.some(value => value.from.trim() === entry.from.trim())).map(entry => entry.from.trim())
    let selected = (selection.value || []).filter(model => !removed.includes(model))
    for (const [index, entry] of next.entries()) {
      const from = entry.from.trim(), to = entry.to.trim()
      if (!previous.some(value => value.from.trim() === from)) {
        selected = selected.filter(model => model !== to || model === from || next.some(value => value.from.trim() === model))
        if (!selected.includes(from)) selected.push(from)
      }
      const old = previous[index]
      capabilities.value = capabilities.value.map(capability => {
        if (capability.model === from) return { ...capability, upstream_model: to === from ? undefined : to }
        if (previous.length === next.length && old && removed.includes(old.from.trim()) && capability.model === old.from.trim()) {
          return { ...capability, model: from, upstream_model: to === from ? undefined : to }
        }
        return capability
      })
    }
    selection.value = selected
    appliedEntries = next.map(entry => ({ ...entry }))
  }

  function updateCapabilities(next: VideoModelCapability[]) {
    for (const capability of next) {
      const before = capabilities.value.find(value => value.model === capability.model)
      if (before && before.upstream_model === capability.upstream_model) continue
      const entry = entries.value.find(value => value.from.trim() === capability.model)
      if (entry) entry.to = capability.upstream_model || capability.model
      else if (capability.upstream_model && capability.upstream_model !== capability.model && selection.value?.includes(capability.model)) {
        entries.value.push({ from: capability.model, to: capability.upstream_model })
      }
    }
    capabilities.value = next
    appliedEntries = entries.value.map(entry => ({ ...entry }))
  }

  return { entries, mapping, contextMapping, valid, upstreamModels, load, updateEntries, updateCapabilities }
}

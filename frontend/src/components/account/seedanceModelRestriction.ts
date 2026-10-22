import { isCapabilityVideoProvider, normalizeSeedanceModelID, videoProviderModelOptions, type VideoProviderID } from '@/constants/videoProviders'
import { readVideoCapabilities, type VideoModelCapability } from '@/api/admin/videoTasks'

// null explicitly means unrestricted; [] is an invalid, empty whitelist.
export type SeedanceModelSelection = string[] | null

export function filterSeedanceModelSelection(
  selection: SeedanceModelSelection,
  provider: VideoProviderID
): SeedanceModelSelection {
  if (isCapabilityVideoProvider(provider)) return selection === null ? null : [...new Set(selection.map((model) => model.trim()).filter(Boolean))]
  if (selection === null) return null
  const selected = new Set(selection.map(normalizeSeedanceModelID))
  return videoProviderModelOptions(provider).filter(({ id }) => selected.has(id)).map(({ id }) => id)
}

export function readSeedanceModelSelection(
  mapping: unknown,
  provider: VideoProviderID
): SeedanceModelSelection {
  if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) return null
  const models = Object.keys(mapping)
  return models.length === 0 ? null : filterSeedanceModelSelection(models, provider)
}

export function buildSeedanceModelMapping(
  selection: SeedanceModelSelection,
  capabilities: VideoModelCapability[] = [],
  savedMapping?: unknown
): Record<string, string> | undefined {
  const previous = readSeedanceVideoModelMapping(savedMapping)
  return selection === null ? undefined : Object.fromEntries(selection.map((model) => {
    const capability = capabilities.find((entry) => entry.model === model)
    return [model, capability ? capability.upstream_model || model : previous[model] || model]
  }))
}

export function readSeedanceVideoModelMapping(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
}

// An API client may leave the supplier target only in model_mapping. Preserve
// that target explicitly when editing a capability stored under the public ID.
export function readSeedanceVideoCapabilities(value: unknown, mapping: unknown): VideoModelCapability[] {
  const aliases = Object.entries(readSeedanceVideoModelMapping(mapping))
  return readVideoCapabilities(value).map((capability) => {
    const alias = aliases.find(([model]) => model.toLowerCase() === capability.model.toLowerCase())
    return alias && alias[0] !== alias[1]
      ? { ...capability, model: alias[0], upstream_model: capability.upstream_model || alias[1] }
      : capability
  })
}

import { normalizeSeedanceModelID, videoProviderModelOptions, type VideoProviderID } from '@/constants/videoProviders'

// null explicitly means unrestricted; [] is an invalid, empty whitelist.
export type SeedanceModelSelection = string[] | null

export function filterSeedanceModelSelection(
  selection: SeedanceModelSelection,
  provider: VideoProviderID
): SeedanceModelSelection {
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
  selection: SeedanceModelSelection
): Record<string, string> | undefined {
  return selection === null ? undefined : Object.fromEntries(selection.map((model) => [model, model]))
}

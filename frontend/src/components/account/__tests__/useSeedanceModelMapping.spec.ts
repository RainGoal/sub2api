import { ref } from 'vue'
import { describe, expect, it } from 'vitest'
import type { VideoModelCapability, VideoModelDiscovery } from '@/api/admin/videoTasks'
import type { VideoProviderID } from '@/constants/videoProviders'
import { readSeedanceModelSelection, type SeedanceModelSelection } from '../seedanceModelRestriction'
import { useSeedanceModelMapping, validSeedanceModelMappings } from '../useSeedanceModelMapping'

function setup(protocol: VideoProviderID, mapping?: Record<string, string>, original: VideoModelCapability[] = []) {
  const selection = ref<SeedanceModelSelection>(readSeedanceModelSelection(mapping, protocol))
  const capabilities = ref(original)
  const discovery = ref<VideoModelDiscovery | null>(null)
  const state = useSeedanceModelMapping(selection, capabilities, discovery, ref(protocol))
  state.load(mapping)
  return { ...state, selection, capabilities, discovery }
}

describe('Seedance account model mapping', () => {
  it.each(['bblabu_v1', 'fflink_v1', 'video_tasks_json_v1', 'zhiqi_v1'] as const)(
    'preserves and edits exact public and upstream IDs for %s', protocol => {
      const state = setup(protocol, { 'Public/Video': 'Seedance-2.0' })
      expect(state.mapping.value).toEqual({ 'Public/Video': 'Seedance-2.0' })
      state.updateEntries([{ from: 'seedance-2.0', to: 'Seedance-2.0' }])
      expect(state.selection.value).toEqual(['seedance-2.0'])
      expect(state.mapping.value).toEqual({ 'seedance-2.0': 'Seedance-2.0' })
      expect(state.upstreamModels.value).toContain('Seedance-2.0')
      expect(state.upstreamModels.value).not.toContain('Public/Video')
    },
  )

  it('keeps an unconfigured legacy account unrestricted', () => {
    const state = setup('bblabu_v1')
    expect(state.mapping.value).toBeUndefined()
    expect(state.selection.value).toBeNull()
    expect(state.upstreamModels.value).toEqual(['Seedance-2.0', 'Seedance-2.5'])
  })

  it('uses driver-default wire IDs for legacy identity whitelists and exact targets for aliases', () => {
    const state = setup('bblabu_v1', { 'seedance-2.0': 'seedance-2.0' })
    expect(state.upstreamModels.value).toEqual(['Seedance-2.0'])
    state.load({ 'Public/Video': 'seedance-2.0' })
    state.selection.value = ['Public/Video']
    expect(state.upstreamModels.value).toEqual(['seedance-2.0'])
    const fflink = setup('fflink_v1', { 'bytedance/Seedance-2.5': 'bytedance/Seedance-2.5' })
    expect(fflink.upstreamModels.value).toEqual(['bytedance/seedance-2.5'])
  })

  it('replaces the upstream authorization atomically when adding a case-only public alias', () => {
    const state = setup('video_tasks_json_v1', { 'Seedance-2.0': 'Seedance-2.0' })
    state.updateEntries([{ from: 'seedance-2.0', to: 'Seedance-2.0' }])
    expect(state.selection.value).toEqual(['seedance-2.0'])
    expect(state.mapping.value).toEqual({ 'seedance-2.0': 'Seedance-2.0' })
  })

  it('preserves the previous authorization while an alias is temporarily incomplete', () => {
    const state = setup('zhiqi_v1', { 'Public/Old': 'seedance-2.0' })
    state.updateEntries([{ from: '', to: 'seedance-2.0' }])
    expect(state.valid.value).toBe(false)
    state.updateEntries([{ from: 'Public/New', to: 'seedance-2.0' }])
    expect(state.selection.value).toEqual(['Public/New'])
    expect(state.mapping.value).toEqual({ 'Public/New': 'seedance-2.0' })
  })

  it('keeps unrestricted behavior when an unused blank mapping row is removed', () => {
    const state = setup('bblabu_v1')
    state.updateEntries([{ from: '', to: '' }])
    state.updateEntries([])
    expect(state.selection.value).toBeNull()
    expect(state.mapping.value).toBeUndefined()
  })

  it('synchronizes public overrides without rewriting capabilities stored by upstream ID', () => {
    const original = [
      { model: 'Public/Video', upstream_model: 'Supplier/Old', profile: 'zhiqi_v1', enabled: true },
      { model: 'Supplier/Old', profile: 'zhiqi_v1', enabled: false },
    ] as VideoModelCapability[]
    const state = setup('zhiqi_v1', { 'Public/Video': 'Supplier/Old' }, original)
    state.updateEntries([{ from: 'Public/Video', to: 'Supplier/New' }])
    expect(state.capabilities.value[0].upstream_model).toBe('Supplier/New')
    expect(state.capabilities.value[1]).toEqual(original[1])
    expect(original[0].upstream_model).toBe('Supplier/Old')
    state.updateCapabilities([{ ...state.capabilities.value[0], upstream_model: 'Supplier/Edited' }, state.capabilities.value[1]])
    expect(state.mapping.value).toEqual({ 'Public/Video': 'Supplier/Edited' })
  })

  it('does not rename another capability when deleting a preceding mapping row', () => {
    const original = [
      { model: 'Public/A', upstream_model: 'Supplier/A', profile: 'zhiqi_v1', enabled: true },
      { model: 'Public/B', upstream_model: 'Supplier/B', profile: 'zhiqi_v1', enabled: true },
    ] as VideoModelCapability[]
    const state = setup('zhiqi_v1', { 'Public/A': 'Supplier/A', 'Public/B': 'Supplier/B' }, original)
    state.updateEntries([{ from: 'Public/B', to: 'Supplier/B' }])
    expect(state.mapping.value).toEqual({ 'Public/B': 'Supplier/B' })
    expect(state.capabilities.value).toEqual(original)
  })

  it('offers exact mapped targets and discovered upstream IDs for purchase prices', () => {
    const state = setup('video_tasks_json_v1', { 'Public/Stable': 'Supplier/Exact' })
    state.discovery.value = { models: ['Supplier/Exact', 'Supplier/Other'], protocol: 'video_tasks_json_v1',
      base_url: 'https://supplier.example/v1', proxy_id: null, synced_at: '2026-10-24T00:00:00Z' }
    expect(state.upstreamModels.value).toEqual(['Supplier/Exact', 'Supplier/Other'])
  })

  it('rejects duplicate public IDs, invalid targets, and oversized model names', () => {
    expect(validSeedanceModelMappings([{ from: 'Public', to: 'Exact' }, { from: 'public', to: 'Other' }])).toBe(false)
    expect(validSeedanceModelMappings([{ from: 'Public', to: 'bad target' }])).toBe(false)
    expect(validSeedanceModelMappings([{ from: 'Public', to: '中'.repeat(43) }])).toBe(false)
    expect(validSeedanceModelMappings([{ from: 'Public', to: 'Seedance-2.0' }])).toBe(true)
  })
})

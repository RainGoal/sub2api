import type { VideoModelCapability } from '@/api/admin/videoTasks'
import type { CapabilityVideoProviderID } from '@/constants/videoProviders'

export const capabilityLimitFields = ['max_images', 'max_videos', 'max_audios', 'max_total_assets', 'max_reference_video_seconds', 'max_combined_video_seconds'] as const
export const capabilityFlagFields = ['frames', 'allow_first_only', 'allow_frames_with_references', 'frames_require_auto_ratio', 'generate_audio', 'audio_requires_image', 'require_reference_video_durations', 'supports_cancel', 'metering_trusted'] as const
type LimitField = typeof capabilityLimitFields[number]
type FlagField = typeof capabilityFlagFields[number]

export interface VideoCapabilityForm {
  original: Record<string, unknown>
  enabled: boolean
  profile: string
  upstream_model: string
  duration_mode: string
  min_duration: string
  max_duration: string
  default_duration: string
  allowed_durations: string
  resolutions: string
  default_resolution: string
  resolution_control: boolean
  ratios: string
  default_ratio: string
  billing_unit: string
  billing_policy: string
  metering_policy: string
  content_mode: string
  limits: Record<LimitField, string>
  flags: Record<FlagField, boolean>
  imageLimits: { resolution: string; limit: string }[]
  unverifiedReferenceVideos: boolean
}

function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T }
function list(value: unknown): string { return Array.isArray(value) ? value.join(', ') : '' }
function text(value: unknown): string { return typeof value === 'string' ? value : '' }
export function splitCapabilityOptions(value: string): string[] { return value.split(/[\s,，]+/).filter(Boolean) }

export function createVideoCapabilityForm(capability?: VideoModelCapability, protocol: CapabilityVideoProviderID = 'video_tasks_json_v1'): VideoCapabilityForm {
  const source: Record<string, unknown> = capability ? clone(capability) : {}
  return {
    original: source,
    enabled: source.enabled === true,
    profile: text(source.profile) || (protocol === 'zhiqi_v1' ? 'zhiqi_v1' : ''), upstream_model: text(source.upstream_model), duration_mode: text(source.duration_mode),
    min_duration: String(source.min_duration ?? ''), max_duration: String(source.max_duration ?? ''), default_duration: String(source.default_duration ?? ''),
    allowed_durations: list(source.allowed_durations), resolutions: list(source.resolutions), default_resolution: text(source.default_resolution), resolution_control: source.resolution_control === true,
    ratios: list(source.ratios), default_ratio: text(source.default_ratio), billing_unit: text(source.billing_unit), billing_policy: text(source.billing_policy), metering_policy: text(source.metering_policy),
    content_mode: text(source.content_mode) || 'content_endpoint',
    limits: Object.fromEntries(capabilityLimitFields.map((key) => [key, String(source[key] ?? 0)])) as Record<LimitField, string>,
    flags: Object.fromEntries(capabilityFlagFields.map((key) => [key, source[key] === true])) as Record<FlagField, boolean>,
    imageLimits: source.max_images_by_resolution && typeof source.max_images_by_resolution === 'object' && !Array.isArray(source.max_images_by_resolution)
      ? Object.entries(source.max_images_by_resolution).map(([resolution, limit]) => ({ resolution, limit: String(limit) })) : [],
    unverifiedReferenceVideos: Array.isArray(source.unverified_combinations) && source.unverified_combinations.includes('reference_videos')
  }
}

// Keep fields introduced by a newer server when editing the fields known here.
export function buildVideoCapability(model: string, form: VideoCapabilityForm): VideoModelCapability {
  const unknownDuration = form.duration_mode === 'unknown'
  const unverified = Array.isArray(form.original.unverified_combinations)
    ? form.original.unverified_combinations.filter((value) => value !== 'reference_videos') : []
  if (form.unverifiedReferenceVideos) unverified.push('reference_videos')
  return {
    ...clone(form.original), model, enabled: form.enabled, profile: form.profile,
    ...(form.upstream_model.trim() ? { upstream_model: form.upstream_model.trim() } : { upstream_model: undefined }),
    duration_mode: form.duration_mode,
    min_duration: unknownDuration ? 0 : number(form.min_duration),
    max_duration: unknownDuration ? 0 : number(form.max_duration),
    default_duration: unknownDuration ? 0 : number(form.default_duration),
    allowed_durations: form.duration_mode === 'controlled' ? splitCapabilityOptions(form.allowed_durations).map(Number) : [],
    resolutions: splitCapabilityOptions(form.resolutions), default_resolution: form.default_resolution.trim(), resolution_control: form.resolution_control,
    ratios: splitCapabilityOptions(form.ratios), default_ratio: form.default_ratio.trim(),
    ...Object.fromEntries(capabilityLimitFields.map((key) => [key, number(form.limits[key])])),
    ...form.flags,
    max_images_by_resolution: Object.fromEntries(form.imageLimits.map(({ resolution, limit }) => [resolution.trim(), number(limit)])),
    billing_unit: form.billing_unit, billing_policy: form.billing_policy, metering_policy: form.metering_policy,
    content_mode: form.content_mode, unverified_combinations: unverified
  } as VideoModelCapability
}

function number(value: string | number): number { return String(value).trim() === '' ? NaN : Number(value) }
function integer(value: unknown, minimum = 0, maximum = 3600): value is number { return typeof value === 'number' && Number.isInteger(value) && value >= minimum && value <= maximum }
function contains(values: string[], value: unknown): boolean { return typeof value === 'string' && values.some((item) => item.toLowerCase() === value.toLowerCase()) }

/** Error keys use admin.videoTasks.capabilityEditor.errors and match the server contract. */
export function videoCapabilityErrors(capability: unknown, protocol: CapabilityVideoProviderID = 'video_tasks_json_v1'): string[] {
  if (!capability || typeof capability !== 'object' || Array.isArray(capability)) return ['schema']
  const raw = capability as Record<string, unknown>
  const strings = ['model', 'upstream_model', 'profile', 'duration_mode', 'default_resolution', 'default_ratio', 'billing_unit', 'billing_policy', 'metering_policy', 'content_mode']
  const numbers = [...capabilityLimitFields, 'min_duration', 'max_duration', 'default_duration']
  const flags = [...capabilityFlagFields, 'enabled', 'resolution_control']
  if (strings.some((key) => raw[key] != null && typeof raw[key] !== 'string') || numbers.some((key) => raw[key] != null && !Number.isInteger(raw[key])) || flags.some((key) => raw[key] != null && typeof raw[key] !== 'boolean')) return ['schema']
  for (const key of ['resolutions', 'ratios', 'allowed_durations', 'unverified_combinations']) {
    if (raw[key] != null && (!Array.isArray(raw[key]) || (raw[key] as unknown[]).some((value) => key === 'allowed_durations' ? !Number.isInteger(value) : typeof value !== 'string'))) return ['schema']
  }
  if (raw.max_images_by_resolution != null && (typeof raw.max_images_by_resolution !== 'object' || Array.isArray(raw.max_images_by_resolution))) return ['schema']
  const c = { ...Object.fromEntries(numbers.map((key) => [key, 0])), ...Object.fromEntries(flags.map((key) => [key, false])), ...raw } as VideoModelCapability
  const errors: string[] = []
  const add = (key: string) => { if (!errors.includes(key)) errors.push(key) }
  if (typeof c.model !== 'string' || !c.model || c.model !== c.model.trim() || new TextEncoder().encode(c.model).length > 128 || /[\r\n\0]/.test(c.model)) add('model')
  if (c.upstream_model && (c.upstream_model !== c.upstream_model.trim() || new TextEncoder().encode(c.upstream_model).length > 128 || /[\r\n\0]/.test(c.upstream_model))) add('model')
  if (!(protocol === 'zhiqi_v1' ? ['zhiqi_v1'] : ['bblabu_official', 'bblabu_route']).includes(c.profile)) add('profile')
  if (protocol === 'zhiqi_v1' && c.supports_cancel) add('cancellationUnsupported')
  if (protocol === 'zhiqi_v1' && (c.billing_policy === 'output_and_reference' || c.metering_policy === 'output_and_reference_seconds')) add('referenceMeteringUnsupported')
  const durations = Array.isArray(c.allowed_durations) ? c.allowed_durations : []
  if (c.duration_mode === 'controlled' || c.duration_mode === 'fixed') {
    if (!integer(c.min_duration, 1) || !integer(c.max_duration, 1) || !integer(c.default_duration, 1) || c.max_duration < c.min_duration || c.default_duration < c.min_duration || c.default_duration > c.max_duration || c.duration_mode === 'fixed' && c.min_duration !== c.max_duration) add('durationBounds')
    if (durations.some((duration) => !integer(duration, c.min_duration, c.max_duration)) || durations.length > 0 && !durations.includes(c.default_duration)) add('allowedDurations')
  } else if (c.duration_mode === 'unknown') {
    if (c.min_duration !== 0 || c.max_duration !== 0 || c.default_duration !== 0 || durations.length > 0) add('durationBounds')
  } else add('durationMode')
  const options = (value: unknown): string[] => Array.isArray(value) && value.every((item) => typeof item === 'string') ? value : []
  const resolutions = options(c.resolutions), ratios = options(c.ratios)
  for (const values of [resolutions, ratios]) {
    const keys = values.map((value) => value.toLowerCase())
    if (values.some((value) => !value || value !== value.trim() || new TextEncoder().encode(value).length > 32) || new Set(keys).size !== keys.length) add('options')
  }
  if (resolutions.length > 0 && !contains(resolutions, c.default_resolution) || c.resolution_control && resolutions.length === 0) add('resolution')
  if (ratios.length > 0 && !contains(ratios, c.default_ratio)) add('ratio')
  if (capabilityLimitFields.some((key) => !integer(c[key]))) add('limits')
  if (c.max_images_by_resolution && typeof c.max_images_by_resolution === 'object') {
    for (const [resolution, limit] of Object.entries(c.max_images_by_resolution)) {
      if (!contains(resolutions, resolution) || !integer(limit, 0, Number(c.max_images))) add('imageLimits')
    }
  }
  if ((c.allow_first_only || c.allow_frames_with_references || c.frames_require_auto_ratio) && !c.frames) add('frames')
  if (c.frames_require_auto_ratio && !contains(ratios, 'auto')) add('frameRatio')
  if (c.billing_unit === 'per_video') {
    if (c.metering_policy !== 'per_video' || c.billing_policy !== 'output_only') add('perVideo')
  } else if (c.billing_unit === 'per_second') {
    if (c.duration_mode === 'unknown') add('unknownSecond')
    if (c.metering_policy === 'output_seconds') {
      if (c.billing_policy !== 'output_only') add('billingPolicy')
    } else if (c.metering_policy === 'output_and_reference_seconds') {
      if (!integer(c.max_reference_video_seconds, 1) || c.billing_policy !== 'output_and_reference') add('referenceMetering')
    } else add('metering')
  } else add('billingUnit')
  if (c.content_mode !== 'content_endpoint') add('content')
  if (Array.isArray(c.unverified_combinations) && c.unverified_combinations.some((value) => value !== 'reference_videos')) add('unverified')
  return errors
}

export function validateVideoModelCapability(capability: unknown, protocol: CapabilityVideoProviderID = 'video_tasks_json_v1'): string | null {
  return videoCapabilityErrors(capability, protocol)[0] || null
}

<template>
  <section class="space-y-4 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="video-capability-editor" :aria-label="label('title')">
    <div>
      <h4 class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ label('title') }}</h4>
      <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200" data-testid="capability-model">{{ model }}</p>
      <p class="input-hint">{{ label('modelHint') }}</p>
    </div>
    <div v-if="templates.length" class="space-y-2">
      <label class="input-label">
        {{ label('historicalTemplate') }}
        <select v-model="selectedTemplate" class="input" data-testid="capability-template">
          <option value="">{{ label('noTemplate') }}</option>
          <option v-for="template in templates" :key="template.model" :value="template.model">{{ template.model }}</option>
        </select>
      </label>
      <p class="input-hint">{{ label('templateHint') }}</p>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedTemplate" data-testid="capability-copy-template" @click="copyTemplate">{{ label('copyTemplate') }}</button>
    </div>
    <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
      <input v-model="form.enabled" type="checkbox" class="checkbox" data-testid="capability-enabled" />{{ label('enabled') }}
    </label>
    <p class="input-hint">{{ label('authorizationHint') }}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="input-label">
        {{ label('profile') }}
        <select v-model="form.profile" class="input" data-testid="capability-profile">
          <option value="">{{ label('choose') }}</option>
          <option value="bblabu_official">{{ label('officialProfile') }}</option>
          <option value="bblabu_route">{{ label('routeProfile') }}</option>
        </select>
      </label>
      <label class="input-label">
        {{ label('contentMode') }}
        <select v-model="form.content_mode" class="input" data-testid="capability-content-mode"><option value="content_endpoint">{{ label('contentEndpoint') }}</option></select>
      </label>
    </div>
    <p class="input-hint">{{ label('profileHint') }}</p>
    <fieldset class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-600">
      <legend class="text-sm font-medium">{{ label('duration') }}</legend>
      <label class="input-label">
        {{ label('durationMode') }}
        <select v-model="form.duration_mode" class="input" data-testid="capability-duration-mode">
          <option value="">{{ label('choose') }}</option>
          <option value="controlled">{{ label('controlled') }}</option>
          <option value="fixed">{{ label('fixed') }}</option>
          <option value="unknown">{{ label('unknown') }}</option>
        </select>
      </label>
      <div v-if="form.duration_mode !== 'unknown'" class="grid gap-3 sm:grid-cols-3">
        <label v-for="field in durationFields" :key="field" class="input-label">{{ label(field) }}<input v-model="form[field]" type="number" min="1" max="3600" step="1" class="input" :data-testid="`capability-${field}`" /></label>
      </div>
      <label v-if="form.duration_mode === 'controlled'" class="input-label">{{ label('allowedDurations') }}<input v-model="form.allowed_durations" class="input" data-testid="capability-allowed-durations" :placeholder="label('durationExample')" /></label>
      <p class="input-hint">{{ label('durationHint') }}</p>
    </fieldset>
    <fieldset class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-600">
      <legend class="text-sm font-medium">{{ label('specifications') }}</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="input-label">{{ label('resolutions') }}<input v-model="form.resolutions" class="input" data-testid="capability-resolutions" :placeholder="label('resolutionExample')" /></label>
        <label class="input-label">{{ label('defaultResolution') }}<input v-model="form.default_resolution" class="input" data-testid="capability-default-resolution" /></label>
        <label class="input-label">{{ label('ratios') }}<input v-model="form.ratios" class="input" data-testid="capability-ratios" :placeholder="label('ratioExample')" /></label>
        <label class="input-label">{{ label('defaultRatio') }}<input v-model="form.default_ratio" class="input" data-testid="capability-default-ratio" /></label>
      </div>
      <label class="flex items-center gap-2 text-sm"><input v-model="form.resolution_control" type="checkbox" class="checkbox" data-testid="capability-resolution-control" />{{ label('resolutionControl') }}</label>
      <p class="input-hint">{{ label('optionsHint') }}</p>
    </fieldset>
    <fieldset class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-600">
      <legend class="text-sm font-medium">{{ label('assets') }}</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <label v-for="field in capabilityLimitFields" :key="field" class="input-label">{{ label(field) }}<input v-model="form.limits[field]" type="number" min="0" max="3600" step="1" class="input" :data-testid="`capability-${field}`" /></label>
      </div>
      <div class="space-y-2">
        <span class="input-label">{{ label('imageLimits') }}</span>
        <div v-for="(limit, index) in form.imageLimits" :key="index" class="flex flex-wrap items-end gap-2">
          <label class="input-label mb-0 min-w-0 flex-1">{{ label('limitResolution') }}<input v-model="limit.resolution" class="input" :data-testid="`capability-image-resolution-${index}`" /></label>
          <label class="input-label mb-0 w-28">{{ label('imageCount') }}<input v-model="limit.limit" type="number" min="0" max="3600" step="1" class="input" :data-testid="`capability-image-limit-${index}`" /></label>
          <button type="button" class="btn btn-secondary btn-sm" @click="form.imageLimits.splice(index, 1)">{{ label('remove') }}</button>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" data-testid="capability-add-image-limit" @click="form.imageLimits.push({ resolution: '', limit: '0' })">{{ label('addImageLimit') }}</button>
      </div>
      <div class="grid gap-2 sm:grid-cols-2">
        <label v-for="field in capabilityFlagFields" :key="field" class="flex items-start gap-2 text-sm"><input v-model="form.flags[field]" type="checkbox" class="checkbox mt-0.5" :data-testid="`capability-${field}`" />{{ label(field) }}</label>
        <label class="flex items-start gap-2 text-sm"><input v-model="form.unverifiedReferenceVideos" type="checkbox" class="checkbox mt-0.5" data-testid="capability-unverified-reference-videos" />{{ label('unverifiedReferenceVideos') }}</label>
      </div>
    </fieldset>
    <fieldset class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-600">
      <legend class="text-sm font-medium">{{ label('billing') }}</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="input-label">{{ label('billingUnit') }}<select v-model="form.billing_unit" class="input" data-testid="capability-billing-unit"><option value="">{{ label('choose') }}</option><option value="per_video">{{ t('admin.videoTasks.perVideo') }}</option><option value="per_second">{{ t('admin.videoTasks.perSecond') }}</option></select></label>
        <label class="input-label">{{ label('meteringPolicy') }}<select v-model="form.metering_policy" class="input" data-testid="capability-metering-policy"><option value="">{{ label('choose') }}</option><option value="per_video">{{ label('perVideoMetering') }}</option><option value="output_seconds">{{ label('outputSeconds') }}</option><option value="output_and_reference_seconds">{{ label('outputReferenceSeconds') }}</option></select></label>
        <label class="input-label">{{ label('billingPolicy') }}<select v-model="form.billing_policy" class="input" data-testid="capability-billing-policy"><option value="">{{ label('choose') }}</option><option value="output_only">{{ label('outputOnly') }}</option><option value="output_and_reference">{{ label('outputReference') }}</option></select></label>
      </div>
      <p class="input-hint">{{ label('billingHint') }}</p>
    </fieldset>
    <ul v-if="errors.length" class="list-inside list-disc text-xs text-red-600 dark:text-red-400" role="alert" data-testid="capability-errors"><li v-for="error in errors" :key="error">{{ label(`errors.${error}`) }}</li></ul>
    <div class="flex flex-wrap justify-end gap-2">
      <button type="button" class="btn btn-secondary btn-sm" data-testid="capability-cancel" @click="emit('cancel')">{{ label('cancel') }}</button>
      <button type="button" class="btn btn-primary btn-sm" :disabled="errors.length > 0" data-testid="capability-save" @click="save">{{ label('save') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { VideoModelCapability } from '@/api/admin/videoTasks'
import { buildVideoCapability, capabilityFlagFields, capabilityLimitFields, createVideoCapabilityForm, videoCapabilityErrors } from './videoModelCapabilityForm'

const props = defineProps<{ model: string; capability?: VideoModelCapability; templates: VideoModelCapability[] }>()
const emit = defineEmits<{ save: [value: VideoModelCapability]; cancel: [] }>()
const { t } = useI18n()
const label = (key: string) => t(`admin.videoTasks.capabilityEditor.${key}`)
const durationFields = ['min_duration', 'max_duration', 'default_duration'] as const
const form = ref(createVideoCapabilityForm(props.capability))
const selectedTemplate = ref('')
const candidate = computed(() => buildVideoCapability(props.model, form.value))
const errors = computed(() => {
  const result = videoCapabilityErrors(candidate.value)
  const resolutionKeys = form.value.imageLimits.map((row) => row.resolution.trim().toLowerCase())
  if (new Set(resolutionKeys).size !== resolutionKeys.length && !result.includes('imageLimits')) result.push('imageLimits')
  return result
})
watch(() => [props.model, props.capability], () => { form.value = createVideoCapabilityForm(props.capability); selectedTemplate.value = '' }, { deep: true })

function copyTemplate() {
  const template = props.templates.find((value) => value.model === selectedTemplate.value)
  if (template) form.value = createVideoCapabilityForm({ ...form.value.original, ...template, model: props.model, enabled: form.value.enabled })
}
function save() { if (errors.value.length === 0) emit('save', candidate.value) }
</script>

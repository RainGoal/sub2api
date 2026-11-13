<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { VideoCapabilityLimits } from '@/api/admin/channels'

const props = defineProps<{ modelValue?: VideoCapabilityLimits | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: VideoCapabilityLimits | null] }>()
const { t } = useI18n()
const numbers = ['min_duration', 'max_duration', 'default_duration', 'max_images', 'max_videos', 'max_audios', 'max_total_assets', 'max_reference_video_seconds', 'max_combined_video_seconds'] as const
const lists = ['allowed_durations', 'resolutions', 'ratios'] as const
const defaults = ['default_resolution', 'default_ratio'] as const
const flags = ['frames', 'allow_first_only', 'allow_frames_with_references', 'generate_audio', 'supports_cancel'] as const

function update(field: keyof VideoCapabilityLimits, value: unknown) {
  const next = { ...props.modelValue }
  if (value === undefined) delete next[field]
  else Object.assign(next, { [field]: value })
  emit('update:modelValue', next)
}
function number(field: typeof numbers[number], event: Event) {
  const value = (event.target as HTMLInputElement).value
  update(field, value === '' ? undefined : Number(value))
}
function list(field: typeof lists[number], event: Event) {
  const values = (event.target as HTMLInputElement).value.split(/[,，\s]+/).filter(Boolean)
  update(field, values.length ? field === 'allowed_durations' ? values.map(Number) : values : undefined)
}
</script>

<template>
  <section class="mt-4 border-t border-gray-200 pt-3 dark:border-dark-500">
    <label class="flex items-center gap-2 text-sm font-medium">
      <input type="checkbox" :checked="modelValue != null" @change="emit('update:modelValue', ($event.target as HTMLInputElement).checked ? {} : null)" />
      {{ t('admin.channels.videoCapabilities.title') }}
    </label>
    <p class="mt-1 text-xs text-gray-500">{{ t('admin.channels.videoCapabilities.hint') }}</p>
    <div v-if="modelValue != null" class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <label v-for="field in numbers" :key="field" class="text-xs text-gray-500">
        {{ t(`admin.channels.videoCapabilities.${field}`) }}
        <input class="input mt-1 text-sm" type="number" :min="field.includes('duration') || field.includes('seconds') || field === 'max_total_assets' ? 1 : 0" max="3600" step="1" :value="modelValue[field]" :placeholder="t('admin.channels.videoCapabilities.inherit')" @input="number(field, $event)" />
      </label>
      <label v-for="field in lists" :key="field" class="text-xs text-gray-500">
        {{ t(`admin.channels.videoCapabilities.${field}`) }}
        <input class="input mt-1 text-sm" :value="modelValue[field]?.join(', ')" :placeholder="t('admin.channels.videoCapabilities.inherit')" @input="list(field, $event)" />
      </label>
      <label v-for="field in defaults" :key="field" class="text-xs text-gray-500">
        {{ t(`admin.channels.videoCapabilities.${field}`) }}
        <input class="input mt-1 text-sm" :value="modelValue[field]" :placeholder="t('admin.channels.videoCapabilities.inherit')" @input="update(field, ($event.target as HTMLInputElement).value.trim() || undefined)" />
      </label>
      <label v-for="field in flags" :key="field" class="text-xs text-gray-500">
        {{ t(`admin.channels.videoCapabilities.${field}`) }}
        <select class="input mt-1 text-sm" :value="modelValue[field] === undefined ? '' : String(modelValue[field])" @change="update(field, ($event.target as HTMLSelectElement).value === '' ? undefined : ($event.target as HTMLSelectElement).value === 'true')">
          <option value="">{{ t('admin.channels.videoCapabilities.inherit') }}</option>
          <option value="false">{{ t('admin.channels.videoCapabilities.disabled') }}</option>
          <option value="true">{{ t('admin.channels.videoCapabilities.required') }}</option>
        </select>
      </label>
    </div>
  </section>
</template>

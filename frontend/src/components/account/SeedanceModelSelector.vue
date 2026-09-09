<template>
  <div class="space-y-3" data-testid="seedance-model-selector">
    <label class="input-label" :for="`${id}-mode`">{{ t('admin.accounts.seedance.models') }}</label>
    <select
      :id="`${id}-mode`"
      :value="modelValue === null ? 'all' : 'selected'"
      class="input"
      data-testid="seedance-model-mode"
      @change="changeMode"
    >
      <option value="all">{{ t('admin.accounts.seedance.allModels') }}</option>
      <option value="selected">{{ t('admin.accounts.seedance.selectedModelsOnly') }}</option>
    </select>
    <p class="input-hint">{{ t('admin.accounts.seedance.modelsHint') }}</p>
    <div class="grid gap-2 sm:grid-cols-2">
      <label
        v-for="model in models"
        :key="model.id"
        class="flex items-start gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600"
      >
        <input
          type="checkbox"
          class="checkbox mt-0.5"
          :checked="modelValue === null || modelValue.includes(model.id)"
          :disabled="modelValue === null"
          :data-testid="`seedance-model-${model.id}`"
          @change="toggleModel(model.id)"
        />
        <span class="min-w-0">
          <span class="block text-sm text-gray-700 dark:text-gray-200">{{ model.label }}</span>
          <span class="block text-xs text-gray-500 dark:text-gray-400">{{ model.resolutions.join(' / ').replace('4k', '4K') }}</span>
        </span>
      </label>
    </div>
    <p v-if="modelValue?.length === 0" class="text-sm text-red-600 dark:text-red-400" role="alert">
      {{ t('admin.accounts.seedance.modelsRequired') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { videoProviderModelOptions, type VideoProviderID } from '@/constants/videoProviders'
import type { SeedanceModelSelection } from './seedanceModelRestriction'

const props = defineProps<{ id: string; provider: VideoProviderID; modelValue: SeedanceModelSelection }>()
const emit = defineEmits<{ 'update:modelValue': [value: SeedanceModelSelection] }>()
const { t } = useI18n()
const models = computed(() => videoProviderModelOptions(props.provider))

function changeMode(event: Event) {
  const mode = (event.target as HTMLSelectElement).value
  emit('update:modelValue', mode === 'all' ? null : models.value.map(({ id }) => id))
}

function toggleModel(id: string) {
  if (props.modelValue === null) return
  const selected = new Set(props.modelValue)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  emit('update:modelValue', models.value.filter((model) => selected.has(model.id)).map((model) => model.id))
}
</script>

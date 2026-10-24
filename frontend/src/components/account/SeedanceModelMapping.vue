<template>
  <section class="mt-4 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="seedance-model-mapping">
    <label class="input-label">{{ t('admin.accounts.modelMapping') }}</label>
    <p class="input-hint">{{ t('admin.videoTasks.mappingHint') }}</p>
    <div v-for="(entry, index) in modelValue" :key="index" class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto]">
      <input :value="entry.from" class="input col-span-2 min-w-0 font-mono sm:col-span-1" :aria-label="t('admin.accounts.requestModel')"
        :placeholder="t('admin.accounts.requestModel')" :data-testid="`seedance-mapping-from-${index}`"
        @input="update(index, 'from', ($event.target as HTMLInputElement).value)" />
      <span class="hidden text-gray-400 sm:inline" aria-hidden="true">→</span>
      <input :value="entry.to" class="input min-w-0 font-mono" :list="`${id}-upstream-models`"
        :aria-label="t('admin.accounts.actualModel')" :placeholder="t('admin.accounts.actualModel')" :data-testid="`seedance-mapping-to-${index}`"
        @input="update(index, 'to', ($event.target as HTMLInputElement).value)" />
      <button type="button" class="btn btn-secondary btn-sm" :aria-label="t('common.remove')"
        :data-testid="`seedance-mapping-remove-${index}`" @click="emit('update:modelValue', modelValue.filter((_, i) => i !== index))">×</button>
    </div>
    <datalist :id="`${id}-upstream-models`"><option v-for="model in upstreamModels" :key="model" :value="model" /></datalist>
    <p v-if="!valid" class="text-xs text-red-600 dark:text-red-400" role="alert">{{ t('admin.videoTasks.invalidMapping') }}</p>
    <button type="button" class="btn btn-secondary btn-sm" data-testid="seedance-add-mapping"
      @click="emit('update:modelValue', [...modelValue, { from: '', to: '' }])">+ {{ t('admin.accounts.addMapping') }}</button>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelMappingEntry } from '@/composables/useModelWhitelist'
import { validSeedanceModelMappings } from './useSeedanceModelMapping'

const props = defineProps<{ id: string; modelValue: ModelMappingEntry[]; upstreamModels: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: ModelMappingEntry[]] }>()
const { t } = useI18n()
const valid = computed(() => validSeedanceModelMappings(props.modelValue))
function update(index: number, field: keyof ModelMappingEntry, value: string) {
  emit('update:modelValue', props.modelValue.map((entry, i) => i === index ? { ...entry, [field]: value } : entry))
}
</script>

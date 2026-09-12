<template>
  <fieldset :disabled="disabled" class="min-w-0 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="account-model-cost-pricing">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="input-label mb-0">{{ t('admin.accounts.modelCostPricing.title') }}</h3>
      <button type="button" class="btn btn-secondary btn-sm" data-testid="add-account-model-cost" @click="addEntry">
        + {{ t('admin.accounts.modelCostPricing.add') }}
      </button>
    </div>
    <p class="input-hint">{{ t('admin.accounts.modelCostPricing.hint') }}</p>
    <p v-if="platform === 'seedance'" class="input-hint">{{ t('admin.accounts.modelCostPricing.videoHint') }}</p>
    <p v-else class="input-hint">{{ t('admin.accounts.modelCostPricing.defaultPriceHint') }}</p>
    <div v-if="modelValue.length === 0" class="rounded border border-dashed border-gray-300 p-3 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
      {{ t('admin.accounts.modelCostPricing.empty') }}
    </div>
    <PricingEntryCard
      v-for="(entry, index) in modelValue"
      :key="index"
      :entry="entry"
      :platform="platform"
      :auto-fill-default-pricing="false"
      :enable-tier-multipliers="true"
      @update="updateEntry(index, $event)"
      @remove="removeEntry(index)"
    />
    <p v-if="error" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ error }}</p>
  </fieldset>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import type { PricingFormEntry } from '@/components/admin/channel/types'
import { createAccountModelCostEntry, validateAccountModelCostPricing } from './accountModelCostPricing'

const props = withDefaults(defineProps<{
  modelValue: PricingFormEntry[]
  platform: string
  disabled?: boolean
}>(), { disabled: false })
const emit = defineEmits<{ 'update:modelValue': [entries: PricingFormEntry[]] }>()
const { t } = useI18n()
const error = computed(() => validateAccountModelCostPricing(props.modelValue, props.platform, t))

function addEntry() {
  if (!props.disabled) emit('update:modelValue', [...props.modelValue, createAccountModelCostEntry(props.platform)])
}

function updateEntry(index: number, entry: PricingFormEntry) {
  if (props.disabled) return
  emit('update:modelValue', props.modelValue.map((current, currentIndex) => currentIndex === index ? entry : current))
}

function removeEntry(index: number) {
  if (!props.disabled) emit('update:modelValue', props.modelValue.filter((_, currentIndex) => currentIndex !== index))
}
</script>

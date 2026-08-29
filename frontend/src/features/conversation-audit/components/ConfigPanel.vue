<template>
  <section class="card overflow-hidden">
    <header class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.conversationAudit.config.title') }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.conversationAudit.config.version', { version: modelValue.config_version }) }}
        </p>
      </div>
      <label class="flex items-center gap-3 text-sm font-medium text-gray-700 dark:text-dark-200">
        <span>{{ t('admin.conversationAudit.config.enabled') }}</span>
        <Toggle :model-value="modelValue.enabled" :aria-label="t('admin.conversationAudit.config.enabled')" @update:model-value="patch('enabled', $event)" />
      </label>
    </header>

    <div v-if="modelValue.enabled && !modelValue.encryption_ready" class="border-b border-red-200 bg-red-50 px-5 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300" role="alert">
      {{ t('admin.conversationAudit.config.encryptionRequired') }}
    </div>

    <div class="grid grid-cols-1 divide-y divide-gray-200 dark:divide-dark-700 lg:grid-cols-2 lg:divide-x lg:divide-y-0">
      <div class="p-5">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.conversationAudit.config.storage') }}</h3>
        <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <label class="block sm:col-span-2">
            <span class="input-label">{{ t('admin.conversationAudit.config.retentionDays') }}</span>
            <input :value="modelValue.retention_days" type="number" min="1" max="365" step="1" class="input" @input="patchNumber('retention_days', $event)" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.conversationAudit.config.requestLimit') }}</span>
            <input :value="bytesToKiB(modelValue.request_max_bytes)" type="number" min="4" max="4096" step="4" class="input" @input="patchKiB('request_max_bytes', $event)" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.conversationAudit.config.responseLimit') }}</span>
            <input :value="bytesToKiB(modelValue.response_max_bytes)" type="number" min="4" max="4096" step="4" class="input" @input="patchKiB('response_max_bytes', $event)" />
          </label>
        </div>
      </div>

      <div class="p-5">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.conversationAudit.config.capacity') }}</h3>
        <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <label class="block">
            <span class="input-label">{{ t('admin.conversationAudit.config.memoryBudget') }}</span>
            <input :value="bytesToMiB(modelValue.memory_budget_bytes)" type="number" min="64" max="2048" step="64" class="input" @input="patchMiB('memory_budget_bytes', $event)" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.conversationAudit.config.workers') }}</span>
            <input :value="modelValue.worker_count" type="number" min="1" max="8" step="1" class="input" @input="patchNumber('worker_count', $event)" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.conversationAudit.config.queue') }}</span>
            <input :value="modelValue.queue_capacity" type="number" min="128" max="8192" step="128" class="input" @input="patchNumber('queue_capacity', $event)" />
          </label>
        </div>
      </div>
    </div>

    <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 bg-gray-50 px-5 py-4 dark:border-dark-700 dark:bg-dark-800/60">
      <span class="text-xs" :class="dirty ? 'text-amber-600 dark:text-amber-300' : 'text-gray-500 dark:text-dark-400'">
        {{ dirty ? t('admin.conversationAudit.config.dirty') : t('admin.conversationAudit.config.synced') }}
      </span>
      <div class="flex gap-3">
        <button type="button" class="btn btn-secondary" :disabled="!dirty || saving" @click="$emit('reset')">{{ t('common.reset') }}</button>
        <button type="button" class="btn btn-primary" :disabled="!dirty || saving || !valid" @click="$emit('save')">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { ConversationAuditConfig } from '../types'

const props = defineProps<{ modelValue: ConversationAuditConfig; dirty: boolean; saving: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: ConversationAuditConfig]
  save: []
  reset: []
}>()
const { t } = useI18n()

type NumericConfigKey = 'retention_days' | 'request_max_bytes' | 'response_max_bytes' | 'memory_budget_bytes' | 'worker_count' | 'queue_capacity'
const valid = computed(() => {
  const value = props.modelValue
  return (!value.enabled || value.encryption_ready) &&
    value.retention_days >= 1 && value.retention_days <= 365 &&
    value.request_max_bytes >= 4096 && value.request_max_bytes <= 4 * 1024 * 1024 &&
    value.response_max_bytes >= 4096 && value.response_max_bytes <= 4 * 1024 * 1024 &&
    value.memory_budget_bytes >= 64 * 1024 * 1024 && value.memory_budget_bytes <= 2 * 1024 * 1024 * 1024 &&
    value.worker_count >= 1 && value.worker_count <= 8 &&
    value.queue_capacity >= 128 && value.queue_capacity <= 8192
})

function patch<K extends keyof ConversationAuditConfig>(key: K, value: ConversationAuditConfig[K]) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}

function numericValue(event: Event): number {
  return Number((event.target as HTMLInputElement).value)
}

function patchNumber(key: NumericConfigKey, event: Event) {
  patch(key, numericValue(event))
}

function patchKiB(key: 'request_max_bytes' | 'response_max_bytes', event: Event) {
  patch(key, Math.round(numericValue(event) * 1024))
}

function patchMiB(key: 'memory_budget_bytes', event: Event) {
  patch(key, Math.round(numericValue(event) * 1024 * 1024))
}

function bytesToKiB(value: number): number { return value / 1024 }
function bytesToMiB(value: number): number { return value / (1024 * 1024) }
</script>

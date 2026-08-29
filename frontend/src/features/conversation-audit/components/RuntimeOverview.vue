<template>
  <section class="border-y border-gray-200 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-900/60">
    <div class="grid min-h-[92px] grid-cols-2 divide-x divide-y divide-gray-200 dark:divide-dark-700 sm:grid-cols-3 sm:divide-y-0 xl:grid-cols-6">
      <div class="px-4 py-4">
        <div class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.runtime.lifecycle') }}</div>
        <div class="mt-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
          <span class="h-2 w-2 rounded-full" :class="lifecycleDot"></span>
          {{ lifecycleLabel }}
        </div>
      </div>
      <div class="px-4 py-4">
        <div class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.runtime.active') }}</div>
        <div class="mt-2 font-mono text-lg font-semibold text-gray-900 dark:text-white">{{ runtime?.active_captures ?? 0 }}</div>
      </div>
      <div class="px-4 py-4">
        <div class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.runtime.payloadQueue') }}</div>
        <div class="mt-2 font-mono text-sm font-semibold text-gray-900 dark:text-white">
          {{ runtime?.payload_queue_depth ?? 0 }} / {{ runtime?.payload_queue_capacity ?? 0 }}
        </div>
      </div>
      <div class="px-4 py-4">
        <div class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.runtime.metadataQueue') }}</div>
        <div class="mt-2 font-mono text-sm font-semibold text-gray-900 dark:text-white">
          {{ runtime?.metadata_queue_depth ?? 0 }} / {{ runtime?.metadata_queue_capacity ?? 0 }}
        </div>
      </div>
      <div class="px-4 py-4">
        <div class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.runtime.memory') }}</div>
        <div class="mt-2 font-mono text-sm font-semibold text-gray-900 dark:text-white">
          {{ formatBytes(runtime?.buffered_bytes ?? 0) }} / {{ formatBytes(runtime?.memory_budget_bytes ?? 0) }}
        </div>
      </div>
      <div class="px-4 py-4">
        <div class="flex items-center justify-between gap-2 text-xs font-medium text-gray-500 dark:text-dark-400">
          <span>{{ t('admin.conversationAudit.runtime.degraded') }}</span>
          <button
            type="button"
            class="icon-btn h-7 w-7"
            :title="t('admin.conversationAudit.actions.refresh')"
            :aria-label="t('admin.conversationAudit.actions.refresh')"
            :disabled="loading"
            @click="$emit('refresh')"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
          </button>
        </div>
        <div class="mt-2 font-mono text-sm font-semibold" :class="degradedTotal ? 'text-amber-600 dark:text-amber-300' : 'text-gray-900 dark:text-white'">
          {{ degradedTotal }}
        </div>
      </div>
    </div>
    <div v-if="runtime?.last_error" class="border-t border-red-200 bg-red-50 px-4 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300" role="alert">
      {{ runtime.last_error }}
      <span v-if="runtime.last_error_at" class="ml-2 opacity-75">{{ formatTime(runtime.last_error_at) }}</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ConversationAuditRuntime } from '../types'
import { formatBytes } from '../viewModel'

const props = defineProps<{ runtime: ConversationAuditRuntime | null; loading: boolean }>()
defineEmits<{ refresh: [] }>()
const { t, locale } = useI18n()

const lifecycle = computed(() => props.runtime?.lifecycle || 'disabled')
const lifecycleLabel = computed(() => t(`admin.conversationAudit.lifecycle.${lifecycle.value}`))
const lifecycleDot = computed(() => {
  if (lifecycle.value === 'running') return 'bg-emerald-500'
  if (lifecycle.value === 'degraded' || lifecycle.value === 'draining') return 'bg-amber-500'
  if (lifecycle.value === 'error') return 'bg-red-500'
  return 'bg-gray-400'
})
const degradedTotal = computed(() => {
  const runtime = props.runtime
  return runtime ? runtime.queue_full + runtime.budget_full + runtime.encode_failed + runtime.write_failed : 0
})

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}
</script>

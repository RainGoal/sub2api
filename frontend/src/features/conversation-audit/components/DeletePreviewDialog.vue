<template>
  <BaseDialog :show="show" :title="t('admin.conversationAudit.delete.title')" width="narrow" @close="$emit('close')">
    <div v-if="loading" class="flex min-h-36 items-center justify-center">
      <div class="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-b-primary-600 dark:border-dark-700 dark:border-b-primary-400"></div>
    </div>
    <div v-else-if="preview" class="space-y-4">
      <div class="flex items-end justify-between border-b border-gray-200 pb-4 dark:border-dark-700">
        <span class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.delete.matched') }}</span>
        <span class="font-mono text-3xl font-semibold text-gray-900 dark:text-white">{{ preview.matched_count }}</span>
      </div>
      <dl class="space-y-3 text-sm">
        <div class="flex justify-between gap-4">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.delete.cutoff') }}</dt>
          <dd class="text-right font-mono text-xs text-gray-800 dark:text-dark-200">{{ formatTime(preview.eligibility_cutoff) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.conversationAudit.delete.expires') }}</dt>
          <dd class="text-right font-mono text-xs text-gray-800 dark:text-dark-200">{{ formatTime(preview.expires_at) }}</dd>
        </div>
      </dl>
      <p v-if="preview.has_more" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-300">
        {{ t('admin.conversationAudit.delete.hasMore') }}
      </p>
      <p class="text-xs leading-5 text-red-600 dark:text-red-300">{{ t('admin.conversationAudit.delete.warning') }}</p>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="deleting" @click="$emit('close')">{{ t('common.cancel') }}</button>
      <button type="button" class="btn btn-danger" :disabled="deleting || !preview?.confirmation_token || preview.matched_count === 0" @click="$emit('confirm')">
        {{ deleting ? t('common.loading') : t('admin.conversationAudit.delete.confirm') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { ConversationAuditDeletePreview } from '../types'

defineProps<{ show: boolean; preview: ConversationAuditDeletePreview | null; loading: boolean; deleting: boolean }>()
defineEmits<{ close: []; confirm: [] }>()
const { t, locale } = useI18n()

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}
</script>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatBytes } from '@/utils/format'
import { parseBodyContent } from './bodyContent'
import type { RequestAuditPayload } from './types'

const props = defineProps<{ content: string; fallback: RequestAuditPayload }>()
const { t } = useI18n()
const parsed = computed(() => parseBodyContent(props.content))
const failedImages = reactive(new Set<string>())
const formatted = ref(true)
const pretty = computed(() => {
  const value = props.fallback
  if (value.encoding !== 'utf8' || value.offset !== 0 || value.has_more || value.partial) return null
  try { return JSON.stringify(JSON.parse(value.content), null, 2) } catch { return null }
})
const roles = ['system', 'developer', 'user', 'assistant', 'tool', 'model']
const roleLabel = (role: string) => roles.includes(role) ? t(`admin.requestAudit.bodyContent.roles.${role}`) : role || t('admin.requestAudit.bodyContent.roles.unknown')
watch(() => props.content, () => failedImages.clear())
</script>

<template>
  <div data-test="body-content" class="space-y-4 px-4 py-4">
    <p v-if="parsed.limited" role="status" class="text-xs leading-5 text-amber-300">{{ t('admin.requestAudit.bodyContent.limited') }}</p>
    <div v-if="parsed.unsupported && !parsed.messages.length" class="space-y-3">
      <p class="text-sm leading-6 text-slate-400">{{ t('admin.requestAudit.bodyContent.unsupported') }}</p>
      <label v-if="pretty !== null" class="flex items-center gap-2 text-xs text-slate-300"><input v-model="formatted" type="checkbox" data-test="format-body" class="rounded border-slate-600 accent-primary-500" />{{ t('admin.requestAudit.payloads.pretty') }}</label>
      <pre data-test="content-body" tabindex="0" class="max-h-80 overflow-auto whitespace-pre-wrap break-all font-mono text-xs leading-6 text-slate-200">{{ formatted && pretty !== null ? pretty : fallback.content }}</pre>
      <p v-if="fallback.encoding === 'base64'" class="text-xs leading-5 text-slate-500">{{ t('admin.requestAudit.binaryHint') }}</p>
      <p v-if="pretty !== null && formatted" class="text-xs leading-5 text-slate-500">{{ t('admin.requestAudit.payloads.formatHint') }}</p>
    </div>
    <template v-else>
      <p class="text-[11px] text-slate-500">{{ t('admin.requestAudit.bodyContent.extracted') }}</p>
      <div class="max-h-[42rem] space-y-5 overflow-auto pr-1">
        <article v-for="(message, messageIndex) in parsed.messages" :key="messageIndex" data-test="body-message" class="grid min-w-0 gap-2 sm:grid-cols-[6rem_minmax(0,1fr)] sm:gap-4">
          <header class="flex items-center gap-2 self-start py-1 text-xs sm:flex-col sm:items-start sm:gap-1">
            <span class="font-medium" :class="message.role === 'user' ? 'text-sky-300' : message.role === 'assistant' || message.role === 'model' ? 'text-emerald-300' : 'text-slate-300'">{{ roleLabel(message.role) }}</span>
            <span class="font-mono text-[10px] text-slate-600">{{ String(messageIndex + 1).padStart(2, '0') }}</span>
          </header>
          <div class="min-w-0 space-y-3 border-l border-slate-700/70 pl-3 sm:pl-4">
            <template v-for="(block, blockIndex) in message.blocks" :key="blockIndex">
              <p v-if="block.kind === 'text'" data-test="body-message-text" class="whitespace-pre-wrap break-words text-sm leading-7 text-slate-200">{{ block.text }}</p>
              <figure v-else-if="block.kind === 'image'" class="max-w-xl overflow-hidden rounded-md border border-slate-700 bg-slate-950/70">
                <p v-if="failedImages.has(`${messageIndex}:${blockIndex}`)" class="p-4 text-xs leading-5 text-amber-300">{{ t('admin.requestAudit.bodyContent.imageReasons.invalid') }}</p>
                <img v-else :src="block.src" :alt="t('admin.requestAudit.bodyContent.imageAlt', { index: blockIndex + 1 })" class="max-h-96 w-full object-contain" loading="lazy" decoding="async" @error="failedImages.add(`${messageIndex}:${blockIndex}`)" />
                <figcaption class="border-t border-slate-800 px-3 py-2 font-mono text-[10px] text-slate-500">{{ block.mime }} · {{ formatBytes(block.bytes) }}</figcaption>
              </figure>
              <p v-else-if="block.kind === 'image-unavailable'" data-test="body-image-unavailable" class="rounded border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-xs leading-5 text-amber-200">{{ t(`admin.requestAudit.bodyContent.imageReasons.${block.reason}`) }}</p>
              <p v-else data-test="body-block-unsupported" class="rounded border border-slate-700/70 px-3 py-2 text-xs leading-5 text-slate-400">{{ t('admin.requestAudit.bodyContent.unsupportedBlock', { type: block.label }) }}</p>
            </template>
          </div>
        </article>
      </div>
    </template>
  </div>
</template>

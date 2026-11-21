<template>
  <section class="min-w-0 rounded-lg border border-gray-200 dark:border-dark-700">
    <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-3">
      <h3 class="text-sm font-semibold">{{ t('admin.requestAudit.attempts.title') }}</h3>
      <button type="button" class="btn btn-secondary btn-sm" data-test="load-audit-attempts" :disabled="loading"
        :aria-expanded="opened" @click="loadAttempts">
        {{ t(opened ? 'admin.requestAudit.refresh' : 'admin.requestAudit.attempts.show') }}
      </button>
    </div>
    <div v-if="opened" class="border-t border-gray-200 dark:border-dark-700">
      <p v-if="loading" role="status" class="px-4 py-5 text-sm text-gray-500">{{ t('common.loading') }}</p>
      <p v-else-if="error" role="alert" class="px-4 py-5 text-sm text-red-500">{{ error }}</p>
      <p v-else-if="!items.length" class="px-4 py-5 text-sm text-gray-500">{{ t('admin.requestAudit.attempts.empty') }}</p>
      <div v-else class="divide-y divide-gray-200 dark:divide-dark-700">
        <div v-for="attempt in items" :key="attempt.attempt_no">
          <button type="button" class="flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-start hover:bg-gray-50 dark:hover:bg-dark-800"
            :aria-expanded="activeAttempt === attempt.attempt_no" :data-test="`audit-attempt-${attempt.attempt_no}`" @click="toggleAttempt(attempt.attempt_no)">
            <span class="text-sm font-semibold">{{ t('admin.requestAudit.attempts.number', { number: attempt.attempt_no }) }}</span>
            <span class="min-w-0 break-all font-mono text-xs">{{ attempt.method }} {{ attempt.host }}{{ attempt.path }}</span>
            <span v-if="attempt.status_code" class="font-mono text-xs tabular-nums text-gray-500">{{ attempt.status_code }}</span>
            <span v-if="attempt.account_id" class="text-xs text-gray-500">{{ t('admin.requestAudit.attempts.account', { id: attempt.account_id }) }}</span>
            <time v-if="attempt.received_at" :datetime="attempt.received_at" class="text-xs text-gray-500">{{ new Date(attempt.received_at).toLocaleString(locale) }}</time>
          </button>
          <div v-if="activeAttempt === attempt.attempt_no" class="divide-y divide-gray-200 border-t border-gray-200 dark:divide-dark-700 dark:border-dark-700">
            <div v-for="part in parts" :key="part">
              <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-2">
                <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span class="text-sm">{{ t(`admin.requestAudit.attempts.parts.${part}`) }}</span>
                  <span class="text-xs text-gray-500">{{ description(attempt, part) }}</span>
                </div>
                <button v-if="attempt.parts?.[part] && attempt.parts[part]?.reason !== 'not_selected'" type="button" class="btn btn-secondary btn-sm" :aria-expanded="activePart === part"
                  :data-test="`show-attempt-${part}`" @click="togglePart(attempt.attempt_no, part)">
                  {{ t(activePart === part ? 'admin.requestAudit.attempts.hide' : 'admin.requestAudit.attempts.showPayload') }}
                </button>
              </div>
              <div v-if="activePart === part" class="space-y-2 px-4 pb-3">
                <p v-if="reading" role="status" class="text-xs text-gray-500">{{ t('common.loading') }}</p>
                <div v-else-if="readError" role="alert" class="space-y-2 text-sm text-red-500">
                  <p>{{ readError }}</p>
                  <button type="button" class="btn btn-secondary btn-sm" @click="loadPart(attempt.attempt_no, part, offset)">{{ t('admin.requestAudit.retry') }}</button>
                </div>
                <template v-if="payload">
                  <p v-if="payload.partial" class="text-xs text-amber-600 dark:text-amber-400">{{ t('admin.requestAudit.partialHint') }}</p>
                  <p v-if="payload.encoding === 'base64'" class="text-xs text-gray-500">{{ t('admin.requestAudit.binaryHint') }}</p>
                  <p v-if="!payload.returned_bytes" class="text-xs text-gray-500">{{ t('admin.requestAudit.payloads.empty') }}</p>
                  <template v-else>
                    <label v-if="pretty !== null" class="flex w-fit items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
                      <input v-model="formatted" type="checkbox" class="rounded accent-primary-500" data-test="format-attempt-payload" />
                      {{ t('admin.requestAudit.payloads.pretty') }}
                    </label>
                    <pre tabindex="0" :aria-label="t(`admin.requestAudit.attempts.parts.${part}`)" class="request-audit-payload-content max-h-80 overflow-auto whitespace-pre-wrap break-words rounded border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-xs leading-5 text-gray-900 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100" data-test="audit-attempt-content">{{ formatted && pretty !== null ? pretty : payload.content }}</pre>
                  </template>
                  <div class="flex flex-wrap items-center justify-between gap-2">
                    <span class="text-xs tabular-nums text-gray-500">{{ t('admin.requestAudit.payloads.range', { from: payload.offset, to: payload.offset + payload.returned_bytes, total: payload.part_bytes }) }}</span>
                    <div class="flex flex-wrap gap-2">
                      <button v-if="payload.offset > 0" type="button" class="btn btn-secondary btn-sm" :disabled="downloading" @click="loadPart(attempt.attempt_no, part, Math.max(0, offset - pageSize))">{{ t('admin.requestAudit.payloads.previous') }}</button>
                      <button v-if="payload.has_more && payload.returned_bytes" type="button" class="btn btn-secondary btn-sm" :disabled="downloading" @click="loadPart(attempt.attempt_no, part, offset + payload.returned_bytes)">{{ t('admin.requestAudit.payloads.next') }}</button>
                      <button v-if="payload.encoding === 'utf8' && payload.content" type="button" class="btn btn-secondary btn-sm" @click="copyToClipboard(payload.content)">{{ t('admin.requestAudit.attempts.copy') }}</button>
                      <button type="button" class="btn btn-secondary btn-sm" data-test="download-attempt-payload" :disabled="downloading" @click="download(attempt.attempt_no, part)">{{ t(downloading ? 'admin.requestAudit.downloading' : 'admin.requestAudit.attempts.download') }}</button>
                    </div>
                  </div>
                </template>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import { extractI18nErrorMessage } from '@/utils/apiError'
import api from './api'
import { queuePayloadRead } from './payloadReads'
import { formatPayloadJson } from './payloadText'
import { REQUEST_AUDIT_ATTEMPT_PARTS } from './types'
import type { RequestAuditAttempt, RequestAuditAttemptPart, RequestAuditPayload } from './types'

const props = defineProps<{ recordId: string }>()
const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()
const parts = REQUEST_AUDIT_ATTEMPT_PARTS
const pageSize = 65536
const opened = ref(false)
const loading = ref(false)
const items = ref<RequestAuditAttempt[]>([])
const activeAttempt = ref<number | null>(null)
const activePart = ref<RequestAuditAttemptPart | null>(null)
const payload = ref<RequestAuditPayload | null>(null)
const formatted = ref(true)
const pretty = computed(() => {
  const value = payload.value
  if (!value || value.encoding !== 'utf8' || value.offset !== 0 || value.has_more || value.partial !== false) return null
  return formatPayloadJson(value.content)
})
const reading = ref(false)
const downloading = ref(false)
const offset = ref(0)
const error = ref('')
const readError = ref('')
let generation = 0
let disposed = false
const errorText = (reason: unknown) => extractI18nErrorMessage(reason, t, 'admin.requestAudit.errors', t('admin.requestAudit.failed'))

function resetPart() {
  generation++
  activePart.value = null
  payload.value = null
  formatted.value = true
  readError.value = ''
  reading.value = downloading.value = false
}
async function loadAttempts() {
  if (loading.value) return
  opened.value = loading.value = true
  error.value = ''
  activeAttempt.value = null
  resetPart()
  try {
    const result = await api.listAttempts(props.recordId)
    if (!disposed) items.value = result.items ?? []
  } catch (reason) {
    if (!disposed) error.value = errorText(reason)
  } finally {
    if (!disposed) loading.value = false
  }
}
function toggleAttempt(attempt: number) {
  activeAttempt.value = activeAttempt.value === attempt ? null : attempt
  resetPart()
}
function description(attempt: RequestAuditAttempt, part: RequestAuditAttemptPart) {
  const manifest = attempt.parts?.[part]
  if (!manifest) return t('admin.requestAudit.attempts.unavailable')
  if (manifest.reason === 'not_selected') return t('admin.requestAudit.attempts.unavailable')
  if (manifest.complete) return manifest.expected_bytes ? t('admin.requestAudit.payloads.manifestBytes', { count: manifest.expected_bytes.toLocaleString(locale.value) }) : t('admin.requestAudit.payloads.empty')
  return t('admin.requestAudit.states.partial')
}
function togglePart(attempt: number, part: RequestAuditAttemptPart) {
  const hide = activePart.value === part
  resetPart()
  if (!hide) loadPart(attempt, part, 0)
}
function loadPart(attempt: number, part: RequestAuditAttemptPart, start: number) {
  const current = ++generation
  activePart.value = part
  offset.value = start
  reading.value = true
  payload.value = null
  readError.value = ''
  queuePayloadRead(async () => {
    if (disposed || generation !== current) return
    try {
      const result = await api.attemptPayload(props.recordId, attempt, part, start, pageSize)
      if (!disposed && generation === current) payload.value = result
    } catch (reason) {
      if (!disposed && generation === current) readError.value = errorText(reason)
    } finally {
      if (!disposed && generation === current) reading.value = false
    }
  })
}
function download(attempt: number, part: RequestAuditAttemptPart) {
  if (downloading.value || reading.value) return
  const current = generation
  downloading.value = true
  readError.value = ''
  queuePayloadRead(async () => {
    if (disposed || generation !== current) return
    try {
      const { blob, extension, partial } = await api.downloadAttempt(props.recordId, attempt, part)
      if (disposed || generation !== current) return
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `request-${props.recordId}-upstream-${attempt}-${part}${partial ? '-partial' : ''}.${extension}`
      document.body.append(link); link.click(); link.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (reason) {
      if (!disposed && generation === current) readError.value = errorText(reason)
    } finally {
      if (!disposed && generation === current) downloading.value = false
    }
  })
}

onBeforeUnmount(() => { disposed = true; generation++ })
</script>

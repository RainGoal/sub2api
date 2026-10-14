<script lang="ts">
// Share the read budget across dialog instances, including requests still finishing after close.
const pendingPayloadReads: Array<() => Promise<void>> = []
let activePayloadReads = 0

function queuePayloadRead(task: () => Promise<void>) {
  pendingPayloadReads.push(task)
  drainPayloadReads()
}

function drainPayloadReads() {
  while (activePayloadReads < 2 && pendingPayloadReads.length) {
    const task = pendingPayloadReads.shift()!
    activePayloadReads++
    const finish = () => { activePayloadReads--; drainPayloadReads() }
    void task().then(finish, finish)
  }
}
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import api from './api'
import RequestAuditBodyContent from './RequestAuditBodyContent.vue'
import { BODY_CONTENT_MAX_BYTES } from './bodyContent'
import type { RequestAuditPayload, RequestAuditRecord } from './types'

const props = defineProps<{ record: RequestAuditRecord }>()
const { t, locale } = useI18n()
const parts = ['headers', 'body', 'trailers'] as const
type Part = typeof parts[number]
interface PartState {
  payload: RequestAuditPayload | null
  loading: boolean
  downloading: boolean
  error: string
  errorCode?: string
  offset: number
  reveal: boolean
  allowPartial: boolean
  formatted: boolean
  version: number
}
const newState = (): PartState => ({ payload: null, loading: false, downloading: false, error: '', offset: 0, reveal: false, allowPartial: false, formatted: true, version: 0 })
const panels = reactive<Record<Part, PartState>>({ headers: newState(), body: newState(), trailers: newState() })
const pageSize = 65536
const bodyMode = ref<'content' | 'raw'>('content')
const bodyContent = reactive({ content: null as string | null, loading: false, error: '', hint: '', received: 0, total: 0 })
let bodyContentVersion = 0
let generation = 0
let disposed = false
const errorText = (error: unknown) => extractI18nErrorMessage(error, t, 'admin.requestAudit.errors', t('admin.requestAudit.failed'))
const pretty = computed(() => Object.fromEntries(parts.map(part => {
  const value = panels[part].payload
  if (!value || value.encoding !== 'utf8' || value.offset !== 0 || value.has_more || value.partial !== false) return [part, null]
  try { return [part, JSON.stringify(JSON.parse(value.content), null, 2)] } catch { return [part, null] }
})) as Record<Part, string | null>)
const number = (value: number) => value.toLocaleString(locale.value)
const stateLabel = (state: string) => ['complete', 'capturing', 'in_progress', 'not_read', 'partial', 'read_error', 'unknown'].includes(state) ? t(`admin.requestAudit.states.${state}`) : state
// capture_state describes the whole record; partial is the requested part's integrity.
const partState = (part: Part) => panels[part].payload?.partial === false ? 'complete' : panels[part].payload?.partial === true ? 'partial' : 'unknown'
const canAllowPartial = (part: Part) => props.record.capture_state !== 'complete' || props.record.terminal_manifest?.[part]?.complete === false || panels[part].allowPartial || panels[part].payload?.partial || ['AUDIT_PAYLOAD_INCOMPLETE', 'AUDIT_PAYLOAD_NOT_READY'].includes(panels[part].errorCode || '')

function resetBodyContent() {
  bodyContentVersion++
  Object.assign(bodyContent, { content: null, loading: false, error: '', hint: '', received: 0, total: 0 })
}

function setBodyMode(mode: 'content' | 'raw') {
  if (bodyMode.value === mode) return
  bodyMode.value = mode
  resetBodyContent()
  if (mode === 'content') loadPart('body')
}

function loadPart(part: Part, offset = 0) {
  const panel = panels[part]
  const requestGeneration = generation
  const version = ++panel.version
  const id = props.record.id
  const reveal = panel.reveal, allowPartial = panel.allowPartial
  const current = () => !disposed && requestGeneration === generation && version === panels[part].version
  if (part === 'body') resetBodyContent()
  panel.loading = true; panel.error = ''; panel.errorCode = undefined; panel.payload = null; panel.offset = offset
  queuePayloadRead(async () => {
    if (!current()) return
    try {
      const result = await api.payload(id, part, offset, pageSize, reveal, allowPartial)
      if (current()) {
        panel.payload = result; panel.formatted = true
        if (part === 'body' && bodyMode.value === 'content') loadBodyContent(result, current, id, allowPartial)
      }
    } catch (error) {
      if (current()) { panel.error = errorText(error); panel.errorCode = extractApiErrorCode(error) }
    } finally { if (current()) panel.loading = false }
  })
}

function loadBodyContent(first: RequestAuditPayload, isCurrent: () => boolean, id: string, allowPartial: boolean) {
  const version = bodyContentVersion
  const current = () => isCurrent() && bodyMode.value === 'content' && version === bodyContentVersion
  bodyContent.total = first.part_bytes
  if (first.partial !== false) { bodyContent.hint = 'partial'; return }
  if (first.part_bytes > BODY_CONTENT_MAX_BYTES) { bodyContent.hint = 'tooLarge'; return }
  bodyContent.loading = true
  const slices: Uint8Array[] = []
  let received = 0
  const stop = (hint: string) => { bodyContent.hint = hint; bodyContent.loading = false }
  const append = (payload: RequestAuditPayload) => {
    if (!current()) return
    if (payload.partial !== false) { stop('partial'); return }
    if (!Number.isSafeInteger(payload.part_bytes) || payload.part_bytes < 0 || payload.part_bytes !== first.part_bytes || payload.offset !== received || !Number.isSafeInteger(payload.returned_bytes) || payload.returned_bytes < 0 || payload.returned_bytes > pageSize || received + payload.returned_bytes > first.part_bytes || payload.content.length > pageSize * 2) { stop('inconsistent'); return }
    let bytes: Uint8Array
    try {
      bytes = payload.encoding === 'utf8' ? new TextEncoder().encode(payload.content) : Uint8Array.from(atob(payload.content), char => char.charCodeAt(0))
    } catch { stop('binary'); return }
    if (bytes.byteLength !== payload.returned_bytes) { stop('inconsistent'); return }
    slices.push(bytes); received += bytes.byteLength; bodyContent.received = received
    if (!payload.has_more) {
      if (received !== first.part_bytes) { stop('inconsistent'); return }
      const all = new Uint8Array(received)
      let offset = 0
      for (const slice of slices) { all.set(slice, offset); offset += slice.byteLength }
      try { bodyContent.content = new TextDecoder('utf-8', { fatal: true }).decode(all) } catch { stop('binary'); return }
      bodyContent.loading = false
      return
    }
    if (!payload.returned_bytes || received >= first.part_bytes || slices.length >= BODY_CONTENT_MAX_BYTES / pageSize) { stop('inconsistent'); return }
    queuePayloadRead(async () => {
      if (!current()) return
      try { append(await api.payload(id, 'body', received, pageSize, false, allowPartial)) }
      catch (error) { if (current()) { bodyContent.error = errorText(error); bodyContent.loading = false } }
    })
  }
  append(first)
}

function downloadPart(part: Part) {
  const panel = panels[part]
  if (panel.downloading || panel.loading) return
  const id = props.record.id, allowPartial = panel.allowPartial, requestGeneration = generation, version = panel.version
  const current = () => !disposed && requestGeneration === generation
  panel.downloading = true; panel.error = ''
  queuePayloadRead(async () => {
    if (!current()) return
    try {
      const blob = await api.download(id, part, allowPartial)
      if (!current()) return
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `request-${id}-${part}${allowPartial ? '-partial-allowed' : ''}.bin`
      document.body.appendChild(link); link.click(); link.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (error) {
      if (current() && version === panel.version) { panel.error = errorText(error); panel.errorCode = extractApiErrorCode(error) }
    } finally { if (current()) panel.downloading = false }
  })
}

watch(() => props.record.id, () => {
  generation++
  bodyMode.value = 'content'
  for (const part of parts) { panels[part] = newState(); loadPart(part) }
}, { immediate: true })
onBeforeUnmount(() => { disposed = true; generation++ })
</script>

<template>
  <div class="space-y-4" data-test="audit-payloads">
    <section v-for="part in parts" :key="part" :data-test="`payload-panel-${part}`" :aria-label="t(`admin.requestAudit.parts.${part}`)" :aria-busy="panels[part].loading || panels[part].downloading || (part === 'body' && bodyContent.loading)" class="overflow-hidden rounded-lg border border-slate-700/80 bg-[#0b1220] text-slate-100">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-700/70 px-4 py-3">
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <h3 class="font-mono text-sm font-semibold">{{ t(`admin.requestAudit.parts.${part}`) }}</h3>
          <template v-if="panels[part].payload">
            <span class="rounded border px-1.5 py-0.5 text-[11px]" :class="panels[part].payload?.partial === false ? 'border-emerald-400/30 text-emerald-300' : 'border-amber-400/30 text-amber-300'">{{ stateLabel(partState(part)) }}</span>
            <span class="font-mono text-[11px] text-slate-400">{{ part === 'body' && bodyMode === 'content' && bodyContent.content !== null ? 'utf8' : panels[part].payload?.encoding }}</span>
          </template>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" :data-test="`refresh-${part}`" class="btn btn-secondary btn-sm" :disabled="panels[part].loading || panels[part].downloading" @click="loadPart(part)">{{ t('admin.requestAudit.refresh') }}</button>
          <button type="button" :data-test="`download-${part}`" class="btn btn-secondary btn-sm" :disabled="panels[part].loading || panels[part].downloading" @click="downloadPart(part)">{{ panels[part].downloading ? t('admin.requestAudit.downloading') : t('admin.requestAudit.download') }}</button>
        </div>
      </header>
      <div class="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-slate-700/50 px-4 py-2.5 text-xs text-slate-300">
        <div v-if="part === 'body'" class="inline-flex gap-1 rounded border border-slate-700 p-0.5" :aria-label="t('admin.requestAudit.bodyContent.view')">
          <button v-for="mode in ['content', 'raw'] as const" :key="mode" type="button" :data-test="`body-mode-${mode}`" :aria-pressed="bodyMode === mode" class="rounded px-2.5 py-1 transition-colors" :class="bodyMode === mode ? 'bg-slate-700 text-white' : 'text-slate-400 hover:text-slate-100'" @click="setBodyMode(mode)">{{ t(`admin.requestAudit.bodyContent.${mode}`) }}</button>
        </div>
        <label v-if="part !== 'body'" class="flex items-center gap-2"><input v-model="panels[part].reveal" type="checkbox" :data-test="`reveal-${part}`" class="rounded border-slate-600 accent-primary-500" @change="loadPart(part)" />{{ t('admin.requestAudit.reveal') }}</label>
        <label v-if="canAllowPartial(part)" class="flex items-center gap-2 text-amber-300"><input v-model="panels[part].allowPartial" type="checkbox" :data-test="`partial-${part}`" class="rounded border-slate-600 accent-amber-500" @change="loadPart(part)" />{{ t('admin.requestAudit.allowPartial') }}</label>
        <label v-if="pretty[part] !== null && (part !== 'body' || bodyMode === 'raw')" class="flex items-center gap-2" :title="t('admin.requestAudit.formatJson')"><input v-model="panels[part].formatted" type="checkbox" :data-test="`format-${part}`" class="rounded border-slate-600 accent-primary-500" />{{ t('admin.requestAudit.payloads.pretty') }}</label>
      </div>
      <div class="bg-[#070c15]">
        <p v-if="panels[part].loading" role="status" class="px-4 py-10 text-center text-xs text-slate-400">{{ t('common.loading') }}</p>
        <div v-else-if="panels[part].error" role="alert" class="space-y-3 px-4 py-5">
          <p class="break-words text-sm text-red-300">{{ panels[part].error }}</p>
          <button type="button" :data-test="`retry-${part}`" class="btn btn-secondary btn-sm" :disabled="panels[part].downloading" @click="loadPart(part, panels[part].offset)">{{ t('admin.requestAudit.retry') }}</button>
        </div>
        <template v-else-if="panels[part].payload">
          <p v-if="panels[part].payload?.returned_bytes === 0" :data-test="`empty-${part}`" class="px-4 py-8 text-center text-xs text-slate-400">{{ t('admin.requestAudit.payloads.empty') }}</p>
          <template v-else-if="part === 'body' && bodyMode === 'content'">
            <p v-if="bodyContent.loading" role="status" class="px-4 py-10 text-center text-xs text-slate-400">{{ t('admin.requestAudit.bodyContent.loading', { received: number(bodyContent.received), total: number(bodyContent.total) }) }}</p>
            <div v-else-if="bodyContent.error" role="alert" class="space-y-3 px-4 py-5">
              <p class="break-words text-sm text-red-300">{{ bodyContent.error }}</p>
              <button type="button" data-test="retry-body-content" class="btn btn-secondary btn-sm" @click="loadPart('body')">{{ t('admin.requestAudit.retry') }}</button>
            </div>
            <p v-else-if="bodyContent.hint" data-test="body-content-hint" class="px-4 py-6 text-sm leading-6 text-amber-300">{{ t(`admin.requestAudit.bodyContent.hints.${bodyContent.hint}`) }}</p>
            <RequestAuditBodyContent v-else-if="bodyContent.content !== null" :content="bodyContent.content" :fallback="panels.body.payload!" />
            <pre v-if="bodyContent.hint || bodyContent.error" data-test="content-body" tabindex="0" class="max-h-80 overflow-auto whitespace-pre-wrap break-all px-4 pb-4 font-mono text-xs leading-6 text-slate-200">{{ panels.body.payload?.content }}</pre>
          </template>
          <pre v-else :data-test="`content-${part}`" :aria-label="t(`admin.requestAudit.parts.${part}`)" tabindex="0" class="min-h-24 overflow-auto whitespace-pre-wrap break-all px-4 py-4 font-mono text-xs leading-6 text-slate-200 selection:bg-primary-500/30 focus:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-primary-500" :class="part === 'body' ? 'max-h-80' : 'max-h-56'">{{ panels[part].formatted && pretty[part] !== null ? pretty[part] : panels[part].payload?.content }}</pre>
        </template>
      </div>
      <footer class="space-y-2 border-t border-slate-700/60 px-4 py-3">
        <div :data-test="`manifest-${part}`" class="space-y-1 text-xs leading-5 text-slate-400" :title="t('admin.requestAudit.manifestHint')">
          <p v-if="record.terminal_manifest?.[part]" class="flex flex-wrap gap-x-2">
            <span>{{ t('admin.requestAudit.payloads.manifest') }} <span :class="record.terminal_manifest[part]?.complete ? 'text-emerald-300' : 'text-amber-300'">{{ stateLabel(record.terminal_manifest[part]?.complete ? 'complete' : 'partial') }}</span></span>
            <span class="font-mono tabular-nums">{{ t('admin.requestAudit.chunks', { count: number(record.terminal_manifest[part]!.expected_chunks) }) }}</span>
            <span class="font-mono tabular-nums">{{ t('admin.requestAudit.payloads.manifestBytes', { count: number(record.terminal_manifest[part]!.expected_bytes) }) }}</span>
          </p>
          <p v-else>{{ t('admin.requestAudit.manifestUnavailable') }}</p>
          <p v-if="record.terminal_manifest?.[part]?.reason" class="break-all text-amber-300">{{ record.terminal_manifest[part]?.reason }}</p>
        </div>
        <p v-if="panels[part].payload?.partial" class="text-xs leading-5 text-amber-300">{{ t('admin.requestAudit.partialHint') }}</p>
        <p v-if="panels[part].payload?.redacted" class="text-xs leading-5 text-slate-400">{{ t('admin.requestAudit.redactedHint') }}</p>
        <p v-if="panels[part].payload?.encoding === 'base64' && (part !== 'body' || bodyMode === 'raw' || bodyContent.hint || bodyContent.error)" class="text-xs leading-5 text-slate-400">{{ t('admin.requestAudit.binaryHint') }}</p>
        <p v-if="pretty[part] !== null && panels[part].formatted && (part !== 'body' || bodyMode === 'raw')" class="text-xs leading-5 text-slate-400">{{ t('admin.requestAudit.payloads.formatHint') }}</p>
        <p v-if="part === 'body' && bodyMode === 'content'" class="text-xs leading-5 text-slate-400">{{ t('admin.requestAudit.bodyContent.limitHint') }}</p>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="font-mono text-[11px] tabular-nums text-slate-400">{{ part === 'body' && bodyMode === 'content' ? t('admin.requestAudit.payloads.range', { from: 0, to: number(bodyContent.received), total: number(bodyContent.total) }) : panels[part].payload ? t('admin.requestAudit.payloads.range', { from: number(panels[part].payload!.offset), to: number(panels[part].payload!.offset + panels[part].payload!.returned_bytes), total: number(panels[part].payload!.part_bytes) }) : t('admin.requestAudit.payloads.pageSize') }}</span>
          <div v-if="part !== 'body' || bodyMode === 'raw'" class="flex gap-2">
            <button type="button" :data-test="`previous-${part}`" class="btn btn-secondary btn-sm" :disabled="!panels[part].payload?.offset || panels[part].loading || panels[part].downloading" @click="loadPart(part, Math.max(0, panels[part].offset - pageSize))">{{ t('admin.requestAudit.payloads.previous') }}</button>
            <button type="button" :data-test="`next-${part}`" class="btn btn-secondary btn-sm" :disabled="!panels[part].payload?.has_more || !panels[part].payload?.returned_bytes || panels[part].loading || panels[part].downloading" @click="loadPart(part, panels[part].offset + (panels[part].payload?.returned_bytes || 0))">{{ t('admin.requestAudit.payloads.next') }}</button>
          </div>
        </div>
      </footer>
    </section>
  </div>
</template>

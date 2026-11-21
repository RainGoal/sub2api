<template>
  <form class="card p-5" @submit.prevent="save">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="font-semibold">{{ t('admin.requestAudit.groupPolicies.title') }}</h2>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || saving" @click="load">{{ t('admin.requestAudit.reload') }}</button>
    </div>
    <p v-if="loading" role="status" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-500">{{ error }}</p>
    <fieldset v-if="draft" :disabled="loading || saving" class="mt-4 space-y-4">
      <label class="flex items-center justify-between gap-4 text-sm">
        {{ t('admin.requestAudit.groupPolicies.groupOnly') }}
        <input v-model="draft.group_only" type="checkbox" role="switch" class="h-4 w-4" data-test="audit-group-only" />
      </label>
      <p v-if="draft.group_only && !draft.rules.length" role="status" class="text-xs text-amber-600 dark:text-amber-400">
        {{ t('admin.requestAudit.groupPolicies.empty') }}
      </p>
      <div v-if="draft.group_only" class="space-y-3">
        <p v-if="upstreamCaptureAvailable === false && selectsUpstream" role="status" class="text-xs text-amber-600 dark:text-amber-400">
          {{ t('admin.requestAudit.groupPolicies.upstreamUnavailable') }}
        </p>
        <label for="audit-policy-group" class="block text-sm">{{ t('admin.requestAudit.groupPolicies.addGroup') }}</label>
        <div class="flex flex-wrap items-start gap-2">
          <div class="min-w-0 flex-1 sm:max-w-sm">
            <Select id="audit-policy-group" v-model="selected" :options="available" searchable clearable
              :aria-label="t('admin.requestAudit.groupPolicies.addGroup')" :placeholder="t('admin.requestAudit.groupSearchPlaceholder')"
              :disabled="loadingGroups || !!groupError" :loading="loadingGroups" />
          </div>
          <button type="button" class="btn btn-secondary" :disabled="!selected" data-test="audit-policy-add" @click="add">{{ t('admin.requestAudit.groupPolicies.add') }}</button>
        </div>
        <p v-if="groupError" role="alert" class="text-sm text-red-500">
          {{ groupError }}
          <button type="button" class="ms-2 text-primary-500 underline" @click="loadGroups">{{ t('admin.requestAudit.retry') }}</button>
        </p>
        <div v-if="draft.rules.length" class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700">
          <section v-for="rule in draft.rules" :key="rule.group_id" class="grid gap-3 py-4 lg:grid-cols-[minmax(9rem,1fr)_3fr_auto]" :data-test="`audit-policy-${rule.group_id}`">
            <h3 class="min-w-0 break-words text-sm font-semibold">{{ groupName(rule.group_id) }}</h3>
            <div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
              <label v-for="field in fields" :key="field" class="flex min-w-0 items-start gap-2 text-sm">
                <input v-model="rule[field]" type="checkbox" class="mt-0.5 h-4 w-4 shrink-0" :data-test="`policy-${field}`" />
                <span>{{ t(`admin.requestAudit.captureFields.${field}`) }}</span>
              </label>
            </div>
            <button type="button" class="btn btn-secondary btn-sm justify-self-start lg:justify-self-end" :aria-label="t('admin.requestAudit.groupPolicies.removeGroup', { group: groupName(rule.group_id) })" @click="remove(rule.group_id)">
              {{ t('admin.requestAudit.groupPolicies.remove') }}
            </button>
          </section>
        </div>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2 border-t border-gray-200 pt-3 dark:border-dark-700">
        <p v-if="draft.group_only && invalidRule" class="me-auto text-xs text-amber-600 dark:text-amber-400">{{ t('admin.requestAudit.groupPolicies.fieldsRequired') }}</p>
        <button type="submit" class="btn btn-primary" data-test="save-audit-policies" :disabled="!dirty || saving || (draft.group_only && invalidRule)">{{ saving ? t('common.saving') : t('common.save') }}</button>
      </div>
    </fieldset>
  </form>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import api from './api'
import { REQUEST_AUDIT_CAPTURE_FIELDS } from './types'
import type { RequestAuditGroupPolicies, RequestAuditGroupRule } from './types'

defineProps<{ upstreamCaptureAvailable?: boolean }>()
const { t } = useI18n()
const app = useAppStore()
const fields = REQUEST_AUDIT_CAPTURE_FIELDS
const initial = ref<RequestAuditGroupPolicies | null>(null)
const draft = ref<RequestAuditGroupPolicies | null>(null)
const groups = ref<{ value: number; label: string }[]>([])
const selected = ref<number | null>(null)
const loading = ref(false)
const loadingGroups = ref(false)
const saving = ref(false)
const error = ref('')
const groupError = ref('')
let active = true

const available = computed(() => groups.value.filter(option => !draft.value?.rules.some(rule => rule.group_id === option.value)))
const invalidRule = computed(() => !!draft.value?.rules.some(rule => fields.every(field => !rule[field])))
const selectsUpstream = computed(() => !!draft.value?.rules.some(rule => fields.some(field => field.startsWith('upstream_') && rule[field])))
const dirty = computed(() => JSON.stringify(initial.value) !== JSON.stringify(draft.value))
const groupName = (id: number) => groups.value.find(group => group.value === id)?.label || t('admin.requestAudit.groupFallback', { id })
const errorText = (reason: unknown) => extractI18nErrorMessage(reason, t, 'admin.requestAudit.errors', t('admin.requestAudit.failed'))

function applyPolicies(policies: RequestAuditGroupPolicies) {
  initial.value = { ...policies, rules: policies.rules ?? [] }
  draft.value = { ...initial.value, rules: initial.value.rules.map(rule => ({ ...rule })) }
  selected.value = null
}
async function load() {
  if (loading.value || saving.value) return
  loading.value = true
  error.value = ''
  try {
    const data = await api.getGroupPolicies()
    if (active) applyPolicies(data)
  } catch (reason) {
    if (active) error.value = errorText(reason)
  } finally {
    if (active) loading.value = false
  }
}
async function loadGroups() {
  if (loadingGroups.value) return
  loadingGroups.value = true
  groupError.value = ''
  try {
    const data = await getAllIncludingInactive()
    if (active) groups.value = data.map(group => ({ value: group.id, label: group.name }))
  } catch (reason) {
    if (active) groupError.value = errorText(reason)
  } finally {
    if (active) loadingGroups.value = false
  }
}
function add() {
  if (!draft.value || !selected.value || !available.value.some(group => group.value === selected.value)) return
  const rule: RequestAuditGroupRule = {
    group_id: selected.value, inbound_headers: false, inbound_body: false,
    upstream_request_headers: false, upstream_request_body: false,
    upstream_response_headers: false, upstream_response_body: false,
  }
  draft.value.rules.push(rule)
  selected.value = null
}
function remove(id: number) {
  if (draft.value) draft.value.rules = draft.value.rules.filter(rule => rule.group_id !== id)
}
async function save() {
  if (!draft.value || saving.value || loading.value || !dirty.value || (draft.value.group_only && invalidRule.value)) return
  saving.value = true
  error.value = ''
  try {
    const input = { ...draft.value, rules: draft.value.rules.filter(rule => fields.some(field => rule[field])) }
    const data = await api.updateGroupPolicies(input)
    if (active) {
      applyPolicies(data)
      app.showSuccess(t('admin.requestAudit.saved'))
    }
  } catch (reason) {
    if (active) error.value = errorText(reason)
  } finally {
    if (active) saving.value = false
  }
}

watch(() => draft.value?.group_only, enabled => { if (enabled && !groups.value.length) void loadGroups() })
onMounted(() => { void load() })
onBeforeUnmount(() => { active = false })
</script>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { OpenAICollaborationSettings } from '@/api/admin/settings'
import type { AdminGroup } from '@/types'
import { useAppStore } from '@/stores'
import Toggle from '@/components/common/Toggle.vue'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const { t } = useI18n()
const appStore = useAppStore()
const prefix = 'admin.settings.openaiCollaboration.'
const defaultBlankPreludeWaitSeconds = 2
const minBlankPreludeWaitSeconds = 2
const maxBlankPreludeWaitSeconds = 15
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const groupsLoaded = ref(false)
const error = ref('')
const groupsError = ref('')
const saved = ref<OpenAICollaborationSettings | null>(null)
const draft = ref<OpenAICollaborationSettings>({
  enabled: false, plaintext_enabled: false, legacy_cipher_recovery_enabled: false,
  blank_prelude_wait_seconds: defaultBlankPreludeWaitSeconds,
  group_ids: [], api_key_ids: []
})
const groups = ref<AdminGroup[]>([])
const keyIDs = ref('')
const busy = computed(() => loading.value || saving.value)
const availableGroups = computed(() => {
  const options = groups.value.map(group => ({ id: group.id, name: group.name }))
  for (const id of draft.value.group_ids) {
    if (!options.some(group => group.id === id)) options.push({ id, name: t(prefix + 'unavailableGroup', { id }) })
  }
  return options
})

function normalizeConfig(value: unknown): OpenAICollaborationSettings | null {
  if (!value || typeof value !== 'object') return null
  const config = value as OpenAICollaborationSettings
  if (typeof config.enabled !== 'boolean' || typeof config.plaintext_enabled !== 'boolean' ||
    typeof config.legacy_cipher_recovery_enabled !== 'boolean' ||
    ![config.group_ids, config.api_key_ids].every(ids => Array.isArray(ids) &&
      ids.every(id => Number.isSafeInteger(id) && id > 0))) return null
  const wait = config.blank_prelude_wait_seconds === undefined ? defaultBlankPreludeWaitSeconds : config.blank_prelude_wait_seconds
  if (!Number.isInteger(wait) || wait < minBlankPreludeWaitSeconds || wait > maxBlankPreludeWaitSeconds) return null
  return { ...config, blank_prelude_wait_seconds: wait, group_ids: [...config.group_ids], api_key_ids: [...config.api_key_ids] }
}
function copy(config: OpenAICollaborationSettings): OpenAICollaborationSettings {
  return { ...config, group_ids: [...config.group_ids], api_key_ids: [...config.api_key_ids] }
}
function accept(config: OpenAICollaborationSettings): void {
  saved.value = copy(config)
  draft.value = copy(config)
  keyIDs.value = config.api_key_ids.join(', ')
  loaded.value = true
}
function failure(cause: unknown, fallback: string): string {
  const status = cause && typeof cause === 'object' ? cause as { status?: number; response?: { status?: number } } : undefined
  return t(prefix + (status?.status === 403 || status?.response?.status === 403 ? 'forbidden' : fallback))
}
async function load(): Promise<void> {
  if (busy.value) return
  loading.value = true
  loaded.value = false
  groupsLoaded.value = false
  error.value = ''
  groupsError.value = ''
  const [settingsResult, groupsResult] = await Promise.allSettled([
    adminAPI.settings.getSettings(), adminAPI.groups.getAll()
  ])
  const normalized = settingsResult.status === 'fulfilled' ? normalizeConfig(settingsResult.value?.openai_collaboration_settings) : null
  if (normalized) {
    accept(normalized)
  } else {
    error.value = failure(settingsResult.status === 'rejected' ? settingsResult.reason : null, 'loadFailed')
  }
  if (groupsResult.status === 'fulfilled' && Array.isArray(groupsResult.value)) {
    groups.value = groupsResult.value.filter(group => group.platform === 'openai' || group.platform === 'composite')
    groupsLoaded.value = true
  } else {
    groupsError.value = failure(groupsResult.status === 'rejected' ? groupsResult.reason : null, 'groupsFailed')
  }
  loading.value = false
}
const dirty = computed(() => loaded.value && saved.value &&
  (JSON.stringify(draft.value) !== JSON.stringify(saved.value) || keyIDs.value !== saved.value.api_key_ids.join(', ')))
async function save(stop = false): Promise<void> {
  if (!loaded.value || !saved.value || busy.value) return
  error.value = ''
  const config = copy(stop ? saved.value : draft.value)
  if (stop) {
    config.enabled = false
  } else {
    const parts = keyIDs.value.trim() ? keyIDs.value.trim().split(/[\s,，]+/) : []
    if (parts.some(id => !/^[1-9]\d*$/.test(id) || !Number.isSafeInteger(Number(id)))) {
      error.value = t(prefix + 'invalidKeyIDs')
      return
    }
    config.api_key_ids = [...new Set(parts.map(Number))]
    if (!Number.isInteger(config.blank_prelude_wait_seconds) || config.blank_prelude_wait_seconds < minBlankPreludeWaitSeconds || config.blank_prelude_wait_seconds > maxBlankPreludeWaitSeconds) {
      error.value = t(prefix + 'invalidBlankPreludeWaitSeconds')
      return
    }
    if (config.enabled && (!groupsLoaded.value || config.group_ids.length === 0)) {
      error.value = t(prefix + 'selectGroups')
      return
    }
    if (config.enabled && !config.plaintext_enabled && !config.legacy_cipher_recovery_enabled) {
      error.value = t(prefix + 'selectMode')
      return
    }
  }
  saving.value = true
  try {
    const result = await adminAPI.settings.updateSettings({ openai_collaboration_settings: config })
    const normalized = normalizeConfig(result?.openai_collaboration_settings)
    if (!normalized || normalized.blank_prelude_wait_seconds !== config.blank_prelude_wait_seconds) {
      loaded.value = false
      error.value = t(prefix + 'unconfirmed')
      return
    }
    accept(normalized)
    appStore.showSuccess(t(prefix + (stop ? 'stopped' : 'saved')))
  } catch (cause) {
    error.value = failure(cause, 'saveFailed')
    appStore.showError(error.value)
  } finally {
    saving.value = false
  }
}

watch(() => props.active, active => {
  if (active && !loaded.value && !loading.value) void load()
}, { immediate: true })
</script>

<template>
  <section class="card" :aria-busy="busy" aria-labelledby="collaboration-title">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 id="collaboration-title" class="text-lg font-semibold text-gray-900 dark:text-white">{{ t(prefix + 'title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'description') }}</p>
    </div>
    <div class="space-y-5 p-6">
      <p v-if="loading" class="text-sm text-gray-500 dark:text-gray-400" role="status">{{ t(prefix + 'loading') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <p v-if="groupsError" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ groupsError }}</p>
      <template v-if="loaded">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p data-test="collaboration-status" class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t(prefix + (saved?.enabled ? 'active' : 'inactive')) }}
          </p>
          <button v-if="saved?.enabled" type="button" data-test="collaboration-stop" class="btn btn-secondary text-red-600 dark:text-red-300" :disabled="busy" @click="save(true)">
            {{ t(prefix + 'stop') }}
          </button>
        </div>
        <p class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-900 dark:text-gray-300">{{ t(prefix + 'rollbackHint') }}</p>
        <fieldset :disabled="busy" class="space-y-5">
          <div class="flex items-start justify-between gap-4">
            <div>
              <label id="collaboration-enabled-label" class="font-medium text-gray-900 dark:text-white">{{ t(prefix + 'enabled') }}</label>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'enabledHint') }}</p>
            </div>
            <Toggle v-model="draft.enabled" data-test="collaboration-enabled" aria-labelledby="collaboration-enabled-label" :disabled="busy" />
          </div>
          <div class="grid gap-4 lg:grid-cols-2">
            <div class="flex items-start justify-between gap-4 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
              <div>
                <label id="collaboration-plaintext-label" class="font-medium text-gray-900 dark:text-white">{{ t(prefix + 'plaintext') }}</label>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'plaintextHint') }}</p>
              </div>
              <Toggle v-model="draft.plaintext_enabled" data-test="collaboration-plaintext" aria-labelledby="collaboration-plaintext-label" :disabled="busy" />
            </div>
            <div class="flex items-start justify-between gap-4 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
              <div>
                <label id="collaboration-recovery-label" class="font-medium text-gray-900 dark:text-white">{{ t(prefix + 'recovery') }}</label>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'recoveryHint') }}</p>
              </div>
              <Toggle v-model="draft.legacy_cipher_recovery_enabled" data-test="collaboration-recovery" aria-labelledby="collaboration-recovery-label" :disabled="busy" />
            </div>
          </div>
          <p v-if="draft.legacy_cipher_recovery_enabled" class="text-sm text-amber-700 dark:text-amber-300">{{ t(prefix + 'lossyHint') }}</p>
          <div>
            <label for="collaboration-blank-wait" class="mb-2 block font-medium text-gray-900 dark:text-white">{{ t(prefix + 'blankPreludeWaitSeconds') }}</label>
            <input id="collaboration-blank-wait" v-model.number="draft.blank_prelude_wait_seconds" data-test="collaboration-blank-wait" type="number" :min="minBlankPreludeWaitSeconds" :max="maxBlankPreludeWaitSeconds" step="1" class="input max-w-40" aria-describedby="collaboration-blank-wait-hint" />
            <p id="collaboration-blank-wait-hint" class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'blankPreludeWaitHint') }}</p>
          </div>
          <fieldset :disabled="!groupsLoaded" aria-describedby="collaboration-groups-hint">
            <legend class="font-medium text-gray-900 dark:text-white">{{ t(prefix + 'groups') }}</legend>
            <p id="collaboration-groups-hint" class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'groupsHint') }}</p>
            <div class="mt-3 grid max-h-56 gap-2 overflow-y-auto sm:grid-cols-2">
              <label v-for="group in availableGroups" :key="group.id" class="flex min-w-0 items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-900 dark:border-dark-700 dark:text-white">
                <input v-model="draft.group_ids" type="checkbox" :value="group.id" :data-test="`collaboration-group-${group.id}`" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
                <span class="min-w-0 break-words">{{ group.name }}</span>
                <span class="ml-auto shrink-0 text-xs text-gray-500 dark:text-gray-400">#{{ group.id }}</span>
              </label>
            </div>
            <p v-if="groupsLoaded && availableGroups.length === 0" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'noGroups') }}</p>
          </fieldset>
          <div>
            <label for="collaboration-key-ids" class="mb-2 block font-medium text-gray-900 dark:text-white">{{ t(prefix + 'keyIDs') }}</label>
            <input id="collaboration-key-ids" v-model="keyIDs" data-test="collaboration-key-ids" type="text" class="input font-mono text-sm" :placeholder="t(prefix + 'keyIDsPlaceholder')" aria-describedby="collaboration-key-ids-hint" autocomplete="off" />
            <p id="collaboration-key-ids-hint" class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(prefix + 'keyIDsHint') }}</p>
          </div>
        </fieldset>
      </template>
      <div class="flex flex-wrap items-center gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
        <button type="button" data-test="collaboration-save" class="btn btn-primary" :disabled="!loaded || busy || !dirty" @click="save()">
          {{ t(prefix + (saving ? 'saving' : 'save')) }}
        </button>
        <button type="button" data-test="collaboration-reload" class="btn btn-secondary" :disabled="busy" @click="load()">{{ t(prefix + 'reload') }}</button>
        <span class="text-xs text-gray-500 dark:text-gray-400">{{ t(prefix + 'saveHint') }}</span>
      </div>
    </div>
  </section>
</template>

<template>
  <div class="space-y-1 text-xs text-gray-500 dark:text-dark-300">
    <label for="request-audit-user">{{ t('admin.requestAudit.user') }}</label>
    <Select id="request-audit-user" :model-value="modelValue || null" :options="options" remote clearable
      :loading="loading" :error="!!error" :aria-label="t('admin.requestAudit.user')"
      :placeholder="t('admin.requestAudit.userSearchPlaceholder')" :search-placeholder="t('admin.requestAudit.userSearchPlaceholder')"
      :empty-text="error || t(hasQuery ? 'admin.requestAudit.userSearchNoResults' : 'admin.requestAudit.userSearchEmpty')" @search="search" @change="select" />
    <p v-if="error" role="alert" class="text-red-500">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import { searchUsers } from '@/api/admin/usage'

const props = defineProps<{ modelValue: string, label?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string], 'update:label': [value: string] }>()
const { t } = useI18n()
const results = ref<SelectOption[]>([])
const loading = ref(false)
const error = ref('')
const hasQuery = ref(false)
let generation = 0
const options = computed(() => props.modelValue && !results.value.some(item => item.value === props.modelValue)
  ? [{ value: props.modelValue, label: props.label || t('admin.requestAudit.userFallback', { id: props.modelValue }) }, ...results.value] : results.value)

async function search(query: string) {
  const current = ++generation
  results.value = []; error.value = ''; loading.value = !!query.trim()
  hasQuery.value = !!query.trim()
  if (!query.trim()) return
  try {
    const users = await searchUsers(query.trim())
    if (current !== generation) return
    results.value = users.map(user => ({ value: String(user.id), label: `${user.email} · #${user.id}${user.deleted ? ` · ${t('admin.requestAudit.deletedUser')}` : ''}` }))
  } catch {
    if (current === generation) error.value = t('admin.requestAudit.userSearchFailed')
  } finally {
    if (current === generation) loading.value = false
  }
}
function select(value: SelectOption['value'], option: SelectOption | null) {
  generation++; loading.value = false; error.value = ''; results.value = []
  emit('update:modelValue', value ? String(value) : '')
  emit('update:label', option?.label || '')
}
watch(() => props.modelValue, value => {
  if (!value) {
    generation++; loading.value = false; error.value = ''; results.value = []
  }
})
onUnmounted(() => { generation++ })
</script>

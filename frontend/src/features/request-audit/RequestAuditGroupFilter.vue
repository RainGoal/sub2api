<template>
  <div class="flex min-w-0 flex-col gap-2 text-xs font-medium leading-5 text-gray-500 dark:text-dark-300">
    <label for="request-audit-group">{{ t('admin.requestAudit.group') }}</label>
    <Select id="request-audit-group" :model-value="modelValue || null" :options="options" searchable clearable
      :loading="loading" :error="!!error" :aria-label="t('admin.requestAudit.group')"
      :aria-describedby="error ? 'request-audit-group-error' : undefined"
      :placeholder="t(loading ? 'common.loading' : 'admin.requestAudit.allGroups')"
      :search-placeholder="t('admin.requestAudit.groupSearchPlaceholder')"
      :empty-text="error || t('admin.requestAudit.groupSearchEmpty')" @change="select" />
    <div v-if="error" class="flex items-start justify-between gap-2">
      <p id="request-audit-group-error" role="alert" class="text-red-500">{{ error }}</p>
      <button type="button" class="shrink-0 text-primary-500 hover:underline" :disabled="loading" data-test="retry-groups" @click="loadGroups">{{ t('admin.requestAudit.retry') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import { getAllIncludingInactive } from '@/api/admin/groups'

const props = defineProps<{ modelValue: string; label?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; 'update:label': [value: string] }>()
const { t } = useI18n()
const groups = ref<SelectOption[]>([])
const loading = ref(false)
const error = ref('')
let generation = 0
const options = computed(() => props.modelValue && !groups.value.some(group => group.value === props.modelValue)
  ? [{ value: props.modelValue, label: props.label || t('admin.requestAudit.groupFallback', { id: props.modelValue }) }, ...groups.value] : groups.value)

async function loadGroups() {
  if (loading.value) return
  const current = ++generation
  loading.value = true; error.value = ''
  try {
    const result = await getAllIncludingInactive()
    if (current === generation) groups.value = result.map(group => ({ value: String(group.id), label: group.name }))
  } catch {
    if (current === generation) error.value = t('admin.requestAudit.groupLoadFailed')
  } finally {
    if (current === generation) loading.value = false
  }
}
function select(value: SelectOption['value'], option: SelectOption | null) {
  emit('update:modelValue', value ? String(value) : '')
  emit('update:label', option?.label || '')
}
onMounted(() => { void loadGroups() })
onUnmounted(() => { generation++ })
</script>

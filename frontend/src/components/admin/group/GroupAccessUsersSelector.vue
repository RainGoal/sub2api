<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="group-access-users">
    <div>
      <h4 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.groups.accessUsers.title') }}</h4>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.accessUsers.description') }}</p>
    </div>

    <p v-if="loading" role="status" class="py-3 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('common.loading') }}
    </p>
    <div v-else-if="loadFailed" role="alert" class="flex flex-wrap items-center justify-between gap-2 text-sm text-red-600 dark:text-red-400">
      <span>{{ t('admin.groups.accessUsers.loadFailed') }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled" @click="loadUsers">
        {{ t('admin.groups.accessUsers.retry') }}
      </button>
    </div>
    <p v-else-if="users.length === 0" class="py-3 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.groups.accessUsers.empty') }}
    </p>
    <template v-else>
      <input
        v-model="search"
        type="search"
        class="input w-full"
        :placeholder="t('admin.groups.accessUsers.search')"
        :aria-label="t('admin.groups.accessUsers.search')"
        :disabled="disabled"
      />
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || !pageUsers.some(canSelect)" @click="selectUsers(pageUsers)">
          {{ t('admin.groups.accessUsers.selectPage') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || !users.some(canSelect)" @click="selectUsers(users)">
          {{ t('admin.groups.accessUsers.selectAll') }}
        </button>
        <button type="button" class="btn btn-ghost btn-sm" :disabled="disabled || selected.size === 0" @click="clearSelection">
          {{ t('admin.groups.accessUsers.clearSelection') }}
        </button>
        <span class="text-gray-600 dark:text-gray-400" aria-live="polite">
          {{ t('admin.groups.accessUsers.summary', { selected: selected.size, authorized: authorizedCount }) }}
        </span>
      </div>
      <div class="overflow-x-auto rounded border border-gray-200 dark:border-dark-600">
        <table class="w-full text-left text-sm">
          <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">
            <tr>
              <th class="px-2 py-2"><span class="sr-only">{{ t('admin.groups.accessUsers.selectUser') }}</span></th>
              <th class="px-2 py-2">{{ t('admin.groups.userInfo') }}</th>
              <th class="px-2 py-2">{{ t('admin.groups.columns.userNotes') }}</th>
              <th class="whitespace-nowrap px-2 py-2">{{ t('admin.groups.columns.rateMultiplier') }}</th>
              <th class="whitespace-nowrap px-2 py-2">{{ t('admin.groups.accessUsers.authorization') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
            <tr v-for="user in pageUsers" :key="user.user_id" :data-user-id="user.user_id">
              <td class="px-2 py-2 align-top">
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700"
                  :checked="user.has_explicit_access || selected.has(user.user_id)"
                  :disabled="disabled || !canSelect(user)"
                  :aria-label="t('admin.groups.accessUsers.selectUserLabel', { user: user.user_email || user.user_name || user.user_id })"
                  @change="toggleUser(user, ($event.target as HTMLInputElement).checked)"
                />
              </td>
              <td class="px-2 py-2">
                <div class="break-all font-medium text-gray-900 dark:text-white">{{ user.user_name || user.user_email }}</div>
                <div v-if="user.user_name" class="break-all text-xs text-gray-500 dark:text-gray-400">{{ user.user_email }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">
                  #{{ user.user_id }} · {{ user.user_status === 'active' ? t('common.active') : t('common.inactive') }}
                </div>
              </td>
              <td class="max-w-36 break-words px-2 py-2 text-xs text-gray-500 dark:text-gray-400">{{ user.user_notes || '—' }}</td>
              <td class="whitespace-nowrap px-2 py-2 text-gray-700 dark:text-gray-300">{{ user.rate_multiplier }}x</td>
              <td class="px-2 py-2 text-xs" :class="user.has_explicit_access ? 'text-green-700 dark:text-green-400' : 'text-gray-500 dark:text-gray-400'">
                {{ user.has_explicit_access ? t('admin.groups.accessUsers.authorized') : t('admin.groups.accessUsers.notAuthorized') }}
              </td>
            </tr>
            <tr v-if="pageUsers.length === 0">
              <td colspan="5" class="px-2 py-4 text-center text-gray-500 dark:text-gray-400">{{ t('admin.groups.accessUsers.noMatches') }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <!-- Pagination is also used outside forms; prevent its buttons from submitting this form. -->
      <Pagination
        v-if="filteredUsers.length > pageSize"
        :total="filteredUsers.length"
        :page="page"
        :page-size="pageSize"
        :show-page-size-selector="false"
        @update:page="page = $event"
        @click.prevent
      />
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.accessUsers.hint') }}</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { GroupRateMultiplierEntry } from '@/api/admin/groups'
import Pagination from '@/components/common/Pagination.vue'

const props = withDefaults(defineProps<{
  groupId: number
  selectByDefault: boolean
  disabled?: boolean
}>(), { disabled: false })

const emit = defineEmits<{
  'selection-change': [userIds: number[]]
  'ready-change': [ready: boolean]
}>()

const { t } = useI18n()
const users = ref<GroupRateMultiplierEntry[]>([])
const selected = ref(new Set<number>())
const loading = ref(true)
const loadFailed = ref(false)
const search = ref('')
const page = ref(1)
const pageSize = 10
let requestVersion = 0

const canSelect = (user: GroupRateMultiplierEntry) => user.user_status === 'active' && !user.has_explicit_access
const authorizedCount = computed(() => users.value.filter(user => user.has_explicit_access).length)
const filteredUsers = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return users.value.filter(user => !query || [user.user_id, user.user_name, user.user_email, user.user_notes]
    .some(value => String(value).toLocaleLowerCase().includes(query)))
})
const pageUsers = computed(() => filteredUsers.value.slice((page.value - 1) * pageSize, page.value * pageSize))

const emitSelection = () => emit('selection-change', [...selected.value])
const selectUsers = (entries: GroupRateMultiplierEntry[]) => {
  if (props.disabled) return
  for (const user of entries) if (canSelect(user)) selected.value.add(user.user_id)
  emitSelection()
}
const clearSelection = () => {
  if (props.disabled) return
  selected.value.clear()
  emitSelection()
}
const toggleUser = (user: GroupRateMultiplierEntry, checked: boolean) => {
  if (props.disabled || !canSelect(user)) return
  if (checked) selected.value.add(user.user_id)
  else selected.value.delete(user.user_id)
  emitSelection()
}

const loadUsers = async () => {
  const version = ++requestVersion
  loading.value = true
  loadFailed.value = false
  users.value = []
  selected.value.clear()
  search.value = ''
  page.value = 1
  emitSelection()
  emit('ready-change', false)
  try {
    const entries = await adminAPI.groups.getGroupRateMultipliers(props.groupId)
    if (version !== requestVersion) return
    users.value = entries.filter(user => user.rate_multiplier != null)
    if (props.selectByDefault) selectUsers(users.value)
    emit('ready-change', true)
  } catch {
    if (version === requestVersion) loadFailed.value = true
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

watch(() => props.groupId, loadUsers, { immediate: true })
watch(search, () => { page.value = 1 })
onUnmounted(() => { requestVersion++ })
</script>

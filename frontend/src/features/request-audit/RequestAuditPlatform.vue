<template>
  <span
    class="inline-flex min-w-0 items-center gap-1.5 rounded px-2 py-1 text-xs font-medium"
    :class="[platformBadgeClass(platform || ''), groupName ? 'max-w-44 sm:max-w-56' : 'max-w-full']"
    :title="groupName ? `${label} · ${t('admin.requestAudit.groupPlatformHint')}` : t('admin.requestAudit.groupPlatformHint')"
  >
    <PlatformIcon v-if="platform" :platform="platform as GroupPlatform" size="sm" aria-hidden="true" />
    <span class="min-w-0 truncate">{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { platformBadgeClass, platformLabel } from '@/utils/platformColors'
import type { GroupPlatform } from '@/types'

const props = defineProps<{ platform?: string; groupName?: string }>()
const { t } = useI18n()
const label = computed(() => {
  const platform = props.platform === 'composite' ? t('admin.requestAudit.multiPlatform') : props.platform ? platformLabel(props.platform) : t('admin.requestAudit.unknown')
  return props.groupName || platform
})
</script>

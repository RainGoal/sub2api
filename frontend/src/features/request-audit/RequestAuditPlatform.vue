<template>
  <span
    class="inline-flex max-w-full items-center gap-1.5 rounded px-2 py-1 text-xs font-medium"
    :class="platformBadgeClass(platform || '')"
    :title="t('admin.requestAudit.groupPlatformHint')"
  >
    <PlatformIcon v-if="platform" :platform="platform as GroupPlatform" size="sm" aria-hidden="true" />
    {{ platform === 'composite' ? t('admin.requestAudit.multiPlatform') : platform ? platformLabel(platform) : t('admin.requestAudit.unknown') }}
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { platformBadgeClass, platformLabel } from '@/utils/platformColors'
import type { GroupPlatform } from '@/types'

defineProps<{ platform?: string }>()
const { t } = useI18n()
</script>

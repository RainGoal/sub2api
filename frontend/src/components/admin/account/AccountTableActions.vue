<template>
  <div class="flex flex-wrap items-center gap-2">
    <slot name="before"></slot>
    <button
      type="button"
      class="btn btn-secondary btn-icon"
      :disabled="loading"
      :aria-label="t('common.refresh')"
      :title="t('common.refresh')"
      @click="$emit('refresh')"
    >
      <Icon name="refresh" size="sm" :class="{ 'motion-safe:animate-spin': loading }" />
    </button>
    <slot name="after"></slot>
    <slot name="beforeCreate"></slot>
    <button type="button" class="btn btn-primary" @click="$emit('create')">{{ t('admin.accounts.createAccount') }}</button>
    <slot name="afterCreate"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps(['loading'])
defineEmits(['refresh', 'create'])

const { t } = useI18n()
</script>

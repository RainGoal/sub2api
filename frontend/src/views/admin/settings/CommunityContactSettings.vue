<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CommunityContact } from '@/types'
import type { CommunityContactValidationError } from './communityContact'

const props = defineProps<{
  modelValue: CommunityContact
  disabled?: boolean
  error?: CommunityContactValidationError | null
}>()
const emit = defineEmits<{ 'update:modelValue': [value: CommunityContact] }>()
const { t } = useI18n()
const localizedFields = [
  { field: 'group_name', locale: 'zh-CN', label: 'groupNameZh', multiline: false },
  { field: 'group_name', locale: 'en-US', label: 'groupNameEn', multiline: false },
  { field: 'description', locale: 'zh-CN', label: 'descriptionZh', multiline: true },
  { field: 'description', locale: 'en-US', label: 'descriptionEn', multiline: true },
] as const
const contactFields = [
  { field: 'group_number', label: 'groupNumber', type: 'text', inputmode: 'numeric' },
  { field: 'invite_url', label: 'inviteUrl', type: 'url', inputmode: 'url' },
  { field: 'qr_image_url', label: 'qrImageUrl', type: 'text', inputmode: 'url' },
] as const

function updateLocalized(field: 'group_name' | 'description', locale: 'zh-CN' | 'en-US', event: Event) {
  emit('update:modelValue', {
    ...props.modelValue,
    [field]: { ...props.modelValue[field], [locale]: (event.target as HTMLInputElement).value },
  })
}

function updateContact(field: 'group_number' | 'invite_url' | 'qr_image_url', event: Event) {
  emit('update:modelValue', { ...props.modelValue, [field]: (event.target as HTMLInputElement).value })
}
</script>

<template>
  <fieldset
    :disabled="disabled"
    aria-labelledby="community-contact-title"
    class="min-w-0 border-t border-gray-100 pt-4 dark:border-dark-700"
    data-testid="community-contact-settings"
  >
    <h3 id="community-contact-title" class="text-sm font-medium text-gray-900 dark:text-white">
      {{ t('admin.settings.site.communityContact.title') }}
    </h3>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
      {{ t('admin.settings.site.communityContact.hint') }}
    </p>
    <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div v-for="field in localizedFields" :key="`${field.field}.${field.locale}`" class="min-w-0">
        <label
          :for="`community-contact-${field.field}.${field.locale}`"
          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
        >
          {{ t(`admin.settings.site.communityContact.${field.label}`) }}
        </label>
        <textarea
          v-if="field.multiline"
          :id="`community-contact-${field.field}.${field.locale}`"
          :value="modelValue[field.field][field.locale]"
          rows="3"
          class="input resize-y"
          :aria-invalid="error?.field === `${field.field}.${field.locale}`"
          :aria-describedby="error?.field === `${field.field}.${field.locale}` ? `community-contact-${error.field}-error` : undefined"
          @input="updateLocalized(field.field, field.locale, $event)"
        />
        <input
          v-else
          :id="`community-contact-${field.field}.${field.locale}`"
          :value="modelValue[field.field][field.locale]"
          type="text"
          class="input"
          :aria-invalid="error?.field === `${field.field}.${field.locale}`"
          :aria-describedby="error?.field === `${field.field}.${field.locale}` ? `community-contact-${error.field}-error` : undefined"
          @input="updateLocalized(field.field, field.locale, $event)"
        />
        <p
          v-if="error?.field === `${field.field}.${field.locale}`"
          :id="`community-contact-${error.field}-error`"
          role="alert"
          class="mt-1.5 text-xs text-red-600 dark:text-red-400"
        >
          {{ t(`admin.settings.site.communityContact.errors.${error.message}`) }}
        </p>
      </div>
      <div v-for="field in contactFields" :key="field.field" class="min-w-0 sm:col-span-2">
        <label
          :for="`community-contact-${field.field}`"
          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
        >
          {{ t(`admin.settings.site.communityContact.${field.label}`) }}
        </label>
        <input
          :id="`community-contact-${field.field}`"
          :value="modelValue[field.field]"
          :type="field.type"
          :inputmode="field.inputmode"
          class="input font-mono text-sm"
          :class="{ 'max-w-sm': field.field === 'group_number' }"
          :aria-invalid="error?.field === field.field"
          :aria-describedby="`community-contact-${field.field}-${error?.field === field.field ? 'error' : 'hint'}`"
          @input="updateContact(field.field, $event)"
        />
        <p :id="`community-contact-${field.field}-hint`" class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
          {{ t(`admin.settings.site.communityContact.${field.label}Hint`) }}
        </p>
        <p
          v-if="error?.field === field.field"
          :id="`community-contact-${error.field}-error`"
          role="alert"
          class="mt-1.5 text-xs text-red-600 dark:text-red-400"
        >
          {{ t(`admin.settings.site.communityContact.errors.${error.message}`) }}
        </p>
      </div>
    </div>
  </fieldset>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { renderDocsMarkdown } from '@/utils/docsMarkdown'

const props = defineProps<{ title: string; summary: string; content: string }>()
const rendered = computed(() => renderDocsMarkdown(props.content))
</script>

<template>
  <article class="docs-markdown-preview">
    <h1>{{ title }}</h1>
    <p v-if="summary" class="docs-summary">{{ summary }}</p>
    <div class="docs-prose" v-html="rendered"></div>
  </article>
</template>

<style scoped>
.docs-markdown-preview { @apply mx-auto w-full max-w-3xl p-6 text-gray-800 dark:text-gray-200; overflow-wrap: anywhere; }
.docs-markdown-preview h1 { @apply mb-4 text-3xl font-semibold leading-tight text-gray-900 dark:text-white; }
.docs-summary { @apply mb-8 text-base leading-7 text-gray-500 dark:text-gray-400; }
.docs-prose { @apply text-base leading-8; }
.docs-prose :deep(h1), .docs-prose :deep(h2), .docs-prose :deep(h3), .docs-prose :deep(h4) { @apply mb-4 mt-8 font-semibold leading-snug text-gray-900 dark:text-white; }
.docs-prose :deep(h1) { @apply text-3xl; }
.docs-prose :deep(h2) { @apply border-b border-gray-200 pb-3 text-2xl dark:border-dark-600; }
.docs-prose :deep(h3) { @apply text-xl; }
.docs-prose :deep(p), .docs-prose :deep(ul), .docs-prose :deep(ol), .docs-prose :deep(blockquote), .docs-prose :deep(pre), .docs-prose :deep(table) { @apply my-4; }
.docs-prose :deep(ul) { @apply list-disc pl-6; }
.docs-prose :deep(ol) { @apply list-decimal pl-6; }
.docs-prose :deep(li + li) { @apply mt-2; }
.docs-prose :deep(a) { @apply text-primary-600 underline decoration-primary-500/40 underline-offset-4 dark:text-primary-400; }
.docs-prose :deep(pre) { @apply overflow-x-auto rounded-md border border-gray-200 bg-gray-950 p-4 text-sm leading-7 text-gray-100 dark:border-dark-600; }
.docs-prose :deep(code) { @apply font-mono text-sm; }
.docs-prose :deep(:not(pre) > code) { @apply rounded bg-gray-100 px-1 py-0.5 dark:bg-dark-700; }
.docs-prose :deep(blockquote) { @apply border-l-2 border-gray-300 pl-4 text-gray-500 dark:border-dark-500 dark:text-gray-400; }
.docs-prose :deep(table) { @apply block max-w-full overflow-x-auto border-collapse text-sm; }
.docs-prose :deep(th), .docs-prose :deep(td) { @apply border border-gray-200 px-4 py-2 text-left dark:border-dark-600; }
.docs-prose :deep(img) { @apply my-6 h-auto max-w-full rounded-md border border-gray-200 dark:border-dark-600; }
.docs-prose :deep(hr) { @apply my-8 border-gray-200 dark:border-dark-600; }
</style>

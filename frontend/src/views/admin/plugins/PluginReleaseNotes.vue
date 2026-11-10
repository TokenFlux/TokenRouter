<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

// PluginReleaseNotes.vue renders a publisher's Markdown changelog inside a framed,
// muted surface so it reads as quoted release material rather than host copy.
// Publisher text passes through Markdown rendering and DOMPurify before insertion.
// The styles are deliberately compact: changelogs usually repeat the plugin name
// or version as large headings, so every heading level renders at body size and
// lists keep their markers, letting the entries carry the content.

// Props carries the raw Markdown changelog from a catalog release.
const props = defineProps<{
  markdown: string // Publisher-authored Markdown changelog.
}>()

// html holds the sanitized rendering of the Markdown changelog.
const html = computed(() =>
  DOMPurify.sanitize(marked.parse(props.markdown, { async: false, gfm: true, breaks: true })),
)
</script>

<template>
  <div
    class="release-notes text-link-content rounded-surface border border-gray-100 bg-gray-50/70 px-4 py-3 text-sm leading-6 text-gray-700 dark:border-dark-700 dark:bg-dark-950 dark:text-dark-200"
    v-html="html"
  />
</template>

<style scoped>
/* Rendered Markdown arrives as raw HTML, so element rules reach it through :deep. */
.release-notes :deep(h1),
.release-notes :deep(h2),
.release-notes :deep(h3),
.release-notes :deep(h4) {
  @apply mb-1 mt-3 text-sm font-semibold text-gray-900 dark:text-dark-50;
}

.release-notes :deep(p),
.release-notes :deep(ul),
.release-notes :deep(ol),
.release-notes :deep(pre) {
  @apply my-2;
}

.release-notes :deep(ul) {
  @apply list-disc pl-5;
}

.release-notes :deep(ol) {
  @apply list-decimal pl-5;
}

.release-notes :deep(li) {
  @apply my-0.5 pl-1;
}

.release-notes :deep(li::marker) {
  @apply text-gray-400 dark:text-dark-500;
}

.release-notes :deep(code) {
  @apply rounded-compact bg-gray-100 px-1 py-0.5 font-mono text-xs dark:bg-dark-800;
}

.release-notes :deep(pre) {
  @apply overflow-x-auto rounded-control bg-gray-100 p-3 dark:bg-dark-800;
}

.release-notes :deep(pre code) {
  @apply bg-transparent p-0;
}

/* The frame supplies outer spacing, so the first and last blocks drop their margins. */
.release-notes > :deep(:first-child) {
  @apply mt-0;
}

.release-notes > :deep(:last-child) {
  @apply mb-0;
}
</style>

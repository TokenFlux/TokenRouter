<template>
  <!-- Administrator checkout instructions appear as left-aligned supporting text. -->
  <div class="flex items-start gap-3 text-sm text-gray-500 dark:text-dark-400">
    <Icon
      name="infoCircle"
      size="sm"
      :animate-on-hover="false"
      class="mt-0.5 shrink-0 text-gray-400 dark:text-dark-500"
    />
    <div class="min-w-0 flex-1 space-y-3">
      <div
        v-if="html"
        class="payment-help-markdown text-link-content break-words leading-relaxed"
        v-html="html"
      ></div>
      <button
        v-if="imageUrl"
        type="button"
        class="block overflow-hidden rounded-control border border-gray-200 transition-opacity hover:opacity-80 dark:border-dark-600"
        @click="emit('preview', imageUrl)"
      >
        <img :src="imageUrl" alt="" class="h-32 max-w-full object-contain" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'

/**
 * PaymentHelpNote.vue displays administrator-provided checkout instructions and an
 * optional illustration. Its parent supplies sanitized HTML, and clicking the illustration
 * emits its URL so the page can own the image preview overlay and its lifecycle.
 */

defineProps<{
  /** html contains checkout instructions rendered from Markdown and sanitized by the owning page before this component displays them. */
  html: string
  /** imageUrl names the supporting illustration; an empty value hides the image and its preview action. */
  imageUrl: string
}>()

const emit = defineEmits<{
  preview: [url: string] // requested illustration preview URL
}>()
</script>

<style scoped>
.payment-help-markdown :deep(p) {
  margin: 0.25rem 0;
}

.payment-help-markdown :deep(p:first-child) {
  margin-top: 0;
}

.payment-help-markdown :deep(p:last-child) {
  margin-bottom: 0;
}

.payment-help-markdown :deep(a) {
  font-weight: 500;
}

.payment-help-markdown :deep(ul),
.payment-help-markdown :deep(ol) {
  margin: 0.25rem 0;
  padding-left: 1.25rem;
}

.payment-help-markdown :deep(ul) {
  list-style: disc;
}

.payment-help-markdown :deep(ol) {
  list-style: decimal;
}

.payment-help-markdown :deep(strong) {
  color: theme('colors.gray.700');
  font-weight: 600;
}

.dark .payment-help-markdown :deep(strong) {
  color: theme('colors.dark.100');
}
</style>

<template>
  <div class="min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white">
    <header class="border-b border-gray-200 bg-white/95 dark:border-dark-800 dark:bg-dark-900/95">
      <div class="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-4 sm:px-6">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-3">
          <template v-if="settings">
            <span
              class="flex h-10 w-10 flex-shrink-0 items-center justify-center overflow-hidden rounded-surface bg-white shadow-sm ring-1 ring-gray-200 dark:bg-dark-800 dark:ring-dark-700"
            >
              <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
            </span>
            <span class="truncate text-base font-semibold text-gray-950 dark:text-white">
              {{ siteName }}
            </span>
          </template>
          <template v-else>
            <span
              class="h-10 w-10 flex-shrink-0 animate-pulse rounded-surface bg-gray-200 dark:bg-dark-700"
              aria-hidden="true"
            ></span>
            <span
              class="h-5 w-28 animate-pulse rounded-compact bg-gray-200 dark:bg-dark-700"
              aria-hidden="true"
            ></span>
          </template>
        </RouterLink>
        <RouterLink to="/login" class="btn btn-primary shrink-0">
          {{ t('legal.login') }}
        </RouterLink>
      </div>
    </header>

    <main v-content-reveal="route.path" class="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:py-10">
      <ContentSkeleton v-if="loading" variant="article" :rows="4" class="min-h-80 py-6" />

      <section
        v-else-if="loadError"
        class="rounded-control border border-red-200 bg-red-50 p-6 text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-200"
      >
        <h1 class="text-lg font-semibold">{{ t('legal.loadFailed') }}</h1>
        <p class="mt-2 text-sm">{{ t('legal.retry') }}</p>
      </section>

      <section
        v-else-if="!currentDocument"
        class="rounded-surface border border-gray-200 bg-white p-6 dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="flex items-start gap-3">
          <span
            class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-control bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-300"
          >
            <Icon name="document" size="sm" />
          </span>
          <div>
            <h1 class="text-lg font-semibold text-gray-900 dark:text-white">{{
              t('legal.notFound')
            }}</h1>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('legal.notFoundDescription') }}
            </p>
          </div>
        </div>
      </section>

      <article v-else>
        <div class="mb-8 border-b border-gray-200 pb-6 dark:border-dark-700">
          <div class="flex items-start gap-4">
            <span
              class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-control bg-primary-50 text-primary-700 dark:bg-primary-500/10 dark:text-primary-300"
            >
              <Icon :name="documentIcon" size="md" />
            </span>
            <div class="min-w-0">
              <p class="text-sm font-medium text-primary-700 dark:text-primary-300">{{
                t('legal.title')
              }}</p>
              <h1
                class="mt-2 break-words text-2xl font-bold tracking-normal text-gray-950 dark:text-white sm:text-3xl"
              >
                {{ currentDocument.title }}
              </h1>
              <p v-if="updatedAt" class="mt-3 text-sm text-gray-500 dark:text-dark-400">
                {{ t('legal.updatedAt', { date: updatedAt }) }}
              </p>
            </div>
          </div>
        </div>

        <div
          v-if="hasContent"
          class="legal-document-content text-link-content"
          v-html="renderedHtml"
        ></div>
        <div
          v-else
          class="rounded-control border border-dashed border-gray-300 bg-white px-6 py-14 text-center text-sm text-gray-500 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-400"
        >
          {{ t('legal.empty') }}
        </div>
      </article>
    </main>
  </div>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { vContentReveal } from '@/directives/contentReveal'

import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import { useAppStore } from '@/stores/app'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import type { LoginAgreementDocument } from '@/api/settings.types'

// LegalDocumentView.vue loads the selected public legal document and renders its
// Markdown with the site's current branding. Route identifiers and language
// changes start a new request generation. Public settings are refreshed before
// requesting document content, and only the newest generation can publish the
// document, error state, or loading completion.
//
// Missing documents use the not-found presentation while other failures use the
// load-error presentation. Markdown is sanitized before HTML injection, and
// document-specific icons remain independent of content rendering. Generation
// ownership suppresses older responses during navigation; it does not cancel
// their HTTP requests or explicitly invalidate pending work when the component
// unmounts. The page preserves the shared Markdown parser's configured options.

// LegalDocumentIcon restricts the document heading to the supported semantic icons.
type LegalDocumentIcon = 'document' | 'shield' | 'globe' | 'cog'

// Route and locale services select content, while cached public settings supply
// branding and initialize loading until the first document refresh begins.
const { t, locale } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const settings = computed(() => appStore.cachedPublicSettings)
const loading = ref(!settings.value)
const loadError = ref(false)

// Keep line breaks and GitHub-style Markdown enabled for legal content; this
// updates the imported parser's shared options before computed rendering uses it.
marked.setOptions({
  breaks: true,
  gfm: true,
})

// Document identity and branding derive from the current route and refreshed
// settings; the logo source is filtered before falling back to the built-in asset.
const documentId = computed(() => String(route.params.documentId || ''))
const siteName = computed(() => settings.value?.site_name || 'TokenRouter')
const siteLogo = computed(() =>
  sanitizeUrl(settings.value?.site_logo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const updatedAt = computed(() => settings.value?.login_agreement_updated_at || '')

// The document reference belongs to the newest route-and-language generation;
// older reads may finish but cannot replace its content or completion flags.
const currentDocument = ref<LoginAgreementDocument | null>(null)
let documentGeneration = 0
// Refresh branding before document content, then gate every visible completion
// by generation so overlapping locale or route changes retain their newest state.
watch(
  [documentId, locale],
  async ([id]) => {
    const generation = ++documentGeneration
    loading.value = true
    loadError.value = false
    try {
      await appStore.fetchPublicSettings()
      const { data } = await apiClient.get<LoginAgreementDocument>(
        `/settings/legal/${encodeURIComponent(id)}`,
      )
      if (generation === documentGeneration) currentDocument.value = data
    } catch (error) {
      if (generation !== documentGeneration) return
      currentDocument.value = null
      loadError.value =
        (
          error as {
            // Optional HTTP status; only an explicit 404 selects the missing-document presentation.
            status?: number
          }
        ).status !== 404
    } finally {
      if (generation === documentGeneration) loading.value = false
    }
  },
  { immediate: true },
)

// Content display distinguishes an empty document from a missing document and
// sanitizes generated HTML before the template inserts it into the article body.
const hasContent = computed(() => Boolean(currentDocument.value?.content_md?.trim()))

const renderedHtml = computed(() => {
  const content = currentDocument.value?.content_md?.trim() || ''
  if (!content) {
    return ''
  }
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

// Known document identifiers choose a semantic heading icon; other configured
// legal documents retain the general document symbol without requiring a new mapping.
const documentIcon = computed<LegalDocumentIcon>(() => {
  const id = currentDocument.value?.id || ''
  if (id === 'usage-policy' || id === 'privacy') return 'shield'
  if (id === 'supported-regions') return 'globe'
  if (id === 'service-specific-terms') return 'cog'
  return 'document'
})
</script>

<style scoped>
/* Generated Markdown descendants need deep selectors to receive article styling
   without changing typography in the surrounding page header and status regions. */
.legal-document-content {
  line-height: 1.75;
  overflow-wrap: anywhere;
  color: inherit;
}

.legal-document-content :deep(h1) {
  @apply mb-4 mt-8 border-b border-gray-200 pb-3 text-3xl font-bold dark:border-dark-700;
}

.legal-document-content :deep(h2) {
  @apply mb-3 mt-7 text-2xl font-bold;
}

.legal-document-content :deep(h3) {
  @apply mb-2 mt-6 text-xl font-semibold;
}

.legal-document-content :deep(h4) {
  @apply mb-2 mt-5 text-lg font-semibold;
}

.legal-document-content :deep(p) {
  @apply mb-4 text-gray-700 dark:text-dark-200;
}

.legal-document-content :deep(ul) {
  @apply mb-4 list-disc pl-6;
}

.legal-document-content :deep(ol) {
  @apply mb-4 list-decimal pl-6;
}

.legal-document-content :deep(li) {
  @apply mb-1 text-gray-700 dark:text-dark-200;
}

.legal-document-content :deep(blockquote) {
  @apply my-5 border-l-4 border-gray-300 pl-4 text-gray-600 dark:border-dark-600 dark:text-dark-300;
}

.legal-document-content :deep(code) {
  @apply rounded-surface bg-gray-100 px-1.5 py-0.5 font-mono text-sm dark:bg-dark-800;
}

.legal-document-content :deep(pre) {
  @apply my-5 overflow-x-auto rounded-surface bg-gray-950 p-4 text-gray-100;
}

.legal-document-content :deep(pre code) {
  @apply bg-transparent p-0 text-inherit;
}

.legal-document-content :deep(table) {
  @apply my-5 block w-full overflow-x-auto border-collapse;
}

.legal-document-content :deep(th) {
  @apply border border-gray-300 bg-gray-50 px-3 py-2 text-left font-semibold dark:border-dark-600 dark:bg-dark-800;
}

.legal-document-content :deep(td) {
  @apply border border-gray-300 px-3 py-2 dark:border-dark-600;
}

.legal-document-content :deep(img) {
  @apply my-5 h-auto max-w-full rounded-control;
}

.legal-document-content :deep(hr) {
  @apply my-7 border-gray-200 dark:border-dark-700;
}
</style>

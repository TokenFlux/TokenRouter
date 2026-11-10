<template>
  <AppLayout fit-viewport="all">
    <div class="custom-page-layout">
      <div class="card flex-1 min-h-0 overflow-hidden">
        <ContentSkeleton
          v-if="loading"
          variant="article"
          :rows="4"
          class="h-full overflow-hidden p-6"
        />

        <div v-else-if="!menuItem" class="flex h-full items-center justify-center p-10 text-center">
          <EmptyState
            :title="t('customPage.notFoundTitle')"
            :description="t('customPage.notFoundDesc')"
          >
            <template #icon>
              <Icon name="link" class="empty-state-icon h-10 w-10" />
            </template>
          </EmptyState>
        </div>

        <div v-else-if="isMarkdownMode" class="flex h-full overflow-hidden">
          <MotionTransition persisted name="toc">
            <aside v-show="tocVisible" class="toc-sidebar">
              <div class="toc-header">
                <span class="toc-title">{{ t('customPage.toc') }}</span>
                <button
                  class="toc-close-btn"
                  type="button"
                  :title="t('customPage.hideToc')"
                  @click="tocVisible = false"
                >
                  <Icon name="chevronLeft" size="sm" :animate-on-hover="false" />
                </button>
              </div>
              <nav class="toc-nav">
                <a
                  v-for="item in tocItems"
                  :key="item.id"
                  :href="'#' + item.id"
                  class="toc-item"
                  :class="[
                    `toc-level-${item.level}`,
                    { 'toc-active': activeHeadingId === item.id },
                  ]"
                  @click.prevent="scrollToHeading(item.id)"
                >
                  {{ item.text }}
                </a>
              </nav>
            </aside>
          </MotionTransition>

          <button
            v-show="!tocVisible && tocItems.length > 0"
            type="button"
            class="toc-toggle-btn"
            :title="t('customPage.showToc')"
            @click="tocVisible = true"
          >
            <Icon name="menu" size="sm" />
            <span class="ml-1 text-xs">{{ t('customPage.toc') }}</span>
          </button>

          <div
            ref="markdownContainer"
            class="markdown-page-content text-link-content flex-1 h-full overflow-auto p-6 md:p-10"
            v-html="renderedHtml"
            @scroll="onContentScroll"
          ></div>
        </div>

        <div
          v-else-if="!isValidUrl"
          class="flex h-full items-center justify-center p-10 text-center"
        >
          <EmptyState
            :title="t('customPage.notConfiguredTitle')"
            :description="t('customPage.notConfiguredDesc')"
          >
            <template #icon>
              <Icon name="link" class="empty-state-icon h-10 w-10" />
            </template>
          </EmptyState>
        </div>

        <div v-else class="custom-embed-shell">
          <a
            :href="embeddedUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="btn btn-secondary btn-sm custom-open-fab"
          >
            <Icon name="externalLink" size="sm" class="mr-1.5" :stroke-width="2" />
            {{ t('customPage.openInNewTab') }}
          </a>
          <iframe :src="embeddedUrl" class="custom-embed-frame" allowfullscreen></iframe>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'

import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { buildApiUrl } from '@/api/client'
import { COPY_FEEDBACK_MS } from '@/constants/ui'
import { buildEmbeddedUrl, detectTheme } from '@/utils/embedded-url'
import { useMediaQuery } from '@vueuse/core'
import { MEDIA_MIN_MD, MEDIA_MAX_SM } from '@/constants/layout'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

// CustomPageView.vue displays a configured menu page as either embedded content
// or server-hosted Markdown. Public menu settings take precedence, with cached
// administrator menus available only to administrator accounts. Embedded URLs
// use the existing substitution helper for identity, language, and theme; Markdown
// is fetched with the current locale and optional authentication header.
//
// Markdown rendering rewrites eligible relative image paths, sanitizes generated
// HTML, adds heading identifiers, and installs local copy controls after rendering.
// Request generations are checked after response and text reads, but the error
// path and final copy-control pass retain their existing unconditional behavior.
// Unmount cleanup releases the tracked scroll frame and theme observer; requests
// and copy-feedback timeouts are not owned by that cleanup path.

// TocItem identifies one rendered heading in the page's local contents navigation.
interface TocItem {
  id: string // Generated heading anchor.
  text: string // Plain heading label.
  level: number // Heading depth, one through four.
}

// Page services supply route identity, locale, public menu settings, and the
// current account; administrator settings are read only for the privileged fallback.
const { t, locale } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const adminSettingsStore = useAdminSettingsStore()

// Display state holds the active mode's rendered content, theme, DOM container,
// and heading index while settings or Markdown requests are being resolved.
const loading = ref(false)
const pageTheme = ref<'light' | 'dark'>('light')
const renderedHtml = ref('')
const markdownContainer = ref<HTMLElement | null>(null)
const tocItems = ref<TocItem[]>([])
// Contents visibility samples the medium breakpoint once at setup. User actions
// subsequently own visibility, and resizing does not automatically replace that choice.
const tocVisible = ref(useMediaQuery(MEDIA_MIN_MD).value)
const activeHeadingId = ref('')
// The theme observer and scroll frame are the explicitly tracked browser resources;
// teardown disconnects or cancels them using these same handles.
let themeObserver: MutationObserver | null = null
let scrollRafId = 0

// Route selection first consults public menus and then the administrator cache,
// leaving unrecognized or inaccessible menu IDs in the not-found display state.
const menuItemId = computed(() => route.params.id as string)

const menuItem = computed(() => {
  const id = menuItemId.value
  // Public settings contain the user-visible menu entries and take precedence
  // even when the current administrator also has a complete cached menu list.
  const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
  const found = publicItems.find((item) => item.id === id) ?? null
  if (found) return found
  // Only administrator accounts may resolve an absent public entry from the
  // cached administrative list, which can include administrator-only menu pages.
  if (authStore.isAdmin) {
    return adminSettingsStore.customMenuItems.find((item) => item.id === id) ?? null
  }
  return null
})

// Markdown identity prefers the explicit page slug over the legacy md: URL form.
// A nonempty slug selects Markdown and prevents constructing an embedded URL.
const markdownSlug = computed(() => {
  const item = menuItem.value
  if (!item) return ''
  if (item.page_slug) return item.page_slug
  if (item.url?.startsWith('md:')) return item.url.slice(3)
  return ''
})

const isMarkdownMode = computed(() => !!markdownSlug.value)

// Embedded content uses the shared placeholder substitution contract, including
// account token input; this page then requires an HTTP(S) prefix for rendering.
const embeddedUrl = computed(() => {
  if (!menuItem.value || isMarkdownMode.value) return ''
  return buildEmbeddedUrl(
    menuItem.value.url,
    authStore.user?.id,
    authStore.token,
    pageTheme.value,
    locale.value,
  )
})

const isValidUrl = computed(() => {
  if (isMarkdownMode.value) return false
  const url = embeddedUrl.value
  return url.startsWith('http://') || url.startsWith('https://')
})

// generateHeadingId derives a readable anchor and appends its document-local index.
// Letters, digits, underscores, and the existing Han range survive normalization;
// an empty normalized heading uses the same index with a generic prefix.
function generateHeadingId(text: string, index: number): string {
  const base = text
    .toLowerCase()
    .replace(/[^\w一-鿿]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return base ? `${base}-${index}` : `heading-${index}`
}

// isRelativeMarkdownAsset recognizes image references eligible for page-relative rewriting.
// It rejects schemes, root paths, parent traversal segments, and backslashes after
// separating the query or fragment suffix. The caller retains other references unchanged.
function isRelativeMarkdownAsset(src: string): boolean {
  const trimmed = src.trim()
  if (
    !trimmed ||
    /^[a-z][a-z0-9+.-]*:/i.test(trimmed) ||
    trimmed.startsWith('//') ||
    trimmed.startsWith('/')
  ) {
    return false
  }
  const [pathPart] = trimmed.split(/([?#].*)/, 2)
  return pathPart
    .split('/')
    .filter((part) => part && part !== '.')
    .every((part) => part !== '..' && !part.includes('\\'))
}

// buildPageImageUrl converts a checked relative image reference into a page API URL.
// Callers first apply isRelativeMarkdownAsset so the path belongs beneath the
// selected page's image endpoint. The path and its query/fragment suffix are split
// before encoding, allowing each image segment and the page slug to be encoded
// independently while query values remain intact. When a response language is
// supplied, its encoded locale parameter joins the query before the fragment so
// the server receives the language selection with the image request.
function buildPageImageUrl(slug: string, src: string, language: string): string {
  const trimmed = src.trim()
  const [pathPart, suffix = ''] = trimmed.split(/([?#].*)/, 2)
  const encodedPath = pathPart
    .split('/')
    .filter((part) => part && part !== '.')
    .map((part) => encodeURIComponent(part))
    .join('/')
  const url = buildApiUrl(`/pages/${encodeURIComponent(slug)}/images/${encodedPath}${suffix}`)
  const [base, fragment] = url.split('#', 2)
  return language
    ? `${base}${base.includes('?') ? '&' : '?'}locale=${encodeURIComponent(language)}${fragment ? '#' + fragment : ''}`
    : url
}

// markdownGeneration advances for Markdown fetches and guards their response/text
// publication checkpoints; leaving Markdown mode and unmounting do not advance it.
let markdownGeneration = 0

// fetchAndRenderMarkdown loads one page slug and publishes sanitized article content.
// Each call takes a new generation, clears the heading state, and sends the
// current language and optional account token. Generation checks after the response
// and body reads discard older successful continuations before image rewriting
// and heading extraction publish the resulting content.
//
// Failed reads retain the existing error publication path without a generation
// check. The final block only gates loading completion, then waits for two render
// ticks and installs copy controls regardless of which generation completed.
// Requests are not aborted by a replacement call or component unmount.
async function fetchAndRenderMarkdown(slug: string) {
  const generation = ++markdownGeneration
  loading.value = true
  tocItems.value = []
  activeHeadingId.value = ''
  try {
    const resp = await fetch(buildApiUrl(`/pages/${encodeURIComponent(slug)}`), {
      headers: {
        'Accept-Language': locale.value,
        ...(authStore.token ? { Authorization: `Bearer ${authStore.token}` } : {}),
      },
    })
    if (generation !== markdownGeneration) return
    if (!resp.ok) {
      renderedHtml.value = `<p class="text-red-500">${t('customPage.markdownNotFound')}</p>`
      return
    }
    let raw = await resp.text()
    if (generation !== markdownGeneration) return
    const contentLanguage = resp.headers.get('Content-Language') || ''

    // Rewrite eligible Markdown image destinations before parsing so the generated
    // HTML points to the same page and language as the fetched document response.
    raw = raw.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (match, alt, src) =>
      isRelativeMarkdownAsset(src)
        ? `![${alt}](${buildPageImageUrl(slug, src, contentLanguage)})`
        : match,
    )

    // Sanitize parser output before adding anchors; configured embedded frames
    // retain the explicitly allowed attributes used by this page's content format.
    const html = marked.parse(raw) as string
    const sanitized = DOMPurify.sanitize(html, {
      ADD_TAGS: ['iframe'],
      ADD_ATTR: ['allowfullscreen', 'frameborder', 'src'],
    })

    // Assign document-local indexes after sanitization so repeated headings get
    // different anchors and contents entries refer to the HTML that will render.
    const toc: TocItem[] = []
    let headingIndex = 0
    const withIds = sanitized.replace(
      /<(h[1-4])[^>]*>(.*?)<\/h[1-4]>/gi,
      (_, tag: string, content: string) => {
        const level = Number.parseInt(tag[1], 10)
        const text = content.replace(/<[^>]+>/g, '').trim()
        const id = generateHeadingId(text, headingIndex++)
        toc.push({ id, text, level })
        return `<${tag} id="${id}">${content}</${tag}>`
      },
    )

    renderedHtml.value = withIds
    tocItems.value = toc
  } catch {
    // Fetching, decoding, or rendering failures replace the article with translated
    // error markup; this catch also publishes after supersession because it has no generation guard.
    renderedHtml.value = `<p class="text-red-500">${t('customPage.markdownLoadFailed')}</p>`
  } finally {
    if (generation === markdownGeneration) loading.value = false
    await nextTick()
    await nextTick()
    injectCopyButtons()
  }
}

// scrollToHeading scrolls the current Markdown container to a known anchor.
// Missing containers or targets do nothing. A successful jump updates the active
// entry and closes the contents drawer below the small-screen breakpoint.
function scrollToHeading(id: string) {
  const container = markdownContainer.value
  if (!container) return
  const el = container.querySelector(`#${CSS.escape(id)}`)
  if (!el) return
  el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  activeHeadingId.value = id
  // The maximum-small query ends below the matching minimum breakpoint, keeping
  // this drawer-close behavior separate from the desktop contents layout.
  if (window.matchMedia(MEDIA_MAX_SM).matches) {
    tocVisible.value = false
  }
}

// onContentScroll coalesces heading-position reads into one pending animation frame.
// Additional scroll events reuse the scheduled frame while scrollRafId holds its
// handle, so the callback reads the latest container geometry once. The callback
// clears the handle before inspecting headings, allowing a later event to schedule
// another update. It selects the last heading within the activation margin in
// document order, or clears the active entry when none qualifies. Unmount cleanup
// cancels a pending frame through the same handle.
function onContentScroll() {
  if (scrollRafId) return
  scrollRafId = requestAnimationFrame(() => {
    scrollRafId = 0
    const container = markdownContainer.value
    if (!container || tocItems.value.length === 0) return

    const containerRect = container.getBoundingClientRect()
    let current = ''
    for (const item of tocItems.value) {
      const el = container.querySelector(`#${CSS.escape(item.id)}`) as HTMLElement | null
      if (!el) continue
      const elRect = el.getBoundingClientRect()
      // Activate headings once they enter the top 100 pixels of the scroll container.
      if (elRect.top - containerRect.top <= 100) {
        current = item.id
      }
    }
    activeHeadingId.value = current
  })
}

// injectCopyButtons adds one native copy control to each rendered code block.
// Existing controls prevent duplicate listeners during repeated finalization passes.
// Each listener reads current code text, writes through the browser clipboard,
// and briefly replaces its label with the translated success or failure result.
//
// Buttons belong to the generated article DOM rather than Vue component instances.
// Their feedback timeouts retain the button reference and are not registered with
// component teardown, so a timeout may update a detached button after content
// replacement. Missing containers simply leave the article without new controls.
function injectCopyButtons() {
  const container = markdownContainer.value
  if (!container) return

  container.querySelectorAll('pre').forEach((pre) => {
    if (pre.querySelector('.copy-btn')) return
    const btn = document.createElement('button')
    btn.className = 'copy-btn'
    btn.textContent = t('common.copy')
    btn.addEventListener('click', async () => {
      const code = pre.querySelector('code')?.textContent ?? pre.textContent ?? ''
      try {
        await navigator.clipboard.writeText(code)
        btn.textContent = t('common.copied')
        window.setTimeout(() => {
          btn.textContent = t('common.copy')
        }, COPY_FEEDBACK_MS)
      } catch {
        // Clipboard rejection displays failure feedback, then restores the copy
        // label so the same button remains available for another user attempt.
        btn.textContent = t('common.copyFailed')
        window.setTimeout(() => {
          btn.textContent = t('common.copy')
        }, COPY_FEEDBACK_MS)
      }
    })
    ;(pre as HTMLElement).style.position = 'relative'
    pre.appendChild(btn)
  })
}

// Slug or locale changes start a new Markdown read; an empty slug clears visible
// article state while preserving the existing generation behavior for pending requests.
watch(
  [markdownSlug, locale],
  ([slug]) => {
    if (slug) {
      fetchAndRenderMarkdown(slug)
    } else {
      renderedHtml.value = ''
      tocItems.value = []
    }
  },
  { immediate: true },
)

// Initialize embedded-page theme from the root class and observe later theme
// changes. Public settings are fetched only when the shared store is not loaded.
onMounted(async () => {
  pageTheme.value = detectTheme()

  if (typeof document !== 'undefined') {
    themeObserver = new MutationObserver(() => {
      pageTheme.value = detectTheme()
    })
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    })
  }

  if (appStore.publicSettingsLoaded) return
  loading.value = true
  try {
    await appStore.fetchPublicSettings()
  } finally {
    loading.value = false
  }
})

// Release the tracked browser resources using their current handles; pending
// Markdown requests and per-button copy timeouts retain their independent completion paths.
onUnmounted(() => {
  if (scrollRafId) {
    cancelAnimationFrame(scrollRafId)
    scrollRafId = 0
  }
  if (themeObserver) {
    themeObserver.disconnect()
    themeObserver = null
  }
})
</script>

<style scoped>
.custom-page-layout {
  /* The viewport-bound layout passes remaining space below the header through
     this flex chain to the iframe or independently scrollable Markdown region. */
  @apply flex min-h-0 flex-1 flex-col;
}

.toc-sidebar {
  @apply flex flex-col h-full border-r border-gray-200 dark:border-dark-600 bg-gray-50 dark:bg-dark-800;
  width: min(240px, 30%);
  min-width: 160px;
  max-width: 280px;
  overflow: hidden;
}

@media (max-width: 639px) {
  /* This maximum is one pixel below BREAKPOINT_SM in constants/layout.ts,
     matching the mutually exclusive small-screen media query used by heading jumps. */
  .toc-sidebar {
    position: absolute;
    left: 0;
    top: 0;
    z-index: 20; /* check-ui-allow: local contents drawer above the custom page body */
    width: 70%;
    max-width: 240px;
    height: 100%;
    box-shadow: 2px 0 8px rgba(0, 0, 0, 0.1);
  }
}

/* Desktop contents collapse their width, while the narrow drawer uses translation
   so opening it does not resize the underlying article's available width. */
.toc-enter-active,
.toc-leave-active {
  transition:
    width var(--motion-layout) var(--motion-ease),
    min-width var(--motion-layout) var(--motion-ease),
    opacity var(--motion-layout) var(--motion-ease);
}
.toc-enter-from,
.toc-leave-to {
  width: 0;
  min-width: 0;
  opacity: 0;
}
/* The narrow drawer shares the maximum immediately below BREAKPOINT_SM. */
@media (max-width: 639px) {
  .toc-enter-active,
  .toc-leave-active {
    transition:
      transform var(--motion-layout) var(--motion-ease),
      opacity var(--motion-layout) var(--motion-ease);
  }
  .toc-enter-from,
  .toc-leave-to {
    width: 70%;
    min-width: 160px;
    transform: translateX(-100%);
  }
}
@media (prefers-reduced-motion: reduce) {
  .toc-enter-from,
  .toc-leave-to {
    transform: none;
  }
}

.toc-header {
  @apply flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-600;
}

.toc-title {
  @apply text-sm font-semibold text-gray-700 dark:text-dark-200;
}

.toc-close-btn {
  @apply p-1 rounded-compact text-gray-400 hover:text-gray-600 dark:hover:text-dark-200 hover:bg-gray-200 dark:hover:bg-dark-700 transition-colors;
}

.toc-nav {
  @apply flex-1 overflow-y-auto py-2 px-2;
}

.toc-item {
  @apply block px-2 py-1.5 text-sm rounded-compact transition-colors truncate;
  @apply text-gray-600 dark:text-dark-300 hover:text-gray-900 dark:hover:text-white hover:bg-gray-200 dark:hover:bg-dark-700;
}

.toc-item.toc-active {
  @apply text-primary-600 dark:text-primary-500 bg-primary-50 dark:bg-primary-500/8 font-medium;
}

.toc-level-1 {
  padding-left: 8px;
}
.toc-level-2 {
  padding-left: 20px;
}
.toc-level-3 {
  padding-left: 32px;
}
.toc-level-4 {
  padding-left: 44px;
}

.toc-toggle-btn {
  @apply absolute left-2 top-2 z-10 flex items-center px-2 py-1.5 rounded-control text-sm;
  @apply bg-white dark:bg-dark-700 border border-gray-200 dark:border-dark-500;
  @apply text-gray-600 dark:text-dark-300 hover:bg-gray-100 dark:hover:bg-dark-700;
  @apply shadow-sm transition-colors cursor-pointer;
}

.custom-embed-shell {
  @apply relative;
  @apply h-full w-full overflow-hidden rounded-surface;
  @apply bg-gradient-to-b from-gray-50 to-white dark:from-dark-900 dark:to-dark-950;
  @apply p-0;
}

.custom-open-fab {
  @apply absolute right-3 top-3 z-10;
  @apply shadow-sm backdrop-blur supports-[backdrop-filter]:bg-white/80 dark:supports-[backdrop-filter]:bg-dark-800/80;
}

.custom-embed-frame {
  display: block;
  margin: 0;
  width: 100%;
  height: 100%;
  border: 0;
  border-radius: 0;
  box-shadow: none;
  background: transparent;
}
</style>

<style>
/* Markdown HTML and injected copy controls do not receive scoped component
   attributes, so these article-specific selectors intentionally use an unscoped block. */
.markdown-page-content {
  line-height: 1.7;
  color: inherit;
}
.markdown-page-content h1 {
  @apply text-3xl font-bold mt-8 mb-4 pb-2 border-b border-gray-200 dark:border-dark-600;
}
.markdown-page-content h2 {
  @apply text-2xl font-bold mt-6 mb-3;
}
.markdown-page-content h3 {
  @apply text-xl font-semibold mt-5 mb-2;
}
.markdown-page-content h4 {
  @apply text-lg font-semibold mt-4 mb-2;
}
.markdown-page-content p {
  @apply mb-4;
}
.markdown-page-content ul {
  @apply list-disc pl-6 mb-4;
}
.markdown-page-content ol {
  @apply list-decimal pl-6 mb-4;
}
.markdown-page-content li {
  @apply mb-1;
}
.markdown-page-content blockquote {
  @apply border-l-4 border-gray-300 dark:border-dark-500 pl-4 italic text-gray-600 dark:text-dark-300 my-4;
}
.markdown-page-content img {
  @apply max-w-full h-auto rounded-control my-4;
}
.markdown-page-content table {
  @apply w-full border-collapse my-4;
}
.markdown-page-content th {
  @apply border border-gray-300 dark:border-dark-500 px-3 py-2 bg-gray-50 dark:bg-dark-700 font-semibold text-left;
}
.markdown-page-content td {
  @apply border border-gray-300 dark:border-dark-500 px-3 py-2;
}
.markdown-page-content code {
  @apply bg-gray-100 dark:bg-dark-700 px-1.5 py-0.5 rounded-surface text-sm font-mono;
}
.markdown-page-content pre {
  @apply bg-gray-900 dark:bg-dark-900 text-gray-100 p-4 rounded-surface overflow-x-auto my-4 relative;
}
.markdown-page-content pre code {
  @apply bg-transparent p-0 text-inherit;
}
.markdown-page-content hr {
  @apply my-6 border-gray-200 dark:border-dark-600;
}

.copy-btn {
  position: absolute;
  top: 8px;
  right: 8px;
  padding: 4px 10px;
  font-size: 12px;
  border-radius: var(--radius-compact);
  background: rgba(255, 255, 255, 0.15);
  color: #e2e8f0;
  border: 1px solid rgba(255, 255, 255, 0.2);
  cursor: pointer;
  opacity: 0;
  transition:
    opacity var(--motion-normal),
    background var(--motion-normal);
  font-family: inherit;
}
.copy-btn:hover {
  background: rgba(255, 255, 255, 0.25);
}
pre:hover .copy-btn {
  opacity: 1;
}
</style>

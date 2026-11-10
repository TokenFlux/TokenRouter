<template>
  <Teleport to="body">
    <MotionTransition name="modal" @after-leave="afterLeave">
      <div
        v-if="globalSearchOpen"
        class="modal-overlay global-search-overlay"
        data-testid="global-search-overlay"
        @click.self="closeGlobalSearch"
      >
        <div
          ref="panelRef"
          role="dialog"
          aria-modal="true"
          tabindex="-1"
          :aria-label="t('globalSearch.dialogLabel')"
          class="modal-content global-search-panel"
          data-testid="global-search-panel"
          @keydown="handlePanelKeydown"
        >
          <!-- The input row stays fixed while results change height below it. -->
          <div class="global-search-input-row">
            <Icon name="search" size="md" class="global-search-muted shrink-0" />
            <input
              ref="inputRef"
              v-model="query"
              type="text"
              role="combobox"
              aria-autocomplete="list"
              aria-expanded="true"
              :aria-controls="LISTBOX_ID"
              :aria-activedescendant="activeOptionId"
              :placeholder="t('globalSearch.inputPlaceholder')"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              enterkeyhint="go"
              class="global-search-input"
              data-testid="global-search-input"
            />
            <button
              v-if="query"
              type="button"
              tabindex="-1"
              class="btn-icon-sm global-search-muted shrink-0 hover:bg-primary-100 dark:hover:bg-dark-800"
              :aria-label="t('globalSearch.clear')"
              @click="clearQuery"
            >
              <Icon name="x" size="sm" />
            </button>
            <kbd class="kbd hidden sm:inline-flex">Esc</kbd>
            <button
              type="button"
              class="shrink-0 text-sm font-medium text-primary-600 dark:text-primary-500 sm:hidden"
              @click="closeGlobalSearch"
            >
              {{ t('common.cancel') }}
            </button>
          </div>

          <!-- Scope filters; Tab and Shift+Tab cycle them while focus stays in the input. -->
          <div class="shrink-0 px-3 pt-3">
            <div
              v-segmented
              class="segmented"
              role="group"
              :aria-label="t('globalSearch.scopeLabel')"
            >
              <button
                v-for="option in scopeOptions"
                :key="option.key"
                type="button"
                tabindex="-1"
                :aria-pressed="scope === option.key"
                :data-testid="`global-search-scope-${option.key}`"
                :class="[
                  'segmented-item flex items-center gap-1.5 px-3 py-1 text-sm',
                  scope === option.key && 'segmented-item-active',
                  option.count === 0 && 'opacity-50',
                ]"
                @click="selectScope(option.key)"
              >
                {{ option.label }}
                <span v-if="option.count !== null" class="text-xs tabular-nums">{{
                  option.count
                }}</span>
              </button>
            </div>
          </div>

          <!--
            Pointer presses keep focus in the input so typing continues after a click;
            rows are activated by their click events.
          -->
          <div
            :id="LISTBOX_ID"
            role="listbox"
            :aria-label="t('globalSearch.resultsLabel')"
            class="global-search-results"
            @mousedown.prevent
          >
            <p v-if="noResults" class="global-search-muted px-4 pb-1 pt-6 text-center text-sm">
              {{ t('globalSearch.noResults', { query: trimmedQuery }) }}
            </p>

            <section
              v-for="section in sectionLayout"
              :key="section.key"
              class="menu-section"
              role="group"
              :aria-label="section.title || undefined"
              :data-testid="`global-search-section-${section.key}`"
            >
              <div v-if="section.title" class="menu-heading">{{ section.title }}</div>

              <div
                v-if="section.status === 'loading'"
                aria-busy="true"
                :aria-label="t('globalSearch.loadingModels')"
              >
                <div
                  v-for="placeholder in LOADING_ROWS"
                  :key="placeholder"
                  class="flex min-h-11 items-center gap-3 px-2.5"
                >
                  <Skeleton variant="circle" :width="20" :height="20" />
                  <div class="flex-1 space-y-1.5">
                    <Skeleton width="40%" :height="12" />
                    <Skeleton width="24%" :height="10" />
                  </div>
                </div>
              </div>
              <div
                v-else-if="section.status === 'error'"
                class="flex items-center justify-between gap-3 px-2.5 py-2 text-sm"
              >
                <span class="global-search-muted">{{ t('globalSearch.loadFailed') }}</span>
                <button
                  type="button"
                  tabindex="-1"
                  class="btn btn-secondary btn-sm"
                  data-testid="global-search-retry"
                  @click="modelIndex.retry()"
                >
                  {{ t('common.retry') }}
                </button>
              </div>

              <GlobalSearchResultRow
                v-for="(row, index) in section.rows"
                :key="row.item.key"
                :ranked="row"
                :active="section.offset + index === activeIndex"
                :option-id="optionId(section.offset + index)"
                @select="selectRow(row)"
                @copy="copyModelId(row.item)"
                @pointer="pointAt(section.offset + index)"
              />
            </section>

            <p v-if="truncated" class="global-search-muted px-4 pb-3 pt-1 text-center text-xs">
              {{ t('globalSearch.truncated', { count: SCOPE_LIMIT }) }}
            </p>
          </div>

          <!-- Keyboard hints are hidden on touch-first narrow screens. -->
          <div class="global-search-footer">
            <span class="flex items-center gap-1">
              <kbd class="kbd">↑</kbd><kbd class="kbd">↓</kbd>{{ t('globalSearch.hints.navigate') }}
            </span>
            <span class="flex items-center gap-1">
              <kbd class="kbd">↵</kbd>{{ t('globalSearch.hints.open') }}
            </span>
            <span class="flex items-center gap-1">
              <kbd class="kbd">{{ modifierKey }}↵</kbd>{{ t('globalSearch.hints.copy') }}
            </span>
            <span class="flex items-center gap-1">
              <kbd class="kbd">Tab</kbd>{{ t('globalSearch.hints.scope') }}
            </span>
          </div>
        </div>
      </div>
    </MotionTransition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useEventListener } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import GlobalSearchResultRow from '@/components/layout/search/GlobalSearchResultRow.vue'
import {
  closeGlobalSearch,
  globalSearchAvailable,
  globalSearchOpen,
  openGlobalSearch,
} from '@/components/layout/search/globalSearchState'
import {
  MAX_RECENT_SEARCHES,
  readRecentSearches,
  recordRecentSearch,
} from '@/components/layout/search/recentSearches'
import {
  rankItems,
  useSearchCatalog,
  type ActionSearchItem,
  type RankedItem,
  type SearchItem,
  type SearchKind,
} from '@/components/layout/search/searchCatalog'
import {
  hasPrimaryModifier,
  isSlashShortcut,
  isToggleShortcut,
  modifierLabel,
} from '@/components/layout/search/searchShortcuts'
import { useClipboard } from '@/composables/useClipboard'
import { useDialogLifecycle } from '@/composables/useDialogLifecycle'
import { useMarketplaceModelIndex } from '@/composables/useMarketplaceModelIndex'
import { vSegmented } from '@/directives/segmented'
import { useAdminSettingsStore, useAppStore, useAuthStore } from '@/stores'

// GlobalSearchPalette.vue is the command palette opened from the header search entry or
// with Command+K, Control+K, or "/". One input searches marketplace models, the pages
// the account can open, and a few account actions. The palette is mounted once by
// App.vue; its open state and the shortcut switch come from globalSearchState.ts.
//
// Results are grouped into sections, but keyboard navigation treats them as one list:
// each section records the offset of its first row, and activeIndex points into the
// flattened rows. The input keeps focus the whole time and names the current row through
// aria-activedescendant, so arrow keys, Enter, and typing all work without moving focus.
// Before the user types, the palette shows recent visits, featured models, and common
// pages; with a query, each kind is ranked by searchCatalog.ts and the "All" scope shows
// the top few of each.
//
// The shell is written by hand instead of using BaseDialog because the palette has no
// title bar or close button, is anchored near the top of the viewport, and becomes a
// full-screen sheet on phones. It still uses useDialogLifecycle for scroll locking,
// focus restoration, and the topmost-dialog check other dialogs rely on.

// LISTBOX_ID ties the input's aria-controls to the results list; option IDs extend it.
const LISTBOX_ID = 'global-search-listbox'

// ALL_SCOPE_LIMIT caps each section in the "All" scope; SCOPE_LIMIT caps a single-kind
// scope, which keeps hundreds of models from rendering at once.
const ALL_SCOPE_LIMIT = 6
const SCOPE_LIMIT = 100

// FEATURED_LIMIT caps featured models, and COMMON_PAGE_KEYS lists the shortcut pages
// shown before typing, in display order. Keys the account cannot see are skipped.
const FEATURED_LIMIT = 5
const COMMON_PAGE_KEYS = [
  'page:/models',
  'page:/keys',
  'page:/usage',
  'page:/purchase',
  'page:/login',
  'page:docs',
]
const COMMON_PAGE_LIMIT = 4

// LOADING_ROWS is the number of skeleton rows shown while the model catalog loads.
const LOADING_ROWS = 3

// SearchScope is the "All" scope or a single result kind.
type SearchScope = 'all' | SearchKind

// ResultSection is one titled group of rows; status replaces the rows with a loading
// placeholder or an error message while the model catalog is unavailable.
interface ResultSection {
  key: string // Stable section identity.
  title: string // Heading text; empty hides it.
  rows: RankedItem[] // Rows in display order.
  status?: 'loading' | 'error' // Model catalog state.
}

// LaidOutSection adds the flattened index of a section's first row.
interface LaidOutSection extends ResultSection {
  offset: number // Index of rows[0] in the flat list.
}

// Translation, routing, stores, and the clipboard serve result rendering and selection;
// the model index and catalog supply the searchable items for the current account, and
// modifierKey labels the copy shortcut in the footer.
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const adminSettingsStore = useAdminSettingsStore()
const { copyToClipboard } = useClipboard()
const modelIndex = useMarketplaceModelIndex()
const catalog = useSearchCatalog(modelIndex.entries)
const modifierKey = modifierLabel()

// Panel elements: the lifecycle composable needs the panel, and opening focuses the input.
const panelRef = ref<HTMLElement | null>(null)
const inputRef = ref<HTMLInputElement | null>(null)
const { afterLeave } = useDialogLifecycle(() => globalSearchOpen.value, panelRef)

// query survives closing so reopening returns to the previous search, while scope
// returns to "All" on every open; activeIndex is the current row in the flattened list
// and resets when either changes.
const query = ref('')
const scope = ref<SearchScope>('all')
const activeIndex = ref(0)
const trimmedQuery = computed(() => query.value.trim())

// recentKeys mirrors the stored history for the current account and is reread on open.
const recentKeys = ref<string[]>([])
const historyOwner = computed(() =>
  authStore.isAuthenticated ? (authStore.user?.id ?? null) : null,
)

// itemsByKind and itemsByKey index the catalog for ranking and for resolving stored keys.
const itemsByKind = computed<Record<SearchKind, SearchItem[]>>(() => ({
  model: catalog.models.value,
  page: catalog.pages.value,
  action: catalog.actions.value,
}))
const itemsByKey = computed(() => {
  const map = new Map<string, SearchItem>()
  for (const item of [...catalog.models.value, ...catalog.pages.value, ...catalog.actions.value]) {
    if (!map.has(item.key)) map.set(item.key, item)
  }
  return map
})

// rankedByKind ranks every kind for the current query, or is null before typing.
const rankedByKind = computed<Record<SearchKind, RankedItem[]> | null>(() => {
  if (!trimmedQuery.value) return null
  const recent = recentKeys.value
  return {
    model: rankItems(trimmedQuery.value, itemsByKind.value.model, recent),
    page: rankItems(trimmedQuery.value, itemsByKind.value.page, recent),
    action: rankItems(trimmedQuery.value, itemsByKind.value.action, recent),
  }
})

// modelStatus reports a loading or failed catalog only while no models are published,
// so a background locale refresh keeps showing the current rows.
const modelStatus = computed<ResultSection['status']>(() => {
  if (modelIndex.entries.value.length > 0) return undefined
  const status = modelIndex.status.value
  if (status === 'idle' || status === 'loading') return 'loading'
  return status === 'error' ? 'error' : undefined
})

// unranked wraps items for listings that are not the result of a query.
function unranked(items: readonly SearchItem[]): RankedItem[] {
  return items.map((item) => ({ item, match: null }))
}

// sectionTitle returns the heading of a kind's section.
function sectionTitle(kind: SearchKind): string {
  return t(`globalSearch.sections.${kind}`)
}

// suggestionSections lists what the "All" scope shows before typing: recent visits,
// featured models, and common pages, each skipping items already listed above it.
function suggestionSections(): ResultSection[] {
  const listed = new Set<string>()
  const take = (items: Array<SearchItem | undefined>, limit: number) => {
    const picked: SearchItem[] = []
    for (const item of items) {
      if (!item || listed.has(item.key) || picked.length >= limit) continue
      listed.add(item.key)
      picked.push(item)
    }
    return picked
  }

  const sections: ResultSection[] = []
  const recent = take(
    recentKeys.value.map((key) => itemsByKey.value.get(key)),
    MAX_RECENT_SEARCHES,
  )
  if (recent.length > 0) {
    sections.push({
      key: 'recent',
      title: t('globalSearch.sections.recent'),
      rows: unranked(recent),
    })
  }

  const featuredIds = appStore.cachedPublicSettings?.home_featured_models ?? []
  if (featuredIds.length > 0) {
    const featured = take(
      featuredIds.map((id) => itemsByKey.value.get(`model:${id}`)),
      FEATURED_LIMIT,
    )
    // A failed catalog drops the featured section; the model scope offers the retry.
    const status = modelStatus.value === 'loading' ? 'loading' : undefined
    if (featured.length > 0 || status) {
      sections.push({
        key: 'featured',
        title: t('globalSearch.sections.featured'),
        rows: unranked(featured),
        status,
      })
    }
  }

  const common = take(
    COMMON_PAGE_KEYS.map((key) => itemsByKey.value.get(key)),
    COMMON_PAGE_LIMIT,
  )
  if (common.length > 0) {
    sections.push({
      key: 'common',
      title: t('globalSearch.sections.common'),
      rows: unranked(common),
    })
  }
  return sections
}

// sections builds the visible groups for the current query and scope. With a query, the
// "All" scope shows up to ALL_SCOPE_LIMIT rows per kind and a single scope up to
// SCOPE_LIMIT; without one, "All" shows suggestions and a single scope lists everything
// of that kind. The model section carries the catalog status while models are missing.
const sections = computed<ResultSection[]>(() => {
  const kinds: SearchKind[] = scope.value === 'all' ? ['model', 'page', 'action'] : [scope.value]
  const ranked = rankedByKind.value
  if (!ranked && scope.value === 'all') return suggestionSections()

  const limit = scope.value === 'all' ? ALL_SCOPE_LIMIT : SCOPE_LIMIT
  const result: ResultSection[] = []
  for (const kind of kinds) {
    const rows = ranked ? ranked[kind] : unranked(itemsByKind.value[kind])
    const status = kind === 'model' ? modelStatus.value : undefined
    if (rows.length === 0 && !status) continue
    result.push({ key: kind, title: sectionTitle(kind), rows: rows.slice(0, limit), status })
  }
  return result
})

// noResults reports a typed query with nothing to show, including no loading section.
const noResults = computed(() => Boolean(trimmedQuery.value) && sections.value.length === 0)

// marketplaceFallback offers the marketplace's own search when nothing matched here.
const marketplaceFallback = computed<ActionSearchItem>(() => ({
  kind: 'action',
  key: 'action:searchMarketplace',
  title: t('globalSearch.searchMarketplace', { query: trimmedQuery.value }),
  keywords: [],
  icon: 'modelMarketplace',
  run: () => router.push({ path: '/models', query: { q: trimmedQuery.value } }),
}))

// sectionLayout adds each section's flat offset, appending the marketplace fallback
// row when the query matched nothing.
const sectionLayout = computed<LaidOutSection[]>(() => {
  const visible: ResultSection[] = noResults.value
    ? [{ key: 'fallback', title: '', rows: unranked([marketplaceFallback.value]) }]
    : sections.value
  let offset = 0
  return visible.map((section) => {
    const laidOut = { ...section, offset }
    offset += section.rows.length
    return laidOut
  })
})
const flatRows = computed(() => sectionLayout.value.flatMap((section) => section.rows))

// truncated reports that a single-kind scope hid rows beyond SCOPE_LIMIT.
const truncated = computed(() => {
  if (scope.value === 'all') return false
  const total = rankedByKind.value
    ? rankedByKind.value[scope.value].length
    : itemsByKind.value[scope.value].length
  return total > SCOPE_LIMIT
})

// scopeOptions labels each scope; counts appear only for a typed query.
const scopeOptions = computed(() =>
  (['all', 'model', 'page', 'action'] as const).map((key) => ({
    key,
    label: t(`globalSearch.scopes.${key}`),
    count: rankedByKind.value && key !== 'all' ? rankedByKind.value[key].length : null,
  })),
)

// optionId returns the DOM id of the row at a flat index.
function optionId(index: number): string {
  return `${LISTBOX_ID}-option-${index}`
}
const activeOptionId = computed(() =>
  flatRows.value.length > 0 ? optionId(activeIndex.value) : undefined,
)

// pointerMoved marks the next current-row change as pointer driven; that row is already
// under the pointer, so the scroll watcher leaves the list where the user put it.
let pointerMoved = false

// pointAt makes the row under the pointer current without scrolling the list.
function pointAt(index: number) {
  if (activeIndex.value === index) return
  pointerMoved = true
  activeIndex.value = index
}

// moveActive moves the current row by delta and wraps around both ends of the list.
function moveActive(delta: number) {
  const count = flatRows.value.length
  if (count === 0) return
  activeIndex.value = (activeIndex.value + delta + count) % count
}

// selectScope switches the visible scope and returns the current row to the top.
function selectScope(next: SearchScope) {
  scope.value = next
}

// cycleScope moves to the next or previous scope, wrapping at either end.
function cycleScope(delta: number) {
  const keys = scopeOptions.value.map((option) => option.key)
  const index = keys.indexOf(scope.value)
  scope.value = keys[(index + delta + keys.length) % keys.length]
}

// clearQuery empties the input and keeps focus there for the next search.
function clearQuery() {
  query.value = ''
  inputRef.value?.focus()
}

// copyModelId copies a model row's ID and leaves the palette open for more lookups.
function copyModelId(item: SearchItem) {
  if (item.kind !== 'model') return
  void copyToClipboard(item.model.id)
}

// selectRow opens a result and closes the palette. Models and pages are recorded as
// recent visits before navigation; actions are not, because history lists places to
// return to. Navigation and action failures are logged because the palette has already
// closed and has nowhere to show them.
async function selectRow(row: RankedItem) {
  const item = row.item
  if (item.kind !== 'action') {
    recentKeys.value = recordRecentSearch(historyOwner.value, item.key)
  }
  closeGlobalSearch()
  try {
    if (item.kind === 'model') {
      await router.push({ path: '/models', query: { q: item.model.id } })
    } else if (item.kind === 'page' && item.external) {
      window.open(item.to, '_blank', 'noopener,noreferrer')
    } else if (item.kind === 'page') {
      await router.push(item.to)
    } else {
      await item.run()
    }
  } catch (error) {
    console.error('Global search selection failed:', error)
  }
}

// handlePanelKeydown implements list navigation while focus stays in the input. Keys
// pressed during input method composition belong to the composition, so they are left
// alone; Escape first clears a query and closes only an empty palette.
function handlePanelKeydown(event: KeyboardEvent) {
  // Some engines clear isComposing before dispatching the composition keydown and use
  // keyCode 229 as the remaining IME signal.
  if (event.isComposing || event.keyCode === 229) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    moveActive(event.key === 'ArrowDown' ? 1 : -1)
  } else if (event.key === 'Enter') {
    const row = flatRows.value[activeIndex.value]
    if (!row) return
    event.preventDefault()
    if (hasPrimaryModifier(event)) {
      copyModelId(row.item)
    } else {
      void selectRow(row)
    }
  } else if (event.key === 'Tab') {
    // Tab cycles scopes instead of moving focus, which also keeps focus in the palette.
    event.preventDefault()
    cycleScope(event.shiftKey ? -1 : 1)
  } else if (event.key === 'Escape') {
    // Stop the event here so document-level Escape handlers of other dialogs ignore it.
    event.preventDefault()
    event.stopPropagation()
    if (query.value) {
      query.value = ''
    } else {
      closeGlobalSearch()
    }
  }
}

// handleGlobalKeydown opens and closes the palette from the keyboard while a search
// trigger is mounted. Command+K or Control+K toggles the palette; "/" only opens it from
// outside text fields. Neither opens the palette over another dialog, which owns the
// keyboard while it is shown.
function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  if (!globalSearchAvailable.value && !globalSearchOpen.value) return
  const otherDialogOpen = !globalSearchOpen.value && document.body.classList.contains('modal-open')
  if (isToggleShortcut(event)) {
    if (otherDialogOpen) return
    event.preventDefault()
    if (globalSearchOpen.value) {
      closeGlobalSearch()
    } else {
      openGlobalSearch()
    }
  } else if (!globalSearchOpen.value && !otherDialogOpen && isSlashShortcut(event)) {
    event.preventDefault()
    openGlobalSearch()
  }
}
useEventListener(window, 'keydown', handleGlobalKeydown)

// Opening rereads history for the current account, starts the model catalog request,
// refreshes administrator switches for gated pages, and focuses the input with the
// previous query selected so typing replaces it.
watch(globalSearchOpen, async (open) => {
  if (!open) return
  scope.value = 'all'
  activeIndex.value = 0
  recentKeys.value = readRecentSearches(historyOwner.value)
  void modelIndex.ensureLoaded()
  if (authStore.isAdmin) {
    void adminSettingsStore.fetch()
  }
  await nextTick()
  inputRef.value?.focus()
  inputRef.value?.select()
})

// A route change, including browser back and forward, closes the palette so it never
// covers a page the user navigated to by other means.
watch(
  () => route.fullPath,
  () => closeGlobalSearch(),
)

// A new query or scope starts again at the first row.
watch([trimmedQuery, scope], () => {
  activeIndex.value = 0
})

// A shrinking list keeps the current row in range, for example when the catalog
// refreshes or a recent item disappears.
watch(
  () => flatRows.value.length,
  (count) => {
    if (activeIndex.value >= count) activeIndex.value = Math.max(0, count - 1)
  },
)

// Keyboard moves scroll the current row into view after the DOM reflects the change;
// pointer moves skip the scroll.
watch(activeIndex, async (index) => {
  if (pointerMoved) {
    pointerMoved = false
    return
  }
  await nextTick()
  document.getElementById(optionId(index))?.scrollIntoView({ block: 'nearest' })
})
</script>

<style scoped>
/*
 * Phones get a full-screen sheet without overlay padding; from sm upward the panel
 * hangs 12% below the top so the input stays put while the result list grows.
 */
.global-search-overlay {
  @apply items-stretch p-0 sm:items-start sm:p-4 sm:pt-[12vh];
}

/*
 * The panel overrides the shared modal-content sizing: full height and square corners
 * on phones, a content-hugging dialog with a viewport-relative cap from sm upward.
 * overflow-hidden clips the footer's fill to the rounded corners.
 */
.global-search-panel {
  @apply h-[100dvh] max-h-none overflow-hidden rounded-none border-0;
  @apply sm:h-auto sm:max-h-[min(80dvh,40rem)] sm:max-w-2xl sm:rounded-dialog sm:border;
}

.global-search-input-row {
  @apply flex h-14 shrink-0 items-center gap-3 border-b border-primary-900/10 px-4;
}

.dark .global-search-input-row {
  @apply border-dark-600;
}

/* The input is borderless inside its row; 16px text avoids zoom on mobile Safari. */
.global-search-input {
  @apply h-full min-w-0 flex-1 bg-transparent text-base text-primary-900 outline-none;
  @apply placeholder:text-primary-900/45;
}

.dark .global-search-input {
  @apply text-dark-50 placeholder:text-dark-400;
}

.global-search-results {
  @apply min-h-0 flex-1 overflow-y-auto overscroll-contain pt-1.5;
}

.global-search-footer {
  @apply hidden shrink-0 items-center gap-4 border-t border-primary-900/10 bg-gray-50/80 px-4 py-2 text-xs sm:flex;
  @apply text-primary-900/50;
}

.dark .global-search-footer {
  @apply border-dark-600 bg-dark-950 text-dark-400;
}

/* Muted text and icons inside the palette share one secondary color per theme. */
.global-search-muted {
  @apply text-primary-900/50;
}

.dark .global-search-muted {
  @apply text-dark-400;
}
</style>

<template>
  <div class="table-page-layout" :class="{ 'mobile-mode': isMobile }">
    <!-- Action controls stay above the table scroll area. -->
    <div v-if="$slots.actions" class="layout-section-fixed">
      <slot name="actions" />
    </div>

    <!-- Search and filters stay above the table scroll area. -->
    <div v-if="$slots.filters" class="layout-section-fixed">
      <slot name="filters" />
    </div>

    <!-- The table owns the scrollable content area. -->
    <div class="layout-section-scrollable">
      <div class="card table-scroll-container">
        <slot name="table" />
        <!--
          Desktop pagination stays inside the card frame while the table body scrolls
          independently.
        -->
        <div v-if="$slots.pagination" class="table-pagination-footer">
          <slot name="pagination" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { TABLE_DESKTOP_MIN_WIDTH } from '@/constants/layout'

// TablePageLayout.vue arranges action and filter slots above a table card with an
// optional pagination footer. Desktop layout receives remaining height through the
// AppLayout flex chain and scrolls table content, while mobile layout lets the page grow
// with its contents.

// isMobile records whether the browser width falls below the shared table breakpoint,
// selecting document scrolling or the desktop table card layout.
const isMobile = ref(false)

// checkMobile updates mobile layout from the current browser width.
//
// It requires window and runs on mount and resize; the matching listener is removed when
// this table page layout unmounts.
const checkMobile = () => {
  isMobile.value = window.innerWidth < TABLE_DESKTOP_MIN_WIDTH
}

// Read the current width before subscribing to resize so the mounted table receives the
// appropriate scrolling layout immediately and on later viewport changes.
onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
})

// Remove the resize callback when the layout unmounts so later viewport changes cannot
// update a detached table layout instance.
onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})
</script>

<style scoped>
/*
 * Desktop height comes from the AppLayout flex chain, allowing the table to fill
 * remaining space; mobile rules restore natural content height.
 */
.table-page-layout {
  @apply flex flex-1 flex-col min-h-0;
}

.layout-section-fixed {
  @apply flex-shrink-0;
}

/*
 * Adjacent tool groups use eight pixels of spacing; the final tool group leaves
 * sixteen pixels before the table card.
 */
.layout-section-fixed + .layout-section-fixed {
  @apply mt-2;
}

.layout-section-fixed + .layout-section-scrollable {
  @apply mt-4;
}

.layout-section-scrollable {
  @apply flex-1 min-h-0 flex flex-col;
}

/*
 * The card holds a flexible scroll area and a fixed pagination footer within one
 * clipped frame on desktop.
 */
.table-scroll-container {
  @apply flex flex-col overflow-hidden h-full bg-white dark:bg-dark-900 rounded-surface border border-gray-200 dark:border-dark-600 shadow-none;
}

.table-scroll-container :deep(.table-wrapper) {
  @apply flex-1 overflow-x-auto overflow-y-auto;
  /*
   * Reserve the vertical scrollbar gutter so column positions remain stable as rows
   * begin or stop requiring vertical scrolling.
   */
  scrollbar-gutter: stable;
}

.table-scroll-container :deep(table) {
  @apply w-full;
  min-width: max-content; /* Content width can exceed the card and scroll horizontally. */
  display: table; /* Table display keeps header and body columns in the same layout. */
}

.table-scroll-container :deep(thead) {
  /*
   * An opaque header background covers scrolled rows and keeps sticky header text
   * readable against the moving table body.
   */
  @apply bg-gray-50 dark:bg-dark-900;
}

.table-scroll-container :deep(tbody) {
  /* The body retains its browser table-row-group display. */
}

.table-scroll-container :deep(th) {
  /*
   * Header density matches DataTable and the shared table class, leaving more
   * vertical room for data rows in the card.
   */
  @apply px-4 py-2 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-dark-300 border-b border-gray-200 dark:border-dark-700;
}

.table-scroll-container :deep(td) {
  @apply px-4 py-3 text-sm text-gray-700 dark:text-dark-100 border-b border-gray-100 dark:border-dark-700;
}

/*
 * The desktop footer stays in the table frame while its sibling body region consumes
 * the available scrolling height.
 */
.table-pagination-footer {
  @apply flex-shrink-0;
}

.table-pagination-footer:empty {
  display: none;
}

.table-page-layout:not(.mobile-mode) .table-pagination-footer {
  @apply border-t border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-900;
}

/*
 * Pagination inherits the card frame and footer background; remove its own outer
 * border and background while keeping its padding and control sizing, which match
 * the table header height.
 */
.table-page-layout:not(.mobile-mode) .table-pagination-footer :deep(.pagination-root) {
  border-top: 0;
  background: transparent;
}

/* Mobile pages grow with table content. */
.table-page-layout.mobile-mode {
  /*
   * Natural card height lets subsequent mobile sections follow the table, with
   * document scrolling handling the accumulated page height.
   */
  @apply h-auto flex-none;
}

.table-page-layout.mobile-mode .table-scroll-container {
  @apply h-auto overflow-visible border-none shadow-none bg-transparent;
}

.table-page-layout.mobile-mode .layout-section-scrollable {
  @apply flex-none min-h-fit;
}

.table-page-layout.mobile-mode .table-pagination-footer {
  @apply mt-4;
}

.table-page-layout.mobile-mode .table-scroll-container :deep(table) {
  @apply flex-none;
  display: table;
  min-width: 100%;
}
</style>

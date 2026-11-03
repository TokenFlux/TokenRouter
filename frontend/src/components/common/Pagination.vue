<template>
  <div
    class="pagination-root flex shrink-0 items-center justify-between border-t border-gray-200 bg-white px-4 py-1 dark:border-dark-700 dark:bg-dark-900 lg:bg-gray-50/80 lg:px-6"
  >
    <div class="pagination-mobile flex flex-1 items-center justify-between lg:hidden">
      <!-- Mobile pagination -->
      <button
        @click="goToPage(page - 1)"
        :disabled="page <= 1"
        class="pagination-control relative inline-flex h-7 items-center rounded-control border border-gray-300 bg-white px-4 py-0 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-950 dark:text-gray-200 dark:hover:bg-dark-800"
      >
        {{ t('pagination.previous') }}
      </button>
      <span class="text-sm text-gray-700 dark:text-gray-300">
        {{ t('pagination.pageOf', { page, total: totalPages }) }}
      </span>
      <button
        @click="goToPage(page + 1)"
        :disabled="page >= totalPages"
        class="pagination-control relative ml-3 inline-flex h-7 items-center rounded-control border border-gray-300 bg-white px-4 py-0 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-950 dark:text-gray-200 dark:hover:bg-dark-800"
      >
        {{ t('pagination.next') }}
      </button>
    </div>

    <div
      class="pagination-desktop hidden lg:flex lg:flex-1 lg:flex-wrap lg:items-center lg:justify-between lg:gap-x-4 lg:gap-y-2"
    >
      <!-- Desktop pagination info -->
      <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
        <p class="pagination-summary text-sm text-gray-700 dark:text-gray-300">
          {{ t('pagination.showing') }}
          <span class="font-medium">{{ fromItem }}</span>
          {{ t('pagination.to') }}
          <span class="font-medium">{{ toItem }}</span>
          {{ t('pagination.of') }}
          <span class="font-medium">{{ total }}</span>
          {{ t('pagination.results') }}
        </p>

        <!-- Page size selector -->
        <div v-if="showPageSizeSelector" class="flex items-center space-x-2">
          <span class="text-sm text-gray-700 dark:text-gray-300"
            >{{ t('pagination.perPage') }}:</span
          >
          <!--
            The 96px wrapper leaves room for three-digit page sizes after trigger
            padding and the dropdown icon consume 60px.
          -->
          <div class="page-size-select w-24">
            <Select
              :model-value="pageSize"
              :options="pageSizeSelectOptions"
              @update:model-value="handlePageSizeChange"
            />
          </div>
        </div>

        <div v-if="showJump" class="flex items-center space-x-2">
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('pagination.jumpTo') }}</span>
          <input
            v-model="jumpPage"
            type="number"
            min="1"
            :max="totalPages"
            class="pagination-jump-input input w-20 text-sm"
            :placeholder="t('pagination.jumpPlaceholder')"
            @keyup.enter="submitJump"
          />
          <button
            type="button"
            class="pagination-jump-button btn btn-ghost btn-sm h-7"
            @click="submitJump"
          >
            {{ t('pagination.jumpAction') }}
          </button>
        </div>
      </div>

      <!-- Desktop pagination buttons -->
      <nav
        class="pagination-nav relative z-0 inline-flex -space-x-px rounded-control shadow-sm"
        aria-label="Pagination"
      >
        <!-- Previous button -->
        <button
          @click="goToPage(page - 1)"
          :disabled="page <= 1"
          class="pagination-control relative inline-flex h-7 items-center rounded-l-control border border-gray-300 bg-white px-2 py-0 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-950 dark:text-gray-400 dark:hover:bg-dark-800"
          :aria-label="t('pagination.previous')"
        >
          <Icon name="chevronLeft" size="sm" :animate-on-hover="false" />
        </button>

        <!-- Page numbers -->
        <button
          v-for="(pageNum, index) in visiblePages"
          :key="`${pageNum}-${index}`"
          @click="typeof pageNum === 'number' && goToPage(pageNum)"
          :disabled="typeof pageNum !== 'number'"
          :class="[
            'pagination-control pagination-page-button relative inline-flex h-7 min-w-7 items-center justify-center border px-2 py-0 text-sm font-medium',
            pageNum === page
              ? 'z-10 border-primary-500 bg-primary-50 text-primary-600 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
              : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-950 dark:text-gray-300 dark:hover:bg-dark-800',
            typeof pageNum !== 'number' && 'cursor-default',
          ]"
          :aria-label="
            typeof pageNum === 'number' ? t('pagination.goToPage', { page: pageNum }) : undefined
          "
          :aria-current="pageNum === page ? 'page' : undefined"
        >
          {{ pageNum }}
        </button>

        <!-- Next button -->
        <button
          @click="goToPage(page + 1)"
          :disabled="page >= totalPages"
          class="pagination-control relative inline-flex h-7 items-center rounded-r-control border border-gray-300 bg-white px-2 py-0 text-sm font-medium text-gray-500 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:bg-dark-950 dark:text-gray-400 dark:hover:bg-dark-800"
          :aria-label="t('pagination.next')"
        >
          <Icon name="chevronRight" size="sm" :animate-on-hover="false" />
        </button>
      </nav>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from './Select.vue'
import { getConfiguredTablePageSizeOptions, normalizeTablePageSize } from '@/utils/tablePreferences'
import { setPersistedPageSize } from '@/composables/usePersistedPageSize'

// Pagination.vue renders page navigation, record counts, page-size selection, and
// optional numeric jumping. The parent owns the current page and record total, while
// valid size selections update the shared persisted preference before publishing the
// new page size.
//
// Every control in the bar is 28px tall with 4px of vertical padding, so the bar
// matches the 36px table header row that sits above it in the shared table frame.

// t resolves the component action and status labels using the active application
// locale.
const { t } = useI18n()

// Props describes the values and options supplied by the parent component.
interface Props {
  total: number // Total matching record count.
  page: number // Current one-based page number.
  pageSize: number // Current records per page.
  // Declared caller page-size options; the current selector derives its choices from
  // shared configuration plus the active size and does not read this prop.
  pageSizeOptions?: number[]
  showPageSizeSelector?: boolean // Page-size selector visibility flag.
  showJump?: boolean // Numeric page-jump visibility flag.
}

// Emits defines the component notifications delivered to its parent listeners.
interface Emits {
  // Requests selected page number.
  (e: 'update:page', page: number): void
  // Publishes selected page size.
  (e: 'update:pageSize', pageSize: number): void
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  pageSizeOptions: () => getConfiguredTablePageSizeOptions(),
  showPageSizeSelector: true,
  showJump: false,
})

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<Emits>()

// totalPages, fromItem, and toItem derive one-based paging feedback from parent
// counts. pageSizeSelectOptions merges configured sizes with the active normalized
// size; jumpPage holds typed navigation, and visiblePages bounds displayed page links.
// Keep one empty page so filtered zero-result tables retain a valid one-based range
// for both navigation controls and the mobile page-count label.
const totalPages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))

const fromItem = computed(() => {
  if (props.total === 0) return 0
  return (props.page - 1) * props.pageSize + 1
})

const toItem = computed(() => {
  const to = props.page * props.pageSize
  return to > props.total ? props.total : to
})

const pageSizeSelectOptions = computed(() => {
  const options = Array.from(
    new Set([...getConfiguredTablePageSizeOptions(), normalizeTablePageSize(props.pageSize)]),
  ).sort((a, b) => a - b)

  return options.map((size) => ({
    value: size,
    label: String(size),
  }))
})

const jumpPage = ref('')

const visiblePages = computed(() => {
  const pages: (number | string)[] = []
  // Small page sets show every page before ellipses become useful.
  const maxVisible = 7
  const total = totalPages.value

  if (total <= maxVisible) {
    for (let i = 1; i <= total; i++) {
      pages.push(i)
    }
  } else {
    pages.push(1)

    // Bound the middle window to two neighbors around the active page.
    const start = Math.max(2, props.page - 2)
    const end = Math.min(total - 1, props.page + 2)

    if (start > 2) {
      pages.push('...')
    }

    for (let i = start; i <= end; i++) {
      pages.push(i)
    }

    if (end < total - 1) {
      pages.push('...')
    }

    pages.push(total)
  }

  return pages
})

// goToPage publishes a page request within the current one-based range. Requests equal
// to the current page or outside totalPages are ignored, leaving the parent-owned
// selection unchanged.
const goToPage = (newPage: number) => {
  if (newPage >= 1 && newPage <= totalPages.value && newPage !== props.page) {
    emit('update:page', newPage)
  }
}

// handlePageSizeChange normalizes a string or numeric selection into a supported page
// size. Null and boolean inputs are ignored; the shared preference is updated before
// the model event reaches the parent.
const handlePageSizeChange = (value: string | number | boolean | null) => {
  if (value === null || typeof value === 'boolean') return
  const newPageSize = normalizeTablePageSize(
    typeof value === 'string' ? parseInt(value, 10) : value,
  )
  // Persist the normalized preference before publishing the model change so subsequent
  // consumers reading the shared page size observe the selected value.
  setPersistedPageSize(newPageSize)
  emit('update:pageSize', newPageSize)
}

// submitJump parses the entered page and clamps it to the current range. Empty or
// unparsable input is retained, while a parsed jump clears the input before delegating
// navigation.
const submitJump = () => {
  const value = jumpPage.value.trim()
  if (!value) return
  const pageNum = Number.parseInt(value, 10)
  if (Number.isNaN(pageNum)) return
  // Bound the jump to page limits before goToPage rejects unavailable pages.
  const nextPage = Math.min(Math.max(pageNum, 1), totalPages.value)
  jumpPage.value = ''
  goToPage(nextPage)
}
</script>

<style scoped>
/*
 * The page-size trigger and jump input inherit the 36px .input baseline. Pagination
 * shrinks both to the 28px height of its buttons so the bar stays at header height.
 */
.page-size-select :deep(.input),
.pagination-jump-input {
  @apply h-7 min-h-7 py-0;
}
</style>

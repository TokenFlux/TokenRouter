<template>
  <div v-if="!isDesktopViewport" class="space-y-4" :aria-busy="loading">
    <template v-if="loading">
      <div
        v-for="i in 5"
        :key="i"
        class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="space-y-3">
          <div v-for="column in dataColumns" :key="column.key" class="flex justify-between">
            <Skeleton :width="80" :height="16" />
            <Skeleton :width="128" :height="16" />
          </div>
          <div v-if="hasActionsColumn" class="border-t border-gray-200 pt-3 dark:border-dark-700">
            <Skeleton :height="32" />
          </div>
        </div>
      </div>
    </template>

    <template v-else-if="!data || data.length === 0">
      <div
        class="rounded-surface border border-gray-200 bg-white p-12 text-center dark:border-dark-700 dark:bg-dark-900"
      >
        <slot name="empty">
          <div class="flex flex-col items-center">
            <Icon name="inbox" size="xl" class="mb-4 h-12 w-12 text-gray-400 dark:text-dark-500" />
            <p class="text-lg font-medium text-gray-900 dark:text-gray-100">
              {{ t('empty.noData') }}
            </p>
          </div>
        </slot>
      </div>
    </template>

    <template v-else>
      <div v-if="selectable" class="flex items-center justify-end gap-2 px-1">
        <label class="flex items-center gap-2 text-sm font-medium text-gray-600 dark:text-gray-300">
          <input
            type="checkbox"
            class="h-4 w-4"
            :checked="allVisibleSelected"
            :indeterminate="someVisibleSelected"
            data-test="select-all-mobile"
            @change="toggleAllVisible(($event.target as HTMLInputElement).checked)"
          />
          <span>{{ t('common.selectAll') }}</span>
        </label>
      </div>
      <div
        v-for="(row, index) in sortedData"
        :key="resolveRowKey(row, index)"
        class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
        :class="{
          'cursor-pointer': clickableRows,
          'border-primary-300 bg-primary-50/40 dark:border-primary-700 dark:bg-primary-900/10':
            selectable && isRowSelected(row, index),
        }"
        @click="clickableRows && emit('rowClick', row)"
      >
        <div class="space-y-3">
          <div v-if="selectable" class="flex justify-end">
            <input
              type="checkbox"
              class="h-4 w-4"
              :checked="isRowSelected(row, index)"
              :aria-label="getRowSelectionLabel(row, index)"
              data-test="select-row"
              @click.stop
              @change="toggleRowSelection(row, index, ($event.target as HTMLInputElement).checked)"
            />
          </div>
          <!-- Labels and values align on their first text baseline. Slots beginning with icons or color bars place self-baseline on their text. -->
          <div
            v-for="column in dataColumns"
            :key="column.key"
            :data-field="column.key"
            class="flex min-w-0 items-baseline justify-between gap-4"
          >
            <span class="text-xs font-medium tracking-wider text-gray-500 dark:text-dark-300">
              {{ column.label }}
            </span>
            <div class="min-w-0 max-w-full text-right text-sm text-gray-900 dark:text-gray-100">
              <slot
                :name="`cell-${column.key}`"
                :row="row"
                :value="row[column.key]"
                :expanded="actionsExpanded"
              >
                {{ column.formatter ? column.formatter(row[column.key], row) : row[column.key] }}
              </slot>
            </div>
          </div>
          <div v-if="hasActionsColumn" class="border-t border-gray-200 pt-3 dark:border-dark-700">
            <slot
              name="cell-actions"
              :row="row"
              :value="row['actions']"
              :expanded="actionsExpanded"
            ></slot>
          </div>
        </div>
      </div>
    </template>
  </div>

  <div
    v-else
    ref="tableWrapperRef"
    class="table-wrapper sticky-boundary-line"
    :aria-busy="loading"
    :class="{
      'actions-expanded': actionsExpanded,
      'is-scrollable': isScrollable,
    }"
    @dragover="handleTableDragOver"
    @dragenter="handleTableDragOver"
    @dragleave="handleTableDragLeave"
  >
    <table class="w-full min-w-max divide-y divide-gray-200 dark:divide-dark-700">
      <thead class="table-header bg-gray-50 dark:bg-dark-900">
        <tr>
          <th v-if="selectable" scope="col" class="sticky-header-cell table-selection-cell py-2">
            <input
              type="checkbox"
              class="h-4 w-4"
              :checked="allVisibleSelected"
              :indeterminate="someVisibleSelected"
              :aria-label="t('common.selectAll')"
              data-test="select-all"
              @change="toggleAllVisible(($event.target as HTMLInputElement).checked)"
            />
          </th>
          <th
            data-icon-trigger
            v-for="(column, index) in orderedColumns"
            :key="column.key"
            :data-column-key="column.key"
            scope="col"
            :style="{ width: column.width }"
            :aria-sort="column.sortable ? getColumnAriaSort(column.key) : undefined"
            :class="[
              'sticky-header-cell py-2 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-dark-300',
              getColumnLayoutClass(column),
              { 'cursor-pointer hover:bg-gray-100 dark:hover:bg-dark-700': column.sortable },
              {
                'opacity-50': draggingColumn === column.key,
                'column-drop-before':
                  dropTarget?.key === column.key && dropTarget.side === 'before',
                'column-drop-after': dropTarget?.key === column.key && dropTarget.side === 'after',
              },
              getStickyColumnClass(column, index),
              column.class,
            ]"
            @click="column.sortable && handleSort(column.key)"
            @dragover="handleColumnDragOver($event, column.key)"
            @dragenter="handleColumnDragOver($event, column.key)"
            @drop="handleColumnDrop($event, column.key)"
          >
            <div :class="['flex items-center space-x-1', getHeaderContentAlignmentClass(column)]">
              <button
                v-if="canReorder(column.key)"
                type="button"
                draggable="true"
                class="column-drag-handle"
                :data-column-key="column.key"
                :title="t('common.reorderColumn', { column: column.label })"
                :aria-label="t('common.reorderColumn', { column: column.label })"
                @click.stop
                @dragstart.stop="startColumnDrag($event, column.key)"
                @dragend="endColumnDrag"
                @keydown.left.prevent.stop="moveColumnByKeyboard(column.key, -1)"
                @keydown.right.prevent.stop="moveColumnByKeyboard(column.key, 1)"
              >
                <Icon name="grip" size="sm" class="pointer-events-none" :animate-on-hover="false" />
              </button>
              <slot
                :name="`header-${column.key}`"
                :column="column"
                :sort-key="sortKey"
                :sort-order="sortOrder"
              >
                <span>{{ column.label }}</span>
              </slot>
              <span
                v-if="column.sortable"
                class="inline-flex h-5 w-4 flex-col items-center justify-center"
                aria-hidden="true"
              >
                <Icon
                  name="chevronUp"
                  size="md"
                  :animate-on-hover="false"
                  class="h-2.5 w-2.5"
                  :class="getSortIndicatorClass(column.key, 'asc')"
                />
                <Icon
                  name="chevronDown"
                  size="md"
                  :animate-on-hover="false"
                  class="-mt-0.5 h-2.5 w-2.5"
                  :class="getSortIndicatorClass(column.key, 'desc')"
                />
              </span>
            </div>
          </th>
        </tr>
      </thead>
      <tbody
        class="table-body divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"
      >
        <!-- Desktop columns and mobile cards share the loading skeleton recipe. -->
        <tr v-if="loading" v-for="i in 5" :key="i">
          <td v-if="selectable" class="table-selection-cell py-3">
            <Skeleton :width="16" :height="16" class="mx-auto" />
          </td>
          <td
            v-for="column in orderedColumns"
            :key="column.key"
            :class="['whitespace-nowrap py-3', getColumnLayoutClass(column)]"
          >
            <Skeleton
              :width="column.key === 'select' ? 16 : '75%'"
              :height="16"
              :class="column.key === 'select' ? 'mx-auto' : ''"
            />
          </td>
        </tr>

        <!-- Empty state -->
        <tr v-else-if="!data || data.length === 0">
          <td
            :colspan="tableColumnCount"
            :class="['py-12 text-center text-gray-500 dark:text-dark-400', getColumnLayoutClass()]"
          >
            <slot name="empty">
              <div class="flex flex-col items-center">
                <Icon
                  name="inbox"
                  size="xl"
                  class="mb-4 h-12 w-12 text-gray-400 dark:text-dark-500"
                />
                <p class="text-lg font-medium text-gray-900 dark:text-gray-100">
                  {{ t('empty.noData') }}
                </p>
              </div>
            </slot>
          </td>
        </tr>

        <!-- Large desktop lists render the virtual window; other lists render every row using the same cell template. -->
        <template v-else>
          <tr v-if="virtualPaddingTop > 0" aria-hidden="true">
            <td
              :colspan="tableColumnCount"
              :style="{ height: virtualPaddingTop + 'px', padding: 0, border: 'none' }"
            ></td>
          </tr>
          <tr
            v-for="item in renderRows"
            :key="resolveRowKey(item.row, item.index)"
            :data-row-id="resolveRowKey(item.row, item.index)"
            :data-index="item.index"
            :ref="item.measure ? measureElement : undefined"
            class="hover:bg-gray-50 dark:hover:bg-dark-800"
            :class="{
              'cursor-pointer': clickableRows,
              'bg-primary-50/40 dark:bg-primary-900/10':
                selectable && isRowSelected(item.row, item.index),
            }"
            @click="clickableRows && emit('rowClick', item.row)"
          >
            <td v-if="selectable" class="table-selection-cell py-3">
              <input
                type="checkbox"
                class="h-4 w-4"
                :checked="isRowSelected(item.row, item.index)"
                :aria-label="getRowSelectionLabel(item.row, item.index)"
                data-test="select-row"
                @click.stop
                @change="
                  toggleRowSelection(
                    item.row,
                    item.index,
                    ($event.target as HTMLInputElement).checked,
                  )
                "
              />
            </td>
            <td
              v-for="(column, colIndex) in orderedColumns"
              :key="column.key"
              :data-column-key="column.key"
              :class="[
                'whitespace-nowrap py-3 text-sm text-gray-900 dark:text-dark-100',
                getColumnLayoutClass(column),
                getStickyColumnClass(column, colIndex),
                column.class,
              ]"
            >
              <slot
                :name="`cell-${column.key}`"
                :row="item.row"
                :value="item.row[column.key]"
                :expanded="actionsExpanded"
              >
                {{
                  column.formatter
                    ? column.formatter(item.row[column.key], item.row)
                    : item.row[column.key]
                }}
              </slot>
            </td>
          </tr>
          <tr v-if="virtualPaddingBottom > 0" aria-hidden="true">
            <td
              :colspan="tableColumnCount"
              :style="{ height: virtualPaddingBottom + 'px', padding: 0, border: 'none' }"
            ></td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import {
  useVirtualizer,
  observeElementRect as observeElementRectDefault,
} from '@tanstack/vue-virtual'
import { useI18n } from 'vue-i18n'
import type { Column } from './types'
import Icon from '@/components/icons/Icon.vue'
import Skeleton from '../Skeleton.vue'
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'
import { useTableColumnOrder } from '@/components/common/table/useTableColumnOrder'

// DataTable.vue renders responsive data rows with sorting, controlled selection,
// movable columns, and optional desktop virtualization. Desktop and mobile
// presentations share row ordering and column definitions. Selection retains
// keys outside the visible page, and structural selection and action columns
// keep their positions when data columns move. Browser preferences provide
// independent sort and column order state for each caller-supplied storage key.
//
// Virtual rows use resolved row keys for measurement lookup, including array
// indexes when explicit keys are absent. A separate identity comparison retains
// cached measurements when the same distinct identities reorder and resets them
// when identities change or repeat. The virtualizer starts with an estimated
// viewport and ignores zero-height observations during layout, keeping a usable
// range until the wrapper has a positive measured height. Desktop listeners and
// drag animation frames are released as their owning view or component ends.

// Translation and collation use the active application locale for selection
// labels and human-readable text ordering.
const { t, locale } = useI18n()

// isDesktopViewport chooses table or card rendering and controls whether
// viewport-dependent tracking and virtualization are active.
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(TABLE_DESKTOP_MEDIA_QUERY).matches,
)

// emit publishes sort choices, row activation, and controlled selection changes
// for the parent to apply to its data source.
const emit = defineEmits<{
  sort: [key: string, order: 'asc' | 'desc'] // Requested server sort choice.
  rowClick: [row: any] // Activated caller row record.
  'update:selectedKeys': [keys: Array<string | number>] // Controlled selected row identities.
  selectionChange: [keys: Array<string | number>] // Selected row change notification.
}>()

// Wrapper state tracks horizontal overflow and whether action contents require
// their expanded presentation.
const tableWrapperRef = ref<HTMLElement | null>(null)
const isScrollable = ref(false)
const actionsColumnNeedsExpanding = ref(false)

// estimatedViewportHeight estimates usable table height before the first
// measured viewport. Server rendering uses six hundred pixels; browsers reserve
// surrounding page space and keep a minimum four-hundred-pixel estimate.
const estimatedViewportHeight = () => {
  if (typeof window === 'undefined') return 600
  // Reserve space for header, filters, and pagination before the wrapper
  // receives its first usable viewport measurement.
  return Math.max(window.innerHeight - 320, 400)
}

// observeElementRectNonZero observes wrapper geometry while retaining the last
// positive viewport height.
// Zero-height layout observations are discarded because they would remove the
// virtualizer range before the flex layout settles and make all rows disappear.
const observeElementRectNonZero = (
  instance: any,
  cb: (rect: {
    width: number // Measured width in pixels.
    height: number // Measured height in pixels.
  }) => void,
) =>
  observeElementRectDefault(instance, (rect) => {
    if (rect.height > 0) cb(rect)
  })

// checkScrollable updates horizontal overflow state from the mounted table wrapper.
const checkScrollable = () => {
  if (tableWrapperRef.value) {
    isScrollable.value = tableWrapperRef.value.scrollWidth > tableWrapperRef.value.clientWidth
  }
}

// checkActionsColumnWidth measures the first row action controls to decide
// whether expansion is needed.
// It temporarily expands the contents and waits for Vue rendering before
// measuring, then restores the prior expansion state. Two or fewer actions
// remain uncollapsed. Callers watch row and column changes rather than the
// temporary expansion itself, preventing this measurement cycle from
// recursively scheduling its own expansion measurement.
const checkActionsColumnWidth = () => {
  if (!props.expandableActions) {
    actionsColumnNeedsExpanding.value = false
    actionsExpanded.value = false
    return
  }
  if (!tableWrapperRef.value) return

  // Use the first rendered row as the action-width sample.
  const firstActionCell = tableWrapperRef.value.querySelector('tbody tr:first-child td:last-child')
  if (!firstActionCell) return

  // The action slot wraps its controls in a container.
  const actionsContainer = firstActionCell.querySelector('div')
  if (!actionsContainer) return

  // Temporarily reveal all action controls before measuring their widths,
  // retaining the prior expansion state for restoration after the DOM update.
  const wasExpanded = actionsExpanded.value
  actionsExpanded.value = true

  // Wait for the temporary expansion to render before measuring controls,
  // otherwise collapsed controls would understate the required width.
  nextTick(() => {
    // Measure button and link controls in the expanded action slot.
    const actionItems = actionsContainer.querySelectorAll('button, a, [role="button"]')
    if (actionItems.length <= 2) {
      actionsColumnNeedsExpanding.value = false
      actionsExpanded.value = wasExpanded
      return
    }

    // Include the slot gap between neighboring action controls.
    let totalWidth = 0
    actionItems.forEach((item, index) => {
      totalWidth += (item as HTMLElement).offsetWidth
      if (index < actionItems.length - 1) {
        totalWidth += 4 // gap-1 = 4px
      }
    })

    // Exclude both horizontal padding edges from the available cell width.
    const cellWidth = (firstActionCell as HTMLElement).clientWidth - 32 // Remove both sixteen-pixel cell padding edges.

    // Collapsed presentation is needed when actions exceed the cell.
    actionsColumnNeedsExpanding.value = totalWidth > cellWidth

    // Restore the prior presentation after the temporary measurement finishes.
    actionsExpanded.value = wasExpanded
  })
}

// Desktop measurement and media listeners are retained here so viewport changes
// and unmount can release the active subscriptions.
let resizeObserver: ResizeObserver | null = null
let resizeHandler: (() => void) | null = null
let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null

// detachDesktopTableTracking disconnects element observation and the optional
// window resize listener. Viewport switches and component unmount call it before
// discarding the desktop table whose dimensions were being observed.
const detachDesktopTableTracking = () => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (resizeHandler) {
    window.removeEventListener('resize', resizeHandler)
    resizeHandler = null
  }
}

// attachDesktopTableTracking measures the current desktop table and subscribes
// to later size changes. Call it after rendering the desktop wrapper; element
// observation covers wrapper and contents, with window resize as the
// compatibility path.
const attachDesktopTableTracking = () => {
  checkScrollable()
  checkActionsColumnWidth()
  if (tableWrapperRef.value && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => {
      checkScrollable()
      checkActionsColumnWidth()
    })
    resizeObserver.observe(tableWrapperRef.value)
    // Observe table content as well as the wrapper because asynchronously loaded
    // cells can change overflow without resizing the outer layout.
    const table = tableWrapperRef.value.querySelector('table')
    if (table) resizeObserver.observe(table)
  } else {
    // Use window resize events when element observation is unavailable so
    // desktop overflow and action width still refresh on viewport changes.
    resizeHandler = () => {
      checkScrollable()
      checkActionsColumnWidth()
    }
    window.addEventListener('resize', resizeHandler)
  }
}

onMounted(() => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(TABLE_DESKTOP_MEDIA_QUERY)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }
})

onUnmounted(() => {
  detachDesktopTableTracking()
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
    desktopViewportListener = null
  }
  desktopViewportMediaQuery = null
})

// Props configures table data, interaction, persistence, and responsive row
// presentation for its parent.
interface Props {
  columns: Column[] // Ordered caller column definitions.
  data: any[] // Current page row records.
  loading?: boolean // Show row loading placeholders.
  stickyFirstColumn?: boolean // Pin first data column.
  stickyActionsColumn?: boolean // Pin right action column.
  expandableActions?: boolean // Enable action-width expansion checks.
  actionsCount?: number // Declared action count input.
  rowKey?: string | ((row: any) => string | number) // Stable row identity source.
  /**
   * defaultSortKey and defaultSortOrder configure the initial choice when browser storage has no valid preference for a currently sortable column. Both values pass through the same normalization as restored preferences.
   */
  defaultSortKey?: string // Initial sortable column key.
  defaultSortOrder?: 'asc' | 'desc' // Initial sort direction value.
  /**
   * sortStorageKey identifies this table preference in localStorage; a valid stored key and direction take precedence over the configured initial sort choice.
   */
  sortStorageKey?: string
  /** A stable caller-specific key enables column movement and stores column order for this table in the current browser. */
  columnOrderStorageKey?: string
  /**
   * serverSideSort publishes header choices to the parent and renders supplied row order directly; the parent applies sorting when requesting its data.
   */
  serverSideSort?: boolean
  /** Rows emit rowClick when enabled; interactive slot contents stop propagation when their action should not activate the containing row. */
  clickableRows?: boolean
  /** Estimated row height in px for the virtualizer (default 56) */
  estimateRowHeight?: number // Estimated row height pixels.
  /** Number of rows to render beyond the visible area (default 5) */
  overscan?: number // Extra virtual rows per edge.
  /**
   * Desktop lists exceeding this row count use virtualization, defaulting to one hundred rows. Smaller lists render every row at its actual content height.
   */
  virtualizeThreshold?: number
  /** Enable controlled row selection; callers provide stable row keys so selection continues to identify the intended records after sorting. */
  selectable?: boolean
  /** Selected row keys supplied by the caller include other pages; visible-row toggles preserve keys outside the current data set. */
  selectedKeys?: Array<string | number>
  /** Accessible label for row selection checkboxes. */
  selectionLabel?: string | ((row: any) => string)
}

// props applies shared table interaction defaults while callers supply columns,
// data, and optional stable persistence keys.
const props = withDefaults(defineProps<Props>(), {
  loading: false,
  stickyFirstColumn: true,
  stickyActionsColumn: true,
  expandableActions: true,
  defaultSortOrder: 'asc',
  serverSideSort: false,
  selectable: false,
  selectedKeys: () => [],
})

// Sort and action-expansion state drive header feedback, rendered row ordering,
// and the visibility of overflowing action controls.
const sortKey = ref<string>('')
const sortOrder = ref<'asc' | 'desc'>('asc')
const actionsExpanded = ref(false)

// Column-order state separates persisted data-column ordering from transient
// drag markers and animation-frame ownership for the active pointer operation.
const { orderedColumns, movableColumns, canReorder, moveColumn } = useTableColumnOrder(
  () => props.columns,
  computed(() => props.columnOrderStorageKey),
)
const draggingColumn = ref<string | null>(null)
const dropTarget = ref<{
  key: string // Target movable column identifier.
  side: 'before' | 'after' // Insertion side around target.
} | null>(null)
let dragScrollFrame: number | null = null
let dragScrollSpeed = 0

// endColumnDrag clears column drag state and cancels its scheduled scrolling
// frame. Storage-key changes, viewport switches, drop completion, and component
// unmount call it to release the current drag operation.
const endColumnDrag = () => {
  draggingColumn.value = null
  dropTarget.value = null
  dragScrollSpeed = 0
  if (dragScrollFrame !== null) cancelAnimationFrame(dragScrollFrame)
  dragScrollFrame = null
}

// startColumnDrag records a movable column and publishes its drag payload.
// Structural or missing columns and events without data transfer are rejected
// before any drag state is recorded.
const startColumnDrag = (event: DragEvent, key: string) => {
  if (!canReorder(key) || !event.dataTransfer) {
    event.preventDefault()
    return
  }
  draggingColumn.value = key
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', key)
}

// scrollDuringColumnDrag scrolls a wide table while the drag pointer stays near its edge.
// A frame schedules another frame only while a column, wrapper, and nonzero
// speed remain active; drag completion cancels the outstanding frame.
const scrollDuringColumnDrag = () => {
  if (!draggingColumn.value || !tableWrapperRef.value || dragScrollSpeed === 0) {
    dragScrollFrame = null
    return
  }
  tableWrapperRef.value.scrollLeft += dragScrollSpeed
  dragScrollFrame = requestAnimationFrame(scrollDuringColumnDrag)
}

// handleTableDragOver updates edge scrolling while a column is being dragged
// over the table. It starts a frame only when scrolling is needed and no frame
// is already scheduled.
const handleTableDragOver = (event: DragEvent) => {
  if (!draggingColumn.value || !tableWrapperRef.value) return
  event.preventDefault()
  const rect = tableWrapperRef.value.getBoundingClientRect()
  // The edge zone and fixed per-frame speed let dragged columns reach targets
  // beyond the currently visible horizontal viewport.
  dragScrollSpeed = event.clientX < rect.left + 48 ? -12 : event.clientX > rect.right - 48 ? 12 : 0
  if (dragScrollFrame === null && dragScrollSpeed !== 0) {
    dragScrollFrame = requestAnimationFrame(scrollDuringColumnDrag)
  }
}

// handleTableDragLeave stops edge scrolling when the pointer leaves the table
// wrapper. Moves between descendants keep the current drag target, while leaving
// clears the visible drop marker.
const handleTableDragLeave = (event: DragEvent) => {
  if (event.relatedTarget instanceof Node && tableWrapperRef.value?.contains(event.relatedTarget))
    return
  dragScrollSpeed = 0
  dropTarget.value = null
}

// handleColumnDragOver marks a movable target before or after its midpoint.
// Invalid targets clear the marker, and valid drag events advertise a move
// effect to the browser.
const handleColumnDragOver = (event: DragEvent, key: string) => {
  if (!draggingColumn.value || !canReorder(key) || key === draggingColumn.value) {
    dropTarget.value = null
    return
  }
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  dropTarget.value = { key, side: event.clientX < rect.left + rect.width / 2 ? 'before' : 'after' }
}

// handleColumnDrop applies the active drop marker when it belongs to the
// receiving column. It ends the drag afterward, releasing the visual marker and
// any edge-scroll animation frame.
const handleColumnDrop = (event: DragEvent, key: string) => {
  if (!draggingColumn.value) return
  event.preventDefault()
  if (dropTarget.value?.key === key) {
    moveColumn(draggingColumn.value, key, dropTarget.value.side)
  }
  endColumnDrag()
}

// moveColumnByKeyboard moves a data column to its neighbor in the requested
// direction. Invalid neighbors return immediately; successful movement waits for
// rendering before restoring focus to the same column handle.
const moveColumnByKeyboard = async (key: string, direction: number) => {
  const index = movableColumns.value.findIndex((column) => column.key === key)
  const target = movableColumns.value[index + direction]
  if (!target || !moveColumn(key, target.key, direction < 0 ? 'before' : 'after')) return
  await nextTick()
  // Restore focus to the moved column handle after DOM reordering so consecutive
  // arrow presses can continue adjusting that same column.
  const handles = tableWrapperRef.value?.querySelectorAll<HTMLButtonElement>('.column-drag-handle')
  Array.from(handles ?? [])
    .find((handle) => handle.dataset.columnKey === key)
    ?.focus()
}

watch([() => props.columnOrderStorageKey, isDesktopViewport], endColumnDrag)
onUnmounted(endColumnDrag)

// PersistedSortState stores the sortable column key and requested direction for
// browser preferences.
type PersistedSortState = {
  key: string // Sortable column key value.
  order: 'asc' | 'desc' // Stored sort direction value.
}

// collator compares human-readable cell text with numeric segments and case-
// insensitive locale rules for the active language.
const collator = computed(
  () =>
    new Intl.Collator(locale?.value, {
      numeric: true,
      sensitivity: 'base',
    }),
)

// getSortableKeys collects the keys currently declared sortable by the caller.
const getSortableKeys = () => {
  const keys = new Set<string>()
  for (const col of props.columns) {
    if (col.sortable) keys.add(col.key)
  }
  return keys
}

// normalizeSortKey accepts a current sortable column key or returns an empty
// key. Removed columns and keys whose sorting was disabled cannot restore an
// obsolete browser sort preference.
const normalizeSortKey = (candidate: string) => {
  if (!candidate) return ''
  const sortableKeys = getSortableKeys()
  return sortableKeys.has(candidate) ? candidate : ''
}

// normalizeSortOrder accepts descending order and normalizes every other value
// to ascending. Browser preferences and optional defaults use this function
// before their direction is applied or persisted.
const normalizeSortOrder = (candidate: any): 'asc' | 'desc' => {
  return candidate === 'desc' ? 'desc' : 'asc'
}

// readPersistedSortState reads and validates the configured browser sort
// preference. Missing storage keys, missing values, obsolete columns, invalid
// JSON, and storage errors return null so default sorting can apply.
const readPersistedSortState = (): PersistedSortState | null => {
  if (!props.sortStorageKey) return null
  try {
    const raw = localStorage.getItem(props.sortStorageKey)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<PersistedSortState>
    const key = normalizeSortKey(typeof parsed.key === 'string' ? parsed.key : '')
    if (!key) return null
    return { key, order: normalizeSortOrder(parsed.order) }
  } catch (e) {
    // Ignore unusable browser preferences so a valid configured default can
    // still establish sorting for the current columns.
    console.error('[DataTable] Failed to read persisted sort state:', e)
    return null
  }
}

// writePersistedSortState saves the supplied sort choice when a storage key
// exists. Storage errors are logged and leave the current reactive sort state
// available to the table.
const writePersistedSortState = (state: PersistedSortState) => {
  if (!props.sortStorageKey) return
  try {
    localStorage.setItem(props.sortStorageKey, JSON.stringify(state))
  } catch (e) {
    // Keep the selected order active in this page when persistence fails, while
    // logging why another visit may not restore it.
    console.error('[DataTable] Failed to persist sort state:', e)
  }
}

// resolveInitialSortState prefers a valid stored sort choice over the configured
// default. If neither names a currently sortable column it returns null, leaving
// the existing empty sort state untouched.
const resolveInitialSortState = (): PersistedSortState | null => {
  const persisted = readPersistedSortState()
  if (persisted) return persisted

  const key = normalizeSortKey(props.defaultSortKey || '')
  if (!key) return null
  return { key, order: normalizeSortOrder(props.defaultSortOrder) }
}

// applySortState copies a resolved key and direction into the reactive table
// state. A null choice leaves the current values unchanged for callers that have
// no valid sort configuration.
const applySortState = (state: PersistedSortState | null) => {
  if (!state) return
  sortKey.value = state.key
  sortOrder.value = state.order
}

// getSortIndicatorClass highlights the currently active column and direction arrow.
const getSortIndicatorClass = (key: string, order: 'asc' | 'desc') => {
  return sortKey.value === key && sortOrder.value === order
    ? 'text-primary-600 dark:text-primary-400'
    : 'text-gray-300 transition-colors dark:text-dark-500'
}

// getColumnAriaSort describes the active direction for accessible sortable headers.
const getColumnAriaSort = (key: string) => {
  if (sortKey.value !== key) return 'none'
  return sortOrder.value === 'asc' ? 'ascending' : 'descending'
}

// getHeaderContentAlignmentClass aligns header content with the configured cell
// text classes. Custom selection columns center their contents, right-aligned
// cells align to the end, and other columns start normally.
const getHeaderContentAlignmentClass = (column: Column) => {
  if (column.key === 'select') return 'justify-center'
  const className = column.class || ''
  if (className.includes('text-center')) return 'justify-center'
  if (className.includes('text-right')) return 'justify-end'
  return 'justify-start'
}

// isNullishOrEmpty identifies missing values and empty strings for table
// comparisons.
const isNullishOrEmpty = (value: any) => value === null || value === undefined || value === ''

// toFiniteNumberOrNull converts finite numbers, booleans, and numeric strings
// for comparison. Blank strings, nonfinite numbers, and other value kinds return
// null so sorting can use string comparison.
const toFiniteNumberOrNull = (value: any): number | null => {
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  if (typeof value === 'boolean') return value ? 1 : 0
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return null
    const n = Number(trimmed)
    return Number.isFinite(n) ? n : null
  }
  return null
}

// toSortableString converts comparison values into text while retaining existing
// strings and ISO dates. Objects use JSON when serializable and their string
// conversion when JSON serialization fails.
const toSortableString = (value: any): string => {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (value instanceof Date) return value.toISOString()
  try {
    return JSON.stringify(value)
  } catch {
    // Circular or otherwise unserializable cell values still need comparable
    // text, so use their normal string conversion after JSON fails.
    return String(value)
  }
}

// compareSortValues returns a normalized comparison for missing, numeric, or
// textual table values. Empty values compare after nonempty ones before
// direction is applied. Finite numeric pairs compare numerically; other pairs
// use the locale collator so human-readable text follows the current language
// and numeric collation settings. Equal comparisons return zero, allowing
// sortedData to retain input order through its explicit original-index tie
// breaker.
const compareSortValues = (a: any, b: any): number => {
  const aEmpty = isNullishOrEmpty(a)
  const bEmpty = isNullishOrEmpty(b)
  if (aEmpty && bEmpty) return 0
  if (aEmpty) return 1
  if (bEmpty) return -1

  const aNum = toFiniteNumberOrNull(a)
  const bNum = toFiniteNumberOrNull(b)
  if (aNum !== null && bNum !== null) {
    if (aNum === bNum) return 0
    return aNum < bNum ? -1 : 1
  }

  const aStr = toSortableString(a)
  const bStr = toSortableString(b)
  const res = collator.value.compare(aStr, bStr)
  if (res === 0) return 0
  return res < 0 ? -1 : 1
}
// resolveStableRowKey reads identity from the configured resolver, configured
// property, or default id field. Nullish results become undefined, allowing
// rendering to use an index and measurement-reset checks to consider object
// identity.
const resolveStableRowKey = (row: any): string | number | undefined => {
  if (typeof props.rowKey === 'function') {
    const key = props.rowKey(row)
    return key ?? undefined
  }
  if (typeof props.rowKey === 'string' && props.rowKey) {
    const key = row?.[props.rowKey]
    return key ?? undefined
  }
  const key = row?.id
  return key ?? undefined
}

// resolveRowKey returns configured row identity or the supplied current row
// index. Selection callers should supply stable row keys when records may be
// reordered or replaced between renders.
const resolveRowKey = (row: any, index: number) => resolveStableRowKey(row) ?? index

// Column computations omit actions from mobile fields and track sortable key
// changes for measurement and stored-sort reconciliation.
const dataColumns = computed(() =>
  orderedColumns.value.filter((column) => column.key !== 'actions'),
)
const columnsSignature = computed(() =>
  orderedColumns.value.map((column) => `${column.key}:${column.sortable ? '1' : '0'}`).join('|'),
)

watch(
  isDesktopViewport,
  async (isDesktop) => {
    detachDesktopTableTracking()
    if (!isDesktop) return
    await nextTick()
    attachDesktopTableTracking()
  },
  { immediate: true, flush: 'post' },
)

// Recheck width after data or columns change. Keep action expansion outside this
// watch because measurement temporarily mutates it and would trigger another
// measurement cycle.
watch(
  [() => props.data.length, columnsSignature],
  async () => {
    await nextTick()
    checkScrollable()
    checkActionsColumnWidth()
  },
  { flush: 'post' },
)

// Expansion changes refresh horizontal overflow after rendering, while action
// measurement remains driven by data and column changes.
watch(actionsExpanded, async () => {
  await nextTick()
  checkScrollable()
})

// handleSort toggles the current column direction or starts a different column
// ascending. Server sorting publishes the choice to the parent, while client
// sorting relies on the reactive sorted-data computation.
const handleSort = (key: string) => {
  let newOrder: 'asc' | 'desc' = 'asc'
  if (sortKey.value === key) {
    newOrder = sortOrder.value === 'asc' ? 'desc' : 'asc'
  }

  if (props.serverSideSort) {
    // Server-side sort mode: emit event and update internal state for UI feedback
    sortKey.value = key
    sortOrder.value = newOrder
    emit('sort', key, newOrder)
  } else {
    // Client-side sort mode: just update internal state
    sortKey.value = key
    sortOrder.value = newOrder
  }
}

// sortedData returns server-ordered rows directly or creates a stable client-
// side ordering using values and original input positions.
const sortedData = computed(() => {
  // Server-side sort mode: return data as-is (server handles sorting)
  if (props.serverSideSort || !sortKey.value || !props.data) return props.data

  const key = sortKey.value
  const order = sortOrder.value

  // Stable sort (tie-break with original index) to avoid jitter when values are equal.
  return props.data
    .map((row, index) => ({ row, index }))
    .sort((a, b) => {
      const cmp = compareSortValues(a.row?.[key], b.row?.[key])
      if (cmp !== 0) return order === 'asc' ? cmp : -cmp
      return a.index - b.index
    })
    .map((item) => item.row)
})

// Selection computations combine current-page record identities with controlled
// keys, retaining off-page keys when visible rows are toggled.
const tableColumnCount = computed(() => props.columns.length + (props.selectable ? 1 : 0))
const selectedKeySet = computed(() => new Set(props.selectedKeys))
const visibleRowKeys = computed(() =>
  (sortedData.value ?? []).map((row, index) => resolveRowKey(row, index)),
)
const allVisibleSelected = computed(
  () =>
    visibleRowKeys.value.length > 0 &&
    visibleRowKeys.value.every((key) => selectedKeySet.value.has(key)),
)
const someVisibleSelected = computed(() => {
  if (allVisibleSelected.value) return false
  return visibleRowKeys.value.some((key) => selectedKeySet.value.has(key))
})

// emitSelection publishes a copied key array through both controlled-value and
// selection-change events. The parent owns the selectedKeys prop and applies the
// resulting selection to later table renders.
const emitSelection = (next: Set<string | number>) => {
  const keys = Array.from(next)
  emit('update:selectedKeys', keys)
  emit('selectionChange', keys)
}

// isRowSelected checks whether the controlled selection contains the resolved
// row key.
const isRowSelected = (row: any, index: number) =>
  selectedKeySet.value.has(resolveRowKey(row, index))

// getRowSelectionLabel resolves a row callback, fixed label, or translated
// default selection label. The default includes the same resolved row key used
// for controlled selection and rendered identity.
const getRowSelectionLabel = (row: any, index: number) => {
  if (typeof props.selectionLabel === 'function') return props.selectionLabel(row)
  if (props.selectionLabel) return props.selectionLabel
  return `${t('common.selectOption')} ${resolveRowKey(row, index)}`
}

// toggleRowSelection adds or removes one resolved row key in a copy of the
// controlled selection. Publishing the copy retains all other selected records,
// including rows outside this page.
const toggleRowSelection = (row: any, index: number, checked: boolean) => {
  const next = new Set(props.selectedKeys)
  const key = resolveRowKey(row, index)
  if (checked) next.add(key)
  else next.delete(key)
  emitSelection(next)
}

// toggleAllVisible adds or removes every visible row key in a copied selection.
// Keys from other pages remain in the result because only the current sorted-
// data identities are visited.
const toggleAllVisible = (checked: boolean) => {
  const next = new Set(props.selectedKeys)
  for (const key of visibleRowKeys.value) {
    if (checked) next.add(key)
    else next.delete(key)
  }
  emitSelection(next)
}

// --- Virtual scrolling ---
// Virtualization applies to desktop lists above the configured threshold; small
// lists use actual row heights through normal document layout.
const shouldVirtualize = computed(
  () =>
    isDesktopViewport.value && (sortedData.value?.length ?? 0) > (props.virtualizeThreshold ?? 100),
)

// rowVirtualizer owns measured row heights and visible window calculation while
// the component supplies resolved row keys and usable viewport observations.
const rowVirtualizer = useVirtualizer(
  computed(() => ({
    count: shouldVirtualize.value ? (sortedData.value?.length ?? 0) : 0,
    getScrollElement: () => tableWrapperRef.value,
    // Use the same key as template rows. Explicit keys follow records across
    // sorting; rows without them use their current array indexes for measurement lookup.
    getItemKey: (index: number) => {
      const row = sortedData.value?.[index]
      return row != null ? resolveRowKey(row, index) : index
    },
    estimateSize: () => props.estimateRowHeight ?? 56,
    overscan: props.overscan ?? 5,
    // Start with a viewport estimate so rows can render before the first positive
    // wrapper measurement becomes available.
    initialRect: { width: 0, height: estimatedViewportHeight() },
    // Ignore transient zero heights so flex layout changes do not discard the
    // current virtual range and blank the table.
    observeElementRect: observeElementRectNonZero,
    // Batch observer measurement with animation frames so scrolling layout updates
    // are processed together within the rendering frame.
    useAnimationFrameWithResizeObserver: true,
  })),
)

// Virtual row computations expose visible entries and spacer heights so the
// table maintains its full scrollable extent around the rendered window.
const virtualItems = computed(() => rowVirtualizer.value.getVirtualItems())

const virtualPaddingTop = computed(() => {
  const items = virtualItems.value
  return items.length > 0 ? items[0].start : 0
})

const virtualPaddingBottom = computed(() => {
  const items = virtualItems.value
  if (items.length === 0) return 0
  return rowVirtualizer.value.getTotalSize() - items[items.length - 1].end
})

// measureElement forwards a mounted row element to the virtualizer for height
// measurement. Empty template refs are ignored here, while identity changes use
// the separate measurement-reset path.
const measureElement = (el: any) => {
  if (el) {
    rowVirtualizer.value.measureElement(el as Element)
  }
}

// RowIdentityToken identifies records for deciding whether virtual row
// measurements remain reusable.
type RowIdentityToken = string | number | object | symbol

// rowIdentityKeys supplies explicit keys, object references, or fresh primitive
// symbols to decide whether cached measurements reset. The virtualizer's item
// keys are resolved separately by resolveRowKey, which can use array indexes.
const rowIdentityKeys = computed<RowIdentityToken[]>(() =>
  (sortedData.value ?? []).map((row) => {
    const stableKey = resolveStableRowKey(row)
    if (stableKey !== undefined) return stableKey

    // Object references identify rows through sorting when no explicit key
    // exists. Primitive rows receive fresh symbols so uncertain identity
    // invalidates measurement reuse.
    return row !== null && typeof row === 'object' ? row : Symbol('unstable-row')
  }),
)

// hasSameRowIdentitySet reports whether both row lists contain the same distinct
// identities. Reordering those identities retains existing cache entries; actual
// measurement lookup uses resolveRowKey separately. Different lengths, missing
// identities, or duplicates return false so the watcher resets uncertain caches.
// The duplicate check runs before membership comparison, so repeated-key lists cannot pass
// merely because their deduplicated sets match even when key multiplicities
// changed between pages.
const hasSameRowIdentitySet = (current: RowIdentityToken[], previous: RowIdentityToken[]) => {
  if (current.length !== previous.length) return false
  const currentKeys = new Set(current)
  const previousKeys = new Set(previous)
  // Duplicate keys make measurement ownership ambiguous, even when deduplicated
  // sets match, so distributions such as [1, 1, 2] and [1, 2, 2] invalidate
  // reuse.
  if (currentKeys.size !== current.length || previousKeys.size !== previous.length) return false
  return [...currentKeys].every((key) => previousKeys.has(key))
}

watch(
  rowIdentityKeys,
  (current, previous) => {
    if (hasSameRowIdentitySet(current, previous)) return

    // Release retained elements and heights when the identity set changes.
    // Reordering distinct identities skips this reset; measurement lookup still
    // follows the virtualizer's independently resolved item keys.
    rowVirtualizer.value.measureElement(null)
    rowVirtualizer.value.measure()
  },
  { flush: 'post' },
)

// renderRows supplies the shared row template with either measured
// virtual-window entries or every ordinary row, keeping cell rendering common to
// both modes.
const renderRows = computed<
  Array<{
    index: number // Current sorted row index.
    row: any // Caller supplied row record.
    measure: boolean // Virtual row measurement flag.
  }>
>(() => {
  const data = sortedData.value ?? []
  if (shouldVirtualize.value) {
    return virtualItems.value.map((vr) => ({ index: vr.index, row: data[vr.index], measure: true }))
  }
  return data.map((row, index) => ({ index, row, measure: false }))
})

// Structural-column computations select mobile action slots and identify a
// leading custom selection column for sticky layout rules.
const hasActionsColumn = computed(() => {
  return props.columns.some((column) => column.key === 'actions')
})

const hasSelectColumn = computed(() => {
  return props.columns.length > 0 && props.columns[0].key === 'select'
})

// getStickyColumnClass selects left or right sticky positioning for a column.
// A leading custom selection column scrolls normally, while the first data
// column and actions column follow their independent sticky configuration flags.
const getStickyColumnClass = (column: Column, index: number) => {
  const classes: string[] = []

  // Leave the selection column in normal scrolling flow so the first data column
  // becomes the left sticky edge when it reaches the wrapper.
  const firstDataColumnIndex = hasSelectColumn.value ? 1 : 0
  if (props.stickyFirstColumn && index === firstDataColumnIndex) {
    classes.push('sticky-col sticky-col-left')
  }

  // Actions can remain pinned to the right edge.
  if (props.stickyActionsColumn && column.key === 'actions') {
    classes.push('sticky-col sticky-col-right')
  }

  return classes.join(' ')
}

// getColumnLayoutClass gives selection cells their dedicated layout and reduces
// data-cell padding for wider column sets.
// The same class applies to header and body cells so their widths stay aligned.
const getColumnLayoutClass = (column?: Column) => {
  // Custom selection slots use the same width as built-in row selection.
  if (column?.key === 'select') return 'table-selection-cell'
  const columnCount = props.columns.length

  // Reduce padding at wider column counts to leave more horizontal space for cell contents.
  if (columnCount >= 10) {
    return 'px-2' // 8px
  } else if (columnCount >= 7) {
    return 'px-3' // 12px
  } else if (columnCount >= 5) {
    return 'px-4' // 16px
  } else {
    return 'px-6' // 24px for sparse column sets.
  }
}

// didInitSort blocks sort persistence until mounted initialization resolves and
// applies the saved or default sort for the current table columns.
const didInitSort = ref(false)

onMounted(() => {
  const initial = resolveInitialSortState()
  applySortState(initial)
  didInitSort.value = true
})

watch(
  columnsSignature,
  () => {
    // If current sort key is no longer sortable/visible, fall back to default/persisted.
    const normalized = normalizeSortKey(sortKey.value)
    if (!sortKey.value) {
      const initial = resolveInitialSortState()
      applySortState(initial)
      return
    }

    if (!normalized) {
      const fallback = resolveInitialSortState()
      if (fallback) {
        applySortState(fallback)
      } else {
        sortKey.value = ''
        sortOrder.value = 'asc'
      }
    }
  },
  { flush: 'post' },
)

watch(
  [sortKey, sortOrder],
  ([nextKey, nextOrder]) => {
    if (!didInitSort.value) return
    if (!props.sortStorageKey) return
    const key = normalizeSortKey(nextKey)
    if (!key) return
    writePersistedSortState({ key, order: normalizeSortOrder(nextOrder) })
  },
  { flush: 'post' },
)

defineExpose({
  virtualizer: rowVirtualizer,
  shouldVirtualize,
  sortedData,
  resolveRowKey,
  tableWrapperEl: tableWrapperRef,
})
</script>

<style scoped>
/* The wrapper owns scrolling and its local sticky stacking context. */
.table-wrapper {
  --sticky-boundary-line-color: rgb(228 228 231);
  position: relative;
  overflow-x: auto;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
  isolation: isolate;
}

.dark .table-wrapper {
  --sticky-boundary-line-color: theme('borderColor.dark.600');
}

/* Selection cells share the table selection layout. */
.table-wrapper .table-selection-cell {
  @apply w-11 min-w-11 px-3 text-center;
}

/* Drag handles match the sort indicator height. */
.column-drag-handle {
  @apply inline-flex h-5 w-4 shrink-0 cursor-grab items-center justify-center rounded-compact text-gray-400 hover:text-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 active:cursor-grabbing dark:text-dark-400 dark:hover:text-primary-400;
}

.column-drop-before {
  box-shadow: inset 3px 0 0 theme('colors.primary.500');
}

.column-drop-after {
  box-shadow: inset -3px 0 0 theme('colors.primary.500');
}

/* Sticky headers cover scrolling body rows. */
.table-wrapper .table-header {
  position: sticky;
  top: 0;
  z-index: 200; /* check-ui-allow: Table-local header and sticky-column stacking. */
  background-color: rgb(249 250 251);
}

.dark .table-wrapper .table-header {
  background-color: rgb(15 15 16);
}

/* Body rows remain below the sticky header. */
.table-body {
  position: relative;
  z-index: 0; /* check-ui-allow: Table-local stacking. */
}

/* Header cells remain at the wrapper top. */
.sticky-header-cell {
  position: sticky;
  top: 0;
  z-index: 210; /* Above table body content. */ /* check-ui-allow: Table-local stacking. */
  background-color: rgb(249 250 251);
}

.dark .sticky-header-cell {
  background-color: rgb(15 15 16);
}

/* Shared sticky column positioning. */
.sticky-col {
  position: sticky;
  z-index: 20; /* Sticky body column layer. */ /* check-ui-allow: Table-local stacking. */
}

/* The first data column occupies the left sticky edge. */
.sticky-col-left {
  left: 0;
}

/* Actions occupy the right sticky edge. */
.sticky-col-right {
  right: 0;
}

/* Sticky header intersections appear above ordinary header cells. */
.sticky-header-cell.sticky-col {
  z-index: 220; /* Above ordinary headers and sticky body columns. */ /* check-ui-allow: Table-local stacking. */
}

/* Sticky body cells use an opaque background. */
tbody .sticky-col {
  background-color: white;
}

.dark tbody .sticky-col {
  background-color: rgb(15 15 16);
}

/* Sticky cells follow the containing row hover state. */
tbody tr:hover .sticky-col {
  background-color: rgb(249 250 251);
}

.dark tbody tr:hover .sticky-col {
  background-color: rgb(23 23 26);
}

/* Thin separators mark the edges of sticky columns. */
.sticky-boundary-line.is-scrollable .sticky-col-left::after {
  content: '';
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 1px;
  transform: translateX(100%);
  background-color: var(--sticky-boundary-line-color);
  pointer-events: none;
}

.sticky-boundary-line.is-scrollable .sticky-col-right::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  width: 1px;
  transform: translateX(-100%);
  background-color: var(--sticky-boundary-line-color);
  pointer-events: none;
}
</style>

<style>
/* Persistent scrollbars indicate that the table supports horizontal scrolling. */
.table-wrapper {
  scrollbar-width: auto !important;
}

.table-wrapper::-webkit-scrollbar {
  height: 12px !important;
  width: 12px !important;
  display: block !important;
  background-color: transparent !important;
}

.table-wrapper::-webkit-scrollbar-track {
  background-color: rgba(0, 0, 0, 0.03) !important;
  border-radius: 9999px !important;
  margin: 0 4px !important;
}
.dark .table-wrapper::-webkit-scrollbar-track {
  background-color: rgba(255, 255, 255, 0.05) !important;
}

.table-wrapper::-webkit-scrollbar-thumb {
  background-color: rgba(107, 114, 128, 0.75) !important;
  border-radius: 9999px !important;
  border: 2px solid transparent !important;
  background-clip: padding-box !important;
  -webkit-appearance: none !important;
}
.table-wrapper::-webkit-scrollbar-thumb:hover {
  background-color: rgba(75, 85, 99, 0.9) !important;
}

.dark .table-wrapper::-webkit-scrollbar-thumb {
  background-color: rgba(161, 161, 170, 0.45) !important;
}
.dark .table-wrapper::-webkit-scrollbar-thumb:hover {
  background-color: rgba(161, 161, 170, 0.65) !important;
}

@supports (-moz-appearance: none) {
  .table-wrapper {
    scrollbar-width: thin !important;
    scrollbar-color: rgba(156, 163, 175, 0.5) rgba(0, 0, 0, 0.03) !important;
  }
  .dark .table-wrapper {
    scrollbar-color: rgba(75, 85, 99, 0.5) rgba(255, 255, 255, 0.05) !important;
  }
}
</style>

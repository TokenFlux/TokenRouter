<template>
  <div ref="rootRef" class="relative shrink-0" @keydown.esc.stop.prevent="closeWithFocus">
    <button
      ref="triggerRef"
      type="button"
      class="relative"
      :class="[triggerClass, activeCount ? 'filter-trigger-active' : '']"
      :aria-label="t('common.filter')"
      :title="t('common.filter')"
      :aria-expanded="open"
      aria-haspopup="dialog"
      @click="toggle"
    >
      <Icon name="filter" size="sm" />
      <span v-if="activeCount" class="filter-trigger-count">{{ activeCount }}</span>
    </button>

    <MotionTransition name="dropdown-fade" :persisted="keepMounted">
      <div
        v-if="keepMounted || open"
        v-show="open"
        :inert="!open || undefined"
        class="filter-panel"
        :style="panelStyle"
        role="dialog"
        :aria-label="t('common.filter')"
        @click.stop
      >
        <div ref="bodyRef" class="filter-panel-body">
          <slot />
        </div>
        <div v-if="activeCount" class="filter-panel-footer">
          <button
            type="button"
            class="filter-panel-reset"
            data-testid="filter-reset"
            @click="resetFilters"
          >
            <Icon name="undo" size="sm" />
            {{ t('common.resetFilters') }}
          </button>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, provide, readonly, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import { FILTER_PANEL_KEY } from './filterPanel'

// FilterDropdown.vue presents a toolbar filter button and the narrow panel holding its
// FilterField sections. The panel is a single column of collapsible sections, modeled
// on a sidebar filter list: each section expands into checkable options, so changing a
// condition takes one click inside the panel. The parent owns every filter value and
// handles reset; the trigger shows the parent's active count.
//
// Fields learn about each opening through the provided panel context, which counts
// openings and mounted fields so each field can choose its opening expansion. The panel
// width is fixed in root-font units and clamped to the viewport through the shared
// position helper; the resulting horizontal coordinate is converted back into the
// relatively positioned wrapper.

// The props declare the active count, retention, and trigger styling read by the
// template.
withDefaults(
  defineProps<{
    activeCount: number // Active filter condition count.

    // Retains child DOM while closed so nested controls keep their current local input
    // state between openings.
    keepMounted?: boolean
    triggerClass?: string // Trigger button class list.
  }>(),
  {
    keepMounted: false,
    triggerClass: 'btn btn-secondary btn-icon',
  },
)
// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<{
  // Requests parent filter reset.
  (event: 'reset'): void
}>()
// t resolves the component action and status labels using the active application
// locale.
const { t } = useI18n()

// PANEL_WIDTH_REM fixes the panel to a sidebar-like column; narrower viewports shrink it
// through the position helper's padding clamp.
const PANEL_WIDTH_REM = 18

// open controls visibility, rootRef supplies bounds and click ownership, triggerRef
// restores keyboard focus, bodyRef locates the first field heading, and panelStyle
// holds the clamped horizontal placement.
const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)
const triggerRef = ref<HTMLButtonElement | null>(null)
const bodyRef = ref<HTMLElement | null>(null)
const panelStyle = ref<Record<string, string>>({})

// openCount increases on every opening and fieldCount follows mounted FilterField
// sections; fields read both through the provided panel context.
const openCount = ref(0)
const fieldCount = ref(0)
provide(FILTER_PANEL_KEY, {
  openCount: readonly(openCount),
  fieldCount: readonly(fieldCount),
  registerField: () => {
    fieldCount.value += 1
    return () => {
      fieldCount.value -= 1
    }
  },
})

// updatePosition measures the wrapper and delegates viewport clamping to the shared
// helper. It converts rem widths using the root font size and stores an offset
// relative to the wrapper.
function updatePosition() {
  const rect = rootRef.value?.getBoundingClientRect()
  if (!rect) return
  // Use the root font size to convert the panel width into pixels; unavailable numeric
  // font sizing uses sixteen so width calculations remain usable.
  const rem = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
  const viewportWidth = document.documentElement.clientWidth || window.innerWidth
  const position = getFloatingPanelPosition(rect, viewportWidth, window.innerHeight, {
    maxWidth: PANEL_WIDTH_REM * rem,
    align: 'left',
    viewportPadding: rem,
  })
  panelStyle.value = {
    width: `${position.width}px`,
    left: `${position.left - rect.left}px`,
  }
}

// toggle changes panel visibility. Opening measures the position and advances
// openCount so fields reset their expansion; closing keeps child state when keepMounted
// is enabled, with the template applying hidden and inert states during exit.
function toggle() {
  open.value = !open.value
  if (!open.value) return
  openCount.value += 1
  updatePosition()
}

// resetFilters asks the parent to clear its conditions. The reset row disappears once
// nothing is active, so focus that was on it moves to the first field heading and
// keyboard users stay inside the open panel.
function resetFilters() {
  emit('reset')
  nextTick(() => {
    if (rootRef.value?.contains(document.activeElement)) return
    bodyRef.value?.querySelector<HTMLButtonElement>('button')?.focus()
  })
}

// closeWithFocus closes the filter panel and focuses its trigger button. Keyboard
// dismissal uses this path so users can continue moving through the surrounding
// toolbar after leaving the panel.
function closeWithFocus() {
  open.value = false
  triggerRef.value?.focus()
}

// closeOutside dismisses the panel when a click lands outside the wrapper. Every
// control of the panel renders inside the wrapper, so no portal needs an exemption.
function closeOutside(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof Node) || rootRef.value?.contains(target)) return
  open.value = false
}

// handleResize refreshes panel placement when the open panel receives a viewport
// resize. Closed panels defer measurement until their next opening, when their current
// wrapper bounds are available.
function handleResize() {
  if (open.value) updatePosition()
}

// Capture document clicks before dialog handlers stop bubbling, allowing this panel to
// dismiss when another region inside the dialog is clicked.
onMounted(() => {
  document.addEventListener('click', closeOutside, true)
  window.addEventListener('resize', handleResize)
})

// Release document and viewport listeners with the panel owner so removed filters
// cannot receive clicks or schedule fresh position updates.
onBeforeUnmount(() => {
  document.removeEventListener('click', closeOutside, true)
  window.removeEventListener('resize', handleResize)
})

defineExpose({
  // close hides the panel through the exposed component handle.
  close: () => {
    open.value = false
  },
})
</script>

<style scoped>
/*
  Active filters use a brand-colored trigger outline and icon with a count badge at
  the upper right.
*/
.filter-trigger-active {
  @apply border-primary-300 text-primary-700 dark:border-primary-500/40 dark:text-primary-400;
}

.filter-trigger-count {
  @apply pointer-events-none absolute -right-1.5 -top-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full px-1;
  @apply bg-primary-600 text-xs font-semibold leading-none text-white;
  @apply ring-2 ring-white dark:ring-dark-950;
}

/*
  The panel keeps the date-range picker surface. Its 6px padding plus the rows'
  compact radius stays concentric with the surface radius.
*/
.filter-panel {
  @apply absolute top-full z-modal-nested mt-2 flex flex-col overflow-hidden;
  @apply max-h-[min(70vh,42rem)];
  @apply bg-white dark:bg-dark-900;
  @apply rounded-surface border border-primary-900/10 dark:border-dark-600;
  @apply shadow-lg shadow-black/10 dark:shadow-black/30;
}

.filter-panel-body {
  @apply min-h-0 overflow-y-auto p-1.5;
}

/* The reset row appears only with active conditions, separated like inset menu sections. */
.filter-panel-footer {
  @apply shrink-0 border-t border-primary-900/10 p-1.5 dark:border-dark-700;
}

.filter-panel-reset {
  @apply flex h-9 w-full items-center gap-2.5 rounded-compact px-2.5 text-left text-sm font-medium;
  @apply text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50;
  @apply transition-colors duration-fast;
  @apply outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/50;
}
</style>

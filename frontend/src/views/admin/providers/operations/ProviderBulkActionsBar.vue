<template>
  <!-- The selection toolbar occupies the final filter row and appears when provider identifiers are selected. -->
  <div
    v-if="hasSelection"
    class="flex flex-wrap items-center justify-between gap-2 rounded-surface border border-gray-200 bg-white px-4 py-2 dark:border-dark-600 dark:bg-dark-900"
  >
    <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm">
      <span class="font-medium text-gray-900 dark:text-gray-100">
        <template v-if="allResultsSelected">
          {{ t('admin.providers.bulkActions.selectedAll', { count: selectedIds.length }) }}
        </template>
        <template v-else>
          {{ t('admin.providers.bulkActions.selected', { count: selectedIds.length }) }}
        </template>
      </span>
      <span class="h-4 w-px bg-gray-200 dark:bg-dark-600" aria-hidden="true"></span>
      <button type="button" class="bulk-bar-link" @click="$emit('select-page')">
        {{ t('admin.providers.bulkActions.selectCurrentPage') }}
      </button>
      <button
        v-if="!allResultsSelected && totalResults > selectedIds.length"
        type="button"
        class="bulk-bar-link"
        :disabled="selectingAll"
        @click="$emit('select-all-results')"
      >
        {{
          selectingAll
            ? t('admin.providers.bulkActions.selectingAll')
            : t('admin.providers.bulkActions.selectAllResults', { count: totalResults })
        }}
      </button>
      <button type="button" class="bulk-bar-link" @click="$emit('clear')">
        {{ t('admin.providers.bulkActions.clear') }}
      </button>
    </div>

    <!-- Common actions remain visible; maintenance and deletion actions share the overflow menu with neutral button styling. -->
    <div class="flex flex-wrap justify-end gap-2">
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        @click="$emit('toggle-schedulable', true)"
      >
        <Icon name="play" size="sm" />
        {{ t('admin.providers.bulkActions.enableScheduling') }}
      </button>
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        @click="$emit('toggle-schedulable', false)"
      >
        <Icon name="ban" size="sm" />
        {{ t('admin.providers.bulkActions.disableScheduling') }}
      </button>
      <button type="button" class="btn btn-secondary btn-sm" @click="$emit('edit-selected')">
        <Icon name="edit" size="sm" />
        {{ t('admin.providers.bulkActions.edit') }}
      </button>
      <button
        ref="moreButtonRef"
        type="button"
        class="btn btn-secondary btn-sm"
        :aria-expanded="moreMenuOpen"
        aria-haspopup="menu"
        @click="toggleMoreMenu"
      >
        {{ t('admin.providers.bulkActions.more') }}
        <Icon name="chevronDown" size="sm" />
      </button>
    </div>

    <Teleport to="body">
      <template v-if="moreMenuOpen && menuPosition">
        <div class="fixed inset-0 z-menu-overlay" @click="closeMoreMenu"></div>
        <div
          class="dropdown fixed z-action-menu py-0"
          role="menu"
          :style="{
            top: `${menuPosition.top}px`,
            left: `${menuPosition.left}px`,
            width: `${menuPosition.width}px`,
          }"
        >
          <div class="menu-section">
            <button
              type="button"
              role="menuitem"
              class="menu-item"
              @click="runMenuAction('reset-status')"
            >
              <Icon name="undo" size="sm" />
              {{ t('admin.providers.bulkActions.resetStatus') }}
            </button>
            <button
              type="button"
              role="menuitem"
              class="menu-item"
              @click="runMenuAction('refresh-token')"
            >
              <Icon name="key" size="sm" />
              {{ t('admin.providers.bulkActions.refreshToken') }}
            </button>
            <button
              type="button"
              role="menuitem"
              class="menu-item disabled:cursor-not-allowed disabled:opacity-60"
              :disabled="usageLoading"
              @click="runMenuAction('query-usage')"
            >
              <Icon name="chartBar" size="sm" />
              {{ t('admin.providers.bulkActions.queryUsage') }}
            </button>
            <button
              type="button"
              role="menuitem"
              class="menu-item disabled:cursor-not-allowed disabled:opacity-60"
              :disabled="upstreamUsageLoading"
              @click="runMenuAction('query-upstream-usage')"
            >
              <Icon name="cloud" size="sm" />
              {{ t('admin.providers.bulkActions.queryUpstreamUsage') }}
            </button>
          </div>
          <div class="menu-section">
            <button
              type="button"
              role="menuitem"
              class="menu-item menu-item-danger"
              @click="runMenuAction('delete')"
            >
              <Icon name="trash" size="sm" />
              {{ t('admin.providers.bulkActions.delete') }}
            </button>
          </div>
        </div>
      </template>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getFloatingPanelPosition, type FloatingPanelPosition } from '@/utils/floatingPanel'

/**
 * ProviderBulkActionsBar.vue renders actions for the provider page's current selection,
 * including cross-page selection and maintenance commands. It emits action intent while the
 * parent owns selected identifiers and request state. The overflow menu closes before emitting
 * a maintenance action and disappears when selection becomes empty.
 */

/**
 * MenuAction identifies maintenance commands offered by the selected-provider overflow menu.
 */
type MenuAction =
  'reset-status' | 'refresh-token' | 'query-usage' | 'query-upstream-usage' | 'delete'

/**
 * Overflow menu dimensions estimate panel placement so the shared floating helper can choose
 * available space above or below its trigger.
 */
const MORE_MENU_WIDTH = 208
const MORE_MENU_HEIGHT = 240

/**
 * props supplies current selection and operation feedback, while the parent owns page
 * selection, all-result selection, and maintenance requests.
 */
const props = defineProps<{
  selectedIds: number[] // selected provider record identifiers
  usageLoading?: boolean // batched usage request activity
  upstreamUsageLoading?: boolean // upstream query request activity
  totalResults: number // matching provider result count
  selectingAll: boolean // cross-page selection request activity
  allResultsSelected: boolean // all matching provider rows selected
}>()

/**
 * emit names bulk actions and selection changes; the scheduling event carries the target
 * boolean while other events use the parent-owned selection.
 */
const emit = defineEmits<{
  delete: [] // request selected provider deletion
  'edit-selected': [] // open selected provider editor
  clear: [] // clear current provider selection
  'select-page': [] // select current page providers
  'select-all-results': [] // select all matching providers
  'toggle-schedulable': [schedulable: boolean] // request selected scheduling state
  'reset-status': [] // request selected provider recovery
  'refresh-token': [] // request selected credential refresh
  'query-usage': [] // request selected usage refresh
  'query-upstream-usage': [] // request selected upstream queries
}>()

/**
 * Translation and hasSelection control the toolbar display, while overflow anchor, visibility,
 * and position remain local presentation state.
 */
const { t } = useI18n()

const hasSelection = computed(() => props.selectedIds.length > 0)

const moreButtonRef = ref<HTMLButtonElement | null>(null)
const moreMenuOpen = ref(false)
const menuPosition = ref<FloatingPanelPosition | null>(null)

/**
 * closeMoreMenu hides the overflow menu for the selected-provider action bar.
 */
const closeMoreMenu = () => {
  moreMenuOpen.value = false
}

/**
 * toggleMoreMenu closes an open menu or positions and opens it from the trigger. A missing
 * trigger closes the menu; viewport dimensions and estimated panel size guide placement through
 * the shared helper.
 */
const toggleMoreMenu = () => {
  if (moreMenuOpen.value || !moreButtonRef.value) {
    closeMoreMenu()
    return
  }
  menuPosition.value = getFloatingPanelPosition(
    moreButtonRef.value.getBoundingClientRect(),
    window.innerWidth,
    window.innerHeight,
    { maxWidth: MORE_MENU_WIDTH, fixedHeight: MORE_MENU_HEIGHT },
  )
  moreMenuOpen.value = true
}

/**
 * runMenuAction closes the overflow menu before emitting the selected maintenance event. The
 * parent may open a confirmation dialog, so dismissal happens before that next overlay receives
 * the action.
 */
const runMenuAction = (action: MenuAction) => {
  closeMoreMenu()
  if (action === 'reset-status') emit('reset-status')
  else if (action === 'refresh-token') emit('refresh-token')
  else if (action === 'query-usage') emit('query-usage')
  else if (action === 'query-upstream-usage') emit('query-upstream-usage')
  else emit('delete')
}

/**
 * handleEscape closes the selected-provider overflow menu when Escape is pressed.
 */
const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape') closeMoreMenu()
}

// The menu owns Escape handling only while open and releases the listener again when its
// visibility changes or the bar unmounts.
watch(moreMenuOpen, (open) => {
  if (open) window.addEventListener('keydown', handleEscape)
  else window.removeEventListener('keydown', handleEscape)
})

// Clearing selection removes the toolbar, so close its overflow at the same time to avoid a
// detached menu remaining visible.
watch(hasSelection, (selected) => {
  if (!selected) closeMoreMenu()
})

onUnmounted(() => window.removeEventListener('keydown', handleEscape))
</script>

<style scoped>
/* Toolbar text actions use the brand color, underline on hover, and reduced opacity when disabled to match their interaction state. */
.bulk-bar-link {
  @apply text-primary-600 underline-offset-4 transition-colors duration-fast hover:text-primary-700 hover:underline disabled:cursor-not-allowed disabled:no-underline disabled:opacity-60 dark:text-primary-400 dark:hover:text-primary-300;
}
</style>

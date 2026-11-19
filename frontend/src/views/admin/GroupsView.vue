<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col justify-between gap-2 lg:flex-row lg:items-start">
          <!-- Search and filter controls wrap together on narrow layouts. -->
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-gray-500" />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.groups.searchGroups')"
                class="input input-has-icon"
                @input="handleSearch"
              />
            </div>
            <FilterDropdown :active-count="activeFilterCount" @reset="resetGroupFilters">
              <FilterField :label="t('admin.groups.columns.status')" icon="checkCircle">
                <FilterOptions
                  v-model="filters.status"
                  :options="statusOptions"
                  @change="loadGroups"
                />
              </FilterField>
            </FilterDropdown>
          </div>

          <!-- Refresh, ordering, and creation actions. -->
          <div class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-2 lg:w-auto">
            <button
              :disabled="loading"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('common.refresh')"
              @click="loadGroups"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <div ref="columnDropdownRef" class="relative">
              <button
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('admin.groups.columnSettings')"
                @click="showColumnDropdown = !showColumnDropdown"
              >
                <Icon name="grid" size="sm" />
                <span class="hidden">{{ t('admin.groups.columnSettings') }}</span>
              </button>
              <MotionTransition name="dropdown-fade">
                <div
                  v-if="showColumnDropdown"
                  :inert="!showColumnDropdown || undefined"
                  class="dropdown right-0 top-full z-50 mt-1 max-h-menu w-48 overflow-y-auto"
                >
                  <button
                    v-for="col in toggleableColumns"
                    :key="col.key"
                    class="dropdown-item justify-between"
                    @click="toggleColumn(col.key)"
                  >
                    <span>{{ col.label }}</span>
                    <Icon
                      v-if="isColumnVisible(col.key)"
                      name="check"
                      size="sm"
                      class="text-primary-500"
                      :stroke-width="2"
                      :animate-on-hover="false"
                    />
                  </button>
                </div>
              </MotionTransition>
            </div>
            <button
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('admin.groups.sortOrder')"
              @click="openSortModal"
            >
              <Icon name="arrowsUpDown" size="sm" :animate-on-hover="false" />
            </button>
            <button
              class="btn btn-primary whitespace-nowrap"
              data-tour="groups-create-btn"
              @click="openCreateModal"
            >
              <Icon name="plus" size="sm" class="mr-2" />
              {{ t('admin.groups.createGroup') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-groups-column-order"
          :columns="columns"
          :data="groups"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="sort_order"
          default-sort-order="asc"
          @sort="handleSort"
        >
          <template #cell-name="{ value }">
            <div class="flex items-center gap-2">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
            </div>
          </template>

          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ value }}</span>
          </template>

          <template #cell-display_brand="{ value }">
            <span v-if="value" :class="displayBrandBadgeClass(value)">
              <ProviderIcon :brand="String(value)" size="14px" />
              <!-- The brand text establishes the baseline used by the label in the mobile table card. -->
              <span class="self-baseline">{{ displayBrandLabel(value) }}</span>
            </span>
            <span v-else class="text-sm text-gray-700 dark:text-gray-300">-</span>
          </template>

          <template #cell-rate_multiplier="{ value }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ value }}x</span>
          </template>

          <template #cell-is_exclusive="{ value }">
            <span :class="['badge', value ? 'badge-primary' : 'badge-gray']">
              {{ value ? t('admin.groups.exclusive') : t('admin.groups.public') }}
            </span>
          </template>

          <template #cell-session_isolation_enabled="{ value }">
            <span :class="['badge', value ? 'badge-warning' : 'badge-gray']">
              {{
                value
                  ? t('admin.groups.sessionIsolation.enabled')
                  : t('admin.groups.sessionIsolation.disabled')
              }}
            </span>
          </template>

          <template #cell-provider_count="{ row }">
            <div class="space-y-0.5 text-xs">
              <div>
                <span class="text-gray-500 dark:text-gray-400">{{
                  t('admin.groups.providersAvailable')
                }}</span>
                <span class="ml-1 font-medium text-emerald-600 dark:text-emerald-400">{{
                  row.active_provider_count || 0
                }}</span>
              </div>
              <div v-if="row.rate_limited_provider_count">
                <span class="text-gray-500 dark:text-gray-400">{{
                  t('admin.groups.providersRateLimited')
                }}</span>
                <span class="ml-1 font-medium text-amber-600 dark:text-amber-400">{{
                  row.rate_limited_provider_count
                }}</span>
              </div>
              <div>
                <span class="text-gray-500 dark:text-gray-400">{{
                  t('admin.groups.providersTotal')
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300">{{
                  row.provider_count || 0
                }}</span>
              </div>
            </div>
          </template>

          <template #cell-capacity="{ row }">
            <GroupCapacityBadge
              v-if="capacityMap.get(row.id)"
              :concurrency-used="capacityMap.get(row.id)!.concurrencyUsed"
              :concurrency-max="capacityMap.get(row.id)!.concurrencyMax"
              :sessions-used="capacityMap.get(row.id)!.sessionsUsed"
              :sessions-max="capacityMap.get(row.id)!.sessionsMax"
              :rpm-used="capacityMap.get(row.id)!.rpmUsed"
              :rpm-max="capacityMap.get(row.id)!.rpmMax"
            />
            <span v-else class="text-xs text-gray-400">—</span>
          </template>

          <template #cell-usage="{ row }">
            <div
              v-if="usageLoading && !usageMap.has(row.id)"
              class="space-y-1"
              :aria-label="t('common.loading')"
              aria-busy="true"
              data-loading-skeleton
            >
              <Skeleton
                v-for="line in 3"
                :key="line"
                :width="96"
                :height="12"
                class="max-w-full max-lg:ml-auto"
              />
            </div>
            <span
              v-else-if="!usageMap.has(row.id) && !usageLoaded"
              class="text-xs text-gray-500 dark:text-dark-400"
              :title="t('usage.summaryUnavailable')"
              >—</span
            >
            <div v-else class="space-y-0.5 text-xs">
              <div class="text-gray-500 dark:text-gray-400">
                <span class="text-gray-400 dark:text-gray-500">{{
                  t('admin.groups.usageToday')
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300">{{
                  formatGroupBalance(usageMap.get(row.id)?.today_cost ?? 0)
                }}</span>
              </div>
              <div class="text-gray-500 dark:text-gray-400">
                <span class="text-gray-400 dark:text-gray-500">{{
                  t('admin.groups.usageYesterday')
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300">{{
                  formatGroupBalance(usageMap.get(row.id)?.yesterday_cost ?? 0)
                }}</span>
              </div>
              <div class="text-gray-500 dark:text-gray-400">
                <span class="text-gray-400 dark:text-gray-500">{{
                  t('admin.groups.usageTotal')
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300">{{
                  formatGroupBalance(usageMap.get(row.id)?.total_cost ?? 0)
                }}</span>
              </div>
            </div>
          </template>

          <template #cell-status="{ value }">
            <span :class="['badge', value === 'active' ? 'badge-success' : 'badge-danger']">
              {{ t('admin.providers.status.' + value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                @click="handleEdit(row)"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                type="button"
                data-testid="group-more"
                :title="t('common.more')"
                :aria-label="t('common.more')"
                aria-haspopup="menu"
                :aria-expanded="actionMenuGroup?.id === row.id"
                :aria-controls="
                  actionMenuGroup?.id === row.id ? `group-action-menu-${row.id}` : undefined
                "
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
                @click="openGroupActionMenu(row, $event)"
              >
                <Icon name="more" size="sm" />
                <span class="text-xs">{{ t('common.more') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.groups.noGroupsYet')"
              :description="t('admin.groups.createFirstGroup')"
              :action-text="t('admin.groups.createGroup')"
              @action="openCreateModal"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <GroupActionMenu
      :show="actionMenuGroup !== null"
      :group="actionMenuGroup"
      :position="actionMenuPosition"
      :duplicating="actionMenuGroup !== null && duplicatingGroupIds.has(actionMenuGroup.id)"
      @close="closeGroupActionMenu"
      @duplicate="handleDuplicate"
      @rate-multipliers="handleRateMultipliers"
      @rpm-overrides="handleRPMOverrides"
      @delete="handleDelete"
    />

    <GroupFormModal
      :show="editorModal !== null"
      :mode="editorModal?.mode ?? 'create'"
      :group="editorModal?.mode === 'edit' ? editorModal.group : null"
      :all-groups="unavailableFallbackGroups"
      @close="editorModal = null"
      @saved="handleGroupSaved"
    />

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.groups.deleteGroup')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Sort Order Modal -->
    <BaseDialog
      :show="showSortModal"
      :title="t('admin.groups.sortOrder')"
      width="normal"
      @close="closeSortModal"
    >
      <div class="space-y-4">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.groups.sortOrderHint') }}
        </p>
        <VueDraggable v-model="sortableGroups" :animation="200" class="space-y-2">
          <div
            v-for="group in sortableGroups"
            :key="group.id"
            class="flex cursor-grab items-center gap-3 rounded-surface border border-gray-200 bg-white p-3 transition-shadow hover:shadow-md active:cursor-grabbing dark:border-dark-600 dark:bg-dark-700"
          >
            <!-- The icon must not take pointer events: Chromium starts no native
                 drag when the press lands on the animated SVG, which would leave
                 this handle area unable to move the row. -->
            <div class="text-gray-400">
              <Icon name="menu" size="md" class="pointer-events-none" />
            </div>
            <div class="flex-1">
              <div class="font-medium text-gray-900 dark:text-white">
                {{ group.name }}
              </div>
              <div class="text-xs text-gray-500 dark:text-gray-400"></div>
            </div>
            <div class="text-sm text-gray-400">#{{ group.id }}</div>
          </div>
        </VueDraggable>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeSortModal">
            {{ t('common.cancel') }}
          </button>
          <button :disabled="sortSubmitting" class="btn btn-primary" @click="saveSortOrder">
            <Icon
              v-if="sortSubmitting"
              name="loader"
              size="sm"
              :animate-on-hover="false"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ sortSubmitting ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <GroupRateMultipliersModal
      :show="showRateMultipliersModal"
      :group="rateMultipliersGroup"
      @close="showRateMultipliersModal = false"
      @success="loadGroups"
    />

    <GroupRPMOverridesModal
      :show="showRPMOverridesModal"
      :group="rpmOverridesGroup"
      @close="showRPMOverridesModal = false"
      @success="loadGroups"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { VueDraggable } from 'vue-draggable-plus'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { loadProtocolCatalog } from '@/api/admin/protocolCapabilities'
import type { AdminGroup } from '@/api/groups.types'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { usePersistedTableColumns } from '@/composables/usePersistedTableColumns'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import type { FilterOption } from '@/components/common/filterPanel'
import GroupCapacityBadge from '@/components/common/GroupCapacityBadge.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import Pagination from '@/components/common/Pagination.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import type { Column } from '@/components/common/table/types'
import Icon from '@/components/icons/Icon.vue'
import GroupActionMenu from '@/components/admin/group/GroupActionMenu.vue'
import GroupRateMultipliersModal from '@/components/admin/group/GroupRateMultipliersModal.vue'
import GroupRPMOverridesModal from '@/components/admin/group/GroupRPMOverridesModal.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import { providerBrandDisplayName, resolveProviderBrand } from '@/utils/providerBrand'
import GroupFormModal from './groups/GroupFormModal.vue'

// GroupsView.vue lists administrator groups and coordinates creation, editing,
// ordering, duplication, deletion, and per-user rate and RPM override dialogs.
// Group creation and editing share GroupFormModal, while list queries cancel
// superseded reads via AbortController and optional usage or capacity summaries
// run only when their corresponding columns are visible.

const { t } = useI18n()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

// ALWAYS_VISIBLE_COLUMNS keeps identity and actions displayed, while versioned
// storage keys govern optional hidden-column preferences.
const ALWAYS_VISIBLE_COLUMNS = new Set(['name', 'actions'])
const DEFAULT_HIDDEN_COLUMNS = ['id']
const HIDDEN_COLUMNS_KEY = 'group-hidden-columns'
const COLUMN_SETTINGS_VERSION_KEY = 'group-column-settings-version'
const COLUMN_SETTINGS_VERSION = 2
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['id'],
}

// allColumns defines translated group table fields, and toggleableColumns excludes
// mandatory identity and action columns from administrator visibility choices.
const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.groups.columns.name'), sortable: true },
  { key: 'id', label: t('admin.groups.columns.id'), sortable: true },
  { key: 'display_brand', label: t('admin.groups.columns.displayBrand'), sortable: true },
  { key: 'rate_multiplier', label: t('admin.groups.columns.rateMultiplier'), sortable: true },
  { key: 'is_exclusive', label: t('admin.groups.columns.exclusive'), sortable: true },
  {
    key: 'session_isolation_enabled',
    label: t('admin.groups.columns.sessionIsolation'),
    sortable: true,
  },
  { key: 'provider_count', label: t('admin.groups.columns.providers'), sortable: true },
  { key: 'capacity', label: t('admin.groups.columns.capacity'), sortable: false },
  { key: 'usage', label: t('admin.groups.columns.usage'), sortable: false },
  { key: 'status', label: t('admin.groups.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.groups.columns.actions'), sortable: false },
])

const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => !ALWAYS_VISIBLE_COLUMNS.has(col.key)),
)
const showColumnDropdown = ref(false)
const columnDropdownRef = ref<HTMLElement | null>(null)

// getValidHiddenColumnKeys returns the set of columns the administrator may hide.
const getValidHiddenColumnKeys = () => new Set(toggleableColumns.value.map((col) => col.key))

const {
  hiddenColumns,
  loadSavedColumns,
  toggleColumn: togglePersistedColumn,
  isColumnVisible,
} = usePersistedTableColumns({
  storageKey: HIDDEN_COLUMNS_KEY,
  defaultHiddenColumns: DEFAULT_HIDDEN_COLUMNS,
  alwaysVisibleColumns: ALWAYS_VISIBLE_COLUMNS,
  getValidColumnKeys: getValidHiddenColumnKeys,
  migration: {
    versionKey: COLUMN_SETTINGS_VERSION_KEY,
    currentVersion: COLUMN_SETTINGS_VERSION,
    versionNewHiddenColumns: VERSION_NEW_HIDDEN_COLUMNS,
  },
})

const hasVisibleUsageColumn = computed(() => isColumnVisible('usage'))
const hasVisibleCapacityColumn = computed(() => isColumnVisible('capacity'))

// toggleColumn flips visibility for an optional column and loads its summary when revealed.
const toggleColumn = (key: string) => {
  const wasHidden = togglePersistedColumn(key)
  if (wasHidden === null) return

  if (wasHidden && key === 'usage') {
    loadUsageSummary()
  }
  if (wasHidden && key === 'capacity') {
    loadCapacitySummary()
  }
}

const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (col) => ALWAYS_VISIBLE_COLUMNS.has(col.key) || !hiddenColumns.has(col.key),
  ),
)

if (typeof window !== 'undefined') {
  loadSavedColumns()
}

// statusOptions supplies translated activation choices for filtering group rows.
const statusOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.groups.allStatus') },
  { value: 'active', label: t('admin.providers.status.active'), dot: 'green' },
  { value: 'inactive', label: t('admin.providers.status.inactive'), dot: 'red' },
])

// GroupEditorState identifies whether GroupFormModal is closed, creating, or editing a group.
type GroupEditorState = { mode: 'create' } | { mode: 'edit'; group: AdminGroup }

// Page list, summary, filter, and dialog state.
const groups = ref<AdminGroup[]>([])
const unavailableFallbackGroups = ref<AdminGroup[]>([])
const loading = ref(true)
const usageMap = ref<
  Map<
    number,
    {
      today_cost: number // Current day billed amount.
      yesterday_cost: number // Previous day billed amount.
      total_cost: number // Cumulative billed amount.
    }
  >
>(new Map())
const usageLoading = ref(false)
const usageLoaded = ref(false)
let usageRequest = 0
const capacityMap = ref<
  Map<
    number,
    {
      concurrencyUsed: number // Occupied concurrent request slots.
      concurrencyMax: number // Maximum concurrent request slots.
      sessionsUsed: number // Occupied session slots.
      sessionsMax: number // Maximum session slots.
      rpmUsed: number // Current requests per minute.
      rpmMax: number // Maximum requests per minute.
    }
  >
>(new Map())
const searchQuery = ref('')
const filters = reactive({
  status: '',
})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0,
})
const sortState = reactive({
  sort_by: 'sort_order',
  sort_order: 'asc' as 'asc' | 'desc',
})

let abortController: AbortController | null = null

const editorModal = ref<GroupEditorState | null>(null)
const showDeleteDialog = ref(false)
const deletingGroup = ref<AdminGroup | null>(null)
const duplicatingGroupIds = reactive(new Set<number>())
const showSortModal = ref(false)
const sortSubmitting = ref(false)
const sortableGroups = ref<AdminGroup[]>([])
const showRateMultipliersModal = ref(false)
const rateMultipliersGroup = ref<AdminGroup | null>(null)
const showRPMOverridesModal = ref(false)
const rpmOverridesGroup = ref<AdminGroup | null>(null)
const actionMenuGroup = ref<AdminGroup | null>(null)
const actionMenuPosition = ref<{
  top: number // Viewport top pixels.
  left: number // Viewport left pixels.
} | null>(null)

const activeFilterCount = computed(() => [filters.status].filter(Boolean).length)

// resetGroupFilters clears the status filter and reloads the current group page.
const resetGroupFilters = () => {
  filters.status = ''
  loadGroups()
}

const deleteConfirmMessage = computed(() =>
  deletingGroup.value ? t('admin.groups.deleteConfirm', { name: deletingGroup.value.name }) : '',
)

// openGroupActionMenu positions actions for the clicked group relative to its trigger.
const openGroupActionMenu = (group: AdminGroup, event: MouseEvent) => {
  if (actionMenuGroup.value?.id === group.id) {
    closeGroupActionMenu()
    return
  }
  const target = event.currentTarget as HTMLElement | null
  if (!target) return
  const rect = target.getBoundingClientRect()
  const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, {
    maxWidth: 192,
    fixedHeight: 162,
    viewportPadding: 8,
    gap: 4,
    pinLeftOnMobile: false,
  })
  actionMenuPosition.value = { top: position.top ?? 8, left: position.left }
  actionMenuGroup.value = group
}

// closeGroupActionMenu clears the selected group and its floating menu coordinates.
const closeGroupActionMenu = () => {
  actionMenuGroup.value = null
  actionMenuPosition.value = null
}

// loadLiveCapability probes Live capability once on mount with conservative failure fallback.
let liveCapabilityLoaded = false
const loadLiveCapability = async () => {
  if (liveCapabilityLoaded) return
  liveCapabilityLoaded = true
  await adminAPI.groups.getLiveCapability().catch(() => ({ supported: false }))
}

// loadGroups fetches the selected group page and cancels any superseded list request.
const loadGroups = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  const { signal } = currentController
  loading.value = true
  try {
    const response = await adminAPI.groups.list(
      pagination.page,
      pagination.page_size,
      {
        status: filters.status as 'active' | 'inactive' | undefined,
        search: searchQuery.value.trim() || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      { signal },
    )
    if (signal.aborted) return
    groups.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
    if (hasVisibleUsageColumn.value) {
      loadUsageSummary()
    } else {
      usageLoading.value = false
    }
    if (hasVisibleCapacityColumn.value) {
      loadCapacitySummary()
    }
  } catch (error: any) {
    if (signal.aborted || error?.name === 'AbortError' || error?.code === 'ERR_CANCELED') {
      return
    }
    appStore.showError(t('admin.groups.failedToLoad'))
    console.error('Error loading groups:', error)
  } finally {
    if (abortController === currentController && !signal.aborted) {
      loading.value = false
    }
  }
}

// loadUnavailableFallbackGroups loads the complete unpaginated group list for form selectors.
const loadUnavailableFallbackGroups = async () => {
  try {
    unavailableFallbackGroups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Error loading unavailable fallback groups:', error)
  }
}

const formatGroupBalance = (cost: number | null | undefined): string =>
  formatBalanceAmount(cost, { fractionDigits: 2 })

const displayBrandLabel = (value: unknown): string => providerBrandDisplayName(String(value || ''))

const displayBrandBadgeClass = (value: unknown): string => {
  const base = 'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium'
  return `${base} ${resolveProviderBrand(String(value || '')).badgeClass}`
}

// loadUsageSummary fetches group cost summaries when the usage column is visible.
const loadUsageSummary = async () => {
  if (!hasVisibleUsageColumn.value) {
    usageLoading.value = false
    return
  }
  const request = ++usageRequest
  usageLoading.value = true
  try {
    const data = await adminAPI.groups.getUsageSummary()
    if (request !== usageRequest) return
    const map = new Map<
      number,
      { today_cost: number; yesterday_cost: number; total_cost: number }
    >()
    for (const item of data) {
      map.set(item.group_id, {
        today_cost: item.today_cost,
        yesterday_cost: item.yesterday_cost,
        total_cost: item.total_cost,
      })
    }
    usageMap.value = map
    usageLoaded.value = true
  } catch (error) {
    if (request === usageRequest) console.error('Error loading group usage summary:', error)
  } finally {
    if (request === usageRequest) usageLoading.value = false
  }
}

// loadCapacitySummary fetches group concurrency, session, and RPM capacity summaries.
const loadCapacitySummary = async () => {
  if (!hasVisibleCapacityColumn.value) return
  try {
    const data = await adminAPI.groups.getCapacitySummary()
    const map = new Map<
      number,
      {
        concurrencyUsed: number
        concurrencyMax: number
        sessionsUsed: number
        sessionsMax: number
        rpmUsed: number
        rpmMax: number
      }
    >()
    for (const item of data) {
      map.set(item.group_id, {
        concurrencyUsed: item.concurrency_used,
        concurrencyMax: item.concurrency_max,
        sessionsUsed: item.sessions_used,
        sessionsMax: item.sessions_max,
        rpmUsed: item.rpm_used,
        rpmMax: item.rpm_max,
      })
    }
    capacityMap.value = map
  } catch (error) {
    console.error('Error loading group capacity summary:', error)
  }
}

let searchTimeout: ReturnType<typeof setTimeout>
// handleSearch debounces search input and resets pagination to page one.
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadGroups()
  }, 300)
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadGroups()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadGroups()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadGroups()
}

// openCreateModal opens GroupFormModal in create mode.
const openCreateModal = () => {
  editorModal.value = { mode: 'create' }
}

// handleEdit opens GroupFormModal in edit mode immediately for the selected group.
const handleEdit = (group: AdminGroup) => {
  editorModal.value = { mode: 'edit', group }
}

// handleGroupSaved reloads table rows and fallback group options after create or update.
const handleGroupSaved = () => {
  loadGroups()
  loadUnavailableFallbackGroups()
}

const handleRateMultipliers = (group: AdminGroup) => {
  rateMultipliersGroup.value = group
  showRateMultipliersModal.value = true
}

const handleRPMOverrides = (group: AdminGroup) => {
  rpmOverridesGroup.value = group
  showRPMOverridesModal.value = true
}

// handleDuplicate duplicates one group while blocking concurrent duplicates for the same ID.
const handleDuplicate = async (group: AdminGroup) => {
  if (duplicatingGroupIds.has(group.id)) return
  duplicatingGroupIds.add(group.id)
  try {
    const duplicate = await adminAPI.groups.duplicate(group.id)
    appStore.showSuccess(t('admin.groups.duplicateSuccess', { name: duplicate.name }))
    await loadGroups()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.duplicateFailed')))
  } finally {
    duplicatingGroupIds.delete(group.id)
  }
}

const handleDelete = (group: AdminGroup) => {
  deletingGroup.value = group
  showDeleteDialog.value = true
}

// confirmDelete deletes the selected group and refreshes rows and group options.
const confirmDelete = async () => {
  if (!deletingGroup.value) return
  try {
    await adminAPI.groups.delete(deletingGroup.value.id)
    appStore.showSuccess(t('admin.groups.groupDeleted'))
    showDeleteDialog.value = false
    deletingGroup.value = null
    loadGroups()
    loadUnavailableFallbackGroups()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.groups.failedToDelete'))
    console.error('Error deleting group:', error)
  }
}

// handleClickOutside closes the column settings menu when clicking outside its container.
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
}

// openSortModal loads all groups ordered by sort_order and opens the sort dialog.
const openSortModal = async () => {
  try {
    const allGroups = await adminAPI.groups.getAll()
    sortableGroups.value = [...allGroups].sort((a, b) => a.sort_order - b.sort_order)
    showSortModal.value = true
  } catch (error) {
    appStore.showError(t('admin.groups.failedToLoad'))
    console.error('Error loading groups for sorting:', error)
  }
}

const closeSortModal = () => {
  showSortModal.value = false
  sortableGroups.value = []
}

// saveSortOrder persists spaced sort_order positions and refreshes group lists.
const saveSortOrder = async () => {
  sortSubmitting.value = true
  try {
    const updates = sortableGroups.value.map((g, index) => ({
      id: g.id,
      // Space persisted positions by ten so intermediate insertions avoid full renumbering.
      sort_order: index * 10,
    }))
    await adminAPI.groups.updateSortOrder(updates)
    appStore.showSuccess(t('admin.groups.sortOrderUpdated'))
    closeSortModal()
    loadGroups()
    loadUnavailableFallbackGroups()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.groups.failedToUpdateSortOrder'))
    console.error('Error updating sort order:', error)
  } finally {
    sortSubmitting.value = false
  }
}

onMounted(async () => {
  try {
    await loadProtocolCatalog()
  } catch {
    appStore.showError(t('admin.protocols.loadError'))
  }
  loadGroups()
  loadUnavailableFallbackGroups()
  void loadLiveCapability()
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
  document.removeEventListener('click', handleClickOutside)
})
</script>

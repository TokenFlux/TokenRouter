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
              @click="loadGroups"
              :disabled="loading"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <div class="relative" ref="columnDropdownRef">
              <button
                @click="showColumnDropdown = !showColumnDropdown"
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('admin.groups.columnSettings')"
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
                    @click="toggleColumn(col.key)"
                    class="dropdown-item justify-between"
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
              @click="openSortModal"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('admin.groups.sortOrder')"
            >
              <Icon name="arrowsUpDown" size="sm" :animate-on-hover="false" />
            </button>
            <button
              @click="openCreateModal"
              class="btn btn-primary whitespace-nowrap"
              data-tour="groups-create-btn"
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
            <div v-if="usageLoading" class="text-xs text-gray-400">—</div>
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
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
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
                @click="openGroupActionMenu(row, $event)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
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

    <!-- Create Group Modal -->
    <BaseDialog
      :show="showCreateModal"
      :body-scroll="false"
      :close-on-escape="!showAdvancedSchedulerOverridesModal"
      :title="t('admin.groups.createGroup')"
      width="wide"
      @close="closeCreateModal"
    >
      <form
        id="create-group-form"
        @submit.prevent="handleCreateGroup"
        novalidate
        class="flex min-h-0 flex-1 flex-col"
      >
        <GroupSettingsForm
          ref="createSettingsRef"
          mode="create"
          :model-value="createForm"
          :options="{
            copyProviders: copyProvidersGroupOptions,
            unavailableFallback: unavailableFallbackGroupOptions,
            invalidRequestFallback: invalidRequestFallbackOptions,
            clientFallback: fallbackGroupOptions,
            probeModels: createAvailabilityProbeModelOptions,
          }"
          :routing-rules="createModelRoutingRules"
          :models-list="createModelsListState"
          :models-list-loading="createModelsListLoading"
          :provider-search="{
            keywords: providerSearchKeyword,
            results: providerSearchResults,
            open: showProviderDropdown,
          }"
          :get-rule-key="getCreateRuleSearchKey"
          @patch="Object.assign(createForm, $event)"
          @configure-scheduler="openAdvancedSchedulerOverrides('create')"
          @add-rule="addCreateRoutingRule"
          @remove-rule="removeCreateRoutingRule"
          @rule-pattern="(rule, value) => (rule.pattern = value)"
          @search-providers="(rule, keyword) => updateProviderSearch(rule, keyword)"
          @focus-providers="(rule) => onProviderSearchFocus(rule)"
          @select-provider="(rule, provider) => selectProvider(rule, provider)"
          @remove-provider="(rule, id) => removeSelectedProvider(rule, id)"
          @models-enabled="createModelsListState.enabled = $event"
          @select-model="(id, value) => setModelSelection(createModelsListState, id, value)"
          @select-all-models="selectAllModelsListItems(createModelsListState)"
          @invert-models="invertModelsListSelection(createModelsListState)"
          @move-model="(from, to) => moveModelsListItem(createModelsListState, from, to)"
        />
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeCreateModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="create-group-form"
            :disabled="submitting || !protocolCatalog"
            class="btn btn-primary"
            data-tour="group-form-submit"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ submitting ? t('admin.groups.creating') : t('common.create') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Group Modal -->
    <BaseDialog
      :show="showEditModal"
      :body-scroll="false"
      :close-on-escape="!showAdvancedSchedulerOverridesModal"
      :title="t('admin.groups.editGroup')"
      width="wide"
      @close="closeEditModal"
    >
      <form
        v-if="editingGroup"
        id="edit-group-form"
        @submit.prevent="handleUpdateGroup"
        novalidate
        class="flex min-h-0 flex-1 flex-col"
      >
        <GroupSettingsForm
          ref="editSettingsRef"
          mode="edit"
          :model-value="editForm"
          :options="{
            copyProviders: copyProvidersGroupOptionsForEdit,
            unavailableFallback: unavailableFallbackGroupOptionsForEdit,
            invalidRequestFallback: invalidRequestFallbackOptionsForEdit,
            clientFallback: fallbackGroupOptionsForEdit,
            probeModels: editAvailabilityProbeModelOptions,
          }"
          :routing-rules="editModelRoutingRules"
          :models-list="editModelsListState"
          :models-list-loading="editModelsListLoading"
          :provider-search="{
            keywords: providerSearchKeyword,
            results: providerSearchResults,
            open: showProviderDropdown,
          }"
          :get-rule-key="getEditRuleSearchKey"
          @patch="Object.assign(editForm, $event)"
          @configure-scheduler="openAdvancedSchedulerOverrides('edit')"
          @add-rule="addEditRoutingRule"
          @remove-rule="removeEditRoutingRule"
          @rule-pattern="(rule, value) => (rule.pattern = value)"
          @search-providers="(rule, keyword) => updateProviderSearch(rule, keyword, true)"
          @focus-providers="(rule) => onProviderSearchFocus(rule, true)"
          @select-provider="(rule, provider) => selectProvider(rule, provider, true)"
          @remove-provider="(rule, id) => removeSelectedProvider(rule, id, true)"
          @models-enabled="editModelsListState.enabled = $event"
          @select-model="(id, value) => setModelSelection(editModelsListState, id, value)"
          @select-all-models="selectAllModelsListItems(editModelsListState)"
          @invert-models="invertModelsListSelection(editModelsListState)"
          @move-model="(from, to) => moveModelsListItem(editModelsListState, from, to)"
        />
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeEditModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="edit-group-form"
            :disabled="submitting || !protocolCatalog"
            class="btn btn-primary"
            data-tour="group-form-submit"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ submitting ? t('admin.groups.updating') : t('common.update') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
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

    <ConfirmDialog
      :show="showUnsupportedLiveConfirm"
      :title="t('admin.groups.openaiLive.unsupportedTitle')"
      :message="t('admin.groups.openaiLive.unsupportedMessage')"
      :confirm-text="t('admin.groups.openaiLive.enableAnyway')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmUnsupportedLive"
      @cancel="cancelUnsupportedLive"
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
          <button @click="closeSortModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button @click="saveSortOrder" :disabled="sortSubmitting" class="btn btn-primary">
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="sortSubmitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ sortSubmitting ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Group Rate Multipliers Modal -->
    <GroupRateMultipliersModal
      :show="showRateMultipliersModal"
      :group="rateMultipliersGroup"
      @close="showRateMultipliersModal = false"
      @success="loadGroups"
    />

    <!-- Group RPM Overrides Modal -->
    <GroupRPMOverridesModal
      :show="showRPMOverridesModal"
      :group="rpmOverridesGroup"
      @close="showRPMOverridesModal = false"
      @success="loadGroups"
    />

    <GroupAdvancedSchedulerOverridesModal
      :show="showAdvancedSchedulerOverridesModal"
      :model-value="advancedSchedulerOverridesDraft"
      @close="closeAdvancedSchedulerOverrides"
      @save="saveAdvancedSchedulerOverrides"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import type { LocalizedUpdate } from '@/i18n/content'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useOnboardingStore } from '@/stores/onboarding'
import { adminAPI } from '@/api/admin'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import type {
  AdminGroup,
  GroupAvailabilityProbeConfig,
  GroupSchedulerType,
  GroupAdvancedSchedulerOverrides,
} from '@/api/groups.types'
import type { ProtocolID } from '@/api/protocols.types'
import type { Column } from '@/components/common/table/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import type { FilterOption } from '@/components/common/filterPanel'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import GroupRateMultipliersModal from '@/components/admin/group/GroupRateMultipliersModal.vue'
import GroupActionMenu from '@/components/admin/group/GroupActionMenu.vue'
import GroupRPMOverridesModal from '@/components/admin/group/GroupRPMOverridesModal.vue'
import GroupCapacityBadge from '@/components/common/GroupCapacityBadge.vue'
import { loadProtocolCatalog, protocolCatalog } from '@/api/admin/protocolCapabilities'
import GroupAdvancedSchedulerOverridesModal from '@/components/admin/group/GroupAdvancedSchedulerOverridesModal.vue'
import { defaultRoutingPolicy, cloneRoutingPolicy } from '@/components/admin/group/routingPolicy'
import GroupSettingsForm from '@/components/admin/group/GroupSettingsForm.vue'
import type {
  GroupModelRoutingRule as ModelRoutingRule,
  GroupRoutingProvider as SimpleProvider,
} from '@/components/admin/group/groupSettingsTypes'
import { VueDraggable } from 'vue-draggable-plus'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import { providerBrandDisplayName, resolveProviderBrand } from '@/utils/providerBrand'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  effectiveGroupClientProtocols,
  sanitizeGroupProtocolFallbacks,
} from '@/utils/groupClientProtocols'
import { useKeyedDebouncedSearch } from '@/composables/useKeyedDebouncedSearch'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import {
  buildModelsListConfig,
  createModelsListState as createInitialModelsListState,
  getAvailabilityProbeCandidateModels,
  invertModelsListSelection,
  moveModelsListItem,
  selectAllModelsListItems,
  setModelsListCandidates,
} from '../../components/admin/group/groupsModelsList'
import { createModelsListCandidatesTracker } from '../../components/admin/group/groupsModelsListCandidates'
import { normalizeGroupOpenAIFastPolicy } from '../../components/admin/group/groupsOpenAIFast'
import {
  normalizeReasoningEffortForPlatform,
  normalizeReasoningEffortOverLimit,
  reasoningEffortMappingsToAPI,
  reasoningEffortMappingsToRows,
  reasoningEffortOverLimitDowngrade,
  type ReasoningEffortMappingRow,
} from '../../components/admin/group/groupsReasoningEffort'

// GroupsView.vue lists administrator groups and manages their creation, editing, ordering, and
// per-user overrides. Separate create and edit drafts share the settings form, while page-owned
// routing rules keep provider identities and candidate model selections. Payload construction
// translates clearable group references and nested probe fields into API values.
//
// List requests cancel older reads, model-candidate queries track their latest request per
// form, and provider searches use stable rule keys to keep asynchronous results attached to the
// correct object. Column preferences are stored with a version so newly hidden defaults can be
// added once. Optional usage and capacity summaries load when their columns are visible.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()
// appStore displays operation notifications after requests complete or validation prevents the
// requested action.
const appStore = useAppStore()
// onboardingStore advances the matching guided creation step after a group is saved
// successfully through this page.
const onboardingStore = useOnboardingStore()
// formatBalanceAmount applies the configured balance display mode to the usage values shown in
// the group table.
const { formatBalanceAmount } = useBalanceDisplay()

// ALWAYS_VISIBLE_COLUMNS keeps identity and actions displayed, while default-hidden keys and
// version metadata govern optional column preferences.
const ALWAYS_VISIBLE_COLUMNS = new Set(['name', 'actions'])
// Default-hidden columns apply to first-time preferences and versioned migrations that add new
// hidden defaults.
const DEFAULT_HIDDEN_COLUMNS = ['id']
const HIDDEN_COLUMNS_KEY = 'group-hidden-columns'
// Increment the preference version when introducing a new default-hidden column so existing
// administrators receive that migration once.
const COLUMN_SETTINGS_VERSION_KEY = 'group-column-settings-version'
const COLUMN_SETTINGS_VERSION = 2
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['id'],
}

// allColumns defines translated group table fields, and toggleableColumns excludes mandatory
// identity and action columns from administrator visibility choices.
const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.groups.columns.name'), sortable: true },
  { key: 'id', label: t('admin.groups.columns.id'), sortable: true },
  {
    key: 'display_brand',
    label: t('admin.groups.columns.displayBrand'),
    sortable: true,
  },
  {
    key: 'rate_multiplier',
    label: t('admin.groups.columns.rateMultiplier'),
    sortable: true,
  },
  {
    key: 'is_exclusive',
    label: t('admin.groups.columns.exclusive'),
    sortable: true,
  },
  {
    key: 'session_isolation_enabled',
    label: t('admin.groups.columns.sessionIsolation'),
    sortable: true,
  },
  {
    key: 'provider_count',
    label: t('admin.groups.columns.providers'),
    sortable: true,
  },
  {
    key: 'capacity',
    label: t('admin.groups.columns.capacity'),
    sortable: false,
  },
  { key: 'usage', label: t('admin.groups.columns.usage'), sortable: false },
  { key: 'status', label: t('admin.groups.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.groups.columns.actions'), sortable: false },
])

const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => !ALWAYS_VISIBLE_COLUMNS.has(col.key)),
)
// hiddenColumns holds restored preferences, while the dropdown flag and element reference
// support the column-settings menu and outside-click handling.
const hiddenColumns = reactive<Set<string>>(new Set())
const showColumnDropdown = ref(false)
const columnDropdownRef = ref<HTMLElement | null>(null)

// getValidHiddenColumnKeys returns the set of columns the administrator may hide.
const getValidHiddenColumnKeys = () => new Set(toggleableColumns.value.map((col) => col.key))

// activeFilterCount counts the active status constraint for the filter button, independently
// from the free-text group search.
const activeFilterCount = computed(() => [filters.status].filter(Boolean).length)

// resetGroupFilters clears the status filter and reloads the current group page.
const resetGroupFilters = () => {
  filters.status = ''
  loadGroups()
}

// loadSavedColumns restores valid hidden columns and applies pending preference migrations.
// Saved keys are restricted to currently toggleable columns, while missing preferences begin
// with default hidden columns. Invalid or absent version numbers are treated as the first
// preference version.
//
// Each intervening version contributes newly hidden defaults before the updated keys and
// version are persisted. Storage or parsing failures add the default hidden columns to the
// in-memory selection and log the failure, allowing the group table to render.
const loadSavedColumns = () => {
  hiddenColumns.clear()
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    const validKeys = getValidHiddenColumnKeys()

    if (saved) {
      const parsed = JSON.parse(saved)
      if (Array.isArray(parsed)) {
        parsed
          .filter((key): key is string => typeof key === 'string' && validKeys.has(key))
          .forEach((key) => hiddenColumns.add(key))
      }

      // Apply each pending preference version before storing the current version so new
      // default-hidden columns are added once to existing choices.
      const parsedVersion = Number(localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? '1')
      const storedVersion =
        Number.isSafeInteger(parsedVersion) && parsedVersion >= 1 ? parsedVersion : 1
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        let mutated = false
        for (let version = storedVersion + 1; version <= COLUMN_SETTINGS_VERSION; version++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[version] ?? []) {
            if (validKeys.has(key) && !hiddenColumns.has(key)) {
              hiddenColumns.add(key)
              mutated = true
            }
          }
        }
        if (mutated) {
          saveColumnsToStorage()
        } else {
          localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
        }
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => {
        if (validKeys.has(key)) hiddenColumns.add(key)
      })
      saveColumnsToStorage()
    }
  } catch (error) {
    console.error('Failed to load group column settings:', error)
    // A storage or parse failure adds default hidden columns to any recovered state so the
    // table retains its baseline visibility when saved preferences cannot be fully loaded.
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

// saveColumnsToStorage persists the current hidden-column selection and then its preference
// version. Callers update hiddenColumns before saving; unsupported keys are filtered from the
// stored list while the in-memory set remains available to the page. The version write follows
// the key write so a completed marker refers to this attempt's saved selection. These separate
// browser writes can partially succeed: a version-write failure leaves the saved keys in place,
// logs the error, and performs no rollback.
const saveColumnsToStorage = () => {
  try {
    const validKeys = getValidHiddenColumnKeys()
    const keys = [...hiddenColumns].filter((key) => validKeys.has(key))
    // Write the hidden-key list before its version marker so a completed version update
    // refers to the preference values saved by this attempt.
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify(keys))
    localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
  } catch (error) {
    console.error('Failed to save group column settings:', error)
  }
}

// isColumnVisible reports whether the column is absent from the hidden-key set.
const isColumnVisible = (key: string) => !hiddenColumns.has(key)
// hasVisibleUsageColumn and hasVisibleCapacityColumn gate summary requests so hidden optional
// columns do not trigger their API reads.
const hasVisibleUsageColumn = computed(() => isColumnVisible('usage'))
const hasVisibleCapacityColumn = computed(() => isColumnVisible('capacity'))

// toggleColumn changes visibility for a recognized optional column and persists the selection.
// Revealing usage or capacity also starts its summary query, while mandatory or unknown column
// keys are ignored.
const toggleColumn = (key: string) => {
  const validKeys = getValidHiddenColumnKeys()
  if (!validKeys.has(key)) return

  const wasHidden = hiddenColumns.has(key)
  if (wasHidden) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()

  if (wasHidden && key === 'usage') {
    loadUsageSummary()
  }
  if (wasHidden && key === 'capacity') {
    loadCapacitySummary()
  }
}

// columns defines the displayed table fields and their translated headings, ordering, and
// sorting controls.
const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (col) => ALWAYS_VISIBLE_COLUMNS.has(col.key) || !hiddenColumns.has(col.key),
  ),
)

if (typeof window !== 'undefined') {
  loadSavedColumns()
}

// statusOptions supplies translated activation choices for filtering records, including the
// empty choice that clears filtering; dot colors match the status column badges.
const statusOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.groups.allStatus') },
  { value: 'active', label: t('admin.providers.status.active'), dot: 'green' },
  { value: 'inactive', label: t('admin.providers.status.inactive'), dot: 'red' },
])

// cloneAdvancedSchedulerOverrides copies the optional scheduler override record for editing.
// Missing input produces an empty object so the modal can represent complete inheritance with
// its own draft object.
const cloneAdvancedSchedulerOverrides = (
  value?: GroupAdvancedSchedulerOverrides,
): GroupAdvancedSchedulerOverrides => ({ ...(value || {}) })

// openAdvancedSchedulerOverrides copies the selected form settings into the scheduler modal
// draft.
const openAdvancedSchedulerOverrides = (target: 'create' | 'edit') => {
  advancedSchedulerOverridesTarget.value = target
  advancedSchedulerOverridesDraft.value = cloneAdvancedSchedulerOverrides(
    target === 'create'
      ? createForm.advanced_scheduler_overrides
      : editForm.advanced_scheduler_overrides,
  )
  showAdvancedSchedulerOverridesModal.value = true
}

// closeAdvancedSchedulerOverrides hides the modal and clears its target form selection.
const closeAdvancedSchedulerOverrides = () => {
  showAdvancedSchedulerOverridesModal.value = false
  advancedSchedulerOverridesTarget.value = null
}

// saveAdvancedSchedulerOverrides copies modal values into its selected form and closes it.
const saveAdvancedSchedulerOverrides = (value: GroupAdvancedSchedulerOverrides) => {
  if (advancedSchedulerOverridesTarget.value === 'create') {
    createForm.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(value)
  } else if (advancedSchedulerOverridesTarget.value === 'edit') {
    editForm.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(value)
  }
  closeAdvancedSchedulerOverrides()
}

// Creation offers active client fallback groups that do not themselves require the restricted
// Claude Code client.
const fallbackGroupOptions = computed(() => {
  const options: {
    value: number | null // stored selector value
    label: string // visible option label
  }[] = [{ value: null, label: t('admin.groups.claudeCode.noFallback') }]
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) => !g.claude_code_only && g.status === 'active',
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// Editing uses the same client fallback eligibility and excludes the group currently being
// edited from its replacement choices.
const fallbackGroupOptionsForEdit = computed(() => {
  const options: {
    value: number | null // stored selector value
    label: string // visible option label
  }[] = [{ value: null, label: t('admin.groups.claudeCode.noFallback') }]
  const currentId = editingGroup.value?.id
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) => !g.claude_code_only && g.status === 'active' && g.id !== currentId,
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// Creation offers active groups as unavailable-group replacements, plus an empty choice that
// clears the configured replacement.
const unavailableFallbackGroupOptions = computed(() => {
  const options: {
    value: number | null // stored selector value
    label: string // visible option label
  }[] = [{ value: null, label: t('admin.groups.unavailableFallback.noFallback') }]
  const eligibleGroups = unavailableFallbackGroups.value.filter((g) => g.status === 'active')
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// Editing excludes the current group from active unavailable-group replacements so the selector
// cannot point the group back to itself.
const unavailableFallbackGroupOptionsForEdit = computed(() => {
  const options: {
    value: number | null // stored selector value
    label: string // visible option label
  }[] = [{ value: null, label: t('admin.groups.unavailableFallback.noFallback') }]
  const currentId = editingGroup.value?.id
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) => g.status === 'active' && g.id !== currentId,
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// Invalid-request recovery offers active groups with no existing invalid-request replacement,
// keeping the selected recovery path one level deep.
const invalidRequestFallbackOptions = computed(() => {
  const options: {
    value: number | null // stored selector value
    label: string // visible option label
  }[] = [{ value: null, label: t('admin.groups.invalidRequestFallback.noFallback') }]
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) => g.status === 'active' && g.fallback_group_id_on_invalid_request === null,
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// Editing applies invalid-request recovery eligibility and excludes the current group from its
// own possible replacement targets.
const invalidRequestFallbackOptionsForEdit = computed(() => {
  const options: {
    value: number | null // stored selector value
    label: string // visible option label
  }[] = [{ value: null, label: t('admin.groups.invalidRequestFallback.noFallback') }]
  const currentId = editingGroup.value?.id
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) =>
      g.status === 'active' &&
      g.fallback_group_id_on_invalid_request === null &&
      g.id !== currentId,
  )
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name })
  })
  return options
})

// Provider copying offers source groups whose provider count is positive, with labels that
// include the current count.
const copyProvidersGroupOptions = computed(() => {
  const eligibleGroups = unavailableFallbackGroups.value.filter((g) => (g.provider_count || 0) > 0)
  return eligibleGroups.map((g) => ({
    value: g.id,
    label: t('admin.groups.settings.groupProviders', {
      name: g.name,
      count: g.provider_count || 0,
    }),
  }))
})

// Editing provider-copy choices excludes the current group and includes other groups with a
// positive provider count.
const copyProvidersGroupOptionsForEdit = computed(() => {
  const currentId = editingGroup.value?.id
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) => (g.provider_count || 0) > 0 && g.id !== currentId,
  )
  return eligibleGroups.map((g) => ({
    value: g.id,
    label: t('admin.groups.settings.groupProviders', {
      name: g.name,
      count: g.provider_count || 0,
    }),
  }))
})

// groups holds the current server page, while complete group options are loaded separately for
// cross-page replacement and provider-copy choices.
const groups = ref<AdminGroup[]>([])
// This complete group source is loaded outside table pagination; individual replacement
// selectors filter its entries by their own eligibility rules.
const unavailableFallbackGroups = ref<AdminGroup[]>([])
// loading tracks the current list request so the table can present pending data to
// administrators.
const loading = ref(false)
// usageMap and usageLoading supply group cost summaries, while capacityMap holds request,
// session, and RPM measurements indexed by group ID.
const usageMap = ref<
  Map<
    number,
    {
      today_cost: number // current day billed amount
      yesterday_cost: number // previous day billed amount
      total_cost: number // total billed amount
    }
  >
>(new Map())
const usageLoading = ref(false)
const capacityMap = ref<
  Map<
    number,
    {
      concurrencyUsed: number // occupied concurrent request slots
      concurrencyMax: number // maximum concurrent request slots
      sessionsUsed: number // occupied session slots
      sessionsMax: number // maximum session slots
      rpmUsed: number // current requests per minute
      rpmMax: number // maximum requests per minute
    }
  >
>(new Map())
// searchQuery stores the editable query text used when the page requests its next filtered
// list.
const searchQuery = ref('')
// filters stores the selected list constraints, while pagination and sortState supply the
// requested page and ordering.
const filters = reactive({
  status: '',
})
// pagination stores the current page, page size, and server totals used by the table controls.
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0,
})
// sortState stores the selected server ordering so filter and page changes reuse the same sort.
const sortState = reactive({
  sort_by: 'sort_order',
  sort_order: 'asc' as 'asc' | 'desc',
})

// abortController owns the active list read and lets the next query cancel work for the
// previous selection.
let abortController: AbortController | null = null

// showCreateModal and adjacent dialog flags control create, edit, delete, Live confirmation,
// and ordering workflows independently of their draft values.
const showCreateModal = ref(false)
const showEditModal = ref(false)
// showDeleteDialog controls deletion confirmation, while the selected record identifies the
// target used after confirmation.
const showDeleteDialog = ref(false)
// pendingLiveForm identifies the draft awaiting a Live confirmation; liveCapability and its
// shared pending promise supply the capability result.
const pendingLiveForm = ref<'create' | 'edit' | null>(null)
const showUnsupportedLiveConfirm = computed(() => pendingLiveForm.value !== null)
const liveCapability = ref<{
  supported: boolean // Live support flag
  reason?: string // support limitation explanation
} | null>(null)
let liveCapabilityRequest: Promise<{
  supported: boolean // Live support flag
  reason?: string // support limitation explanation
}> | null = null
const showSortModal = ref(false)
// submitting records an active form persistence attempt so submission controls can display
// their busy state.
const submitting = ref(false)
const sortSubmitting = ref(false)
// editingGroup and deletingGroup retain the saved operation targets, while duplicatingGroupIds
// blocks repeated duplication for each pending group ID.
const editingGroup = ref<AdminGroup | null>(null)
const deletingGroup = ref<AdminGroup | null>(null)
const duplicatingGroupIds = reactive(new Set<number>())
// actionMenuGroup and actionMenuPosition identify the row and viewport coordinates used by the
// body-positioned action menu.
const actionMenuGroup = ref<AdminGroup | null>(null)
const actionMenuPosition = ref<{
  top: number // viewport top pixels
  left: number // viewport left pixels
} | null>(null)

// openGroupActionMenu positions actions for the clicked group relative to its trigger. Clicking
// the currently open group's trigger closes the menu, and a missing element skips positioning.
// The floating-panel helper fits the fixed-height menu within the viewport.
const openGroupActionMenu = (group: AdminGroup, event: MouseEvent) => {
  if (actionMenuGroup.value?.id === group.id) {
    closeGroupActionMenu()
    return
  }
  const target = event.currentTarget as HTMLElement | null
  if (!target) return
  const rect = target.getBoundingClientRect()
  // The fixed-height menu flips above a trigger near the bottom and keeps its right edge
  // aligned on narrow screens.
  const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, {
    maxWidth: 192,
    fixedHeight: 162,
    viewportPadding: 8,
    gap: 4,
    pinLeftOnMobile: false,
  })
  // The fixed-height positioning helper supplies a top coordinate for this menu; the default
  // also keeps its stored position numeric.
  actionMenuPosition.value = { top: position.top ?? 8, left: position.left }
  actionMenuGroup.value = group
}

// closeGroupActionMenu clears the selected group and its floating menu coordinates.
const closeGroupActionMenu = () => {
  actionMenuGroup.value = null
  actionMenuPosition.value = null
}
// The override dialog flags and selected groups open per-user rate and RPM editors
// independently from the main group settings forms.
const showRateMultipliersModal = ref(false)
const rateMultipliersGroup = ref<AdminGroup | null>(null)
const showRPMOverridesModal = ref(false)
const rpmOverridesGroup = ref<AdminGroup | null>(null)
// The scheduler modal flag, target, and draft isolate its unsaved inputs until the selected
// create or edit form accepts them.
const showAdvancedSchedulerOverridesModal = ref(false)
const advancedSchedulerOverridesTarget = ref<'create' | 'edit' | null>(null)
const advancedSchedulerOverridesDraft = ref<GroupAdvancedSchedulerOverrides>({})
// sortableGroups contains the complete ordering draft loaded for the sorting dialog,
// independently of the paginated group table.
const sortableGroups = ref<AdminGroup[]>([])
// Create and edit model-list states retain separate saved selections, current candidates,
// loading flags, and settings-form references for each editor.
const createModelsListState = reactive(createInitialModelsListState())
const editModelsListState = reactive(createInitialModelsListState())
const createModelsListLoading = ref(false)
const editModelsListLoading = ref(false)
const createSettingsRef = ref<InstanceType<typeof GroupSettingsForm> | null>(null)
const editSettingsRef = ref<InstanceType<typeof GroupSettingsForm> | null>(null)
// modelsListCandidatesTracker keeps the latest candidate request identity per form so stale
// responses cannot replace a newer group selection.
const modelsListCandidatesTracker = createModelsListCandidatesTracker()
// The create and edit probe options derive from their respective requestable model candidates
// and current custom-list selections.
const createAvailabilityProbeModelOptions = computed(() =>
  buildAvailabilityProbeModelOptions(getAvailabilityProbeCandidateModels(createModelsListState)),
)
const editAvailabilityProbeModelOptions = computed(() =>
  buildAvailabilityProbeModelOptions(getAvailabilityProbeCandidateModels(editModelsListState)),
)

// createForm holds creation defaults, editable nested settings, and compatibility fields that
// submission normalizes into the group API representation.
const createForm = reactive({
  localization: undefined as
    | LocalizedUpdate<{
        display_name: string // localized display name
        description: string // administrator description text
      }>
    | undefined,
  name: '',
  description: '',
  display_brand: '',
  scheduler_type: 'basic' as GroupSchedulerType,
  advanced_scheduler_overrides: {} as GroupAdvancedSchedulerOverrides,
  protocol_fallbacks: {} as Partial<Record<ProtocolID, ProtocolID[]>>,
  responses_image_policy: 'inherit' as 'inherit' | 'enabled' | 'disabled' | 'block',
  allowed_protocols: [] as ProtocolID[],
  rate_multiplier: 1.0,
  is_exclusive: false,
  // Session isolation setting.
  session_isolation_enabled: false,

  routing_policy: defaultRoutingPolicy(),
  // Compatibility image permission fields.
  allow_image_generation: false,
  allow_batch_image_generation: false,

  // Restricted client and replacement settings.
  claude_code_only: false,
  fallback_group_id: null as number | null,
  fallback_group_id_on_invalid_request: null as number | null,
  // Selected replacement when this group is unavailable; null leaves the replacement
  // unconfigured in the draft.
  unavailable_fallback_group_id: null as number | null,
  // Compatibility Live enablement field.
  allow_live: false,
  // Group-level Fast routing policy.
  openai_fast_policy: 'follow_request',

  // OAuth and provider privacy requirements.
  require_oauth_only: false,
  require_privacy_set: false,
  // Model-to-provider routing enablement.
  model_routing_enabled: false,
  // Provider-copy source group identifiers.
  copy_providers_from_group_ids: [] as number[],
  // Per-user group requests per minute; zero represents an unlimited group RPM setting.
  rpm_limit: 0 as number,
  max_reasoning_effort: '',
  max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
  reasoning_effort_mappings: [] as ReasoningEffortMappingRow[],
  // Active probe settings used to construct the nested availability-probe API configuration
  // during submission.
  availability_probe_protocol: '' as ProtocolID | '',
  availability_probe_enabled: false,
  availability_probe_model_id: '',
  availability_probe_prompt: 'hi',
  availability_probe_interval_minutes: 30,
  availability_probe_timeout_seconds: 30,
  availability_probe_max_retries: 3,
  availability_probe_user_agent: '',
})

// Create-form routing rules own their selected provider objects and remain separate from the
// edit-form rule list.
const createModelRoutingRules = ref<ModelRoutingRule[]>([])

// Edit-form routing rules retain object identity across provider search and selection events
// for the selected existing group.
const editModelRoutingRules = ref<ModelRoutingRule[]>([])

// Stable object keys keep search results attached to the same rule when another rule is removed
// or reordered.
const resolveCreateRuleKey = createStableObjectKeyResolver<ModelRoutingRule>('create-rule')
const resolveEditRuleKey = createStableObjectKeyResolver<ModelRoutingRule>('edit-rule')

// getCreateRuleSearchKey returns the stable search identity for a create-form rule.
const getCreateRuleSearchKey = (rule: ModelRoutingRule) => `create-${resolveCreateRuleKey(rule)}`
// getEditRuleSearchKey returns the stable search identity for an edit-form rule.
const getEditRuleSearchKey = (rule: ModelRoutingRule) => `edit-${resolveEditRuleKey(rule)}`

// getRuleSearchKey selects the stable key resolver for the rule's form. The create form is the
// default, and callers editing an existing group pass true to use the edit namespace.
const getRuleSearchKey = (rule: ModelRoutingRule, isEdit: boolean = false) => {
  return isEdit ? getEditRuleSearchKey(rule) : getCreateRuleSearchKey(rule)
}

// Provider search maps store keywords, results, and dropdown visibility under the stable keys
// assigned to individual routing rules.
const providerSearchKeyword = ref<Record<string, string>>({})
const providerSearchResults = ref<Record<string, SimpleProvider[]>>({})
const showProviderDropdown = ref<Record<string, boolean>>({})

// clearProviderSearchStateByKey removes keyword, results, and visibility for one rule search.
const clearProviderSearchStateByKey = (key: string) => {
  delete providerSearchKeyword.value[key]
  delete providerSearchResults.value[key]
  delete showProviderDropdown.value[key]
}

// clearAllProviderSearchState replaces all provider search maps with empty page-owned records.
const clearAllProviderSearchState = () => {
  providerSearchKeyword.value = {}
  providerSearchResults.value = {}
  showProviderDropdown.value = {}
}

// providerSearchRunner debounces queries by stable rule key, cancels superseded work, and
// publishes provider suggestions into the matching keyed result map.
const providerSearchRunner = useKeyedDebouncedSearch<SimpleProvider[]>({
  delay: SEARCH_DEBOUNCE_MS,
  search: async (keyword, { signal }) => {
    const res = await adminAPI.providers.list(
      1,
      20,
      {
        search: keyword,
      },
      { signal },
    )
    return res.items.map((provider) => ({ id: provider.id, name: provider.name }))
  },
  onSuccess: (key, result) => {
    providerSearchResults.value[key] = result
  },
  onError: (key) => {
    // A failed keyed lookup clears that rule's suggestions so earlier provider matches are not
    // presented as results for the failed query.
    providerSearchResults.value[key] = []
  },
})

// searchProviders schedules the keyed provider query using its current draft keyword.
const searchProviders = (key: string) => {
  providerSearchRunner.trigger(key, providerSearchKeyword.value[key] || '')
}

// updateProviderSearch stores a rule keyword and triggers its stable-key provider search.
const updateProviderSearch = (rule: ModelRoutingRule, keyword: string, isEdit: boolean = false) => {
  const key = getRuleSearchKey(rule, isEdit)
  providerSearchKeyword.value[key] = keyword
  searchProviders(key)
}

// selectProvider appends a provider when its ID is absent from the rule. It then clears that
// rule's search text and hides its suggestions while keeping the selected provider object.
const selectProvider = (
  rule: ModelRoutingRule,
  provider: SimpleProvider,
  isEdit: boolean = false,
) => {
  if (!rule) return

  // Keep each selected provider ID once within the rule.
  if (!rule.providers.some((a) => a.id === provider.id)) {
    rule.providers.push(provider)
  }

  // Clear the completed selection query and hide its suggestions.
  const key = getRuleSearchKey(rule, isEdit)
  providerSearchKeyword.value[key] = ''
  showProviderDropdown.value[key] = false
}

// removeSelectedProvider excludes the identified provider from the selected routing rule.
const removeSelectedProvider = (
  rule: ModelRoutingRule,
  providerId: number,
  _isEdit: boolean = false,
) => {
  if (!rule) return

  rule.providers = rule.providers.filter((a) => a.id !== providerId)
}

// onProviderSearchFocus opens suggestions for the focused rule's stable search key. An empty
// result list triggers a search using its current keyword, while existing suggestions can be
// shown immediately.
const onProviderSearchFocus = (rule: ModelRoutingRule, isEdit: boolean = false) => {
  const key = getRuleSearchKey(rule, isEdit)
  showProviderDropdown.value[key] = true
  // The focused rule has no suggestions, so request candidates using its existing keyword
  // before the user makes a selection.
  if (!providerSearchResults.value[key]?.length) {
    searchProviders(key)
  }
}

// addCreateRoutingRule appends an empty model pattern and provider list for creation.
const addCreateRoutingRule = () => {
  createModelRoutingRules.value.push({ pattern: '', providers: [] })
}

// removeCreateRoutingRule cancels keyed search work before removing a create-form rule. Unknown
// rule objects are ignored, and the matching keyword, result, and dropdown state are cleared
// before the list changes.
const removeCreateRoutingRule = (rule: ModelRoutingRule) => {
  const index = createModelRoutingRules.value.indexOf(rule)
  if (index === -1) return

  const key = getCreateRuleSearchKey(rule)
  // Invalidate pending work for the removed rule before clearing its maps so a late result
  // cannot recreate suggestions for that deleted object.
  providerSearchRunner.clearKey(key)
  clearProviderSearchStateByKey(key)
  createModelRoutingRules.value.splice(index, 1)
}

// addEditRoutingRule appends an empty model pattern and provider list for editing.
const addEditRoutingRule = () => {
  editModelRoutingRules.value.push({ pattern: '', providers: [] })
}

// removeEditRoutingRule cancels keyed search work before removing an edit-form rule. Unknown
// rule objects are ignored, and its keyed search state is cleared before the rule leaves the
// draft.
const removeEditRoutingRule = (rule: ModelRoutingRule) => {
  const index = editModelRoutingRules.value.indexOf(rule)
  if (index === -1) return

  const key = getEditRuleSearchKey(rule)
  // Invalidate pending work for the removed rule before clearing its maps so a late result
  // cannot recreate suggestions for that deleted object.
  providerSearchRunner.clearKey(key)
  clearProviderSearchStateByKey(key)
  editModelRoutingRules.value.splice(index, 1)
}

// resetModelsListState replaces a reactive model draft's fields from optional saved
// configuration. It keeps the reactive container itself while clearing candidate rows and
// restoring normalized saved selection for the next hydration.
const resetModelsListState = (
  state: typeof createModelsListState,
  config?: Parameters<typeof createInitialModelsListState>[0],
) => {
  const fresh = createInitialModelsListState(config)
  state.enabled = fresh.enabled
  state.savedModels = fresh.savedModels
  state.candidateModels = fresh.candidateModels
  state.items = fresh.items
}

// loadModelsListCandidates queries requestable models for the selected form and group. It
// registers a request identity before dispatch and chooses the matching create or edit draft
// and loading flag. Results, errors, and completion are accepted only while that identity
// remains current.
//
// Create and edit requests use separate tracker slots, so one form's refresh does not
// invalidate the other. Accepted candidates pass through reconciliation, retaining existing
// user selection and ordering according to the model-list rules.
const loadModelsListCandidates = async (mode: 'create' | 'edit', groupID: number) => {
  const request = { mode, groupID }
  const requestID = modelsListCandidatesTracker.next(request)
  const state = mode === 'create' ? createModelsListState : editModelsListState
  const loadingRef = mode === 'create' ? createModelsListLoading : editModelsListLoading
  loadingRef.value = true
  try {
    const models = await adminAPI.groups.getModelsListCandidates(groupID)
    // Candidate requests can finish out of order; reject superseded work before it changes
    // the form draft or reports an obsolete failure.
    if (!modelsListCandidatesTracker.isCurrent(requestID, request)) {
      return
    }
    setModelsListCandidates(state, models)
  } catch (error) {
    // Candidate requests can finish out of order; reject superseded work before it changes
    // the form draft or reports an obsolete failure.
    if (!modelsListCandidatesTracker.isCurrent(requestID, request)) {
      return
    }
    console.error('Error loading group models list candidates:', error)
  } finally {
    if (modelsListCandidatesTracker.isCurrent(requestID, request)) {
      loadingRef.value = false
    }
  }
}

// setModelSelection changes the matching draft row to the requested selected state.
function setModelSelection(state: typeof createModelsListState, id: string, value: boolean) {
  const item = state.items.find((item) => item.id === id)
  if (item) item.selected = value
}

// buildAvailabilityProbeModelOptions returns normalized unique model choices after an empty
// selection option. Blank identifiers are skipped, and first-seen order is retained so probe
// selection follows the current model candidate order.
function buildAvailabilityProbeModelOptions(models: string[]) {
  const seen = new Set<string>()
  const options = [{ value: '', label: t('admin.groups.availabilityProbe.selectModel') }]
  for (const raw of models) {
    const model = raw.trim()
    if (!model || seen.has(model)) {
      continue
    }
    seen.add(model)
    options.push({ value: model, label: model })
  }
  return options
}

// isAvailabilityProbeModelAvailable accepts an empty selection or a model present in the
// options. Empty means no model has been chosen, so option refreshes leave that draft state
// available for validation.
const isAvailabilityProbeModelAvailable = (
  modelID: string,
  options: ReturnType<typeof buildAvailabilityProbeModelOptions>,
) => {
  // An empty model means no probe choice has been made yet and remains available for subsequent
  // form validation.
  return !modelID || options.some((option) => option.value === modelID)
}

// resetAvailabilityProbeFormState loads saved probe values or their editing defaults. Missing
// settings disable the probe, select automatic routing, and initialize its prompt, period, timeout, retry count, and text
// inputs for the form.
const resetAvailabilityProbeFormState = (
  form: typeof createForm | typeof editForm,
  config?: GroupAvailabilityProbeConfig | null,
) => {
  form.availability_probe_protocol = config?.protocol ?? ''
  form.availability_probe_enabled = config?.enabled ?? false
  form.availability_probe_model_id = config?.model_id ?? ''
  form.availability_probe_prompt = config?.prompt ?? 'hi'
  form.availability_probe_interval_minutes = config?.interval_minutes ?? 30
  form.availability_probe_timeout_seconds = config?.timeout_seconds ?? 30
  form.availability_probe_max_retries = config?.max_retries ?? 3
  form.availability_probe_user_agent = config?.user_agent ?? ''
}

// isAvailabilityProbeProtocolAvailable checks the current draft against loaded test capabilities.
// An empty selection keeps automatic behavior; unavailable explicit selections remain in the
// draft so validation can reveal the protocol control without silently changing the route.
const isAvailabilityProbeProtocolAvailable = (
  form: typeof createForm | typeof editForm,
): boolean => {
  const protocol = form.availability_probe_protocol
  return (
    !protocol ||
    (form.allowed_protocols.includes(protocol) &&
      (protocolCatalog.value?.groups[0]?.probe_protocols ?? []).includes(protocol))
  )
}

// buildAvailabilityProbeConfig constructs the probe payload from the active group draft.
// Disabled probes emit only enabled false; enabled probes require a trimmed model and prompt
// and reject unavailable selected protocols. Missing input produces localized errors. Falsy interval and timeout conversions use
// thirty.
//
// Retry count is converted directly, retaining zero as an explicit no-retry setting. The
// returned object groups these values under the API probe configuration, while submission
// removes the separate browser draft fields from the surrounding request.
const buildAvailabilityProbeConfig = (
  form: typeof createForm | typeof editForm,
): GroupAvailabilityProbeConfig => {
  if (!form.availability_probe_enabled) {
    return { enabled: false }
  }

  if (!isAvailabilityProbeProtocolAvailable(form)) {
    throw new Error(t('admin.groups.availabilityProbe.protocolUnavailable'))
  }
  const modelID = form.availability_probe_model_id.trim()
  const prompt = form.availability_probe_prompt.trim()
  if (!modelID) {
    throw new Error(t('admin.groups.availabilityProbe.modelRequired'))
  }
  if (!prompt) {
    throw new Error(t('admin.groups.availabilityProbe.promptRequired'))
  }

  return {
    enabled: true,
    protocol: form.availability_probe_protocol || undefined,
    model_id: modelID,
    prompt,
    interval_minutes: Number(form.availability_probe_interval_minutes) || 30,
    timeout_seconds: Number(form.availability_probe_timeout_seconds) || 30,
    // Converting the retry count directly retains zero, which represents an explicit probe with
    // no additional retry attempts.
    max_retries: Number(form.availability_probe_max_retries),
    user_agent: form.availability_probe_user_agent.trim(),
  }
}

// convertRoutingRulesToApiFormat serializes nonempty model patterns with positive provider IDs.
// Rules without a pattern or usable providers are skipped, duplicate patterns use the later
// entry, and no usable rules returns null.
const convertRoutingRulesToApiFormat = (
  rules: ModelRoutingRule[],
): Record<string, number[]> | null => {
  const result: Record<string, number[]> = {}
  let hasValidRules = false

  for (const rule of rules) {
    const pattern = rule.pattern.trim()
    if (!pattern) continue

    const providerIds = rule.providers.map((a) => a.id).filter((id) => id > 0)

    if (providerIds.length > 0) {
      result[pattern] = providerIds
      hasValidRules = true
    }
  }

  return hasValidRules ? result : null
}

// convertApiFormatToRoutingRules hydrates stored routing patterns with provider display names
// for the editor. Null configuration returns an empty list; object-entry order and each stored
// ID list determine the resulting rule and provider order. Successful lookups supply the
// returned provider identity and name, while failed lookups retain the requested ID with a
// numeric label so later serialization keeps that routing membership. Callers await all serial
// lookups before assigning the draft; this function provides no cancellation or stale-edit guard.
const convertApiFormatToRoutingRules = async (
  apiFormat: Record<string, number[]> | null,
): Promise<ModelRoutingRule[]> => {
  if (!apiFormat) return []

  const rules: ModelRoutingRule[] = []
  for (const [pattern, providerIds] of Object.entries(apiFormat)) {
    // Resolve the stored provider IDs to their current display names.
    const providers: SimpleProvider[] = []
    for (const id of providerIds) {
      try {
        const provider = await adminAPI.providers.getById(id)
        providers.push({ id: provider.id, name: provider.name })
      } catch {
        // When provider lookup fails, retain the stored ID with a numeric label so editing
        // cannot silently erase its routing membership.
        providers.push({ id, name: `#${id}` })
      }
    }
    rules.push({ pattern, providers })
  }
  return rules
}

// editForm holds the selected group draft, including activation state and compatibility values
// used during hydration and normalized update submission.
const editForm = reactive({
  localization: undefined as
    | LocalizedUpdate<{
        display_name: string // localized display name
        description: string // administrator description text
      }>
    | undefined,
  name: '',
  description: '',
  display_brand: '',
  scheduler_type: 'basic' as GroupSchedulerType,
  advanced_scheduler_overrides: {} as GroupAdvancedSchedulerOverrides,
  protocol_fallbacks: {} as Partial<Record<ProtocolID, ProtocolID[]>>,
  responses_image_policy: 'inherit' as 'inherit' | 'enabled' | 'disabled' | 'block',
  allowed_protocols: [] as ProtocolID[],
  rate_multiplier: 1.0,
  is_exclusive: false,
  // Session isolation setting.
  session_isolation_enabled: false,
  status: 'active' as 'active' | 'inactive',

  routing_policy: defaultRoutingPolicy(),
  // Compatibility image permission fields.
  allow_image_generation: false,
  allow_batch_image_generation: false,

  // Restricted client and replacement settings.
  claude_code_only: false,
  fallback_group_id: null as number | null,
  fallback_group_id_on_invalid_request: null as number | null,
  // Selected replacement when this group is unavailable; null leaves the replacement
  // unconfigured in the draft.
  unavailable_fallback_group_id: null as number | null,
  // Compatibility Live enablement field.
  allow_live: false,
  // Group-level Fast routing policy.
  openai_fast_policy: 'follow_request',

  default_mapped_model: '',
  // OAuth and provider privacy requirements.
  require_oauth_only: false,
  require_privacy_set: false,
  // Model-to-provider routing enablement.
  model_routing_enabled: false,
  // Provider-copy source group identifiers.
  copy_providers_from_group_ids: [] as number[],
  // Per-user group requests per minute; zero represents an unlimited group RPM setting.
  rpm_limit: 0 as number,
  max_reasoning_effort: '',
  max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
  reasoning_effort_mappings: [] as ReasoningEffortMappingRow[],
  // Active probe settings used to construct the nested availability-probe API configuration
  // during submission.
  availability_probe_protocol: '' as ProtocolID | '',
  availability_probe_enabled: false,
  availability_probe_model_id: '',
  availability_probe_prompt: 'hi',
  availability_probe_interval_minutes: 30,
  availability_probe_timeout_seconds: 30,
  availability_probe_max_retries: 3,
  availability_probe_user_agent: '',
})

// initializeGroupProtocolDefaults copies the catalog's group protocol defaults into a form. It
// returns while the catalog is unavailable; callers invoke it at initialization or reset rather
// than treating a user-cleared list as uninitialized.
function initializeGroupProtocolDefaults(form: {
  allowed_protocols: ProtocolID[] // enabled client protocols
  // protocol_fallbacks stores conversion targets by client protocol. Missing keys use automatic
  // conversion, while empty arrays request native handling.
  protocol_fallbacks: Partial<Record<ProtocolID, ProtocolID[]>>
}) {
  const profile = protocolCatalog.value?.groups[0]
  if (!profile) return
  form.allowed_protocols = [...profile.defaults]
  form.protocol_fallbacks = { ...profile.default_fallbacks }
}

// The pending flag defers edit default initialization until the catalog arrives; loading an
// existing group clears it to retain saved protocol choices.
const editProtocolDefaultsPending = ref(false)
watch(
  protocolCatalog,
  (catalog, previous) => {
    if (catalog && !previous) initializeGroupProtocolDefaults(createForm)
  },
  { immediate: true },
)
watch(protocolCatalog, (catalog) => {
  if (catalog && editProtocolDefaultsPending.value) {
    initializeGroupProtocolDefaults(editForm)
    editProtocolDefaultsPending.value = false
  }
})

// deleteConfirmMessage names the selected record in the translated confirmation text presented
// before the delete request.
const deleteConfirmMessage = computed(() => {
  if (!deletingGroup.value) {
    return ''
  }
  return t('admin.groups.deleteConfirm', { name: deletingGroup.value.name })
})

// loadLiveCapability reuses the cached Live capability or its pending request. Simultaneous
// callers await one shared promise, which is cleared when the request settles. API failure
// resolves to unsupported so the page can retain its capability-dependent behavior.
//
// The completed value is cached for later calls, including the unsupported value returned after
// failure. This function does not schedule a retry once that cache exists; its callers use the
// recorded result for the current page lifetime.
const loadLiveCapability = async () => {
  if (liveCapability.value) return liveCapability.value
  if (!liveCapabilityRequest) {
    liveCapabilityRequest = adminAPI.groups
      .getLiveCapability()
      // Capability lookup failure is treated as unsupported, leaving the page on its
      // conservative confirmation path rather than rejecting page initialization.
      .catch(() => ({ supported: false }))
      .finally(() => {
        liveCapabilityRequest = null
      })
  }
  liveCapability.value = await liveCapabilityRequest
  return liveCapability.value ?? { supported: false }
}

// confirmUnsupportedLive enables Live on the pending form and clears confirmation state.
const confirmUnsupportedLive = () => {
  if (pendingLiveForm.value === 'create') createForm.allow_live = true
  if (pendingLiveForm.value === 'edit') editForm.allow_live = true
  pendingLiveForm.value = null
}

// cancelUnsupportedLive clears the pending form selection for the Live confirmation.
const cancelUnsupportedLive = () => {
  pendingLiveForm.value = null
}

// loadGroups fetches the selected group page and supersedes the preceding list request. It
// aborts the old controller before publishing the new controller, then ignores results from
// aborted reads. Accepted rows update pagination and trigger the visible usage and capacity
// summaries.
//
// Cancellation errors are consumed so filter changes do not display a failed-load notice. The
// loading flag is cleared only by the currently stored, non-aborted controller, preventing
// older request completion from hiding the active page load.
const loadGroups = async () => {
  // Supersede the preceding list request before setting the new loading owner so older
  // responses cannot replace the current group page.
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
        status: filters.status as any,
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

// loadUnavailableFallbackGroups loads the complete group option source independently of table
// pagination. Failures are logged and retain the previous options, while each selector applies
// its own eligibility filter afterward.
const loadUnavailableFallbackGroups = async () => {
  try {
    unavailableFallbackGroups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Error loading unavailable fallback groups:', error)
  }
}

// formatGroupBalance formats group costs through the shared two-decimal balance display.
const formatGroupBalance = (cost: number | null | undefined): string =>
  formatBalanceAmount(cost, { fractionDigits: 2 })

// normalizeDisplayBrand trims the brand input and limits it to fifty string positions.
// Submission uses the normalized value for both create and update requests, matching the form's
// constrained brand representation.
const normalizeDisplayBrand = (value: string): string => value.trim().slice(0, 50)

// displayBrandLabel resolves the configured brand value into its shared display name.
const displayBrandLabel = (value: unknown): string => providerBrandDisplayName(String(value || ''))

// displayBrandBadgeClass combines the badge layout with the resolved brand color classes.
const displayBrandBadgeClass = (value: unknown): string => {
  const base = 'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium'
  return `${base} ${resolveProviderBrand(String(value || '')).badgeClass}`
}

// loadUsageSummary fetches group costs when the usage column is visible. Results replace the
// ID-indexed cost map, and failures retain prior values while logging the error and releasing
// the loading flag.
const loadUsageSummary = async () => {
  if (!hasVisibleUsageColumn.value) {
    usageLoading.value = false
    return
  }
  usageLoading.value = true
  try {
    const data = await adminAPI.groups.getUsageSummary()
    const map = new Map<
      number,
      {
        today_cost: number // current day billed amount
        yesterday_cost: number // previous day billed amount
        total_cost: number // total billed amount
      }
    >()
    for (const item of data) {
      map.set(item.group_id, {
        today_cost: item.today_cost,
        yesterday_cost: item.yesterday_cost,
        total_cost: item.total_cost,
      })
    }
    usageMap.value = map
  } catch (error) {
    console.error('Error loading group usage summary:', error)
  } finally {
    usageLoading.value = false
  }
}

// loadCapacitySummary fetches group capacity when its table column is visible. It replaces the
// ID-indexed map after success, and failures retain prior capacity values while logging the
// unsuccessful request.
const loadCapacitySummary = async () => {
  if (!hasVisibleCapacityColumn.value) {
    return
  }
  try {
    const data = await adminAPI.groups.getCapacitySummary()
    const map = new Map<
      number,
      {
        concurrencyUsed: number // occupied concurrent request slots
        concurrencyMax: number // maximum concurrent request slots
        sessionsUsed: number // occupied session slots
        sessionsMax: number // maximum session slots
        rpmUsed: number // current requests per minute
        rpmMax: number // maximum requests per minute
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

// searchTimeout owns the pending search debounce timer, which a subsequent input clears before
// scheduling another query.
let searchTimeout: ReturnType<typeof setTimeout>
// handleSearch debounces the group query and resets pagination before loading. A new keystroke
// clears the pending timer, while dispatched list requests are superseded through loadGroups
// when the timer fires.
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadGroups()
  }, 300)
}

// handlePageChange selects the requested group page and reloads its rows.
const handlePageChange = (page: number) => {
  pagination.page = page
  loadGroups()
}

// handlePageSizeChange changes group page size and reloads from page one.
const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadGroups()
}

// handleSort sets the group ordering and reloads from the first page.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadGroups()
}

// openCreateModal shows the create editor and requests its model candidates.
const openCreateModal = () => {
  showCreateModal.value = true
  loadModelsListCandidates('create', 0)
}

// closeCreateModal closes creation and restores its editable defaults. It clears each routing
// rule's pending search before replacing rule state, resets nested policies and model lists,
// and clears visible reasoning validation.
const closeCreateModal = () => {
  showCreateModal.value = false
  // Closing this draft cancels each rule's keyed work before clearing search maps and rule
  // lists, so pending completions cannot repopulate suggestions for discarded rows.
  createModelRoutingRules.value.forEach((rule) => {
    providerSearchRunner.clearKey(getCreateRuleSearchKey(rule))
  })
  clearAllProviderSearchState()
  createForm.localization = undefined
  createForm.name = ''
  createForm.description = ''
  createForm.display_brand = ''
  createForm.scheduler_type = 'basic'
  createForm.advanced_scheduler_overrides = {}
  initializeGroupProtocolDefaults(createForm)
  createForm.responses_image_policy = 'inherit'
  createForm.rate_multiplier = 1.0
  createForm.is_exclusive = false
  createForm.session_isolation_enabled = false
  createForm.allow_image_generation = false
  createForm.allow_batch_image_generation = false

  createForm.routing_policy = defaultRoutingPolicy()

  createForm.claude_code_only = false
  createForm.fallback_group_id = null
  createForm.fallback_group_id_on_invalid_request = null
  createForm.unavailable_fallback_group_id = null
  createForm.allow_live = false
  createForm.openai_fast_policy = 'follow_request'

  createForm.require_oauth_only = false
  createForm.require_privacy_set = false
  createForm.copy_providers_from_group_ids = []
  createForm.rpm_limit = 0
  createForm.max_reasoning_effort = ''
  createForm.max_reasoning_effort_over_limit = reasoningEffortOverLimitDowngrade
  createForm.reasoning_effort_mappings = []
  createSettingsRef.value?.resetValidation()
  resetAvailabilityProbeFormState(createForm)
  resetModelsListState(createModelsListState)
  createModelRoutingRules.value = []
}

// validateGroupForm checks the selected form and reveals the first failed business field. Child
// validation runs first, then the group name and probe configuration are checked. Probe
// exceptions reveal an unavailable protocol first, then the missing model or prompt field.
const validateGroupForm = async (target: 'create' | 'edit'): Promise<boolean> => {
  // form owns editable fields for the open dialog, keeping pending changes separate from the
  // displayed server records.
  const form = target === 'create' ? createForm : editForm
  const tabs = target === 'create' ? createSettingsRef.value : editSettingsRef.value
  if (tabs && !(await tabs.validate())) return false
  if (!form.name.trim()) {
    appStore.showError(t('admin.groups.nameRequired'))
    await tabs?.revealField('[data-group-field="name"]')
    return false
  }
  try {
    buildAvailabilityProbeConfig(form)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error))
    await tabs?.revealField(
      !isAvailabilityProbeProtocolAvailable(form)
        ? '[data-group-field="probe-protocol"]'
        : form.availability_probe_model_id.trim()
          ? '[data-group-field="probe-prompt"]'
          : '[data-group-field="probe-model"]',
    )
    return false
  }
  return true
}

// handleCreateGroup validates and persists a group assembled from the create draft. Catalog
// readiness and the shared submission flag gate the request, and form visibility is checked
// again after asynchronous validation. Nested model, routing, reasoning, and probe values are
// converted before API submission.
//
// Successful creation closes and resets the draft, refreshes both table and group options, and
// advances an active matching onboarding step. Failures keep the draft available and display
// the extracted API error; finally releases the submission flag.
const handleCreateGroup = async () => {
  if (!protocolCatalog.value || submitting.value || !(await validateGroupForm('create'))) return
  if (submitting.value || !showCreateModal.value) return
  submitting.value = true
  try {
    const availabilityProbeConfig = buildAvailabilityProbeConfig(createForm)
    // Build the API request from the draft and its separately maintained routing, model-list,
    // reasoning, and probe state.
    const requestData = {
      ...createForm,
      allow_image_generation: undefined,
      allow_batch_image_generation: undefined,
      allow_live: undefined,
      allowed_protocols: effectiveGroupClientProtocols(createForm.allowed_protocols),
      protocol_fallbacks: sanitizeGroupProtocolFallbacks(createForm.protocol_fallbacks),
      responses_image_policy: createForm.responses_image_policy,
      display_brand: normalizeDisplayBrand(createForm.display_brand),
      routing_policy: createForm.routing_policy,

      model_routing: convertRoutingRulesToApiFormat(createModelRoutingRules.value),
      models_list_config: buildModelsListConfig(createModelsListState),
      availability_probe_config: availabilityProbeConfig,
      openai_fast_policy: normalizeGroupOpenAIFastPolicy(createForm.openai_fast_policy),

      max_reasoning_effort_over_limit: normalizeReasoningEffortOverLimit(
        createForm.max_reasoning_effort_over_limit,
      ),
      reasoning_effort_mappings: reasoningEffortMappingsToAPI(createForm.reasoning_effort_mappings),
    }
    delete (requestData as any).availability_probe_protocol
    delete (requestData as any).availability_probe_enabled
    delete (requestData as any).availability_probe_model_id
    delete (requestData as any).availability_probe_prompt
    delete (requestData as any).availability_probe_interval_minutes
    delete (requestData as any).availability_probe_timeout_seconds
    delete (requestData as any).availability_probe_max_retries
    delete (requestData as any).availability_probe_user_agent

    await adminAPI.groups.create(requestData)
    appStore.showSuccess(t('admin.groups.groupCreated'))
    closeCreateModal()
    loadGroups()
    loadUnavailableFallbackGroups()
    // Creation has succeeded, so advance a matching submit tour step after the dialog closes
    // and its refreshed group data has been requested.
    if (onboardingStore.isCurrentStep('[data-tour="group-form-submit"]')) {
      onboardingStore.nextStep(500)
    }
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.failedToCreate')))
    console.error('Error creating group:', error)
  } finally {
    submitting.value = false
  }
}

// handleEdit fills the edit draft from a stored group and then opens it. It clones nested
// editable values, translates compatibility fields, resets provider-copy selection, and
// hydrates model and reasoning drafts. Provider names are resolved before the modal becomes
// visible.
//
// Saved protocol choices are retained while the model candidate query refreshes available
// selections. Provider lookup failures keep numeric IDs through the conversion helper, allowing
// the routing rule to remain represented even when its provider details cannot be loaded.
const handleEdit = async (group: AdminGroup) => {
  editingGroup.value = group
  editForm.localization = group.localization
    ? JSON.parse(JSON.stringify(group.localization))
    : undefined
  editForm.name = group.name
  editForm.description = group.description || ''
  editForm.display_brand = group.display_brand || ''
  editForm.scheduler_type = group.scheduler_type ?? 'basic'
  editForm.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(
    group.advanced_scheduler_overrides,
  )
  editForm.rate_multiplier = group.rate_multiplier
  editForm.is_exclusive = group.is_exclusive
  editForm.session_isolation_enabled = group.session_isolation_enabled ?? false
  editForm.status = group.status

  editForm.routing_policy = cloneRoutingPolicy(group.routing_policy)
  editForm.allow_image_generation =
    group.allowed_protocols?.some((id) =>
      ['openai_images_generations', 'openai_images_edits', 'image_batches'].includes(id),
    ) ?? false
  editForm.allow_batch_image_generation =
    group.allowed_protocols?.includes('image_batches') ?? false

  editForm.claude_code_only = group.claude_code_only || false
  editForm.fallback_group_id = group.fallback_group_id
  editForm.fallback_group_id_on_invalid_request = group.fallback_group_id_on_invalid_request
  editForm.unavailable_fallback_group_id = group.unavailable_fallback_group_id
  editForm.allowed_protocols = effectiveGroupClientProtocols(group.allowed_protocols)
  editForm.protocol_fallbacks = sanitizeGroupProtocolFallbacks(group.protocol_fallbacks)
  editProtocolDefaultsPending.value = false
  editForm.responses_image_policy = group.responses_image_policy ?? 'inherit'
  editForm.allow_live = group.allowed_protocols?.includes('openai_live') ?? false
  editForm.openai_fast_policy = normalizeGroupOpenAIFastPolicy(
    group.openai_fast_policy ?? (group.force_openai_fast ? 'force_priority' : 'follow_request'),
  )

  editForm.require_oauth_only = group.require_oauth_only ?? false
  editForm.require_privacy_set = group.require_privacy_set ?? false
  editForm.model_routing_enabled = group.model_routing_enabled || false
  editForm.copy_providers_from_group_ids = [] // Provider copying starts empty for each edit session.
  editForm.rpm_limit = group.rpm_limit ?? 0
  editForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(group.max_reasoning_effort)
  editForm.max_reasoning_effort_over_limit = normalizeReasoningEffortOverLimit(
    group.max_reasoning_effort_over_limit,
  )
  editForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
    group.reasoning_effort_mappings,
  )
  resetAvailabilityProbeFormState(editForm, group.availability_probe_config)
  resetModelsListState(editModelsListState, group.models_list_config)
  // Resolve provider labels before opening the editor so stored rule membership is visible when
  // the dialog appears.
  editModelRoutingRules.value = await convertApiFormatToRoutingRules(group.model_routing)
  loadModelsListCandidates('edit', group.id)
  showEditModal.value = true
}

// closeEditModal cancels rule searches, clears edit-only state, and closes the editor. It
// resets nested routing, reasoning, scheduler, probe, and model-list drafts so a later edit
// hydrates those fields from its selected group.
const closeEditModal = () => {
  // Closing this draft cancels each rule's keyed work before clearing search maps and rule
  // lists, so pending completions cannot repopulate suggestions for discarded rows.
  editModelRoutingRules.value.forEach((rule) => {
    providerSearchRunner.clearKey(getEditRuleSearchKey(rule))
  })
  clearAllProviderSearchState()
  showEditModal.value = false
  editingGroup.value = null
  editForm.max_reasoning_effort = ''
  editForm.max_reasoning_effort_over_limit = reasoningEffortOverLimitDowngrade
  editForm.reasoning_effort_mappings = []
  editSettingsRef.value?.resetValidation()
  editModelRoutingRules.value = []
  editForm.scheduler_type = 'basic'
  editForm.advanced_scheduler_overrides = {}
  editForm.session_isolation_enabled = false
  editForm.unavailable_fallback_group_id = null
  editForm.copy_providers_from_group_ids = []
  resetAvailabilityProbeFormState(editForm)

  editForm.routing_policy = defaultRoutingPolicy()

  editForm.allow_live = false
  editForm.openai_fast_policy = 'follow_request'

  resetModelsListState(editModelsListState)
}

// handleUpdateGroup validates and persists the current group edit. It checks group identity,
// catalog readiness, submission state, and editor visibility around asynchronous validation.
// Payload construction converts absent fallback group selections to zero, the API's
// clear-reference value, and nests probe configuration.
//
// Successful persistence closes and resets editing before refreshing table and group options.
// API errors keep the draft visible and report the extracted message. The shared submission
// flag is released for both successful and failed attempts.
const handleUpdateGroup = async () => {
  if (!editingGroup.value) return
  if (!protocolCatalog.value || submitting.value || !(await validateGroupForm('edit'))) return
  if (submitting.value || !showEditModal.value || !editingGroup.value) return

  submitting.value = true
  try {
    const availabilityProbeConfig = buildAvailabilityProbeConfig(editForm)
    // The update API uses zero to remove saved group references, so convert cleared nullable
    // selections before sending the payload.
    const payload = {
      ...editForm,
      allow_image_generation: undefined,
      allow_batch_image_generation: undefined,
      allow_live: undefined,
      allowed_protocols: effectiveGroupClientProtocols(editForm.allowed_protocols),
      protocol_fallbacks: sanitizeGroupProtocolFallbacks(editForm.protocol_fallbacks),
      responses_image_policy: editForm.responses_image_policy,
      display_brand: normalizeDisplayBrand(editForm.display_brand),
      routing_policy: editForm.routing_policy,

      fallback_group_id: editForm.fallback_group_id === null ? 0 : editForm.fallback_group_id,
      fallback_group_id_on_invalid_request:
        editForm.fallback_group_id_on_invalid_request === null
          ? 0
          : editForm.fallback_group_id_on_invalid_request,
      unavailable_fallback_group_id:
        editForm.unavailable_fallback_group_id === null
          ? 0
          : editForm.unavailable_fallback_group_id,
      model_routing: convertRoutingRulesToApiFormat(editModelRoutingRules.value),
      models_list_config: buildModelsListConfig(editModelsListState),
      availability_probe_config: availabilityProbeConfig,
      openai_fast_policy: normalizeGroupOpenAIFastPolicy(editForm.openai_fast_policy),

      max_reasoning_effort_over_limit: normalizeReasoningEffortOverLimit(
        editForm.max_reasoning_effort_over_limit,
      ),
      reasoning_effort_mappings: reasoningEffortMappingsToAPI(editForm.reasoning_effort_mappings),
    }
    delete (payload as any).availability_probe_protocol
    delete (payload as any).availability_probe_enabled
    delete (payload as any).availability_probe_model_id
    delete (payload as any).availability_probe_prompt
    delete (payload as any).availability_probe_interval_minutes
    delete (payload as any).availability_probe_timeout_seconds
    delete (payload as any).availability_probe_max_retries
    delete (payload as any).availability_probe_user_agent

    await adminAPI.groups.update(editingGroup.value.id, payload)
    appStore.showSuccess(t('admin.groups.groupUpdated'))
    closeEditModal()
    loadGroups()
    loadUnavailableFallbackGroups()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.groups.failedToUpdate')))
    console.error('Error updating group:', error)
  } finally {
    submitting.value = false
  }
}

// handleRateMultipliers selects a group and opens its per-user multiplier dialog.
const handleRateMultipliers = (group: AdminGroup) => {
  rateMultipliersGroup.value = group
  showRateMultipliersModal.value = true
}

// handleRPMOverrides selects a group and opens its per-user RPM dialog.
const handleRPMOverrides = (group: AdminGroup) => {
  rpmOverridesGroup.value = group
  showRPMOverridesModal.value = true
}

// handleDuplicate duplicates one group while blocking repeated requests for that ID. The ID
// remains marked through the follow-up list reload, and finally removes it after success or
// failure so the action becomes available again.
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

// handleDelete stores the selected group and opens its deletion confirmation.
const handleDelete = (group: AdminGroup) => {
  deletingGroup.value = group
  showDeleteDialog.value = true
}

// confirmDelete removes the selected group and refreshes rows and group options. Missing
// selection skips the operation; failures keep confirmation state and display the server detail
// or translated deletion error.
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

watch(createAvailabilityProbeModelOptions, (options) => {
  if (!createModelsListState.enabled && createModelsListState.items.length === 0) {
    return
  }
  if (!isAvailabilityProbeModelAvailable(createForm.availability_probe_model_id, options)) {
    createForm.availability_probe_model_id = ''
  }
})

watch(editAvailabilityProbeModelOptions, (options) => {
  if (!editModelsListState.enabled && editModelsListState.items.length === 0) {
    return
  }
  if (!isAvailabilityProbeModelAvailable(editForm.availability_probe_model_id, options)) {
    editForm.availability_probe_model_id = ''
  }
})

// handleClickOutside closes provider and column menus when their containers exclude the click.
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  // Clicks inside provider search controls retain their open suggestions.
  if (!target.closest('.provider-search-container')) {
    Object.keys(showProviderDropdown.value).forEach((key) => {
      showProviderDropdown.value[key] = false
    })
  }
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
}

// openSortModal loads all groups and orders a copied list by sort_order. It shows the sorting
// dialog after success, while failures report an error and leave the dialog unopened.
const openSortModal = async () => {
  try {
    // Sorting requires the complete group list because table pagination contains only part of
    // the ordering being edited.
    const allGroups = await adminAPI.groups.getAll()
    // The sortable draft follows the persisted order before administrators drag rows into a
    // replacement sequence.
    sortableGroups.value = [...allGroups].sort((a, b) => a.sort_order - b.sort_order)
    showSortModal.value = true
  } catch (error) {
    appStore.showError(t('admin.groups.failedToLoad'))
    console.error('Error loading groups for sorting:', error)
  }
}

// closeSortModal hides group ordering and clears its temporary sortable list.
const closeSortModal = () => {
  showSortModal.value = false
  sortableGroups.value = []
}

// saveSortOrder persists the displayed group order as spaced numeric positions. After success
// it closes the ordering dialog and refreshes rows and group options; failures retain the draft
// for correction or retry.
const saveSortOrder = async () => {
  sortSubmitting.value = true
  try {
    const updates = sortableGroups.value.map((g, index) => ({
      id: g.id,
      // Space persisted positions by ten for the group ordering.
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

// Load the protocol catalog before the initial form defaults and list queries, then install
// the document listener used by provider and column menus.
onMounted(async () => {
  try {
    await loadProtocolCatalog()
  } catch {
    appStore.showError(t('admin.protocols.loadError'))
  }
  loadGroups()
  loadUnavailableFallbackGroups()
  void loadLiveCapability()
  loadModelsListCandidates('create', 0)
  document.addEventListener('click', handleClickOutside)
})

// Remove document handling and cancel all keyed provider searches when leaving the page so
// those searches cannot repopulate detached rule suggestions.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  providerSearchRunner.clearAll()
  clearAllProviderSearchState()
})
</script>

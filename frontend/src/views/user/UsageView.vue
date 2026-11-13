<template>
  <AppLayout>
    <div class="space-y-4">
      <UsageStatsCards
        :stats="usageStats"
        :show-provider-cost="false"
        :show-standard-cost="false"
      />

      <div class="space-y-4">
        <div class="card p-4">
          <div class="time-controls flex flex-wrap items-center justify-between gap-2">
            <div class="flex min-w-0 items-center gap-2">
              <span class="time-control-label text-sm font-medium text-gray-700 dark:text-gray-300"
                >{{ t('admin.dashboard.timeRange') }}:</span
              >
              <DateRangePicker
                v-model:start-date="startDate"
                v-model:end-date="endDate"
                @change="onDateRangeChange"
              />
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <span class="time-control-label text-sm font-medium text-gray-700 dark:text-gray-300"
                >{{ t('admin.dashboard.granularity') }}:</span
              >
              <div class="w-28">
                <Select
                  v-model="granularity"
                  :options="granularityOptions"
                  @change="loadChartData"
                />
              </div>
            </div>
          </div>
        </div>

        <!-- Request-ID mode shows summary cards and records while hiding charts and skipping their retrieval. -->
        <div v-if="!filters.request_id" class="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <ModelDistributionChart
            v-model:metric="modelDistributionMetric"
            :model-stats="requestedModelStats"
            :loading="modelStatsLoading"
            :show-source-toggle="false"
            :show-metric-toggle="true"
            :enable-breakdown="false"
            :show-provider-cost="false"
            :show-standard-cost="false"
            :start-date="startDate"
            :end-date="endDate"
          />
          <GroupDistributionChart
            v-model:metric="groupDistributionMetric"
            :group-stats="groupStats"
            :loading="chartsLoading"
            :show-metric-toggle="true"
            :enable-breakdown="false"
            :show-provider-cost="false"
            :show-standard-cost="false"
            :start-date="startDate"
            :end-date="endDate"
          />
        </div>

        <div v-if="!filters.request_id" class="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <EndpointDistributionChart
            v-model:source="endpointDistributionSource"
            v-model:metric="endpointDistributionMetric"
            :endpoint-stats="inboundEndpointStats"
            :upstream-endpoint-stats="upstreamEndpointStats"
            :endpoint-path-stats="endpointPathStats"
            :loading="endpointStatsLoading"
            :show-source-toggle="false"
            :show-metric-toggle="true"
            :enable-breakdown="false"
            :show-standard-cost="false"
            :title="t('usage.endpointDistribution')"
            :start-date="startDate"
            :end-date="endDate"
          />
          <TokenUsageTrend
            :trend-data="trendData"
            :loading="chartsLoading"
            :granularity="granularity"
            :show-standard-cost="false"
          />
        </div>

        <div v-if="isTeamOwner && !filters.request_id" data-tour="team-member-usage-charts">
          <TeamMemberUsageCharts :series="teamMemberSeries" :loading="teamChartsLoading" />
        </div>
      </div>

      <div class="card p-4">
        <div class="flex items-center justify-between gap-2">
          <FilterDropdown
            :active-count="activeFilterCount"
            keep-mounted
            @reset="resetCurrentFilters"
          >
            <!-- The request identifier filters usage and errors together, and removes date bounds from both queries. -->
            <RequestIdFilterField v-model="filters.request_id" @search="applyFilters" />
            <template v-if="activeTab === 'errors'">
              <FilterField :label="t('usage.errors.keyName')" icon="key">
                <FilterOptions
                  v-model="errorFilter.api_key_id"
                  :options="errorKeyOptions"
                  @change="applyErrorFilters"
                />
              </FilterField>
              <FilterField :label="t('usage.errors.model')" icon="cube">
                <FilterOptions
                  v-model="errorFilter.model"
                  :options="errorModelOptions"
                  searchable
                  creatable
                  :search-placeholder="t('usage.errors.modelPlaceholder')"
                  @change="applyErrorFilters"
                >
                  <template #icon="{ value }">
                    <ModelIcon :model="String(value)" size="16px" hide-fallback />
                  </template>
                </FilterOptions>
              </FilterField>
              <FilterField :label="t('usage.errors.category')" icon="tag">
                <FilterOptions
                  v-model="errorFilter.category"
                  :options="errorCategoryOptions"
                  @change="applyErrorFilters"
                />
              </FilterField>
              <FilterField :label="t('usage.errors.status')" icon="exclamationCircle">
                <FilterOptions
                  v-model="errorFilter.status_code"
                  :options="errorStatusOptions"
                  @change="applyErrorFilters"
                />
              </FilterField>
            </template>
            <template v-else>
              <FilterField :label="t('usage.apiKeyFilter')" icon="key">
                <FilterOptions
                  v-model="filters.api_key_id"
                  :options="apiKeyOptions"
                  @change="applyFilters"
                />
              </FilterField>
              <FilterField :label="t('usage.model')" icon="cube">
                <FilterOptions
                  v-model="filters.model"
                  :options="modelOptions"
                  searchable
                  @change="applyFilters"
                >
                  <template #icon="{ value }">
                    <ModelIcon :model="String(value)" size="16px" hide-fallback />
                  </template>
                </FilterOptions>
              </FilterField>
              <FilterField :label="t('admin.usage.group')" icon="folder">
                <FilterOptions
                  v-model="filters.group_id"
                  :options="groupOptions"
                  searchable
                  @change="applyFilters"
                >
                  <template #icon="{ option }">
                    <ProviderIcon
                      v-if="option?.brand"
                      :brand="String(option.brand)"
                      size="16px"
                      hide-fallback
                    />
                  </template>
                </FilterOptions>
              </FilterField>
              <FilterField :label="t('usage.type')" icon="bolt">
                <FilterOptions
                  v-model="filters.request_type"
                  :options="requestTypeOptions"
                  @change="applyFilters"
                />
              </FilterField>
              <FilterField :label="t('admin.usage.billingType')" icon="creditCard">
                <FilterOptions
                  v-model="filters.billing_type"
                  :options="billingTypeOptions"
                  @change="applyFilters"
                />
              </FilterField>
              <FilterField :label="t('admin.usage.billingMode')" icon="calculator">
                <FilterOptions
                  v-model="filters.billing_mode"
                  :options="billingModeOptions"
                  @change="applyFilters"
                />
              </FilterField>
              <FilterField :label="t('usage.compactionFilter')" icon="minimize">
                <FilterOptions
                  v-model="filters.native_compaction_v2"
                  :options="compactionOptions"
                  @change="applyFilters"
                />
              </FilterField>
            </template>
          </FilterDropdown>

          <div class="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              @click="refreshData"
              :disabled="activeTab === 'errors' ? errorLoading : loading"
              class="btn btn-secondary btn-icon"
              :title="t('common.refresh')"
            >
              <Icon
                name="refresh"
                size="sm"
                :class="(activeTab === 'errors' ? errorLoading : loading) ? 'animate-spin' : ''"
              />
            </button>
            <div class="relative" ref="columnDropdownRef">
              <button
                type="button"
                @click="showColumnDropdown = !showColumnDropdown"
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('admin.users.columnSettings')"
              >
                <Icon name="grid" size="sm" />
                <span class="hidden">{{ t('admin.users.columnSettings') }}</span>
              </button>
              <MotionTransition name="dropdown-fade">
                <div
                  v-if="showColumnDropdown"
                  :inert="!showColumnDropdown || undefined"
                  class="dropdown right-0 top-full z-50 mt-1 max-h-menu w-48 overflow-y-auto"
                >
                  <button
                    v-for="col in currentToggleableColumns"
                    :key="col.key"
                    type="button"
                    @click="toggleCurrentColumn(col.key)"
                    class="dropdown-item justify-between"
                  >
                    <span>{{ col.label }}</span>
                    <Icon
                      v-if="isCurrentColumnVisible(col.key)"
                      name="check"
                      size="sm"
                      class="text-primary-500"
                      :animate-on-hover="false"
                    />
                  </button>
                </div>
              </MotionTransition>
            </div>
            <IpGeoBatchToolbar
              v-if="activeTab === 'usage'"
              :ips="usageLogs.map((row) => row.ip_address)"
              inline
              @failed="handleIpGeoBatchFailed"
            />
            <button
              v-if="activeTab !== 'errors'"
              type="button"
              @click="exportToCSV"
              :disabled="exporting"
              class="btn btn-primary whitespace-nowrap px-3 sm:px-4"
            >
              {{ exporting ? t('usage.exporting') : t('usage.exportCsv') }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="errorViewEnabled" class="flex gap-2 border-b border-gray-200 dark:border-dark-700">
        <button
          class="tab"
          :class="{ 'tab-active': activeTab === 'usage' }"
          @click="activeTab = 'usage'"
        >
          {{ t('usage.tabs.usage') }}
        </button>
        <button
          class="tab"
          :class="{ 'tab-active': activeTab === 'errors' }"
          @click="switchToErrors"
        >
          {{ t('usage.tabs.errors') }}
        </button>
      </div>

      <!-- The shared card owns table and pagination borders and corner clipping. -->
      <div
        v-if="activeTab === 'usage'"
        v-content-reveal
        class="card overflow-hidden"
        data-tour="team-usage-records"
      >
        <UsageTable
          flat
          column-order-storage-key="user-usage-column-order"
          :data="usageLogs"
          :loading="loading"
          :columns="visibleColumns"
          :server-side-sort="true"
          :show-provider-billing="false"
          :show-standard-cost="false"
          :show-upstream-endpoint="false"
          :user-clickable="false"
          :compact-user-column="isTeamOwner"
          :show-ip-geo-toolbar="false"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        />

        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </div>

      <UserErrorRequestsTable
        v-else-if="errorViewEnabled"
        :rows="errorRows"
        :total="errorTotal"
        :loading="errorLoading"
        :page="errorPage"
        :page-size="errorPageSize"
        :visible-column-keys="errVisibleColumnKeys"
        @sort="onErrorSort"
        @update:page="onErrorPage"
        @update:pageSize="onErrorPageSize"
        @ipGeoBatchFailed="handleIpGeoBatchFailed"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import RequestIdFilterField from '@/components/common/RequestIdFilterField.vue'
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { vContentReveal } from '@/directives/contentReveal'

import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { keysAPI, usageAPI, userGroupsAPI } from '@/api'
import {
  teamAPI,
  type TeamAPIKey,
  type TeamContext,
  type TeamMembership,
  type TeamUsageSummary,
} from '@/api/team'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import {
  BILLING_MODE_ICONS,
  BILLING_TYPE_ICONS,
  COMPACTION_ICONS,
  ERROR_CATEGORY_ICONS,
  REQUEST_TYPE_ICONS,
  withOptionIcons,
} from '@/components/common/filterOptionIcons'
import ModelIcon from '@/components/common/ModelIcon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import UsageStatsCards from '@/components/admin/usage/UsageStatsCards.vue'
import UsageTable from '@/components/admin/usage/UsageTable.vue'
import IpGeoBatchToolbar from '@/components/common/IpGeoBatchToolbar.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import GroupDistributionChart from '@/components/charts/GroupDistributionChart.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import TeamMemberUsageCharts from '@/components/charts/TeamMemberUsageCharts.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import UserErrorRequestsTable from '@/components/user/UserErrorRequestsTable.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatReasoningEffortMapping } from '@/utils/format'
import {
  getBillingModeLabel,
  getDisplayBillingMode as resolveDisplayBillingMode,
} from '@/utils/billingMode'
import { resolveUsageRequestType, requestTypeToLegacyStream } from '@/utils/usageRequestType'
import { calculateUsageTps } from '@/utils/usageTps'
import type {
  EndpointStat,
  GroupStat,
  ModelStat,
  TrendDataPoint,
  UsageLog,
  UsageQueryParams,
  UsageRequestType,
  UsageStatsResponse,
  UserErrorRequest,
} from '@/api/usage.types'
import type { Column } from '@/components/common/table/types'
import { COMMON_ERROR_STATUS_CODES } from '@/utils/errorBadges'

// UsageView.vue loads personal and team usage with summaries, charts, error records, and
// CSV export. Nullable selector values are normalized before querying, and request-ID
// searches omit date limits while skipping distribution queries. Team owners receive the
// member column and aggregate member history.
//
// Usage lists cancel superseded requests, and statistics loaders compare request sequences
// before publishing results. Error retrieval checks the public visibility setting before
// sending, with separate pagination and preferences. Locale refresh reloads visible errors
// or clears inactive rows for the next visit. Export uses the user API and user-visible
// fields, and browser column preferences remain separate from administrator settings.

// Translation and application notifications provide labels and request feedback for usage
// records, filters, and export actions.
const { t } = useI18n()
// Export headers identify account charges and standard costs with their respective units.
const { balanceUnitName, usdUnitName } = useBalanceDisplay()
const appStore = useAppStore()

// DistributionMetric selects token count or actual charge for distribution charts.
type DistributionMetric = 'tokens' | 'actual_cost'
// EndpointSource selects normalized inbound, upstream, or raw-path endpoint statistics.
type EndpointSource = 'inbound' | 'upstream' | 'path'

// Usage display state stores the current summary and page separately from trend and
// distribution responses used by charts.
const usageStats = ref<UsageStatsResponse | null>(null)
const usageLogs = ref<UsageLog[]>([])
const trendData = ref<TrendDataPoint[]>([])
const requestedModelStats = ref<ModelStat[]>([])
const groupStats = ref<GroupStat[]>([])
const inboundEndpointStats = ref<EndpointStat[]>([])
const upstreamEndpointStats = ref<EndpointStat[]>([])
const endpointPathStats = ref<EndpointStat[]>([])
// Team display state retains current membership, fetched members, and aggregate series used
// by the owner-only member usage charts.
const teamContext = ref<TeamContext | null>(null)
const teamMembers = ref<TeamMembership[]>([])
const teamMemberSeries = ref<
  Array<{
    userID: number // member actor user identifier
    label: string // member display name
    summary: TeamUsageSummary // aggregate member usage summary
  }>
>([])

// Loading flags distinguish the records table, chart requests, and export operation so each
// section can render its own progress.
const loading = ref(false)
const chartsLoading = ref(false)
const modelStatsLoading = ref(false)
const endpointStatsLoading = ref(false)
const teamChartsLoading = ref(false)
const exporting = ref(false)
// Error request state owns separate paging, sort, filters, loading, and totals so switching
// tabs does not reuse usage pagination.
const errorRows = ref<UserErrorRequest[]>([])
const errorLoading = ref(false)
const errorPage = ref(1)
const errorPageSize = ref(20)
const errorSortBy = ref('created_at')
const errorSortOrder = ref<'asc' | 'desc'>('desc')
const errorTotal = ref(0)
const errorFilter = ref<{
  // model stores null or empty text for an unfiltered model; query builders omit that value
  // before requesting records.
  model: string | null
  category: string // selected backend error category
  // api_key_id stores null for the unfiltered selector; request builders turn null into an
  // omitted API predicate.
  api_key_id: number | null
  status_code: number | null // optional error HTTP status
}>({
  model: '',
  category: '',
  api_key_id: null,
  status_code: null,
})
// activeFilterCount counts nonempty filters for the selected tab and includes the shared
// request identifier when counting error predicates.
const activeFilterCount = computed(() => {
  const source = activeTab.value === 'errors' ? errorFilter.value : filters.value
  const count = Object.entries(source).filter(
    ([key, value]) =>
      !['start_date', 'end_date'].includes(key) &&
      value !== null &&
      value !== undefined &&
      String(value) !== '',
  ).length
  return activeTab.value === 'errors' && filters.value.request_id ? count + 1 : count
})

// errorKeyOptions uses the same loaded personal and team key identities while providing an
// unfiltered error-list choice.
const errorKeyOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allKeys') },
  ...apiKeys.value.map((k) => ({ value: k.id, label: k.name })),
])

// errorModelOptions deduplicates model names observed in current error rows; the selector
// also permits arbitrary text for server matching.
const errorModelOptions = computed<SelectOption[]>(() => {
  const seen = new Set<string>()
  const opts: SelectOption[] = []
  for (const r of errorRows.value) {
    if (r.model && !seen.has(r.model)) {
      seen.add(r.model)
      opts.push({ value: r.model, label: r.model })
    }
  }
  return opts
})

// errorCategoryCodes lists queryable server error categories used to build translated
// filter options for the error tab.
const errorCategoryCodes = [
  'auth',
  'rate_limit',
  'quota',
  'invalid_request',
  'service_unavailable',
  'upstream',
  'internal',
  'cyber',
]

// errorCategoryOptions translates queryable error categories and supplies the empty string
// that clears the selected category predicate.
const errorCategoryOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: '', label: t('usage.errors.allCategories') },
      ...errorCategoryCodes.map((c) => ({ value: c, label: t('usage.errors.categories.' + c) })),
    ],
    ERROR_CATEGORY_ICONS,
  ),
)

// errorStatusOptions uses common status codes shared with administrator filters, keeping
// codes selectable even when absent from the current page.
const errorStatusOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allStatuses') },
  ...COMMON_ERROR_STATUS_CODES.map((c) => ({ value: c, label: String(c) })),
])

// applyErrorFilters resets error pagination before loading the selected predicates.
const applyErrorFilters = () => {
  errorPage.value = 1
  void loadErrors()
}

// abortController owns the latest usage-list request; sequence counters independently
// identify current statistics and chart requests.
let abortController: AbortController | null = null
let chartReqSeq = 0
let statsReqSeq = 0
let modelStatsReqSeq = 0
let teamStatsReqSeq = 0

// formatLocalDate formats browser-local calendar components for date input fields.
const formatLocalDate = (date: Date): string =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`

// getGranularityForRange uses hourly buckets for local-midnight endpoints at most one day
// apart. Longer or invalid ranges select daily buckets through the final comparison result
// used by the chart.
const getGranularityForRange = (start: string, end: string): 'day' | 'hour' => {
  const startTime = new Date(`${start}T00:00:00`).getTime()
  const endTime = new Date(`${end}T00:00:00`).getTime()
  // Round partial elapsed days upward to choose hourly or daily chart
  // buckets.
  return Math.ceil((endTime - startTime) / (1000 * 60 * 60 * 24)) <= 1 ? 'hour' : 'day'
}

// defaultDate initializes both endpoints to the same browser-local calendar day,
// so the initial records and charts cover today.
const defaultDate = formatLocalDate(new Date())
// Date state supplies the selected calendar range to filters and determines trend
// granularity when the range changes.
const startDate = ref(defaultDate)
const endDate = ref(defaultDate)
// granularity selects hourly or daily trend buckets, while distribution selectors choose
// the displayed metric and endpoint source.
const granularity = ref<'day' | 'hour'>(getGranularityForRange(startDate.value, endDate.value))

// Distribution selectors keep model, group, and endpoint metrics independent while
// activeTab selects usage records or errors.
const modelDistributionMetric = ref<DistributionMetric>('tokens')
const groupDistributionMetric = ref<DistributionMetric>('tokens')
const endpointDistributionMetric = ref<DistributionMetric>('tokens')
const endpointDistributionSource = ref<EndpointSource>('inbound')
const activeTab = ref<'usage' | 'errors'>('usage')
// errorViewEnabled requires the public error-visibility setting to be true; a missing
// setting keeps error requests disabled.
const errorViewEnabled = computed(
  () => appStore.cachedPublicSettings?.allow_user_view_error_requests ?? false,
)

// UsageFilterState retains null for unselected dropdown values until normalizedFilters
// removes empty query predicates.
type UsageFilterState = Omit<
  UsageQueryParams,
  'api_key_id' | 'group_id' | 'model' | 'request_type'
> & {
  // api_key_id stores null for the unfiltered selector; request builders turn null into an
  // omitted API predicate.
  api_key_id: number | null
  // group_id stores null for the unfiltered selector and becomes undefined when
  // normalizedFilters prepares API query values.
  group_id: number | null
  // model stores null or empty text for an unfiltered model; query builders omit that value
  // before requesting records.
  model: string | null
  // request_type stores null for all modes; normalizedFilters removes null and derives
  // compatible legacy stream values for selected modes.
  request_type: UsageRequestType | null
}

// filters owns the mutable usage query selection; page changes and exports combine these
// values with sorting and paging.
const filters = ref<UsageFilterState>({
  start_date: startDate.value,
  end_date: endDate.value,
  api_key_id: null,
  group_id: null,
  model: null,
  request_type: null,
  billing_type: null,
  billing_mode: null,
  native_compaction_v2: null,
})

// pagination holds the current one-based page, persisted page size, and most recently
// returned matching-record total.
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
})
// sortState holds the server sort column and direction used consistently by displayed pages
// and exports.
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc',
})

// granularityOptions supplies translated daily and hourly choices for the trend chart
// bucket-size selector in this page.
const granularityOptions = computed<SelectOption[]>(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') },
])
// requestTypeOptions lists canonical usage request modes plus the null selection that
// clears mode filtering in the query.
const requestTypeOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allTypes') },
      { value: 'ws_v2', label: t('usage.ws') },
      { value: 'live', label: t('usage.live') },
      { value: 'stream', label: t('usage.stream') },
      { value: 'sync', label: t('usage.sync') },
    ],
    REQUEST_TYPE_ICONS,
  ),
)
// billingTypeOptions distinguishes balance and subscription usage while retaining null for
// the unfiltered dropdown selection.
const billingTypeOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allBillingTypes') },
      { value: 0, label: t('admin.usage.billingTypeBalance') },
      { value: 1, label: t('admin.usage.billingTypeSubscription') },
    ],
    BILLING_TYPE_ICONS,
  ),
)
// billingModeOptions supplies translated token, per-request, image, and video choices for
// the shared usage filter controls.
const billingModeOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('admin.usage.allBillingModes') },
      { value: 'token', label: t('admin.usage.billingModeToken') },
      { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
      { value: 'image', label: t('admin.usage.billingModeImage') },
      { value: 'video', label: t('admin.usage.billingModeVideo') },
    ],
    BILLING_MODE_ICONS,
  ),
)
// compactionOptions distinguishes native and legacy compaction and supplies a null choice
// for requests without that predicate.
const compactionOptions = computed<SelectOption[]>(() =>
  withOptionIcons(
    [
      { value: null, label: t('usage.allCompactions') },
      { value: true, label: t('usage.nativeCompactionV2') },
      { value: false, label: t('usage.legacyCompaction') },
    ],
    COMPACTION_ICONS,
  ),
)

// UsageKeyOption keeps key identity and name for the usage selectors.
type UsageKeyOption = Pick<TeamAPIKey, 'id' | 'name'>
// UsageGroupOption keeps group identity, display name, and display brand for usage
// selectors; the brand draws the group's filter-row icon.
type UsageGroupOption = {
  id: number // selector record numeric identity
  name: string // selector option display name
  brand?: string // group badge brand, absent for team key groups
}
// Filter option state combines available personal and team keys, their groups, and model
// names accumulated from statistics.
const apiKeys = ref<UsageKeyOption[]>([])
const groups = ref<UsageGroupOption[]>([])
const modelOptionValues = ref<string[]>([])
// isTeamOwner enables member attribution and member charts when the current team membership
// carries the owner role.
const isTeamOwner = computed(() => teamContext.value?.membership.role === 'owner')

// apiKeyOptions prepends the unfiltered selection to the merged personal and team key
// choices for the usage selector.
const apiKeyOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.allApiKeys') },
  ...apiKeys.value.map((key) => ({ value: key.id, label: key.name })),
])
// groupOptions prepends the unfiltered selection to available personal and team group
// choices using their loaded display names and brands.
const groupOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allGroups') },
  ...groups.value.map((group) => ({ value: group.id, label: group.name, brand: group.brand })),
])
// modelOptions prepends the unfiltered selection to accumulated model names while
// preserving the selected model after later refreshes.
const modelOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allModels') },
  ...modelOptionValues.value.map((model) => ({ value: model, label: model })),
])

// normalizedFilters converts UI null selections to omitted query values and resolves
// compatible stream mode. Request-ID queries remove dates, while ordinary queries use the
// currently displayed date range.
const normalizedFilters = computed<UsageQueryParams>(() => {
  const requestType = filters.value.request_type
  // Translate canonical request mode through the shared compatibility map; modes without a
  // legacy stream equivalent omit that older predicate.
  const legacyStream = requestType ? requestTypeToLegacyStream(requestType) : filters.value.stream
  return {
    ...filters.value,
    api_key_id: filters.value.api_key_id ?? undefined,
    group_id: filters.value.group_id ?? undefined,
    model: filters.value.model || undefined,
    request_type: requestType ?? undefined,
    start_date: filters.value.request_id ? undefined : startDate.value,
    end_date: filters.value.request_id ? undefined : endDate.value,
    stream: legacyStream === null ? undefined : legacyStream,
  }
})

// buildUsageListParams combines pagination, normalized filters, and the current server
// sort. Displayed pages and CSV retrieval share this builder so their query selection and
// ordering follow the same rules.
const buildUsageListParams = (page: number, pageSize: number): UsageQueryParams => ({
  page,
  page_size: pageSize,
  ...normalizedFilters.value,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order,
})

// loadLogs replaces the usage page using the current filters and sort order. Starting a new
// load aborts the previous controller before installing a new owner for the loading flag.
//
// A response publishes rows and total only while its signal remains active. Finalization
// checks controller identity so a superseded request cannot hide the loading indicator of a
// newer request. The error path retains previously displayed rows, with cancellation and
// ordinary failures handled by the page's notification policy.
const loadLogs = async () => {
  // Abort the previous list owner before installing the new controller so late completion
  // can identify that it no longer owns publication.
  abortController?.abort()
  const controller = new AbortController()
  abortController = controller
  loading.value = true
  try {
    const res = await usageAPI.query(buildUsageListParams(pagination.page, pagination.page_size), {
      signal: controller.signal,
    })
    if (!controller.signal.aborted) {
      usageLogs.value = res.items
      pagination.total = res.total
    }
  } catch (error: any) {
    // AbortError and client cancellation codes identify superseded work; other failures
    // notify the user while retaining the previously displayed usage records.
    if (error?.name !== 'AbortError' && error?.code !== 'ERR_CANCELED') {
      appStore.showError(t('usage.failedToLoad'))
    }
  } finally {
    if (abortController === controller) loading.value = false
  }
}

// loadStats retrieves the user summary and endpoint totals under normalized filters. Each
// invocation advances a sequence before awaiting the API so only the latest requested
// summary can publish results.
//
// Current success replaces summary and inbound endpoint counts, clearing unsupported
// upstream and raw-path arrays. Current failure clears endpoint arrays but retains the
// earlier summary. The same sequence protects the loading flag, preventing a late
// completion from ending progress for a newer summary request.
const loadStats = async () => {
  // Capture summary ownership before fetching so current statistics and
  // endpoint arrays publish together, with loading cleanup under the same
  // generation.
  const seq = ++statsReqSeq
  endpointStatsLoading.value = true
  try {
    const stats = await usageAPI.getStats(normalizedFilters.value)
    if (seq !== statsReqSeq) return
    usageStats.value = stats
    inboundEndpointStats.value = stats.endpoints || []
    upstreamEndpointStats.value = []
    endpointPathStats.value = []
  } catch (error) {
    // Only the newest failed summary clears endpoint arrays; the earlier summary total
    // remains visible until another successful response replaces it.
    if (seq !== statsReqSeq) return
    console.error('Failed to load usage stats:', error)
    inboundEndpointStats.value = []
    upstreamEndpointStats.value = []
    endpointPathStats.value = []
  } finally {
    if (seq === statsReqSeq) endpointStatsLoading.value = false
  }
}

// loadModelStats retrieves requested-model statistics unless a request identifier is
// selected. It advances the model sequence before fetching and preserves displayed
// statistics until the current response settles.
//
// Success replaces model rows and merges their names into filter choices only when its
// sequence remains current. Failure clears rows under the same condition, and finalization
// likewise checks ownership before releasing progress. A superseded response therefore
// cannot overwrite the currently selected filter's model chart or option accumulation.
const loadModelStats = async () => {
  if (filters.value.request_id?.trim()) return
  // Capture model ownership before fetching so a superseded response cannot
  // publish rows, extend filter choices, or clear newer loading state.
  const seq = ++modelStatsReqSeq
  modelStatsLoading.value = true
  try {
    const response = await usageAPI.getDashboardModels({
      ...normalizedFilters.value,
      model_source: 'requested',
    })
    if (seq !== modelStatsReqSeq) return
    requestedModelStats.value = response.models || []
    refreshModelOptions(response.models || [])
  } catch (error) {
    // Only the newest failed model request clears its rows, while accumulated model
    // choices remain available for selecting another filter.
    if (seq !== modelStatsReqSeq) return
    console.error('Failed to load model stats:', error)
    requestedModelStats.value = []
  } finally {
    if (seq === modelStatsReqSeq) modelStatsLoading.value = false
  }
}

// loadChartData fetches trend and group distributions under normalized filters unless
// request-ID mode hides charts. Model statistics are excluded because their separate loader
// owns that data and its options.
//
// Each request captures a sequence and compares it before publishing success, clearing rows
// after failure, or releasing progress. This makes the most recently started chart request
// authoritative when filters change during retrieval. Current failures clear both chart
// arrays; superseded failures leave the newer selection untouched.
const loadChartData = async () => {
  if (filters.value.request_id?.trim()) return
  // Capture chart ownership before fetching so trend and group arrays publish
  // for the same current request that releases loading.
  const seq = ++chartReqSeq
  chartsLoading.value = true
  try {
    const snapshot = await usageAPI.getDashboardSnapshotV2({
      ...normalizedFilters.value,
      granularity: granularity.value,
      include_trend: true,
      include_model_stats: false,
      include_group_stats: true,
    })
    if (seq !== chartReqSeq) return
    trendData.value = snapshot.trend || []
    groupStats.value = snapshot.groups || []
  } catch (error) {
    // Clear trend and group arrays only for the current failed request so older failures
    // cannot erase a newer chart response.
    if (seq !== chartReqSeq) return
    console.error('Failed to load chart data:', error)
    trendData.value = []
    groupStats.value = []
  } finally {
    if (seq === chartReqSeq) chartsLoading.value = false
  }
}

// refreshModelOptions merges observed model names with existing choices and the current
// selection. Sorting a new set retains selected or previously observed names even when a
// filtered response omits them.
const refreshModelOptions = (models: ModelStat[]) => {
  const current = filters.value.model
  const set = new Set(modelOptionValues.value)
  models.forEach((item) => {
    if (item.model) set.add(item.model)
  })
  if (current) set.add(current)
  modelOptionValues.value = Array.from(set).sort()
}

// applyFilters resets usage pagination and refreshes records, summaries, distributions, and
// owner member charts. It also resets error rows, loading immediately only when the error
// tab is active and permitted.
const applyFilters = () => {
  pagination.page = 1
  void loadLogs()
  void loadStats()
  void loadModelStats()
  void loadChartData()
  void loadTeamMemberUsage()
  resetErrorRows()
}

// resetCurrentFilters clears predicates belonging to the current tab while retaining the
// selected dates. Clearing a shared request identifier also reapplies usage filters so
// previously skipped charts can reload.
const resetCurrentFilters = () => {
  if (activeTab.value === 'errors') {
    errorFilter.value = { model: '', category: '', api_key_id: null, status_code: null }
    if (filters.value.request_id) {
      filters.value.request_id = undefined
      applyFilters()
      return
    }
    applyErrorFilters()
    return
  }
  filters.value = {
    ...filters.value,
    request_id: undefined,
    api_key_id: null,
    group_id: null,
    model: null,
    request_type: null,
    billing_type: null,
    billing_mode: null,
    native_compaction_v2: null,
  }
  applyFilters()
}

// refreshData reloads usage records and statistics under the current selection. It also
// requests owner member charts and active error rows; each loader owns its visibility guard
// and completion state.
const refreshData = () => {
  void loadLogs()
  void loadStats()
  void loadModelStats()
  void loadChartData()
  void loadTeamMemberUsage()
  if (activeTab.value === 'errors') void loadErrors()
}

// onDateRangeChange synchronizes the displayed dates with query filters and chooses trend
// granularity. It then applies filters, resetting record pagination before dependent usage
// requests start using the new range.
const onDateRangeChange = (range: {
  startDate: string // beginning calendar date
  endDate: string // ending calendar date
  preset: string | null // optional date preset identifier
}) => {
  startDate.value = range.startDate
  endDate.value = range.endDate
  filters.value.start_date = range.startDate
  filters.value.end_date = range.endDate
  granularity.value = getGranularityForRange(range.startDate, range.endDate)
  applyFilters()
}

// handlePageChange selects a usage page and reloads its records.
const handlePageChange = (page: number) => {
  pagination.page = page
  void loadLogs()
}

// handlePageSizeChange updates the record count and reloads the first page. Resetting
// pagination prevents an old page index from pointing beyond the newly sized sequence of
// usage pages.
const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  void loadLogs()
}

// handleSort stores the requested server ordering and reloads page one. Returning to the
// first page gives the selected ordering a consistent starting point for the usage table.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  void loadLogs()
}

// handleIpGeoBatchFailed reports the shared batch IP lookup failure notification.
const handleIpGeoBatchFailed = () => {
  appStore.showError(t('usage.ipGeo.batchFailed'))
}

// getRequestTypeExportText translates the resolved request mode for a CSV cell. Unknown
// modes use a translated unknown label after the shared resolver handles historical stream
// and request-type fields.
const getRequestTypeExportText = (log: UsageLog): string => {
  const requestType = resolveUsageRequestType(log)
  if (requestType === 'cyber') return t('usage.cyber')
  if (requestType === 'live') return t('usage.live')
  if (requestType === 'ws_v2') return t('usage.ws')
  if (requestType === 'stream') return t('usage.stream')
  if (requestType === 'sync') return t('usage.sync')
  return t('usage.unknown')
}

// getDisplayBillingMode delegates billing-mode selection to the shared usage resolver.
// Historical rows can derive image mode from image count, and callers receive the
// resolver's nullable result when mode remains unknown.
const getDisplayBillingMode = (
  row: Pick<UsageLog, 'billing_mode' | 'image_count'> | null | undefined,
): string | null | undefined => resolveDisplayBillingMode(row)

// escapeCSVValue serializes one value for the CSV writer, returning empty text for nullish
// values. Quotes are doubled, and fields containing CSV separators or line breaks receive
// enclosing quotes.
//
// Values beginning with spreadsheet formula prefixes receive a quoted apostrophe prefix
// before export. This retains the displayed cell text as text when common spreadsheet
// readers would otherwise evaluate it. The transformation is applied to every data cell and
// header before rows are joined.
const escapeCSVValue = (value: unknown): string => {
  if (value == null) return ''
  const str = String(value)
  // Escape quotes before adding CSV enclosure or the spreadsheet text prefix so embedded
  // delimiters remain part of the same cell value.
  const escaped = str.replace(/"/g, '""')
  if (/^[=+\-@\t\r]/.test(str)) return `"\'${escaped}"`
  if (/[,"\n\r]/.test(str)) return `"${escaped}"`
  return str
}

// exportToCSV retrieves all expected usage pages and downloads their user-facing columns as
// UTF-8 CSV. A zero displayed total returns with a warning, and each page uses the shared
// filter and sort builder.
//
// The initial total determines page count, while returned rows supply actual cell data.
// Text escapes prevent delimiters and formula prefixes from changing spreadsheet
// interpretation. Costs, request mode, and TPS follow their owning user fields and shared
// formatters. A temporary Blob URL triggers the download and is revoked afterward;
// settlement clears export progress. Monetary headers identify account units and USD
// costs while each data cell retains its recorded numeric precision.
const exportToCSV = async () => {
  if (pagination.total === 0) {
    appStore.showWarning(t('usage.noDataToExport'))
    return
  }
  exporting.value = true
  appStore.showInfo(t('usage.preparingExport'))
  try {
    const allLogs: UsageLog[] = []
    const pageSize = 100
    // Round upward to include a final partially filled export page.
    const totalPages = Math.ceil(pagination.total / pageSize)
    for (let page = 1; page <= totalPages; page++) {
      const response = await usageAPI.query(buildUsageListParams(page, pageSize))
      allLogs.push(...response.items)
    }
    if (allLogs.length === 0) {
      appStore.showWarning(t('usage.noDataToExport'))
      return
    }
    const headers = [
      t('usage.time'),
      t('usage.apiKeyFilter'),
      t('usage.model'),
      t('usage.reasoningEffort'),
      t('usage.inboundEndpoint'),
      t('admin.usage.ipAddress'),
      t('usage.type'),
      t('admin.usage.billingMode'),
      t('admin.usage.inputTokens'),
      t('admin.usage.outputTokens'),
      t('admin.usage.cacheReadTokens'),
      t('admin.usage.cacheCreationTokens'),
      t('usage.rate'),
      `${t('usage.userBilled')} (${balanceUnitName.value})`,
      `${t('usage.original')} (${usdUnitName})`,
      t('usage.firstToken'),
      t('usage.ttftMode'),
      t('usage.duration'),
      t('usage.tpsExport'),
    ]
    const rows = allLogs.map((log) =>
      [
        log.created_at,
        log.api_key?.name || '',
        log.model,
        formatReasoningEffortMapping(log.requested_reasoning_effort, log.reasoning_effort),
        log.inbound_endpoint || '',
        log.ip_address || '',
        getRequestTypeExportText(log),
        getBillingModeLabel(getDisplayBillingMode(log), t),
        log.input_tokens,
        log.output_tokens,
        log.cache_read_tokens,
        log.cache_creation_tokens,
        log.rate_multiplier,
        log.actual_cost.toFixed(8),
        log.total_cost.toFixed(8),
        log.ttft_ms ?? '',
        log.ttft_mode === 'first_event'
          ? t('usage.latencyFirstEvent')
          : log.ttft_mode === 'first_output'
            ? t('usage.latencyFirstToken')
            : '',
        log.duration_ms ?? '',
        calculateUsageTps(log)?.toFixed(1) ?? '',
      ].map(escapeCSVValue),
    )
    const csvContent = [
      headers.map(escapeCSVValue).join(','),
      ...rows.map((row) => row.join(',')),
    ].join('\n')
    // Prefix UTF-8 data for spreadsheet decoding, then create a temporary URL used by the
    // download anchor before releasing that URL.
    const blob = new Blob(['\uFEFF' + csvContent], { type: 'text/csv;charset=utf-8;' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `usage_${startDate.value}_to_${endDate.value}.csv`
    link.click()
    window.URL.revokeObjectURL(url)
    appStore.showSuccess(t('usage.exportSuccess'))
  } catch (error) {
    // Report export failure and release busy state through finally; no success
    // notification is emitted for a partially constructed download.
    console.error('CSV Export failed:', error)
    appStore.showError(t('usage.exportFailed'))
  } finally {
    exporting.value = false
  }
}

// Usage column constants define mandatory fields, default hidden choices, and the browser
// storage namespace for saved preferences.
const ALWAYS_VISIBLE = ['created_at']
const DEFAULT_HIDDEN_COLUMNS = ['user_agent']
const HIDDEN_COLUMNS_KEY = 'user-usage-hidden-columns'

// allColumns supplies the canonical translated usage column order before mandatory and
// saved visibility choices are applied.
const allColumns = computed<Column[]>(() => [
  ...(isTeamOwner.value
    ? [{ key: 'user', label: t('team.member'), sortable: false, class: 'w-36 min-w-36 max-w-36' }]
    : []),
  { key: 'api_key', label: t('usage.apiKeyFilter'), sortable: false },
  { key: 'model', label: t('usage.model'), sortable: true },
  { key: 'reasoning_effort', label: t('usage.reasoningEffort'), sortable: false },
  { key: 'endpoint', label: t('usage.endpoint'), sortable: false },
  { key: 'ip_address', label: 'IP', sortable: false },
  { key: 'group', label: t('admin.usage.group'), sortable: false },
  { key: 'stream', label: t('usage.type'), sortable: false },
  { key: 'billing_mode', label: t('admin.usage.billingMode'), sortable: false },
  { key: 'tokens', label: t('usage.tokens'), sortable: false },
  { key: 'cost', label: t('usage.cost'), sortable: false },
  { key: 'latency', label: t('usage.latency'), sortable: false },
  { key: 'tps', label: t('usage.tps'), sortable: false },
  { key: 'created_at', label: t('usage.time'), sortable: true },
  { key: 'request_id', label: t('requests.id'), sortable: false },
  { key: 'user_agent', label: t('usage.userAgent'), sortable: false },
])

// Usage visibility state keeps a mutable hidden-key set and derives the optional controls
// and displayed column definitions.
const hiddenColumns = reactive<Set<string>>(new Set())
const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => !ALWAYS_VISIBLE.includes(col.key)),
)
const visibleColumns = computed(() =>
  allColumns.value.filter((col) => ALWAYS_VISIBLE.includes(col.key) || !hiddenColumns.has(col.key)),
)
// isColumnVisible reports whether a column key is absent from saved hidden choices.
const isColumnVisible = (key: string) => !hiddenColumns.has(key)
// toggleColumn flips a usage column in memory before persisting the hidden set. Storage
// failures propagate from this action, while the current reactive selection has already
// been updated.
const toggleColumn = (key: string) => {
  if (hiddenColumns.has(key)) hiddenColumns.delete(key)
  else hiddenColumns.add(key)
  // Update reactive visibility before writing the browser preference so the current table
  // reflects the choice even if persistence throws.
  localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
}
// loadSavedColumns restores user usage hidden columns from browser storage. Missing
// preferences or parsing failures use the default hidden set so the table can render
// despite unavailable saved preferences.
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    const values = saved ? (JSON.parse(saved) as string[]) : DEFAULT_HIDDEN_COLUMNS
    values.forEach((key) => hiddenColumns.add(key))
  } catch {
    // Use the default usage hidden set if browser preferences are inaccessible or
    // malformed so the table can initialize its column controls.
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

// Error column constants define mandatory detail fields and a separate browser preference
// namespace from the usage table.
const ERR_ALWAYS_VISIBLE = ['status', 'created_at']
const ERR_DEFAULT_HIDDEN_COLUMNS = ['user_agent']
const ERR_HIDDEN_COLUMNS_KEY = 'user-usage-error-hidden-columns'

// errAllColumns supplies settings keys in the order understood by the error table that
// renders these records.
const errAllColumns = computed<Column[]>(() => [
  { key: 'key_name', label: t('usage.errors.keyName') },
  { key: 'model', label: t('usage.errors.model') },
  { key: 'endpoint', label: t('usage.errors.endpoint') },
  { key: 'client_ip', label: 'IP' },
  { key: 'group', label: t('admin.usage.group') },
  { key: 'type', label: t('usage.type') },
  { key: 'platform', label: t('usage.errors.platform') },
  { key: 'category', label: t('usage.errors.category') },
  { key: 'status', label: t('usage.errors.status') },
  { key: 'message', label: t('usage.errors.message') },
  { key: 'created_at', label: t('usage.errors.time') },
  { key: 'request_id', label: t('requests.id') },
  { key: 'user_agent', label: t('usage.userAgent') },
])

// Error visibility state stores its own hidden-key set so changing error columns does not
// alter usage-table preferences.
const errHiddenColumns = reactive<Set<string>>(new Set())
const errToggleableColumns = computed(() =>
  errAllColumns.value.filter((col) => !ERR_ALWAYS_VISIBLE.includes(col.key)),
)
const errVisibleColumnKeys = computed(() =>
  errAllColumns.value
    .filter((col) => ERR_ALWAYS_VISIBLE.includes(col.key) || !errHiddenColumns.has(col.key))
    .map((col) => col.key),
)
// isErrColumnVisible reports whether an error column is absent from hidden preferences.
const isErrColumnVisible = (key: string) => !errHiddenColumns.has(key)
// toggleErrColumn flips an error column in memory before persisting its hidden set. The
// separate storage key keeps these choices independent of usage columns; write failures
// propagate to the caller.
const toggleErrColumn = (key: string) => {
  if (errHiddenColumns.has(key)) errHiddenColumns.delete(key)
  else errHiddenColumns.add(key)
  // Persist error visibility under its own key after mutating the reactive set so usage-
  // column preferences remain independently owned.
  localStorage.setItem(ERR_HIDDEN_COLUMNS_KEY, JSON.stringify([...errHiddenColumns]))
}
// loadSavedErrColumns restores the separate error hidden-column preferences from browser
// storage. Missing values or parsing failures add the error defaults so the error tab can
// render with its normal selection.
const loadSavedErrColumns = () => {
  try {
    const saved = localStorage.getItem(ERR_HIDDEN_COLUMNS_KEY)
    const values = saved ? (JSON.parse(saved) as string[]) : ERR_DEFAULT_HIDDEN_COLUMNS
    values.forEach((key) => errHiddenColumns.add(key))
  } catch {
    // Use the separate error defaults when browser preferences are inaccessible or
    // malformed, leaving usage-column preferences independently owned.
    ERR_DEFAULT_HIDDEN_COLUMNS.forEach((key) => errHiddenColumns.add(key))
  }
}

// currentToggleableColumns selects the current tab's column controls while each table
// retains its own saved visibility state.
const currentToggleableColumns = computed(() =>
  activeTab.value === 'errors' ? errToggleableColumns.value : toggleableColumns.value,
)
// isCurrentColumnVisible checks the active tab's hidden set for the requested key.
const isCurrentColumnVisible = (key: string) =>
  activeTab.value === 'errors' ? isErrColumnVisible(key) : isColumnVisible(key)
// toggleCurrentColumn routes a visibility change to the selected tab's preference updater.
const toggleCurrentColumn = (key: string) => {
  if (activeTab.value === 'errors') toggleErrColumn(key)
  else toggleColumn(key)
}

// Column menu state owns the dropdown visibility and container used to recognize clicks
// outside its controls.
const showColumnDropdown = ref(false)
const columnDropdownRef = ref<HTMLElement | null>(null)
// handleColumnClickOutside closes the column menu when a document click lands outside its
// container. A missing container leaves visibility untouched, allowing the handler to run
// during mount transitions.
const handleColumnClickOutside = (event: MouseEvent) => {
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(event.target as HTMLElement)) {
    showColumnDropdown.value = false
  }
}

// loadFilterOptions combines personal keys and groups with available team data. Personal
// lookups start together; team context determines whether owner membership records are also
// requested. Missing team membership is treated as a personal-only result.
//
// Keys and groups are deduplicated by numeric identity, and team-key group names augment
// personal group choices. Unexpected team failures abort publication and are logged by the
// outer handler. After options publish, owner member usage loads through its separate
// scope-aware loader, retaining aggregate history supplied by that endpoint.
const loadFilterOptions = async () => {
  try {
    const [personalKeys, availableGroups] = await Promise.all([
      keysAPI.list(1, 100, { scope: 'personal' }),
      userGroupsAPI.getAvailable('personal', undefined, false),
    ])
    let teamKeys: TeamAPIKey[] = []
    try {
      teamContext.value = await teamAPI.current()
      const requests: [Promise<TeamAPIKey[]>, Promise<TeamMembership[]> | null] = [
        teamAPI.keys(),
        teamContext.value.membership.role === 'owner' ? teamAPI.members() : null,
      ]
      teamKeys = await requests[0]
      teamMembers.value = requests[1] ? await requests[1] : []
    } catch (error: any) {
      // Absence of a team is expected for personal accounts, so clear team state for known
      // membership failures and propagate other failures.
      const noTeam =
        error?.reason === 'TEAM_NOT_FOUND' ||
        error?.reason === 'TEAM_MEMBERSHIP_REQUIRED' ||
        error?.response?.status === 404
      if (!noTeam) throw error
      teamContext.value = null
      teamMembers.value = []
    }
    const uniqueKeys = new Map<number, UsageKeyOption>()
    for (const key of [...personalKeys.items, ...teamKeys])
      uniqueKeys.set(key.id, { id: key.id, name: key.name })
    apiKeys.value = [...uniqueKeys.values()]
    const uniqueGroups = new Map<number, UsageGroupOption>()
    for (const group of availableGroups)
      uniqueGroups.set(group.id, {
        id: group.id,
        name: group.display_name || group.name,
        brand: group.display_brand,
      })
    for (const key of teamKeys) {
      if (key.group_id && key.group_name)
        uniqueGroups.set(key.group_id, { id: key.group_id, name: key.group_name })
    }
    groups.value = [...uniqueGroups.values()]
    await loadTeamMemberUsage()
  } catch (error) {
    // Unexpected option-loading failure retains previously published choices; logging the
    // failure lets the rest of the usage page continue rendering.
    console.error('Failed to load usage filter options:', error)
  }
}

// loadTeamMemberUsage retrieves aggregate current and historical member usage for team
// owners. Request-ID selection skips the chart query, while a non-owner clears the
// displayed member series.
//
// Each eligible load advances a sequence before requesting the selected date range. Only
// the current sequence may publish mapped actor identities and summaries, clear rows after
// failure, or release progress. This keeps older date-range responses from replacing newer
// member history while preserving the backend's inclusion of departed members.
const loadTeamMemberUsage = async () => {
  if (filters.value.request_id?.trim()) return
  if (!isTeamOwner.value) {
    teamMemberSeries.value = []
    return
  }
  // Capture member request ownership before fetching so successful actor rows
  // and loading cleanup remain tied to the latest requested date range.
  const seq = ++teamStatsReqSeq
  teamChartsLoading.value = true
  try {
    const series = await teamAPI.memberUsage({
      from: startDate.value,
      to: endDate.value,
    })
    if (seq !== teamStatsReqSeq) return
    teamMemberSeries.value = series.map((member) => ({
      userID: member.actor_user_id,
      label: member.display_name,
      summary: member.summary,
    }))
  } catch (error) {
    // Only the newest failed member query clears the chart series so an old rejection
    // cannot erase later team history.
    if (seq !== teamStatsReqSeq) return
    console.error('Failed to load team member usage charts:', error)
    teamMemberSeries.value = []
  } finally {
    if (seq === teamStatsReqSeq) teamChartsLoading.value = false
  }
}

// resetErrorRows returns error pagination to page one after shared filter changes. Active
// errors reload through the visibility guard, while inactive rows and totals clear for the
// next tab visit.
const resetErrorRows = () => {
  errorPage.value = 1
  if (activeTab.value === 'errors') {
    void loadErrors()
  } else {
    errorRows.value = []
    errorTotal.value = 0
  }
}

// loadErrors retrieves the current user error page only when the public visibility setting
// is enabled. Request-ID filtering omits dates, error-specific filters retain their own
// values, and failures keep earlier rows visible.
const loadErrors = async () => {
  if (!errorViewEnabled.value) return
  errorLoading.value = true
  try {
    const resp = await usageAPI.listMyErrorRequests({
      page: errorPage.value,
      page_size: errorPageSize.value,
      request_id: filters.value.request_id?.trim() || undefined,
      start_date: filters.value.request_id ? undefined : startDate.value,
      end_date: filters.value.request_id ? undefined : endDate.value,
      model: (errorFilter.value.model ?? '').trim() || undefined,
      category: errorFilter.value.category || undefined,
      api_key_id: errorFilter.value.api_key_id ?? undefined,
      status_code: errorFilter.value.status_code ?? undefined,
      sort_by: errorSortBy.value,
      sort_order: errorSortOrder.value,
    })
    errorRows.value = resp.items
    errorTotal.value = resp.total
  } catch (error) {
    // Keep the previous error records after retrieval fails and notify the user;
    // visibility permission was checked before starting this request.
    console.error('[UsageView] loadErrors failed:', error)
    appStore.showError(t('usage.errors.failedToLoad'))
  } finally {
    errorLoading.value = false
  }
}

// onErrorSort selects error ordering and reloads the first page.
const onErrorSort = (sortBy: string, sortOrder: 'asc' | 'desc') => {
  errorSortBy.value = sortBy
  errorSortOrder.value = sortOrder
  errorPage.value = 1
  void loadErrors()
}

// onErrorPage selects an error page and starts its retrieval.
const onErrorPage = (page: number) => {
  errorPage.value = page
  void loadErrors()
}

// onErrorPageSize changes error page size and reloads its first page.
const onErrorPageSize = (pageSize: number) => {
  errorPageSize.value = pageSize
  errorPage.value = 1
  void loadErrors()
}

// switchToErrors activates the error tab and loads when its rows are empty. The loader
// still checks the public visibility setting before sending any request for this
// interaction.
const switchToErrors = () => {
  activeTab.value = 'errors'
  if (errorRows.value.length === 0) void loadErrors()
}

onMounted(() => {
  loadSavedColumns()
  loadSavedErrColumns()
  // Register one document listener for this mounted page so clicks outside the column menu
  // can close its floating controls.
  document.addEventListener('click', handleColumnClickOutside)
  void loadFilterOptions()
  refreshData()
})

// Release page-owned request controllers and the outside-click listener when leaving this
// view so pending cancelable work cannot retain those resources.
onUnmounted(() => {
  abortController?.abort()
  document.removeEventListener('click', handleColumnClickOutside)
})

watch(endpointDistributionSource, () => {
  // The chart component selects its endpoint source from statistics already loaded by this page.
})
useLocaleRefresh(() => {
  const requests = [loadFilterOptions(), loadLogs()]
  if (activeTab.value === 'errors') {
    requests.push(loadErrors())
  } else {
    // Invalidate inactive error rows so reopening the tab fetches translated
    // messages after account language restoration or a manual language change.
    errorRows.value = []
    errorTotal.value = 0
  }
  return Promise.all(requests)
})
</script>

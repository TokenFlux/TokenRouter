<template>
  <AppLayout>
    <div class="space-y-4">
      <UsageStatsCards :stats="usageStats" />
      <!-- Charts Section -->
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
        <!-- Request-ID mode shows summary cards and records while hiding trend and distribution charts. -->
        <div v-if="!filters.request_id" class="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <ModelDistributionChart
            v-model:source="modelDistributionSource"
            v-model:metric="modelDistributionMetric"
            :model-stats="requestedModelStats"
            :upstream-model-stats="upstreamModelStats"
            :mapping-model-stats="mappingModelStats"
            :loading="modelStatsLoading"
            :show-source-toggle="true"
            :show-metric-toggle="true"
            :start-date="startDate"
            :end-date="endDate"
            :filters="breakdownFilters"
          />
          <GroupDistributionChart
            v-model:metric="groupDistributionMetric"
            :group-stats="groupStats"
            :loading="chartsLoading"
            :show-metric-toggle="true"
            :start-date="startDate"
            :end-date="endDate"
            :filters="breakdownFilters"
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
            :show-source-toggle="true"
            :show-metric-toggle="true"
            :title="t('usage.endpointDistribution')"
            :start-date="startDate"
            :end-date="endDate"
            :filters="breakdownFilters"
          />
          <TokenUsageTrend
            :trend-data="trendData"
            :loading="chartsLoading"
            :granularity="granularity"
          />
        </div>
      </div>
      <!-- Tabs and filters share one card; each data section uses its own surface below. -->
      <div class="card" data-testid="admin-usage-filters-card">
        <div
          class="flex flex-wrap items-center border-b border-gray-200 px-2 dark:border-dark-700 sm:px-4"
        >
          <button
            v-for="tab in detailTabs"
            :key="tab.key"
            type="button"
            data-testid="usage-detail-tab"
            class="-mb-px inline-flex h-9 items-center gap-1.5 border-b-2 px-3 py-1.5 text-sm font-medium transition-colors sm:px-4"
            :class="
              activeTab === tab.key
                ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700 dark:text-gray-400 dark:hover:border-dark-500 dark:hover:text-gray-200'
            "
            @click="switchTab(tab.key)"
          >
            <Icon :name="tab.icon" size="sm" />
            {{ tab.label }}
          </button>
        </div>

        <UsageFilters
          v-model="filters"
          ref="usageFiltersRef"
          flat
          :mode="activeTab"
          :start-date="startDate"
          :end-date="endDate"
          :exporting="exporting"
          :model-options="modelNameOptions"
          @change="applyFilters"
          @refresh="refreshData"
          @reset="resetFilters"
          @cleanup="openCleanupDialog"
          @export="exportToExcel"
        >
          <template #after-reset>
            <IpGeoBatchToolbar
              v-if="activeTab === 'usage'"
              :ips="usageLogs.map((row) => row.ip_address)"
              inline
              @failed="handleIpGeoBatchFailed"
            />
            <div v-if="activeTab !== 'ranking'" class="relative" ref="columnDropdownRef">
              <button
                type="button"
                @click="showColumnDropdown = !showColumnDropdown"
                class="btn btn-secondary shrink-0 btn-icon"
                :aria-label="t('admin.users.columnSettings')"
                :title="t('admin.users.columnSettings')"
              >
                <Icon name="grid" size="sm" />
                <span class="hidden">{{ t('admin.users.columnSettings') }}</span>
              </button>
              <MotionTransition name="dropdown-fade">
                <div
                  v-if="showColumnDropdown"
                  :inert="!showColumnDropdown || undefined"
                  class="dropdown right-0 top-full z-50 mt-1 max-h-80 w-48 overflow-y-auto"
                >
                  <button
                    v-for="col in currentToggleableColumns"
                    :key="col.key"
                    @click="toggleCurrentColumn(col.key)"
                    class="dropdown-item justify-between"
                  >
                    <span>{{ col.label }}</span>
                    <Icon
                      v-if="isCurrentColumnVisible(col.key)"
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
          </template>
        </UsageFilters>
      </div>

      <!-- The shared card owns table and pagination borders and corner clipping. -->
      <div
        v-show="activeTab === 'usage'"
        v-content-reveal="activeTab === 'usage'"
        class="card overflow-hidden"
        data-testid="admin-usage-table-section"
      >
        <UsageTable
          flat
          column-order-storage-key="admin-usage-column-order"
          :data="usageLogs"
          :loading="loading"
          :columns="visibleColumns"
          compact-user-column
          :show-ip-geo-toolbar="false"
          :server-side-sort="true"
          :default-sort-key="'created_at'"
          :default-sort-order="'desc'"
          @sort="handleSort"
          @userClick="handleUserClick"
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

      <div
        v-show="activeTab === 'errors'"
        v-content-reveal="activeTab === 'errors'"
        data-testid="admin-usage-errors-section"
      >
        <OpsErrorLogTable
          :rows="errRows"
          :total="errTotal"
          :loading="errLoading"
          :page="errPage"
          :page-size="errPageSize"
          :visible-column-keys="errVisibleColumnKeys"
          user-clickable
          @userClick="handleUserClick"
          @openErrorDetail="openError"
          @sort="onErrSort"
          @update:page="onErrPage"
          @update:pageSize="onErrPageSize"
          @ipGeoBatchFailed="handleIpGeoBatchFailed"
        />
      </div>

      <!-- Ranking mounts on its first tab visit, then remains mounted so shared filter changes continue to refresh it. -->
      <div
        v-if="rankingMounted"
        v-show="activeTab === 'ranking'"
        class="card overflow-hidden"
        data-testid="admin-usage-ranking-section"
      >
        <UserTokenRanking
          ref="rankingRef"
          :start-date="startDate"
          :end-date="endDate"
          :filters="breakdownFilters"
          :model="filters.model"
          @select-user="handleRankingSelectUser"
        />
      </div>
      <OpsErrorDetailModal
        v-model:show="showErrorModal"
        :error-id="selectedErrorId"
        :error-type="'request'"
      />
    </div>
  </AppLayout>
  <UsageExportProgress
    :show="exportProgress.show"
    :progress="exportProgress.progress"
    :current="exportProgress.current"
    :total="exportProgress.total"
    :estimated-time="exportProgress.estimatedTime"
    @cancel="cancelExport"
  />
  <UsageCleanupDialog
    :show="cleanupDialogVisible"
    :filters="filters"
    :start-date="startDate"
    :end-date="endDate"
    @close="cleanupDialogVisible = false"
  />
  <!-- Balance history modal triggered from usage table user click -->
  <UserBalanceHistoryModal
    :show="showBalanceHistoryModal"
    :user="balanceHistoryUser"
    :hide-actions="true"
    @close="
      showBalanceHistoryModal = false;
      balanceHistoryUser = null;
    "
  />
</template>

<script setup lang="ts">
import { formatRequestTimingForExport } from '@/utils/requestTiming'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { vContentReveal } from '@/directives/contentReveal'

import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { adminUsageAPI } from '@/api/admin/usage'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatReasoningEffort } from '@/utils/format'
import { resolveUsageRequestType, requestTypeToLegacyStream } from '@/utils/usageRequestType'
import { calculateUsageTps } from '@/utils/usageTps'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import UsageStatsCards from '@/components/admin/usage/UsageStatsCards.vue'
import UsageFilters from '@/components/admin/usage/UsageFilters.vue'
import UsageTable from '@/components/admin/usage/UsageTable.vue'
import UsageExportProgress from '@/components/admin/usage/UsageExportProgress.vue'
import IpGeoBatchToolbar from '@/components/common/IpGeoBatchToolbar.vue'
import UserTokenRanking from '@/components/admin/usage/UserTokenRanking.vue'
import UsageCleanupDialog from '@/components/admin/usage/UsageCleanupDialog.vue'
import UserBalanceHistoryModal from '@/components/admin/user/UserBalanceHistoryModal.vue'
import OpsErrorLogTable from '@/views/admin/ops/components/OpsErrorLogTable.vue'
import OpsErrorDetailModal from '@/views/admin/ops/components/OpsErrorDetailModal.vue'
import { listErrorLogs } from '@/api/admin/ops'
import type { OpsErrorLog } from '@/api/admin/ops'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import GroupDistributionChart from '@/components/charts/GroupDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type {
  AdminUsageLog,
  TrendDataPoint,
  ModelStat,
  GroupStat,
  EndpointStat,
} from '@/api/usage.types'
import type { AdminUser } from '@/api/admin/users.types'
import type { AdminUsageStatsResponse, AdminUsageQueryParams } from '@/api/admin/usage'

// UsageView.vue loads administrator usage records, summaries, distributions, and error
// views under shared filters. Request-ID list and summary queries omit date predicates,
// while chart visibility follows that search mode. The administrator export owns provider
// fields and serializes every requested page into a workbook.
//
// Lists use cancellation ownership; model, summary, chart, and raw endpoint loaders also
// use sequence checks where their responses can overlap. Raw endpoint caches and
// model-source caches have separate invalidation rules. Column preferences are stored
// independently for usage and errors, and hidden-column migration appends newly hidden
// defaults while retaining saved choices. Ranking mounts on first selection and continues
// following shared filters afterward.

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
// ModelDistributionSource selects requested, forwarded, or mapping-grouped model
// statistics.
type ModelDistributionSource = 'requested' | 'upstream' | 'mapping'
// route provides optional initial user and date filters consumed once during the page mount
// sequence.
const route = useRoute()
// Usage display state stores the current summary and page separately from trend and
// distribution responses used by charts.
const usageStats = ref<AdminUsageStatsResponse | null>(null)
const usageLogs = ref<AdminUsageLog[]>([])
// Loading flags distinguish the records table, chart requests, and export operation so each
// section can render its own progress.
const loading = ref(false)
const exporting = ref(false)
const trendData = ref<TrendDataPoint[]>([])
const requestedModelStats = ref<ModelStat[]>([])
const upstreamModelStats = ref<ModelStat[]>([])
const mappingModelStats = ref<ModelStat[]>([])
const groupStats = ref<GroupStat[]>([])
const chartsLoading = ref(false)
const modelStatsLoading = ref(false)
// granularity selects hourly or daily trend buckets, while distribution selectors choose
// the displayed metric and endpoint source.
const granularity = ref<'day' | 'hour'>('hour')
const modelDistributionMetric = ref<DistributionMetric>('tokens')
const modelDistributionSource = ref<ModelDistributionSource>('requested')
// loadedModelSources records which model-source results remain reusable until filters
// invalidate their individual cache flags.
const loadedModelSources = reactive<Record<ModelDistributionSource, boolean>>({
  requested: false,
  upstream: false,
  mapping: false,
})
// Group and endpoint selectors change the visible metric independently of the model chart's
// selected metric.
const groupDistributionMetric = ref<DistributionMetric>('tokens')
const endpointDistributionMetric = ref<DistributionMetric>('tokens')
const endpointDistributionSource = ref<EndpointSource>('inbound')
// Endpoint arrays hold normalized inbound counts separately from lazily fetched upstream
// and raw-path statistics for this selection.
const inboundEndpointStats = ref<EndpointStat[]>([])
const upstreamEndpointStats = ref<EndpointStat[]>([])
const endpointPathStats = ref<EndpointStat[]>([])
// Endpoint loading state separates the summary's inbound request from raw-source requests
// so changing source selects the correct indicator.
const inboundEndpointLoading = ref(false)
const rawEndpointLoading = ref(false)
const endpointStatsLoading = computed(() =>
  endpointDistributionSource.value === 'inbound'
    ? inboundEndpointLoading.value
    : rawEndpointLoading.value,
)
// loadedRawEndpoints marks reusable upstream and path results; summary reloads clear both
// flags before fetching the active raw source.
const loadedRawEndpoints = reactive({ upstream: false, path: false })
// Endpoint request ownership combines a sequence with a controller, while export and
// summary controllers own their separate requests.
let endpointReqSeq = 0
let endpointAbortController: AbortController | null = null
// abortController owns the latest usage-list request; sequence counters independently
// identify current statistics and chart requests.
let abortController: AbortController | null = null
let exportAbortController: AbortController | null = null
// Chart and summary sequences reject superseded responses independently; modelStatsReqSeq
// governs the selected model-source request.
let chartReqSeq = 0
let statsReqSeq = 0
let statsAbortController: AbortController | null = null
let modelStatsReqSeq = 0
// Export progress holds dialog visibility, percentage, processed rows, expected total, and
// an optional formatted remaining-time estimate.
const exportProgress = reactive({
  show: false,
  progress: 0,
  current: 0,
  total: 0,
  estimatedTime: '',
})
// cleanupDialogVisible controls the separate cleanup dialog that copies the current
// selection when it opens.
const cleanupDialogVisible = ref(false)
// Balance inspection state pairs modal visibility with the fetched administrator user
// record selected from a usage row.
const showBalanceHistoryModal = ref(false)
const balanceHistoryUser = ref<AdminUser | null>(null)

// breakdownFilters selects ranking-supported identity and billing predicates. It keeps
// explicit false and zero for nullable options while dates and model are supplied
// separately to the ranking component.
const breakdownFilters = computed(() => {
  const f: Record<string, any> = {}
  f.request_source = filters.value.request_source || 'all'
  if (filters.value.user_id) f.user_id = filters.value.user_id
  if (filters.value.api_key_id) f.api_key_id = filters.value.api_key_id
  if (filters.value.provider_id) f.provider_id = filters.value.provider_id
  if (filters.value.group_id) f.group_id = filters.value.group_id
  if (filters.value.team_id) f.team_id = filters.value.team_id
  if (filters.value.request_type != null) f.request_type = filters.value.request_type
  if (filters.value.native_compaction_v2 != null)
    f.native_compaction_v2 = filters.value.native_compaction_v2
  if (filters.value.billing_type != null) f.billing_type = filters.value.billing_type
  return f
})

// modelNameOptions deduplicates nonempty requested model names and sorts them for the
// filter panel's model choices.
const modelNameOptions = computed(() =>
  Array.from(new Set(requestedModelStats.value.map((m) => m.model).filter(Boolean))).sort(),
)

// handleUserClick retrieves a user including soft-deleted records before opening balance
// history. Failures show a notification and leave the modal selection unchanged, keeping
// historical usage identities inspectable.
const handleUserClick = async (userId: number) => {
  try {
    const user = await adminAPI.users.getById(userId, true)
    balanceHistoryUser.value = user
    showBalanceHistoryModal.value = true
  } catch {
    // A failed user lookup leaves balance history unopened and reports the missing detail
    // while keeping the usage row available.
    appStore.showError(t('admin.usage.failedToLoadUser'))
  }
}

// handleRankingSelectUser applies a ranking identity to the page filter and search label.
// It switches back to usage records and applies the new selection across the page's
// dependent queries.
const handleRankingSelectUser = (userId: number, email: string) => {
  filters.value = { ...filters.value, user_id: userId }
  usageFiltersRef.value?.setUserKeyword?.(email || '')
  activeTab.value = 'usage'
  applyFilters()
}

// granularityOptions supplies translated daily and hourly choices for the trend chart
// bucket-size selector in this page.
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') },
])
// formatLD formats browser-local calendar components as a year-month-day date string.
const formatLD = (d: Date) => {
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}
// getLast24HoursRangeDates returns local calendar dates around the last twenty-four hours.
// The resulting day strings describe calendar endpoints, so downstream date queries may
// cover more than exactly twenty-four hours.
const getLast24HoursRangeDates = (): {
  start: string // beginning local calendar date
  end: string // ending local calendar date
} => {
  const end = new Date()
  // Subtract an elapsed day before formatting local calendar endpoints.
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: formatLD(start),
    end: formatLD(end),
  }
}
// getGranularityForRange chooses hourly buckets for ranges no longer than one day.
// Date-only strings are interpreted at local midnight, invalid endpoints select daily
// buckets, and longer ranges use daily buckets.
const getGranularityForRange = (start: string, end: string): 'day' | 'hour' => {
  // parseRangePoint reads an endpoint timestamp, interpreting date-only input at local
  // midnight. Invalid dates return null so the enclosing granularity selection can choose
  // its daily default safely.
  const parseRangePoint = (value: string) => {
    const timestamp = new Date(value.length === 10 ? `${value}T00:00:00` : value).getTime()
    return Number.isFinite(timestamp) ? timestamp : null
  }
  const startTime = parseRangePoint(start)
  const endTime = parseRangePoint(end)
  if (startTime === null || endTime === null) return 'day'
  // Round partial elapsed days upward before choosing hourly or daily
  // buckets.
  const daysDiff = Math.ceil((endTime - startTime) / (1000 * 60 * 60 * 24))
  return daysDiff <= 1 ? 'hour' : 'day'
}
// defaultRange initializes both calendar endpoints from now and the instant twenty-four
// hours earlier in browser local time.
const defaultRange = getLast24HoursRangeDates()
// Date state supplies the selected calendar range to filters and determines trend
// granularity when the range changes.
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)
// requestDateParams removes both date predicates when a request ID is selected. Otherwise
// it returns an empty override object so the existing selected dates remain in the query.
const requestDateParams = () =>
  filters.value.request_id ? { start_date: undefined, end_date: undefined } : {}
// filters owns the mutable usage query selection; page changes and exports combine these
// values with sorting and paging.
const filters = ref<AdminUsageQueryParams>({
  user_id: undefined,
  model: undefined,
  group_id: undefined,
  request_type: undefined,
  billing_type: null,
  native_compaction_v2: null,
  start_date: startDate.value,
  end_date: endDate.value,
})
// pagination holds the current one-based page, persisted page size, and most recently
// returned matching-record total.
const pagination = reactive({ page: 1, page_size: getPersistedPageSize(), total: 0 })
// sortState holds the server sort column and direction used consistently by displayed pages
// and exports.
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc',
})

// getSingleQueryValue extracts a nonempty string from a router query value. Arrays use
// their first nonempty string, while null, missing values, and empty strings produce
// undefined for omission.
const getSingleQueryValue = (
  value: string | null | Array<string | null> | undefined,
): string | undefined => {
  if (Array.isArray(value))
    return value.find((item): item is string => typeof item === 'string' && item.length > 0)
  return typeof value === 'string' && value.length > 0 ? value : undefined
}

// getNumericQueryValue converts the selected query string to a finite number. Missing
// strings and nonfinite conversions produce undefined; this helper does not restrict
// identifiers to positive integer values.
const getNumericQueryValue = (
  value: string | null | Array<string | null> | undefined,
): number | undefined => {
  const raw = getSingleQueryValue(value)
  if (!raw) return undefined
  const parsed = Number(raw)
  return Number.isFinite(parsed) ? parsed : undefined
}

// applyRouteQueryFilters imports initial date and user values from the route. Supplied date
// endpoints replace the default range individually, then filter dates and trend granularity
// follow the resulting selection.
const applyRouteQueryFilters = () => {
  const queryStartDate = getSingleQueryValue(route.query.start_date)
  const queryEndDate = getSingleQueryValue(route.query.end_date)
  const queryUserId = getNumericQueryValue(route.query.user_id)

  if (queryStartDate) {
    startDate.value = queryStartDate
  }
  if (queryEndDate) {
    endDate.value = queryEndDate
  }

  filters.value = {
    ...filters.value,
    user_id: queryUserId,
    start_date: startDate.value,
    end_date: endDate.value,
  }
  granularity.value = getGranularityForRange(startDate.value, endDate.value)
}

// loadRouteUserFilterLabel resolves the route-selected user into a readable filter label. A
// missing user identifier skips retrieval, and a current failed lookup displays the numeric
// identifier.
//
// Before requesting, it captures both user identity and the filter component's search
// revision. Success and failure compare both values before assigning a label. Typing or
// changing selection during the request therefore keeps the newer label, even when the
// selected numeric user remains unchanged.
const loadRouteUserFilterLabel = async () => {
  // Capture the user and search revision together so successful label
  // hydration cannot overwrite a newer selection or typed search.
  const requestedUserId = filters.value.user_id
  if (!requestedUserId) return
  const userSearchRevision = usageFiltersRef.value?.getUserSearchRevision?.()

  // routeUserFilterIsCurrent compares user identity and search revision with the captured
  // route lookup.
  const routeUserFilterIsCurrent = () =>
    filters.value.user_id === requestedUserId &&
    usageFiltersRef.value?.getUserSearchRevision?.() === userSearchRevision

  try {
    const user = await adminAPI.users.getById(requestedUserId, true)
    if (!routeUserFilterIsCurrent()) return
    usageFiltersRef.value?.setUserKeyword?.(user.email || String(requestedUserId))
  } catch {
    // Use numeric identity only if both the filter and search revision still match; newer
    // typed labels survive a failed hydration.
    if (!routeUserFilterIsCurrent()) return
    usageFiltersRef.value?.setUserKeyword?.(String(requestedUserId))
  }
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
  filters.value = {
    ...filters.value,
    start_date: range.startDate,
    end_date: range.endDate,
  }
  granularity.value = getGranularityForRange(range.startDate, range.endDate)
  applyFilters()
}

// buildUsageListParams combines paging, filters, sort, and the requested total-count mode.
// Request-ID filtering removes dates, while canonical request type controls compatible
// legacy stream values sent to the usage API.
const buildUsageListParams = (
  page: number,
  pageSize: number,
  exactTotal: boolean,
): AdminUsageQueryParams => {
  const requestType = filters.value.request_type
  // Translate canonical request mode through the shared compatibility map; modes without a
  // legacy stream equivalent omit that older predicate.
  const legacyStream = requestType ? requestTypeToLegacyStream(requestType) : filters.value.stream
  return {
    page,
    page_size: pageSize,
    exact_total: exactTotal,
    ...filters.value,
    ...requestDateParams(),
    stream: legacyStream === null ? undefined : legacyStream,
    sort_by: sortState.sort_by,
    sort_order: sortState.sort_order,
  }
}

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
  const c = new AbortController()
  abortController = c
  loading.value = true
  try {
    const res = await adminAPI.usage.list(
      buildUsageListParams(pagination.page, pagination.page_size, false),
      { signal: c.signal },
    )
    if (!c.signal.aborted) {
      usageLogs.value = res.items
      pagination.total = res.total
    }
  } catch (error: any) {
    // Canceled list work stays quiet, while other failures are logged and the previous
    // records remain available for inspection.
    if (error?.name !== 'AbortError') console.error('Failed to load usage logs:', error)
  } finally {
    if (abortController === c) loading.value = false
  }
}
// statsParams builds summary filters for an endpoint source and optional forced refresh. It
// applies request-ID date omission and compatible stream values before adding nocache for
// callers requesting fresh totals.
const statsParams = (source: EndpointSource, force: boolean) => {
  const requestType = filters.value.request_type
  const legacyStream = requestType ? requestTypeToLegacyStream(requestType) : filters.value.stream
  return {
    ...filters.value,
    ...requestDateParams(),
    stream: legacyStream === null ? undefined : legacyStream,
    endpoint_source: source,
    ...(force ? { nocache: 1 } : {}),
  }
}

// loadEndpointStats lazily loads upstream or raw-path totals and reuses successfully loaded
// sources unless forced. Inbound data belongs to the summary loader and returns immediately
// here.
//
// Every invocation aborts the earlier raw-source request and advances its sequence,
// including cache hits. Response publication checks both sequence and signal, while loading
// cleanup checks sequence ownership. Summary refresh clears the caches before calling this
// function, so a previous source request cannot repopulate an invalidated selection.
const loadEndpointStats = async (source: EndpointSource, force = false) => {
  // Changing raw endpoint source invalidates the previous request even on cache hits,
  // preventing its late response from repopulating an obsolete selection.
  endpointAbortController?.abort()
  const seq = ++endpointReqSeq
  if (source === 'inbound' || (!force && loadedRawEndpoints[source])) {
    rawEndpointLoading.value = false
    return
  }
  const controller = new AbortController()
  endpointAbortController = controller
  rawEndpointLoading.value = true
  try {
    const response = await adminAPI.usage.getStats(statsParams(source, force), {
      signal: controller.signal,
    })
    if (seq !== endpointReqSeq || controller.signal.aborted) return
    if (source === 'upstream') upstreamEndpointStats.value = response.upstream_endpoints || []
    else endpointPathStats.value = response.endpoint_paths || []
    loadedRawEndpoints[source] = true
  } catch (error) {
    // Ignore superseded or aborted raw-source failures; a current failure leaves its cache
    // flag unset so later selection can retry.
    if (seq !== endpointReqSeq || controller.signal.aborted) return
    console.error('Failed to load endpoint statistics:', error)
  } finally {
    if (seq === endpointReqSeq) rawEndpointLoading.value = false
  }
}

// loadStats refreshes the usage summary and inbound endpoint counts, optionally bypassing
// the server cache. It cancels the prior summary, advances its sequence, and invalidates
// both raw endpoint caches.
//
// The active raw endpoint starts independently, allowing summary cards and that chart to
// finish separately. Only the current summary sequence can replace totals, clear inbound
// results on error, or release its loading state. A failed current summary keeps the
// earlier usageStats record but clears inbound endpoint counts.
const loadStats = async (force = false) => {
  // Cancel the prior summary before invalidating raw-source caches so the replacement
  // summary and selected raw chart use the refreshed filter state.
  statsAbortController?.abort()
  const controller = new AbortController()
  statsAbortController = controller
  const seq = ++statsReqSeq
  loadedRawEndpoints.upstream = false
  loadedRawEndpoints.path = false
  upstreamEndpointStats.value = []
  endpointPathStats.value = []
  void loadEndpointStats(endpointDistributionSource.value, force)
  inboundEndpointLoading.value = true
  try {
    const response = await adminAPI.usage.getStats(statsParams('inbound', force), {
      signal: controller.signal,
    })
    if (seq !== statsReqSeq) return
    usageStats.value = response
    inboundEndpointStats.value = response.endpoints || []
  } catch (error) {
    // Only the current summary failure clears inbound counts; previously displayed summary
    // totals remain until another successful summary response arrives.
    if (seq !== statsReqSeq) return
    console.error('Failed to load usage statistics:', error)
    inboundEndpointStats.value = []
  } finally {
    if (seq === statsReqSeq) inboundEndpointLoading.value = false
  }
}

// invalidateModelStatsCache marks every model-source result as needing retrieval. It
// retains displayed arrays so a refresh can keep earlier chart data visible until the next
// response replaces it.
const invalidateModelStatsCache = () => {
  // Invalidate reuse flags together while keeping displayed arrays, allowing charts to
  // retain their last values until a replacement request finishes.
  loadedModelSources.requested = false
  loadedModelSources.upstream = false
  loadedModelSources.mapping = false
}

// loadModelStats retrieves one model source unless its cache flag permits reuse and refresh
// is not forced. Requests include the selected dates and supported identity, request-mode,
// and billing filters.
//
// A shared model sequence makes the latest started request authoritative for publication
// and loading cleanup. Current success stores the result and marks that source loaded;
// current failure empties its array and clears its flag. Earlier responses are ignored,
// while invalidation itself retains previously displayed arrays until a current result
// arrives.
const loadModelStats = async (source: ModelDistributionSource, force = false) => {
  if (!force && loadedModelSources[source]) {
    return
  }

  // Capture model request ownership before fetching so a newer started model
  // request controls published rows, cache flags, and loading cleanup.
  const seq = ++modelStatsReqSeq
  modelStatsLoading.value = true
  try {
    const requestType = filters.value.request_type
    const legacyStream = requestType ? requestTypeToLegacyStream(requestType) : filters.value.stream
    const baseParams = {
      start_date: filters.value.start_date || startDate.value,
      end_date: filters.value.end_date || endDate.value,
      user_id: filters.value.user_id,
      model: filters.value.model,
      api_key_id: filters.value.api_key_id,
      provider_id: filters.value.provider_id,
      group_id: filters.value.group_id,
      team_id: filters.value.team_id,
      request_type: requestType,
      request_source: filters.value.request_source || 'all',
      stream: legacyStream === null ? undefined : legacyStream,
      billing_type: filters.value.billing_type,
      native_compaction_v2: filters.value.native_compaction_v2,
    }

    const response = await adminAPI.dashboard.getModelStats({ ...baseParams, model_source: source })

    if (seq !== modelStatsReqSeq) return

    const models = response.models || []
    if (source === 'requested') {
      requestedModelStats.value = models
    } else if (source === 'upstream') {
      upstreamModelStats.value = models
    } else {
      mappingModelStats.value = models
    }
    loadedModelSources[source] = true
  } catch (error) {
    // Clear only the failed current source and mark it unloaded so a later source
    // selection can retry the model query.
    if (seq !== modelStatsReqSeq) return
    console.error('Failed to load model stats:', error)
    if (source === 'requested') {
      requestedModelStats.value = []
    } else if (source === 'upstream') {
      upstreamModelStats.value = []
    } else {
      mappingModelStats.value = []
    }
    loadedModelSources[source] = false
  } finally {
    if (seq === modelStatsReqSeq) modelStatsLoading.value = false
  }
}

// loadChartData retrieves trend and group distribution together unless request-ID filtering
// hides charts. Each request captures a new sequence so an older successful response cannot
// replace a later selection.
//
// The sequence also protects loading cleanup. Failures log an error and retain the
// displayed chart arrays, including failures from superseded requests. Model statistics are
// excluded from the snapshot because their source selector has a separate loader and cache
// lifecycle.
const loadChartData = async () => {
  if (filters.value.request_id) return
  // Capture chart ownership before awaiting the snapshot so only the latest
  // request publishes successful arrays and releases loading.
  const seq = ++chartReqSeq
  chartsLoading.value = true
  try {
    const requestType = filters.value.request_type
    const legacyStream = requestType ? requestTypeToLegacyStream(requestType) : filters.value.stream
    const snapshot = await adminAPI.dashboard.getSnapshotV2({
      start_date: filters.value.start_date || startDate.value,
      end_date: filters.value.end_date || endDate.value,
      granularity: granularity.value,
      user_id: filters.value.user_id,
      model: filters.value.model,
      api_key_id: filters.value.api_key_id,
      provider_id: filters.value.provider_id,
      group_id: filters.value.group_id,
      team_id: filters.value.team_id,
      request_type: requestType,
      request_source: filters.value.request_source || 'all',
      stream: legacyStream === null ? undefined : legacyStream,
      billing_type: filters.value.billing_type,
      native_compaction_v2: filters.value.native_compaction_v2,
      include_stats: false,
      include_trend: true,
      include_model_stats: false,
      include_group_stats: true,
      include_users_trend: false,
    })
    if (seq !== chartReqSeq) return
    trendData.value = snapshot.trend || []
    groupStats.value = snapshot.groups || []
  } catch (error) {
    // Keep the preceding trend and group observations after a snapshot failure so those
    // charts retain their last available data.
    console.error('Failed to load chart data:', error)
  } finally {
    if (seq === chartReqSeq) chartsLoading.value = false
  }
}
// applyFilters resets usage and error pagination, invalidates model caches, and reloads
// usage sections. Errors reload only while their tab is active; inactive rows are cleared
// for the next visit.
const applyFilters = () => {
  pagination.page = 1
  invalidateModelStatsCache()
  loadLogs()
  loadStats()
  loadModelStats(modelDistributionSource.value, true)
  loadChartData()
  errPage.value = 1
  if (activeTab.value === 'errors') {
    loadAdminErrors()
  } else {
    errRows.value = []
  }
}
// refreshData refreshes usage sections and forces fresh summary and model data. It also
// reloads active errors and an already mounted ranking, leaving each operation to manage
// its own completion.
const refreshData = () => {
  invalidateModelStatsCache()
  loadLogs()
  loadStats(true)
  loadModelStats(modelDistributionSource.value, true)
  loadChartData()
  if (activeTab.value === 'errors') loadAdminErrors()
  if (rankingMounted.value) rankingRef.value?.reload()
}
// resetFilters restores the local last-day range and clears query predicates. It
// recalculates granularity and applies the resulting selection so list, summary, model, and
// visible error queries use matching filters.
const resetFilters = () => {
  const range = getLast24HoursRangeDates()
  startDate.value = range.start
  endDate.value = range.end
  filters.value = {
    start_date: startDate.value,
    end_date: endDate.value,
    request_type: undefined,
    billing_type: null,
    native_compaction_v2: null,
    billing_mode: undefined,
  }
  granularity.value = getGranularityForRange(startDate.value, endDate.value)
  applyFilters()
}
// handlePageChange selects a usage page and reloads its records.
const handlePageChange = (p: number) => {
  pagination.page = p
  loadLogs()
}
// handlePageSizeChange updates the record count and reloads the first page. Resetting
// pagination prevents an old page index from pointing beyond the newly sized sequence of
// usage pages.
const handlePageSizeChange = (s: number) => {
  pagination.page_size = s
  pagination.page = 1
  loadLogs()
}
// handleSort stores the requested server ordering and reloads page one. Returning to the
// first page gives the selected ordering a consistent starting point for the usage table.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadLogs()
}

// handleIpGeoBatchFailed reports the shared batch IP lookup failure notification.
const handleIpGeoBatchFailed = () => {
  appStore.showError(t('usage.ipGeo.batchFailed'))
}
// cancelExport requests cancellation through the current workbook export controller.
// With no controller it has no effect; the export flow owns request settlement,
// progress cleanup, and releasing its controller.
const cancelExport = () => exportAbortController?.abort()
// openCleanupDialog opens the cleanup task editor with the current page filters.
const openCleanupDialog = () => {
  cleanupDialogVisible.value = true
}
// getRequestTypeLabel translates the canonical usage request mode after legacy resolution.
// Unrecognized modes produce the unknown label so exported rows retain an explicit
// request-type cell for incomplete records.
const getRequestTypeLabel = (log: AdminUsageLog): string => {
  const requestType = resolveUsageRequestType(log)
  if (requestType === 'cyber') return t('usage.cyber')
  if (requestType === 'live') return t('usage.live')
  if (requestType === 'ws_v2') return t('usage.ws')
  if (requestType === 'stream') return t('usage.stream')
  if (requestType === 'sync') return t('usage.sync')
  return t('usage.unknown')
}

// exportToExcel retrieves filtered usage pages and writes an administrator workbook. A
// running export blocks another start, and its controller supports cancellation while pages
// are being fetched.
//
// The first page establishes the expected total; each appended batch advances row counts
// and sets numeric TPS precision on the corresponding spreadsheet cells. Administrator
// columns retain provider costs, upstream identities, and diagnostic timing separately from
// user charges, with monetary units in column headers. Cancellation skips saving, and
// final cleanup releases progress only when
// the controller still belongs to this export.
const exportToExcel = async () => {
  if (exporting.value) return
  exporting.value = true
  exportProgress.show = true
  const c = new AbortController()
  exportAbortController = c
  try {
    let p = 1
    let total = pagination.total
    let exportedCount = 0
    const XLSX = await import('xlsx')
    const headers = [
      t('usage.time'),
      t('admin.usage.user'),
      t('usage.apiKeyFilter'),
      t('admin.usage.provider'),
      t('usage.model'),
      t('usage.upstreamModel'),
      t('usage.upstreamResponseModel'),
      t('usage.upstreamModelMismatch'),
      t('usage.requestedReasoningEffort'),
      t('usage.reasoningEffort'),
      t('admin.usage.group'),
      t('usage.inboundEndpoint'),
      t('usage.upstreamEndpoint'),
      t('usage.type'),
      t('admin.usage.inputTokens'),
      t('admin.usage.outputTokens'),
      t('admin.usage.cacheReadTokens'),
      t('admin.usage.cacheCreationTokens'),
      `${t('admin.usage.inputCost')} (${usdUnitName})`,
      `${t('admin.usage.outputCost')} (${usdUnitName})`,
      `${t('admin.usage.cacheReadCost')} (${usdUnitName})`,
      `${t('admin.usage.cacheCreationCost')} (${usdUnitName})`,
      t('usage.rate'),
      t('usage.providerMultiplier'),
      `${t('usage.original')} (${usdUnitName})`,
      `${t('usage.userBilled')} (${balanceUnitName.value})`,
      `${t('usage.providerBilled')} (${usdUnitName})`,
      t('usage.firstToken'),
      t('usage.ttftMode'),
      t('usage.duration'),
      t('usage.tpsExport'),
      t('usage.detailedTiming'),
      t('admin.usage.requestId'),
      t('admin.usage.upstreamRequestId'),
      t('admin.usage.requestSource'),
      t('admin.usage.costStatus'),
      t('usage.userAgent'),
      t('admin.usage.ipAddress'),
    ]
    const ws = XLSX.utils.aoa_to_sheet([headers])
    const tpsColumn = headers.indexOf(t('usage.tpsExport'))
    while (true) {
      const res = await adminUsageAPI.list(buildUsageListParams(p, 100, true), { signal: c.signal })
      if (c.signal.aborted) break
      if (p === 1) {
        total = res.total
        exportProgress.total = total
      }
      const rows = (res.items || []).map((log: AdminUsageLog) => [
        log.created_at,
        log.user?.email || '',
        log.api_key?.name || '',
        log.provider?.name || '',
        log.model,
        log.upstream_model || '',
        log.upstream_response_model || '',
        log.upstream_model_mismatch == null
          ? ''
          : t(log.upstream_model_mismatch ? 'common.yes' : 'common.no'),
        formatReasoningEffort(log.requested_reasoning_effort || log.reasoning_effort),
        formatReasoningEffort(log.reasoning_effort),
        log.group?.name || '',
        log.inbound_endpoint || '',
        log.upstream_endpoint || '',
        getRequestTypeLabel(log),
        log.input_tokens,
        log.output_tokens,
        log.cache_read_tokens,
        log.cache_creation_tokens,
        log.input_cost?.toFixed(6) || '0.000000',
        log.output_cost?.toFixed(6) || '0.000000',
        log.cache_read_cost?.toFixed(6) || '0.000000',
        log.cache_creation_cost?.toFixed(6) || '0.000000',
        log.rate_multiplier?.toPrecision(4) || '1.00',
        (log.provider_rate_multiplier ?? 1).toPrecision(4),
        log.total_cost?.toFixed(6) || '0.000000',
        log.actual_cost?.toFixed(6) || '0.000000',
        log.system_usage?.cost_state === 'unknown'
          ? ''
          : (
              (log.provider_stats_cost ?? log.total_cost) * (log.provider_rate_multiplier ?? 1)
            ).toFixed(6),
        log.ttft_ms ?? '',
        log.ttft_mode === 'first_event'
          ? t('usage.latencyFirstEvent')
          : log.ttft_mode === 'first_output'
            ? t('usage.latencyFirstToken')
            : '',
        log.duration_ms,
        calculateUsageTps(log) ?? '',
        formatRequestTimingForExport(log.detailed_timing),
        log.request_id || '',
        log.upstream_request_id || '',
        t(`admin.usage.requestSources.${log.request_source || 'user'}`),
        log.system_usage?.cost_state || '',
        log.user_agent || '',
        log.ip_address || '',
      ])
      if (rows.length) {
        XLSX.utils.sheet_add_aoa(ws, rows, { origin: -1 })
        // Apply display precision after appending each batch so TPS cells retain
        // numeric values usable by spreadsheet calculations.
        for (let index = 0; index < rows.length; index++) {
          const cell = ws[XLSX.utils.encode_cell({ r: exportedCount + index + 1, c: tpsColumn })]
          if (cell?.t === 'n') cell.z = '0.0'
        }
      }
      exportedCount += rows.length
      exportProgress.current = exportedCount
      // Show rounded progress capped at a completed percentage.
      exportProgress.progress =
        total > 0 ? Math.min(100, Math.round((exportedCount / total) * 100)) : 0
      if (exportedCount >= total || res.items.length < 100) break
      p++
    }
    if (!c.signal.aborted) {
      const wb = XLSX.utils.book_new()
      XLSX.utils.book_append_sheet(wb, ws, 'Usage')
      saveAs(
        new Blob([XLSX.write(wb, { bookType: 'xlsx', type: 'array' })], {
          type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
        }),
        `usage_${filters.value.start_date}_to_${filters.value.end_date}.xlsx`,
      )
      appStore.showSuccess(t('usage.exportSuccess'))
    }
  } catch (error) {
    // An export rejection reports failure and skips workbook saving; controller-owned
    // cleanup still releases the progress dialog for subsequent attempts.
    console.error('Failed to export:', error)
    appStore.showError('Export Failed')
  } finally {
    if (exportAbortController === c) {
      exportAbortController = null
      exporting.value = false
      exportProgress.show = false
    }
  }
}

// Usage column constants define mandatory fields, default hidden choices, and the browser
// storage namespace for saved preferences.
const ALWAYS_VISIBLE = ['user', 'created_at']
const DEFAULT_HIDDEN_COLUMNS = ['reasoning_effort', 'upstream_request_id', 'user_agent']
const HIDDEN_COLUMNS_KEY = 'usage-hidden-columns'
const HIDDEN_COLUMNS_VERSION_KEY = 'usage-hidden-columns-version'
// HIDDEN_COLUMNS_CURRENT_VERSION identifies the migration that adds upstream request ID to
// stored hidden preferences while keeping existing choices.
const HIDDEN_COLUMNS_CURRENT_VERSION = 'upstream-request-id-hidden-by-default'

// allColumns supplies the canonical translated usage column order before mandatory and
// saved visibility choices are applied.
const allColumns = computed(() => [
  { key: 'user', label: t('admin.usage.user'), sortable: false, class: 'w-36 min-w-36 max-w-36' },
  { key: 'api_key', label: t('usage.apiKeyFilter'), sortable: false },
  { key: 'provider', label: t('admin.usage.provider'), sortable: false },
  { key: 'model', label: t('usage.model'), sortable: true },
  { key: 'reasoning_effort', label: t('usage.reasoningEffort'), sortable: false },
  { key: 'endpoint', label: t('usage.endpoint'), sortable: false },
  { key: 'group', label: t('admin.usage.group'), sortable: false },
  { key: 'stream', label: t('usage.type'), sortable: false },
  { key: 'billing_mode', label: t('admin.usage.billingMode'), sortable: false },
  { key: 'tokens', label: t('usage.tokens'), sortable: false },
  { key: 'cost', label: t('usage.cost'), sortable: false },
  { key: 'latency', label: t('usage.latency'), sortable: false },
  { key: 'tps', label: t('usage.tps'), sortable: false },
  { key: 'created_at', label: t('usage.time'), sortable: true },
  { key: 'request_id', label: t('admin.usage.requestId'), sortable: false },
  { key: 'upstream_request_id', label: t('admin.usage.upstreamRequestId'), sortable: false },
  { key: 'user_agent', label: t('usage.userAgent'), sortable: false },
  { key: 'ip_address', label: t('admin.usage.ipAddress'), sortable: false },
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

// toggleColumn flips one usage visibility choice before persisting its hidden set and
// migration version. Storage errors are logged, leaving the in-memory selection active for
// the current page session.
const toggleColumn = (key: string) => {
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  try {
    // Write the hidden set before its migration version so a later load can retry
    // migration when the second write fails.
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(HIDDEN_COLUMNS_VERSION_KEY, HIDDEN_COLUMNS_CURRENT_VERSION)
  } catch (e) {
    // Browser persistence failure leaves the selected usage visibility active in memory,
    // while logging that a future reload may not retain it.
    console.error('Failed to save columns:', e)
  }
}

// Error column constants define mandatory detail fields and a separate browser preference
// namespace from the usage table.
const ERR_ALWAYS_VISIBLE = ['user', 'status', 'created_at', 'actions']
const ERR_DEFAULT_HIDDEN_COLUMNS = ['user_agent']
const ERR_HIDDEN_COLUMNS_KEY = 'usage-error-hidden-columns'

// errAllColumns supplies settings keys in the order understood by the error table that
// renders these records.
const errAllColumns = computed(() => [
  { key: 'user', label: t('admin.ops.errorLog.user') },
  { key: 'api_key', label: t('admin.ops.errorLog.apiKey') },
  { key: 'provider', label: t('admin.ops.errorLog.provider') },
  { key: 'platform', label: t('admin.ops.errorLog.platform') },
  { key: 'model', label: t('admin.ops.errorLog.model') },
  { key: 'endpoint', label: t('admin.ops.errorLog.endpoint') },
  { key: 'group', label: t('admin.ops.errorLog.group') },
  { key: 'type', label: t('admin.ops.errorLog.type') },
  { key: 'category', label: t('usage.errors.category') },
  { key: 'status', label: t('admin.ops.errorLog.status') },
  { key: 'message', label: t('admin.ops.errorLog.message') },
  { key: 'created_at', label: t('admin.ops.errorLog.time') },
  { key: 'user_agent', label: t('usage.userAgent') },
  { key: 'client_ip', label: t('admin.ops.errorLog.ip') },
  { key: 'actions', label: t('admin.ops.errorLog.action') },
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

// toggleErrColumn flips one error visibility choice and persists the error hidden set.
// Storage errors are logged while the current in-memory table selection remains available
// to the user.
const toggleErrColumn = (key: string) => {
  if (errHiddenColumns.has(key)) {
    errHiddenColumns.delete(key)
  } else {
    errHiddenColumns.add(key)
  }
  try {
    // Persist the error hidden set separately from usage preferences so changing one tab
    // leaves the other tab's saved column choices intact.
    localStorage.setItem(ERR_HIDDEN_COLUMNS_KEY, JSON.stringify([...errHiddenColumns]))
  } catch (e) {
    // Browser persistence failure leaves error visibility active in memory and reports the
    // issue only to the console for this interaction.
    console.error('Failed to save error columns:', e)
  }
}

// loadSavedErrColumns restores the error hidden-key set from browser storage. Missing
// preferences or unreadable JSON use default hidden columns so the error table still starts
// with a usable selection.
const loadSavedErrColumns = () => {
  try {
    const saved = localStorage.getItem(ERR_HIDDEN_COLUMNS_KEY)
    const keys = saved ? (JSON.parse(saved) as string[]) : ERR_DEFAULT_HIDDEN_COLUMNS
    keys.forEach((key) => errHiddenColumns.add(key))
  } catch {
    // Use error-column defaults when saved values cannot be read or decoded, allowing the
    // table to initialize despite unavailable browser preferences.
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
  activeTab.value === 'errors' ? !errHiddenColumns.has(key) : isColumnVisible(key)
// toggleCurrentColumn delegates a visibility change to the active tab's preference owner.
const toggleCurrentColumn = (key: string) =>
  activeTab.value === 'errors' ? toggleErrColumn(key) : toggleColumn(key)

// loadSavedColumns restores administrator usage visibility and migrates old hidden-column
// preferences. Existing saved keys remain in the hidden set, while an outdated version adds
// upstream request ID before saving the updated set and version.
//
// New profiles use the default hidden choices and record the current version. Storage or
// parsing failures add defaults to the in-memory set, including any keys already restored
// before failure. The writes are sequential, so a failed version write can leave the
// updated hidden set available for the next load.
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      ;(JSON.parse(saved) as string[]).forEach((key) => {
        hiddenColumns.add(key)
      })
      // Append the newly hidden column before storing the migration version so saved
      // choices for existing columns remain intact.
      const savedVersion = localStorage.getItem(HIDDEN_COLUMNS_VERSION_KEY)
      if (savedVersion !== HIDDEN_COLUMNS_CURRENT_VERSION) {
        hiddenColumns.add('upstream_request_id')
        localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
        localStorage.setItem(HIDDEN_COLUMNS_VERSION_KEY, HIDDEN_COLUMNS_CURRENT_VERSION)
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => {
        hiddenColumns.add(key)
      })
      localStorage.setItem(HIDDEN_COLUMNS_VERSION_KEY, HIDDEN_COLUMNS_CURRENT_VERSION)
    }
  } catch {
    // Add usage defaults after a read or write failure; any keys already restored remain
    // in the set for this session.
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => {
      hiddenColumns.add(key)
    })
  }
}

// DetailTab identifies usage records, errors, or the administrator user ranking.
type DetailTab = 'usage' | 'errors' | 'ranking'
// Tab state selects the displayed section while rankingMounted retains the ranking instance
// after its first visit.
const activeTab = ref<DetailTab>('usage')
const detailTabs = computed(() => [
  { key: 'usage' as const, label: t('usage.tabs.usage'), icon: 'document' as const },
  { key: 'errors' as const, label: t('usage.tabs.errors'), icon: 'exclamationTriangle' as const },
  { key: 'ranking' as const, label: t('usage.tabs.ranking'), icon: 'chart' as const },
])
// Component refs coordinate ranking reloads and programmatic user-label changes with the
// shared filter panel.
const usageFiltersRef = ref<InstanceType<typeof UsageFilters> | null>(null)
const rankingMounted = ref(false)
const rankingRef = ref<InstanceType<typeof UserTokenRanking> | null>(null)

// switchTab selects the visible detail section and lazily starts its data needs. Empty
// error rows trigger retrieval, while first ranking selection mounts its component for
// subsequent reactive filter updates.
const switchTab = (tab: DetailTab) => {
  activeTab.value = tab
  if (tab === 'errors' && errRows.value.length === 0) loadAdminErrors()
  if (tab === 'ranking') rankingMounted.value = true
}

// Error request state owns its records, loading flag, paging, server sort, and selected
// detail dialog independently of usage rows.
const errRows = ref<OpsErrorLog[]>([])
const errLoading = ref(false)
const errPage = ref(1)
const errPageSize = ref(20)
const errTotal = ref(0)
const errSortBy = ref('created_at')
const errSortOrder = ref<'asc' | 'desc'>('desc')
const showErrorModal = ref(false)
const selectedErrorId = ref<number | null>(null)

// toRFC3339 converts a calendar date into a local day endpoint expressed in UTC. Missing
// dates return undefined, and the end flag selects the final millisecond of that local day.
const toRFC3339 = (d: string | undefined, endOfDay = false): string | undefined =>
  d ? new Date(d + (endOfDay ? 'T23:59:59.999' : 'T00:00:00')).toISOString() : undefined

// loadAdminErrors retrieves administrator error records using supported shared and
// error-specific predicates. Request-ID searches omit time bounds; failures retain earlier
// rows and report an error, and loading clears after settlement.
const loadAdminErrors = async () => {
  errLoading.value = true
  try {
    const resp = await listErrorLogs({
      page: errPage.value,
      page_size: errPageSize.value,
      view: 'all',
      request_id: filters.value.request_id || undefined,
      start_time: filters.value.request_id ? undefined : toRFC3339(filters.value.start_date),
      end_time: filters.value.request_id ? undefined : toRFC3339(filters.value.end_date, true),
      user_id: filters.value.user_id ?? undefined,
      api_key_id: filters.value.api_key_id ?? undefined,
      provider_id: filters.value.provider_id ?? undefined,
      group_id: filters.value.group_id ?? undefined,
      model: filters.value.model || undefined,
      phase: filters.value.error_phase || undefined,
      category: filters.value.error_category || undefined,
      status_codes:
        filters.value.status_code != null ? String(filters.value.status_code) : undefined,
      sort_by: errSortBy.value,
      sort_order: errSortOrder.value,
    })
    errRows.value = resp.items
    errTotal.value = resp.total
  } catch (error) {
    // Keep the earlier error page after retrieval fails and notify the administrator; the
    // finalizer still releases its loading indicator.
    console.error('Failed to load admin errors:', error)
    appStore.showError(t('usage.errors.failedToLoad'))
  } finally {
    errLoading.value = false
  }
}

// onErrSort stores the error ordering and reloads its first page.
const onErrSort = (sortBy: string, sortOrder: 'asc' | 'desc') => {
  errSortBy.value = sortBy
  errSortOrder.value = sortOrder
  errPage.value = 1
  loadAdminErrors()
}
// onErrPage selects an error page and starts its retrieval.
const onErrPage = (p: number) => {
  errPage.value = p
  loadAdminErrors()
}
// onErrPageSize changes error page size and reloads from the first page.
const onErrPageSize = (s: number) => {
  errPageSize.value = s
  errPage.value = 1
  loadAdminErrors()
}
// openError selects an error record and opens its administrator detail dialog.
const openError = (id: number) => {
  selectedErrorId.value = id
  showErrorModal.value = true
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

onMounted(() => {
  applyRouteQueryFilters()
  void loadRouteUserFilterLabel()
  loadLogs()
  loadStats()
  loadModelStats(modelDistributionSource.value, true)
  // Defer the initial trend and group snapshot briefly after starting table and summary
  // retrieval; the scheduled callback is not canceled on unmount.
  window.setTimeout(() => {
    void loadChartData()
  }, 120)
  loadSavedColumns()
  loadSavedErrColumns()
  // Register one document listener for this mounted page so clicks outside the column menu
  // can close its floating controls.
  document.addEventListener('click', handleColumnClickOutside)
})
watch(endpointDistributionSource, (source) => {
  void loadEndpointStats(source)
})

// Release page-owned request controllers and the outside-click listener when leaving this
// view so pending cancelable work cannot retain those resources.
onUnmounted(() => {
  statsReqSeq += 1
  endpointReqSeq += 1
  statsAbortController?.abort()
  endpointAbortController?.abort()
  abortController?.abort()
  exportAbortController?.abort()
  document.removeEventListener('click', handleColumnClickOutside)
})

watch(modelDistributionSource, (source) => {
  void loadModelStats(source)
})

defineExpose({ requestedModelStats, refreshData })
</script>

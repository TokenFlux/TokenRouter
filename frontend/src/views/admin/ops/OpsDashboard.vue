<template>
  <component
    :is="isFullscreen ? 'div' : AppLayout"
    :class="isFullscreen ? 'ba-theme-shell flex min-h-screen flex-col justify-center' : ''"
  >
    <div v-if="isFullscreen" class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

    <div :class="[isFullscreen ? 'relative z-10 p-4 md:p-6' : '', 'space-y-4 pb-12']">
      <div
        v-if="errorMessage"
        class="rounded-surface bg-red-50 p-4 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400"
      >
        {{ errorMessage }}
      </div>

      <OpsDashboardSkeleton v-if="loading && !hasLoadedOnce" :fullscreen="isFullscreen" />

      <OpsDashboardHeader
        v-else-if="opsEnabled"
        :overview="overview"
        :platform="platform"
        :group-id="groupId"
        :time-range="timeRange"
        :loading="loading"
        :last-updated="lastUpdated"
        :thresholds="metricThresholds"
        :auto-refresh-enabled="autoRefreshEnabled"
        :auto-refresh-countdown="autoRefreshCountdown"
        :fullscreen="isFullscreen"
        :custom-start-time="customStartTime"
        :custom-end-time="customEndTime"
        :groups="groups"
        @update:time-range="onTimeRangeChange"
        @update:platform="onPlatformChange"
        @update:group="onGroupChange"
        @update:custom-time-range="onCustomTimeRangeChange"
        @refresh="fetchData"
        @open-request-details="handleOpenRequestDetails"
        @open-error-details="openErrorDetails"
        @open-settings="showSettingsDialog = true"
        @open-alert-rules="showAlertRulesCard = true"
        @enter-fullscreen="enterFullscreen"
        @exit-fullscreen="exitFullscreen"
      />

      <!-- Row: Concurrency + Throughput -->
      <div
        v-if="opsEnabled && !(loading && !hasLoadedOnce)"
        class="grid grid-cols-1 gap-4 lg:grid-cols-4"
      >
        <div class="h-[360px] lg:col-span-1">
          <OpsConcurrencyCard
            :platform-filter="platform"
            :group-id-filter="groupId"
            :refresh-token="dashboardRefreshToken"
          />
        </div>
        <div class="lg:col-span-1 h-[360px]">
          <OpsSwitchRateTrendChart
            :points="switchTrend?.points ?? []"
            :loading="loadingSwitchTrend"
            :time-range="switchTrendTimeRange"
            :fullscreen="isFullscreen"
          />
        </div>
        <div class="lg:col-span-2 h-[360px]">
          <OpsThroughputTrendChart
            :points="throughputTrend?.points ?? []"
            :by-platform="throughputTrend?.by_platform ?? []"
            :top-groups="throughputTrend?.top_groups ?? []"
            :loading="loadingTrend"
            :time-range="timeRange"
            :fullscreen="isFullscreen"
            @select-platform="handleThroughputSelectPlatform"
            @select-group="handleThroughputSelectGroup"
            @open-details="handleOpenRequestDetails"
          />
        </div>
      </div>

      <!-- Row: Visual Analysis (baseline 3-up grid) -->
      <div
        v-if="opsEnabled && !(loading && !hasLoadedOnce)"
        class="grid grid-cols-1 gap-4 md:grid-cols-3"
      >
        <OpsLatencyChart
          :latency-data="latencyHistogram"
          :loading="loadingLatency"
          :bucket-boundaries="latencyBucketBoundaries"
          @update:bucket-boundaries="onLatencyBucketBoundariesChange"
        />
        <OpsErrorDistributionChart
          :data="errorDistribution"
          :loading="loadingErrorDistribution"
          @open-details="openErrorDetails('request')"
        />
        <OpsErrorTrendChart
          :points="errorTrend?.points ?? []"
          :loading="loadingErrorTrend"
          :time-range="timeRange"
          @open-request-errors="openErrorDetails('request')"
          @open-upstream-errors="openErrorDetails('upstream')"
        />
      </div>

      <!-- Token statistics remain visible and share the group selection with the dashboard toolbar. -->
      <div v-if="opsEnabled && !(loading && !hasLoadedOnce)" class="grid grid-cols-1 gap-4">
        <OpsTokenStatsCard
          :platform-filter="platform"
          :group-id-filter="groupId"
          :groups="groups"
          @update:group="onGroupChange"
        />
      </div>

      <!-- Alert Events -->
      <OpsAlertEventsCard v-if="opsEnabled && showAlertEvents && !(loading && !hasLoadedOnce)" />

      <!-- System Logs -->
      <OpsSystemLogTable
        v-if="opsEnabled && !(loading && !hasLoadedOnce)"
        :platform-filter="platform"
        :refresh-token="dashboardRefreshToken"
      />

      <!-- Settings Dialog (hidden in fullscreen mode) -->
      <template v-if="!isFullscreen">
        <OpsSettingsDialog
          :show="showSettingsDialog"
          @close="showSettingsDialog = false"
          @saved="onSettingsSaved"
        />

        <BaseDialog
          :show="showAlertRulesCard"
          :title="t('admin.ops.alertRules.title')"
          width="extra-wide"
          @close="showAlertRulesCard = false"
        >
          <OpsAlertRulesCard />
        </BaseDialog>

        <OpsErrorDetailsModal
          :show="showErrorDetails"
          :time-range="timeRange"
          :custom-start-time="customStartTime"
          :custom-end-time="customEndTime"
          :platform="platform"
          :group-id="groupId"
          :error-type="errorDetailsType"
          :resume-state="resumeListState"
          @update:show="showErrorDetails = $event"
          @openErrorDetail="openError"
        />

        <OpsErrorDetailModal
          v-model:show="showErrorModal"
          :error-id="selectedErrorId"
          :error-type="errorDetailsType"
          :back-to-list="detailReturnTarget !== null"
          @back="handleBackToList"
        />

        <OpsRequestDetailsModal
          v-model="showRequestDetails"
          :time-range="timeRange"
          :custom-start-time="customStartTime"
          :custom-end-time="customEndTime"
          :preset="requestDetailsPreset"
          :platform="platform"
          :group-id="groupId"
          :resume-state="resumeListState"
          @openErrorDetail="openError"
        />
      </template>
    </div>
  </component>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useDebounceFn, useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api'
import {
  opsAPI,
  type OpsDashboardOverview,
  type OpsErrorDistributionResponse,
  type OpsErrorTrendResponse,
  type OpsLatencyHistogramResponse,
  type OpsThroughputTrendResponse,
  type OpsMetricThresholds,
} from '@/api/admin/ops'
import { useAdminSettingsStore, useAppStore } from '@/stores'
import OpsDashboardHeader from './components/OpsDashboardHeader.vue'
import OpsDashboardSkeleton from './components/OpsDashboardSkeleton.vue'
import OpsConcurrencyCard from './components/OpsConcurrencyCard.vue'
import OpsErrorDetailModal from './components/OpsErrorDetailModal.vue'
import OpsErrorDistributionChart from './components/OpsErrorDistributionChart.vue'
import OpsErrorDetailsModal from './components/OpsErrorDetailsModal.vue'
import OpsErrorTrendChart from './components/OpsErrorTrendChart.vue'
import OpsLatencyChart from './components/OpsLatencyChart.vue'
import OpsThroughputTrendChart from './components/OpsThroughputTrendChart.vue'
import OpsSwitchRateTrendChart from './components/OpsSwitchRateTrendChart.vue'
import OpsAlertEventsCard from './components/OpsAlertEventsCard.vue'
import OpsTokenStatsCard from './components/OpsTokenStatsCard.vue'
import OpsSystemLogTable from './components/OpsSystemLogTable.vue'
import OpsRequestDetailsModal, {
  type OpsRequestDetailsPreset,
} from './components/OpsRequestDetailsModal.vue'
import OpsSettingsDialog from './components/OpsSettingsDialog.vue'
import OpsAlertRulesCard from './components/OpsAlertRulesCard.vue'
import {
  areDefaultLatencyBucketBoundaries,
  areLatencyBucketBoundariesEqual,
  defaultLatencyBucketBoundaries,
  normalizeLatencyBucketBoundaries,
  parseLatencyBucketBoundaries,
  serializeLatencyBucketBoundaries,
  LATENCY_BUCKET_STORAGE_KEY,
} from './latencyBuckets'

// OpsDashboard.vue coordinates historical Ops panels, shared filters, drilldown
// dialogs, and refresh timing. Dashboard requests share a generation and
// AbortController, while latency histograms use a separate pair so changing
// bucket boundaries can refresh that chart independently. URL filters, browser
// preferences, and defaults determine the initial histogram boundaries.
//
// The core snapshot loads before deferred histogram and error-distribution
// panels. When a current snapshot request fails, split legacy endpoints provide
// its constituent measurements. Generation checks prevent superseded responses
// from publishing panel data or loading state. List-return navigation records
// the originating dialog and briefly supplies resumeState so returning from a
// single error can retain filters and pagination.

// Route, application stores, and translations provide filter synchronization,
// monitoring enablement, and request feedback for the dashboard owner.
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const adminSettingsStore = useAdminSettingsStore()
const { t } = useI18n()

// opsEnabled follows the admin monitoring setting used to gate dashboard
// requests and the shared refresh countdown.
const opsEnabled = computed(() => adminSettingsStore.opsMonitoringEnabled)

// TimeRange names supported dashboard presets and the custom timestamp selection
// mode.
type TimeRange = '5m' | '30m' | '1h' | '6h' | '24h' | 'custom'
// allowedTimeRanges validates URL and selector inputs before they replace the
// dashboard time-range state used by queries.
const allowedTimeRanges = new Set<TimeRange>(['5m', '30m', '1h', '6h', '24h', 'custom'])

// Dashboard presentation tracks initial completion separately from current
// refresh progress so loaded panels can remain visible during updates.
const loading = ref(true)
const hasLoadedOnce = ref(false)
const errorMessage = ref('')
const lastUpdated = ref<Date | null>(new Date())

// Filter state owns the selected time interval, execution platform, group, and
// histogram boundaries shared by child panels.
const timeRange = ref<TimeRange>('1h')
const platform = ref<string>('')
const groupId = ref<number | null>(null)
const groups = ref<
  Array<{
    id: number // persisted group identifier
    name: string // group selector display name
    display_brand?: string // group badge brand, drawn as the filter icon
  }>
>([])
const latencyBucketBoundaries = ref<number[]>(defaultLatencyBucketBoundaries())
const invalidLatencyBoundsQuery = ref(false)
const customStartTime = ref<string | null>(null)
const customEndTime = ref<string | null>(null)
// Switch history uses a fixed five-hour interval in milliseconds, independent of
// the toolbar range selected for other panels.
const switchTrendWindowHours = 5
const switchTrendTimeRange = `${switchTrendWindowHours}h`
const switchTrendWindowMs = switchTrendWindowHours * 60 * 60 * 1000

// QUERY_KEYS names dashboard-owned URL parameters that are removed and rebuilt
// while unrelated query parameters remain intact.
const QUERY_KEYS = {
  timeRange: 'tr',
  platform: 'platform',
  groupId: 'group_id',
  latencyBounds: 'latency_bounds',
  fullscreen: 'fullscreen',

  // Deep links
  openErrorDetails: 'open_error_details',
  errorType: 'error_type',
  alertRuleId: 'alert_rule_id',
  openAlertRules: 'open_alert_rules',
} as const

// Route synchronization flags distinguish applying incoming filters from
// replacing the URL, preventing each direction from immediately echoing itself.
const isApplyingRouteQuery = ref(false)
const isSyncingRouteQuery = ref(false)

// isFullscreen recognizes the supported truthy query values used to select the
// dashboard presentation layout and Escape behavior.
const isFullscreen = computed(() => {
  const val = route.query[QUERY_KEYS.fullscreen]
  return val === '1' || val === 'true'
})

// exitFullscreen removes the fullscreen query key while retaining other route
// parameters.
function exitFullscreen() {
  const nextQuery = { ...route.query }
  delete nextQuery[QUERY_KEYS.fullscreen]
  router.replace({ query: nextQuery })
}

// enterFullscreen enables the fullscreen query flag while retaining other route
// parameters.
function enterFullscreen() {
  const nextQuery = { ...route.query, [QUERY_KEYS.fullscreen]: '1' }
  router.replace({ query: nextQuery })
}

// handleKeydown exits fullscreen when Escape is pressed in that presentation
// mode.
function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && isFullscreen.value) {
    exitFullscreen()
  }
}

// Dashboard controller and sequence belong to the shared snapshot refresh;
// latency has its own controller and sequence for independent bucket edits.
let dashboardFetchController: AbortController | null = null
let dashboardFetchSeq = 0
let latencyFetchController: AbortController | null = null
let latencyFetchSeq = 0

// isCanceledRequest recognizes the cancellation code returned by the HTTP
// client.
function isCanceledRequest(err: unknown): boolean {
  return (
    !!err &&
    typeof err === 'object' &&
    'code' in err &&
    (err as Record<string, unknown>).code === 'ERR_CANCELED'
  )
}

// abortDashboardFetch cancels the currently owned dashboard controller and
// releases its reference. Callers advancing the refresh generation separately
// invalidate pending panel publications after the abort has been requested.
function abortDashboardFetch() {
  if (dashboardFetchController) {
    dashboardFetchController.abort()
    dashboardFetchController = null
  }
}

// abortLatencyFetch cancels the currently owned histogram controller and
// releases its reference. Histogram refresh separately advances its generation
// so late completions from older bucket selections cannot replace the latest
// data.
function abortLatencyFetch() {
  if (latencyFetchController) {
    latencyFetchController.abort()
    latencyFetchController = null
  }
}

// readQueryString reads a string query value or the first string in its array
// form. Other shapes return an empty string for callers that select their own
// defaults.
const readQueryString = (key: string): string => {
  const value = route.query[key]
  if (typeof value === 'string') return value
  if (Array.isArray(value) && typeof value[0] === 'string') return value[0]
  return ''
}

// readQueryNumber parses a query string as an integer prefix and returns null
// when absent or nonfinite. Callers apply positive-ID validation when the value
// represents a group or alert rule.
const readQueryNumber = (key: string): number | null => {
  const raw = readQueryString(key)
  if (!raw) return null
  const n = Number.parseInt(raw, 10)
  return Number.isFinite(n) ? n : null
}

// readPersistedLatencyBucketBoundaries parses the browser preference into
// validated millisecond boundaries. Unavailable browser storage, parsing
// failure, or a missing value returns null so route initialization can choose
// standard defaults.
function readPersistedLatencyBucketBoundaries(): number[] | null {
  if (typeof window === 'undefined') return null

  try {
    const raw = window.localStorage.getItem(LATENCY_BUCKET_STORAGE_KEY) || ''
    return parseLatencyBucketBoundaries(raw)
  } catch {
    // When browser storage cannot be read, return no preference so
    // initialization can continue with validated defaults.
    return null
  }
}

// persistLatencyBucketBoundaries stores custom boundaries or removes the
// preference when defaults are selected. Browser storage failures are suppressed
// so the applied in-memory selection can remain usable in the current page.
function persistLatencyBucketBoundaries(boundaries: readonly number[]) {
  if (typeof window === 'undefined') return

  try {
    if (areDefaultLatencyBucketBoundaries(boundaries)) {
      window.localStorage.removeItem(LATENCY_BUCKET_STORAGE_KEY)
      return
    }
    window.localStorage.setItem(
      LATENCY_BUCKET_STORAGE_KEY,
      serializeLatencyBucketBoundaries(boundaries),
    )
  } catch {
    // Storage failure leaves the accepted in-memory boundaries available, so
    // histogram requests can still use the current selection.
  }
}

// applyRouteQueryToState applies supported filters and opens dialogs requested
// by deep links. A valid URL histogram setting wins over persisted browser
// preferences, which win over standard boundaries.
//
// Invalid explicit histogram text is recorded for later URL cleanup even when a
// stored preference can be used. Group IDs require a positive number, and
// unsupported time ranges leave the current range unchanged. Deep-link flags
// open their target dialogs but do not close dialogs when absent, retaining the
// current local navigation context.
const applyRouteQueryToState = () => {
  const nextTimeRange = readQueryString(QUERY_KEYS.timeRange)
  if (nextTimeRange && allowedTimeRanges.has(nextTimeRange as TimeRange)) {
    timeRange.value = nextTimeRange as TimeRange
  }

  platform.value = readQueryString(QUERY_KEYS.platform) || ''

  const groupIdRaw = readQueryNumber(QUERY_KEYS.groupId)
  groupId.value = typeof groupIdRaw === 'number' && groupIdRaw > 0 ? groupIdRaw : null

  const rawLatencyBounds = readQueryString(QUERY_KEYS.latencyBounds)
  const parsedLatencyBounds = parseLatencyBucketBoundaries(rawLatencyBounds)
  invalidLatencyBoundsQuery.value = rawLatencyBounds !== '' && parsedLatencyBounds === null
  latencyBucketBoundaries.value =
    parsedLatencyBounds ??
    readPersistedLatencyBucketBoundaries() ??
    defaultLatencyBucketBoundaries()

  // Deep links
  const openRules = readQueryString(QUERY_KEYS.openAlertRules)
  if (openRules === '1' || openRules === 'true') {
    showAlertRulesCard.value = true
  }

  const ruleID = readQueryNumber(QUERY_KEYS.alertRuleId)
  if (typeof ruleID === 'number' && ruleID > 0) {
    showAlertRulesCard.value = true
  }

  const openErr = readQueryString(QUERY_KEYS.openErrorDetails)
  if (openErr === '1' || openErr === 'true') {
    const typ = readQueryString(QUERY_KEYS.errorType)
    errorDetailsType.value = typ === 'upstream' ? 'upstream' : 'request'
    showErrorDetails.value = true
  }
}

// buildQueryFromState copies unrelated route parameters and rebuilds
// dashboard-owned filter keys. Default values are omitted, custom histogram
// boundaries are serialized, and active fullscreen state is reinstated.
//
// Deep-link action keys are removed with the owned key set and are not
// regenerated from open-dialog state. The result therefore describes persistent
// filter and presentation choices after an action link is consumed. Custom
// timestamps are maintained in component state and are not serialized by this
// query builder.
const buildQueryFromState = () => {
  const next: Record<string, any> = { ...route.query }

  Object.values(QUERY_KEYS).forEach((k) => {
    delete next[k]
  })

  if (timeRange.value !== '1h') next[QUERY_KEYS.timeRange] = timeRange.value
  if (platform.value) next[QUERY_KEYS.platform] = platform.value
  if (typeof groupId.value === 'number' && groupId.value > 0)
    next[QUERY_KEYS.groupId] = String(groupId.value)
  if (!areDefaultLatencyBucketBoundaries(latencyBucketBoundaries.value)) {
    next[QUERY_KEYS.latencyBounds] = serializeLatencyBucketBoundaries(latencyBucketBoundaries.value)
  }
  // Reinsert fullscreen after rebuilding owned query keys so changing filters
  // keeps the current dashboard presentation mode active.
  if (isFullscreen.value) next[QUERY_KEYS.fullscreen] = '1'

  return next
}

// syncQueryToRoute debounces publication of dashboard filters into the route
// query. It skips incoming-route application and compares generated key values
// with the current query before deciding whether replacement is needed.
//
// The synchronization flag stays set until router.replace settles, allowing the
// route watcher to distinguish this publication from outside navigation. Only
// dashboard-owned keys are rebuilt by buildQueryFromState, so unrelated query
// values survive. The 250-millisecond debounce coalesces nearby filter edits
// before that comparison and replacement sequence.
const syncQueryToRoute = useDebounceFn(async () => {
  if (isApplyingRouteQuery.value) return
  const nextQuery = buildQueryFromState()

  const curr = route.query as Record<string, any>
  const nextKeys = Object.keys(nextQuery)
  const currKeys = Object.keys(curr)
  const sameLength = nextKeys.length === currKeys.length
  const sameValues =
    sameLength && nextKeys.every((k) => String(curr[k] ?? '') === String(nextQuery[k] ?? ''))
  if (sameValues) return

  try {
    isSyncingRouteQuery.value = true
    await router.replace({ query: nextQuery })
  } finally {
    isSyncingRouteQuery.value = false
  }
}, 250)

// Overview and threshold snapshots supply the header, while each chart retains
// its own response and refresh flag for independent presentation.
const overview = ref<OpsDashboardOverview | null>(null)
const metricThresholds = ref<OpsMetricThresholds | null>(null)

const throughputTrend = ref<OpsThroughputTrendResponse | null>(null)
const loadingTrend = ref(false)

const switchTrend = ref<OpsThroughputTrendResponse | null>(null)
const loadingSwitchTrend = ref(false)

const latencyHistogram = ref<OpsLatencyHistogramResponse | null>(null)
const loadingLatency = ref(false)

const errorTrend = ref<OpsErrorTrendResponse | null>(null)
const loadingErrorTrend = ref(false)

const errorDistribution = ref<OpsErrorDistributionResponse | null>(null)
const loadingErrorDistribution = ref(false)

// Dialog state selects one error or list view and stores the request drilldown
// preset supplied by header and chart actions.
const selectedErrorId = ref<number | null>(null)
const showErrorModal = ref(false)

const showErrorDetails = ref(false)
const errorDetailsType = ref<'request' | 'upstream'>('request')

const showRequestDetails = ref(false)
const requestDetailsPreset = ref<OpsRequestDetailsPreset>({
  title: '',
  kind: 'all',
  sort: 'created_at_desc',
})

// DetailReturnTarget remembers which list opened a single-error dialog, or no
// list.
type DetailReturnTarget = 'errorList' | 'requestList' | null
// detailReturnTarget and resumeListState coordinate reopening the originating
// list before its normal fresh-open reset resumes for later navigation.
const detailReturnTarget = ref<DetailReturnTarget>(null)

// resumeListState briefly tells list watchers to retain filters and pagination
// during the return transition from a single error.
const resumeListState = ref(false)

// Settings and alert-rule visibility belongs to the dashboard, allowing toolbar
// actions and route deep links to open their panels.
const showSettingsDialog = ref(false)
const showAlertRulesCard = ref(false)

applyRouteQueryToState()
if (invalidLatencyBoundsQuery.value) {
  syncQueryToRoute()
}

// Refresh settings come from advanced configuration; the interval is
// milliseconds while the displayed countdown is whole seconds.
const showAlertEvents = ref(true)
const autoRefreshEnabled = ref(false)
const autoRefreshIntervalMs = ref(30000) // default 30 seconds
const autoRefreshCountdown = ref(0)

// dashboardRefreshToken advances after core refresh completion so child panels
// can follow the shared dashboard cadence.
const dashboardRefreshToken = ref(0)

// The countdown interval ticks once per second while enabled and monitoring is
// active, pausing countdown work during a core refresh.
const { pause: pauseCountdown, resume: resumeCountdown } = useIntervalFn(
  () => {
    if (!autoRefreshEnabled.value) return
    if (!opsEnabled.value) return
    if (loading.value) return

    if (autoRefreshCountdown.value <= 0) {
      // Fetch immediately when the countdown reaches 0. fetchData() will reset
      // the countdown to the full interval.
      fetchData()
      return
    }

    autoRefreshCountdown.value -= 1
  },
  1000,
  { immediate: false },
)

// loadDashboardAdvancedSettings loads display and refresh settings from the
// server. Failure restores visible alerts with automatic refresh disabled;
// successful intervals convert seconds to milliseconds for the dashboard timer
// state.
async function loadDashboardAdvancedSettings() {
  try {
    const settings = await opsAPI.getAdvancedSettings()
    showAlertEvents.value = settings.display_alert_events
    autoRefreshEnabled.value = settings.auto_refresh_enabled
    // Convert server seconds to milliseconds for the configured timer interval.
    autoRefreshIntervalMs.value = settings.auto_refresh_interval_seconds * 1000
    autoRefreshCountdown.value = settings.auto_refresh_interval_seconds
  } catch (err) {
    // A settings failure restores visible alerts and disables automatic
    // refresh so dashboard reads can still be triggered manually.
    console.error('[OpsDashboard] Failed to load dashboard advanced settings', err)
    showAlertEvents.value = true
    autoRefreshEnabled.value = false
    autoRefreshIntervalMs.value = 30000
    autoRefreshCountdown.value = 0
  }
}

// loadGroups retrieves selectable group identities, names, and display brands for child
// controls.
// Failure logs the request error and clears the choices while leaving the
// currently selected filter value untouched.
async function loadGroups() {
  try {
    const list = await adminAPI.groups.getAll()
    groups.value = list.map((group) => ({
      id: group.id,
      name: group.name,
      display_brand: group.display_brand,
    }))
  } catch (err) {
    // Clear unavailable group choices after a failed lookup while retaining
    // the selected filter for subsequent dashboard queries.
    console.error('[OpsDashboard] Failed to load groups', err)
    groups.value = []
  }
}

// handleThroughputSelectPlatform applies the chart platform choice and clears
// the group filter.
function handleThroughputSelectPlatform(nextPlatform: string) {
  platform.value = nextPlatform || ''
  groupId.value = null
}

// handleThroughputSelectGroup applies a positive finite chart group identifier.
// Invalid or nonpositive selections clear the group filter, leaving the active
// platform selection available for the next dashboard query.
function handleThroughputSelectGroup(nextGroupId: number) {
  const id = Number.isFinite(nextGroupId) && nextGroupId > 0 ? nextGroupId : null
  groupId.value = id
}

// handleOpenRequestDetails opens the request list with merged drilldown
// defaults. A missing title receives the translated standard title, and both
// error dialogs close before the request list becomes visible.
function handleOpenRequestDetails(preset?: OpsRequestDetailsPreset) {
  const basePreset: OpsRequestDetailsPreset = {
    title: t('admin.ops.requestDetails.title'),
    kind: 'all',
    sort: 'created_at_desc',
  }

  requestDetailsPreset.value = { ...basePreset, ...(preset ?? {}) }
  if (!requestDetailsPreset.value.title) requestDetailsPreset.value.title = basePreset.title
  // Ensure only one modal visible at a time.
  showErrorDetails.value = false
  showErrorModal.value = false
  showRequestDetails.value = true
}

// openErrorDetails selects request or upstream errors and opens that list. Other
// request and error-detail dialogs close first so the dashboard exposes one
// investigation dialog at a time.
function openErrorDetails(kind: 'request' | 'upstream') {
  errorDetailsType.value = kind
  // Ensure only one modal visible at a time.
  showRequestDetails.value = false
  showErrorModal.value = false
  showErrorDetails.value = true
}

// onTimeRangeChange accepts supported string presets and the custom mode token.
// Other selector value types or unknown strings leave the current range
// unchanged so query construction keeps a recognized mode.
function onTimeRangeChange(v: string | number | boolean | null) {
  if (typeof v !== 'string') return
  if (!allowedTimeRanges.has(v as TimeRange)) return
  timeRange.value = v as TimeRange
}

// onCustomTimeRangeChange records both timestamp bounds supplied by the header
// editor.
function onCustomTimeRangeChange(startTime: string, endTime: string) {
  customStartTime.value = startTime
  customEndTime.value = endTime
}

// onSettingsSaved reloads advanced settings before refreshing thresholds and
// dashboard data. The latter requests are started independently after refresh
// timing and alert visibility settings have been applied locally.
async function onSettingsSaved() {
  await loadDashboardAdvancedSettings()
  loadThresholds()
  fetchData()
}

// onPlatformChange accepts a string platform selection and clears it for other
// selector value types. The filter watcher then owns any dashboard refresh and
// URL synchronization caused by the change.
function onPlatformChange(v: string | number | boolean | null) {
  const nextPlatform = typeof v === 'string' ? v : ''
  platform.value = nextPlatform
}

// onGroupChange normalizes supported selector values into a positive group
// identifier. Null clears the filter; nonpositive or invalid numeric strings
// clear it, and booleans leave the existing group unchanged.
function onGroupChange(v: string | number | boolean | null) {
  if (v === null) {
    groupId.value = null
    return
  }
  if (typeof v === 'number') {
    groupId.value = v > 0 ? v : null
    return
  }
  if (typeof v === 'string') {
    const n = Number.parseInt(v, 10)
    groupId.value = Number.isFinite(n) && n > 0 ? n : null
  }
}

// onLatencyBucketBoundariesChange validates a new histogram configuration before
// applying it. Invalid or identical boundaries do nothing; accepted boundaries
// update memory, browser preference, and URL before refreshing the histogram
// when monitoring is enabled.
function onLatencyBucketBoundariesChange(values: number[]) {
  const normalized = normalizeLatencyBucketBoundaries(values)
  if (!normalized || areLatencyBucketBoundariesEqual(normalized, latencyBucketBoundaries.value))
    return

  latencyBucketBoundaries.value = normalized
  persistLatencyBucketBoundaries(normalized)
  syncQueryToRoute()
  if (opsEnabled.value) {
    void refreshLatencyHistogram()
  }
}

// openError selects a single error and remembers the currently visible
// originating list. Both list dialogs close before details open, so
// handleBackToList can later restore the correct local list context.
function openError(id: number) {
  selectedErrorId.value = id
  // Capture the visible list before hiding it so returning from details can
  // restore the correct pagination context.
  detailReturnTarget.value = showRequestDetails.value
    ? 'requestList'
    : showErrorDetails.value
      ? 'errorList'
      : null
  // Ensure only one modal visible at a time.
  showErrorDetails.value = false
  showRequestDetails.value = false
  showErrorModal.value = true
}

// handleBackToList reopens the list recorded when single-error details were
// opened. It first enables resumeListState so the list visibility watcher keeps
// existing filters and pagination during this transition.
//
// After selecting the appropriate dialog, the origin is cleared and a zero-delay
// timer resets the resume flag. This lets the current visibility watchers
// consume the return context while a later fresh opening resets normally. A
// missing origin opens neither list, though the temporary resume flag is still
// cleared.
function handleBackToList() {
  const target = detailReturnTarget.value
  resumeListState.value = true
  if (target === 'requestList') {
    showErrorModal.value = false
    showErrorDetails.value = false
    showRequestDetails.value = true
  } else if (target === 'errorList') {
    showErrorModal.value = false
    showRequestDetails.value = false
    showErrorDetails.value = true
  }
  detailReturnTarget.value = null
  // Clear the resume flag after visibility watchers consume it, allowing a later
  // fresh opening to reset its local filters.
  window.setTimeout(() => {
    resumeListState.value = false
  }, 0)
}

// buildApiParams builds shared execution filters and a supported dashboard time
// selection. Complete custom bounds produce timestamps; incomplete custom bounds
// use one hour, and empty platform or group values are omitted.
function buildApiParams() {
  const params: any = {
    platform: platform.value || undefined,
    group_id: groupId.value ?? undefined,
  }

  if (timeRange.value === 'custom') {
    if (customStartTime.value && customEndTime.value) {
      params.start_time = customStartTime.value
      params.end_time = customEndTime.value
    } else {
      // Incomplete custom bounds cannot form an explicit interval; use a supported
      // one-hour preset while retaining the platform and group predicates.
      params.time_range = '1h'
    }
  } else {
    params.time_range = timeRange.value
  }

  return params
}

// buildSwitchTrendParams creates a fixed five-hour interval ending at the
// current instant. It keeps the active platform and group filters while ignoring
// the toolbar range used by other panels.
function buildSwitchTrendParams() {
  const params: any = {
    platform: platform.value || undefined,
    group_id: groupId.value ?? undefined,
  }
  const endTime = new Date()
  const startTime = new Date(endTime.getTime() - switchTrendWindowMs)
  params.start_time = startTime.toISOString()
  params.end_time = endTime.toISOString()
  return params
}

// refreshOverviewWithCancel retrieves overview for the supplied dashboard
// generation and signal. Monitoring-disabled calls return before querying, and
// superseded or canceled requests cannot publish new data or errors.
//
// A current request failure clears its panel response and reports the available
// error message. Callers advance dashboardFetchSeq when replacing a refresh; the
// captured sequence determines whether completion still belongs to the active
// dashboard selection. Any panel loading flag is cleared only by that matching
// generation.
async function refreshOverviewWithCancel(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  try {
    const data = await opsAPI.getDashboardOverview(buildApiParams(), { signal })
    // A completed read can belong to an older filter generation; reject it before
    // replacing the overview visible for the current selection.
    if (fetchSeq !== dashboardFetchSeq) return
    overview.value = data
  } catch (err: any) {
    // A current failed read clears the overview and reports its error;
    // obsolete or canceled work leaves displayed measurements untouched.
    if (fetchSeq !== dashboardFetchSeq || isCanceledRequest(err)) return
    overview.value = null
    appStore.showError(err?.message || t('admin.ops.failedToLoadOverview'))
  }
}

// refreshSwitchTrendWithCancel retrieves fixed-window switch history for the
// supplied dashboard generation and signal. Monitoring-disabled calls return
// before querying, and superseded or canceled requests cannot publish new data
// or errors.
//
// A current request failure clears its panel response and reports the available
// error message. Callers advance dashboardFetchSeq when replacing a refresh; the
// captured sequence determines whether completion still belongs to the active
// dashboard selection. Any panel loading flag is cleared only by that matching
// generation.
async function refreshSwitchTrendWithCancel(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  loadingSwitchTrend.value = true
  try {
    const data = await opsAPI.getThroughputTrend(buildSwitchTrendParams(), { signal })
    if (fetchSeq !== dashboardFetchSeq) return
    switchTrend.value = data
  } catch (err: any) {
    // A current failed read clears the switch trend and reports its error;
    // obsolete or canceled work leaves displayed measurements untouched.
    if (fetchSeq !== dashboardFetchSeq || isCanceledRequest(err)) return
    switchTrend.value = null
    appStore.showError(err?.message || t('admin.ops.failedToLoadSwitchTrend'))
  } finally {
    if (fetchSeq === dashboardFetchSeq) {
      loadingSwitchTrend.value = false
    }
  }
}

// refreshThroughputTrendWithCancel retrieves throughput history for the supplied
// dashboard generation and signal. Monitoring-disabled calls return before
// querying, and superseded or canceled requests cannot publish new data or
// errors.
//
// A current request failure clears its panel response and reports the available
// error message. Callers advance dashboardFetchSeq when replacing a refresh; the
// captured sequence determines whether completion still belongs to the active
// dashboard selection. Any panel loading flag is cleared only by that matching
// generation.
async function refreshThroughputTrendWithCancel(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  loadingTrend.value = true
  try {
    const data = await opsAPI.getThroughputTrend(buildApiParams(), { signal })
    if (fetchSeq !== dashboardFetchSeq) return
    throughputTrend.value = data
  } catch (err: any) {
    // A current failed read clears the throughput chart and reports its
    // error; obsolete or canceled work leaves displayed measurements
    // untouched.
    if (fetchSeq !== dashboardFetchSeq || isCanceledRequest(err)) return
    throughputTrend.value = null
    appStore.showError(err?.message || t('admin.ops.failedToLoadThroughputTrend'))
  } finally {
    if (fetchSeq === dashboardFetchSeq) {
      loadingTrend.value = false
    }
  }
}

// refreshCoreSnapshotWithCancel retrieves overview, throughput, and error trends
// in one request for the current generation. A successful current result
// publishes those three snapshots together.
//
// If the request fails without cancellation or supersession, the legacy
// endpoints are queried in parallel to populate the same panels independently.
// Each delegated request checks the shared generation before publication, and
// the matching generation clears loading flags after the sequence settles.
// Partial legacy failures can therefore leave different panels populated or
// cleared according to their own results.
async function refreshCoreSnapshotWithCancel(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  loadingTrend.value = true
  loadingErrorTrend.value = true
  try {
    const data = await opsAPI.getDashboardSnapshotV2(buildApiParams(), { signal })
    if (fetchSeq !== dashboardFetchSeq) return
    overview.value = data.overview
    throughputTrend.value = data.throughput_trend
    errorTrend.value = data.error_trend
  } catch (err: any) {
    if (fetchSeq !== dashboardFetchSeq || isCanceledRequest(err)) return
    // Any current non-cancellation failure retries the three panels through split
    // endpoints, allowing successful panels to remain available when another fails.
    await Promise.all([
      refreshOverviewWithCancel(fetchSeq, signal),
      refreshThroughputTrendWithCancel(fetchSeq, signal),
      refreshErrorTrendWithCancel(fetchSeq, signal),
    ])
  } finally {
    if (fetchSeq === dashboardFetchSeq) {
      loadingTrend.value = false
      loadingErrorTrend.value = false
    }
  }
}

// refreshLatencyHistogram starts an independently cancellable request for the
// current millisecond bucket boundaries. It aborts the previous histogram
// request and advances a dedicated sequence before creating the next controller.
//
// Only a matching sequence publishes histogram data, reports failure, or
// releases loading state and controller ownership. This separates bucket edits
// from dashboard-wide refreshes while preventing an older histogram from
// replacing a newer selection. The request sends a copied boundary array
// alongside the current dashboard filters, and monitoring-disabled calls return
// before changing request ownership.
async function refreshLatencyHistogram() {
  if (!opsEnabled.value) return

  // Cancel the preceding histogram read before publishing a new sequence so bucket
  // edits replace ownership of both response and loading state.
  abortLatencyFetch()
  latencyFetchSeq += 1
  const fetchSeq = latencyFetchSeq
  latencyFetchController = new AbortController()
  loadingLatency.value = true
  try {
    const data = await opsAPI.getLatencyHistogram(
      {
        ...buildApiParams(),
        bucket_boundaries_ms: [...latencyBucketBoundaries.value],
      },
      { signal: latencyFetchController.signal },
    )
    if (fetchSeq !== latencyFetchSeq) return
    latencyHistogram.value = data
  } catch (err: any) {
    // A current failed read clears the histogram and reports its error;
    // obsolete or canceled work leaves displayed measurements untouched.
    if (fetchSeq !== latencyFetchSeq || isCanceledRequest(err)) return
    latencyHistogram.value = null
    appStore.showError(err?.message || t('admin.ops.failedToLoadLatencyHistogram'))
  } finally {
    if (fetchSeq === latencyFetchSeq) {
      loadingLatency.value = false
      latencyFetchController = null
    }
  }
}

// refreshErrorTrendWithCancel retrieves error history for the supplied dashboard
// generation and signal. Monitoring-disabled calls return before querying, and
// superseded or canceled requests cannot publish new data or errors.
//
// A current request failure clears its panel response and reports the available
// error message. Callers advance dashboardFetchSeq when replacing a refresh; the
// captured sequence determines whether completion still belongs to the active
// dashboard selection. Any panel loading flag is cleared only by that matching
// generation.
async function refreshErrorTrendWithCancel(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  loadingErrorTrend.value = true
  try {
    const data = await opsAPI.getErrorTrend(buildApiParams(), { signal })
    if (fetchSeq !== dashboardFetchSeq) return
    errorTrend.value = data
  } catch (err: any) {
    // A current failed read clears the error trend and reports its error;
    // obsolete or canceled work leaves displayed measurements untouched.
    if (fetchSeq !== dashboardFetchSeq || isCanceledRequest(err)) return
    errorTrend.value = null
    appStore.showError(err?.message || t('admin.ops.failedToLoadErrorTrend'))
  } finally {
    if (fetchSeq === dashboardFetchSeq) {
      loadingErrorTrend.value = false
    }
  }
}

// refreshErrorDistributionWithCancel retrieves error distribution for the
// supplied dashboard generation and signal. Monitoring-disabled calls return
// before querying, and superseded or canceled requests cannot publish new data
// or errors.
//
// A current request failure clears its panel response and reports the available
// error message. Callers advance dashboardFetchSeq when replacing a refresh; the
// captured sequence determines whether completion still belongs to the active
// dashboard selection. Any panel loading flag is cleared only by that matching
// generation.
async function refreshErrorDistributionWithCancel(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  loadingErrorDistribution.value = true
  try {
    const data = await opsAPI.getErrorDistribution(buildApiParams(), { signal })
    if (fetchSeq !== dashboardFetchSeq) return
    errorDistribution.value = data
  } catch (err: any) {
    // A current failed read clears the error distribution and reports its
    // error; obsolete or canceled work leaves displayed measurements
    // untouched.
    if (fetchSeq !== dashboardFetchSeq || isCanceledRequest(err)) return
    errorDistribution.value = null
    appStore.showError(err?.message || t('admin.ops.failedToLoadErrorDistribution'))
  } finally {
    if (fetchSeq === dashboardFetchSeq) {
      loadingErrorDistribution.value = false
    }
  }
}

// refreshDeferredPanels starts histogram and error-distribution refreshes after
// the core view is available. Distribution uses the dashboard generation and
// signal; histogram refresh owns its separate cancellation and publication
// sequence.
async function refreshDeferredPanels(fetchSeq: number, signal: AbortSignal) {
  if (!opsEnabled.value) return
  await Promise.all([
    refreshLatencyHistogram(),
    refreshErrorDistributionWithCancel(fetchSeq, signal),
  ])
}

// isOpsDisabledError recognizes the explicit Ops-disabled code on structured API
// failures.
function isOpsDisabledError(err: unknown): boolean {
  return (
    !!err &&
    typeof err === 'object' &&
    'code' in err &&
    typeof (err as Record<string, unknown>).code === 'string' &&
    (err as Record<string, unknown>).code === 'OPS_DISABLED'
  )
}

// fetchData begins a new dashboard generation and cancels the preceding shared
// request. Core snapshot and fixed-window switch data run together, and only the
// current generation updates refresh time and child tokens.
//
// After core helpers settle, the countdown resets and deferred panels start
// without blocking the main loading state. Core helpers can handle individual
// failures internally, so completion does not imply that every panel succeeded.
// The current generation alone clears loading and marks the first load complete,
// while monitoring-disabled errors suppress the generic page message.
async function fetchData() {
  if (!opsEnabled.value) return

  // Cancel the preceding controller before advancing the generation so older
  // dashboard reads lose ownership before this refresh publishes its results.
  abortDashboardFetch()
  dashboardFetchSeq += 1
  const fetchSeq = dashboardFetchSeq
  dashboardFetchController = new AbortController()

  loading.value = true
  errorMessage.value = ''
  try {
    await Promise.all([
      refreshCoreSnapshotWithCancel(fetchSeq, dashboardFetchController.signal),
      refreshSwitchTrendWithCancel(fetchSeq, dashboardFetchController.signal),
    ])
    if (fetchSeq !== dashboardFetchSeq) return

    lastUpdated.value = new Date()

    // Trigger child component refreshes using the same cadence as the header.
    dashboardRefreshToken.value += 1

    if (autoRefreshEnabled.value) {
      // Use whole seconds for the visible refresh countdown.
      autoRefreshCountdown.value = Math.floor(autoRefreshIntervalMs.value / 1000)
    }

    // Defer non-core visual panels to reduce initial blocking.
    void refreshDeferredPanels(fetchSeq, dashboardFetchController.signal)
  } catch (err) {
    if (!isOpsDisabledError(err)) {
      console.error('[ops] failed to fetch dashboard data', err)
      errorMessage.value = t('admin.ops.failedToLoadData')
    }
  } finally {
    if (fetchSeq === dashboardFetchSeq) {
      loading.value = false
      hasLoadedOnce.value = true
    }
  }
}

watch(
  // Observe custom bounds as well as the range token so editing an
  // already-custom interval refreshes the displayed measurements.
  () =>
    [
      timeRange.value,
      customStartTime.value,
      customEndTime.value,
      platform.value,
      groupId.value,
    ] as const,
  () => {
    if (isApplyingRouteQuery.value) return
    if (opsEnabled.value) {
      fetchData()
    }
    syncQueryToRoute()
  },
)

watch(
  () => route.query,
  () => {
    if (isSyncingRouteQuery.value) return

    const prevTimeRange = timeRange.value
    const prevPlatform = platform.value
    const prevGroupId = groupId.value
    const prevLatencyBounds = [...latencyBucketBoundaries.value]

    // Mark incoming route application while filters change so the state watcher does
    // not immediately publish those same changes back to the router.
    isApplyingRouteQuery.value = true
    applyRouteQueryToState()
    isApplyingRouteQuery.value = false

    const dashboardFiltersChanged =
      prevTimeRange !== timeRange.value ||
      prevPlatform !== platform.value ||
      prevGroupId !== groupId.value
    const latencyBoundsChanged = !areLatencyBucketBoundariesEqual(
      prevLatencyBounds,
      latencyBucketBoundaries.value,
    )
    if (dashboardFiltersChanged) {
      if (opsEnabled.value) {
        fetchData()
      }
    } else if (latencyBoundsChanged && opsEnabled.value) {
      void refreshLatencyHistogram()
    }
    if (invalidLatencyBoundsQuery.value) {
      syncQueryToRoute()
    }
  },
)

// Register Escape handling before loading admin settings, then wait for
// presentation preferences and group choices before fetching initial dashboard
// measurements.
onMounted(async () => {
  // Fullscreen mode: listen for ESC key
  window.addEventListener('keydown', handleKeydown)

  await adminSettingsStore.fetch()
  if (!adminSettingsStore.opsMonitoringEnabled) {
    await router.replace('/admin/settings')
    return
  }

  // Load thresholds configuration
  loadThresholds()

  // Group choices and presentation settings are independent reads; both settle
  // before the initial dashboard request uses their state.
  await Promise.all([loadDashboardAdvancedSettings(), loadGroups()])

  if (opsEnabled.value) {
    await fetchData()
  }

  // Start auto refresh if enabled
  if (autoRefreshEnabled.value) {
    resumeCountdown()
  }
})

// loadThresholds retrieves metric badge thresholds for the dashboard header.
// Missing responses or request failures set the snapshot to null, leaving
// threshold helpers to render their normal no-threshold state.
async function loadThresholds() {
  try {
    const thresholds = await opsAPI.getMetricThresholds()
    metricThresholds.value = thresholds || null
  } catch (err) {
    // Clear unavailable thresholds after a failed settings read so metric
    // helpers use their normal no-threshold presentation for subsequent
    // renders.
    console.warn('[OpsDashboard] Failed to load thresholds', err)
    metricThresholds.value = null
  }
}

// Release the keyboard listener, active request controllers, and countdown
// interval when this dashboard unmounts so owned work stops following the
// removed page.
onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
  abortDashboardFetch()
  abortLatencyFetch()
  pauseCountdown()
})

// Watch auto refresh settings changes
watch(autoRefreshEnabled, (enabled) => {
  if (enabled) {
    // Start the visible countdown in complete timer seconds.
    autoRefreshCountdown.value = Math.floor(autoRefreshIntervalMs.value / 1000)
    resumeCountdown()
  } else {
    pauseCountdown()
    autoRefreshCountdown.value = 0
  }
})

// Reload auto refresh settings after settings dialog is closed
watch(showSettingsDialog, async (show) => {
  if (!show) {
    await loadDashboardAdvancedSettings()
  }
})
</script>

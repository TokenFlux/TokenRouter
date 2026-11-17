<template>
  <AppLayout>
    <template #page-heading-actions>
      <button
        type="button"
        class="btn btn-secondary shrink-0 btn-icon"
        :disabled="chartsLoading"
        :title="t('common.refresh')"
        @click="loadDashboardStats"
      >
        <Icon name="refresh" size="sm" :class="chartsLoading ? 'animate-spin' : ''" />
      </button>
    </template>

    <div class="space-y-4">
      <DashboardSkeleton v-if="loading && !stats" />

      <template v-else-if="stats">
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div class="shrink-0 self-start rounded-control bg-blue-100 p-2 dark:bg-blue-900/30">
                <Icon
                  name="key"
                  size="md"
                  class="text-blue-600 dark:text-blue-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.apiKeys') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                >
                  {{ stats.total_api_keys }}
                </p>
                <p class="mt-0.5 text-xs text-green-600 dark:text-green-400">
                  {{ stats.active_api_keys }} {{ t('common.active') }}
                </p>
              </div>
            </div>
          </div>

          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div
                class="shrink-0 self-start rounded-control bg-purple-100 p-2 dark:bg-purple-900/30"
              >
                <Icon
                  name="server"
                  size="md"
                  class="text-purple-600 dark:text-purple-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.providers') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                >
                  {{ stats.total_providers }}
                </p>
                <p class="mt-0.5 text-xs">
                  <span class="whitespace-nowrap text-green-600 dark:text-green-400"
                    >{{ stats.normal_providers }} {{ t('common.active') }}</span
                  >
                  <span v-if="stats.error_providers > 0" class="ml-1 whitespace-nowrap text-red-500"
                    >{{ stats.error_providers }} {{ t('common.error') }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div
                class="shrink-0 self-start rounded-control bg-green-100 p-2 dark:bg-green-900/30"
              >
                <Icon
                  name="chart"
                  size="md"
                  class="text-green-600 dark:text-green-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayRequests') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                >
                  {{ stats.today_requests }}
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_requests) }}
                </p>
              </div>
            </div>
          </div>

          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div
                class="shrink-0 self-start rounded-control bg-emerald-100 p-2 dark:bg-emerald-900/30"
              >
                <Icon
                  name="userPlus"
                  size="md"
                  class="text-emerald-600 dark:text-emerald-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.users') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-emerald-600 dark:text-emerald-400 lg:text-xl"
                >
                  +{{ stats.today_new_users }}
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_users) }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div
                class="shrink-0 self-start rounded-control bg-amber-100 p-2 dark:bg-amber-900/30"
              >
                <Icon
                  name="cube"
                  size="md"
                  class="text-amber-600 dark:text-amber-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayTokens') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                >
                  {{ formatTokens(stats.today_tokens) }}
                </p>

                <div class="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs">
                  <span
                    class="whitespace-nowrap text-green-600 dark:text-green-400"
                    :title="t('admin.dashboard.actualDescription')"
                    >{{
                      formatBalanceAmount(stats.today_actual_cost, {
                        withSymbol: true,
                        fractionDigits: costDigits(stats.today_actual_cost),
                      })
                    }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="whitespace-nowrap text-orange-500 dark:text-orange-400"
                    :title="t('admin.dashboard.providerCostDescription')"
                    >{{
                      formatUsdAmount(stats.today_provider_cost, {
                        withSymbol: true,
                        fractionDigits: costDigits(stats.today_provider_cost),
                      })
                    }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="whitespace-nowrap text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standardDescription')"
                    >{{
                      formatUsdAmount(stats.today_cost, {
                        withSymbol: true,
                        fractionDigits: costDigits(stats.today_cost),
                      })
                    }}</span
                  >
                </div>
              </div>
            </div>
          </div>

          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div
                class="shrink-0 self-start rounded-control bg-indigo-100 p-2 dark:bg-indigo-900/30"
              >
                <Icon
                  name="database"
                  size="md"
                  class="text-indigo-600 dark:text-indigo-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.totalTokens') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                >
                  {{ formatTokens(stats.total_tokens) }}
                </p>

                <div class="mt-0.5 flex flex-wrap items-center gap-x-1 text-xs">
                  <span
                    class="whitespace-nowrap text-green-600 dark:text-green-400"
                    :title="t('admin.dashboard.actualDescription')"
                    >{{
                      formatBalanceAmount(stats.total_actual_cost, {
                        withSymbol: true,
                        fractionDigits: costDigits(stats.total_actual_cost),
                      })
                    }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="whitespace-nowrap text-orange-500 dark:text-orange-400"
                    :title="t('admin.dashboard.providerCostDescription')"
                    >{{
                      formatUsdAmount(stats.total_provider_cost, {
                        withSymbol: true,
                        fractionDigits: costDigits(stats.total_provider_cost),
                      })
                    }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="whitespace-nowrap text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standardDescription')"
                    >{{
                      formatUsdAmount(stats.total_cost, {
                        withSymbol: true,
                        fractionDigits: costDigits(stats.total_cost),
                      })
                    }}</span
                  >
                </div>
              </div>
            </div>
          </div>

          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div
                class="shrink-0 self-start rounded-control bg-violet-100 p-2 dark:bg-violet-900/30"
              >
                <Icon
                  name="bolt"
                  size="md"
                  class="text-violet-600 dark:text-violet-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0 flex-1">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.performance') }}
                </p>
                <div class="mt-0.5 flex items-baseline gap-2">
                  <p
                    class="whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                  >
                    {{ formatTokens(stats.rpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">RPM</span>
                </div>
                <div class="mt-0.5 flex items-baseline gap-2">
                  <p
                    class="whitespace-nowrap text-sm font-semibold tabular-nums text-violet-600 dark:text-violet-400"
                  >
                    {{ formatTokens(stats.tpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">TPM</span>
                </div>
              </div>
            </div>
          </div>

          <div class="card p-4">
            <div class="flex flex-col gap-2 lg:flex-row lg:items-center">
              <div class="shrink-0 self-start rounded-control bg-rose-100 p-2 dark:bg-rose-900/30">
                <Icon
                  name="clock"
                  size="md"
                  class="text-rose-600 dark:text-rose-400"
                  :stroke-width="2"
                />
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.avgResponse') }}
                </p>
                <p
                  class="mt-0.5 whitespace-nowrap text-lg font-bold tabular-nums text-gray-900 dark:text-white lg:text-xl"
                >
                  {{ formatDuration(stats.average_duration_ms) }}
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ stats.active_users }} {{ t('admin.dashboard.activeUsers') }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <div class="space-y-4">
          <div class="card p-4">
            <div class="time-controls flex flex-wrap items-center justify-between gap-2">
              <div class="flex min-w-0 items-center gap-2">
                <span
                  class="time-control-label text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.timeRange') }}:</span
                >
                <DateRangePicker
                  v-model:start-date="startDate"
                  v-model:end-date="endDate"
                  @change="onDateRangeChange"
                />
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <span
                  class="time-control-label text-sm font-medium text-gray-700 dark:text-gray-300"
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

          <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <ModelDistributionChart
              :model-stats="modelStats"
              :enable-ranking-view="true"
              :ranking-items="rankingItems"
              :ranking-total-actual-cost="rankingTotalActualCost"
              :ranking-total-requests="rankingTotalRequests"
              :ranking-total-tokens="rankingTotalTokens"
              :loading="chartsLoading"
              :ranking-loading="rankingLoading"
              :ranking-error="rankingError"
              :start-date="startDate"
              :end-date="endDate"
              @ranking-click="goToUserUsage"
            />
            <TokenUsageTrend
              :trend-data="trendData"
              :loading="chartsLoading"
              :granularity="granularity"
            />
          </div>

          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.recentUsage') }} (Top 12)
            </h3>
            <div class="h-64">
              <ChartSkeleton v-if="userTrendLoading" height="100%" />
              <Line
                v-else-if="userTrendChartData"
                :data="userTrendChartData"
                :options="lineOptions"
              />
              <div
                v-else
                class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.dashboard.noDataAvailable') }}
              </div>
            </div>
          </div>
        </div>
      </template>
      <LoadErrorState
        v-else
        class="card"
        :message="t('admin.dashboard.failedToLoad')"
        @retry="loadDashboardStats"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

import { adminAPI } from '@/api/admin'
import type {
  DashboardStats,
  UserUsageTrendPoint,
  UserSpendingRankingItem,
} from '@/api/admin/dashboard.types'
import type { TrendDataPoint, ModelStat } from '@/api/usage.types'
import LoadErrorState from '@/components/common/LoadErrorState.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import DashboardSkeleton from '@/components/common/DashboardSkeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'

import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import { formatTokens } from '@/utils/format'
import {
  useChartTheme,
  CHART_TICK_FONT_SIZE,
  CHART_LEGEND_FONT_SIZE,
} from '@/composables/useChartTheme'

// DashboardView.vue displays administrator totals, usage charts, user trends, and
// spending rankings for a selected date range. The snapshot, user trend, and ranking
// endpoints progress independently, so each request family owns a sequence counter that
// decides which completion may publish data or release its loading flag.
//
// User chart points are grouped by user identifier before their dates are aligned into
// datasets. This keeps accounts with equal display names distinct and fills absent
// dates with zero tokens. Chart reloads retain summary statistics, while initial
// loading requests statistics together with trend data. Ranking failures clear ranking
// rows and totals and expose the ranking error state.

// The translation function supplies dashboard labels and messages throughout the
// mounted administrator view and its charts.
const { t } = useI18n()

// Register the scales, elements, tooltip, and legend needed by the line chart before
// any child chart instance is mounted.
ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Filler)

// The external tooltip lives outside the chart tree; hide it before teardown so its
// floating DOM content cannot remain visible.
onBeforeUnmount(hideExternalTooltip)

// Application notifications, navigation, balance formatting, and access refresh support
// dashboard actions and the initial load.
const appStore = useAppStore()
const router = useRouter()
const { formatBalanceAmount, formatUsdAmount } = useBalanceDisplay()
// Summary data and separate loading/error flags control initial skeletons, chart
// progress, user trends, and ranking feedback.
const stats = ref<DashboardStats | null>(null)
const loading = ref(true)
const chartsLoading = ref(false)
const userTrendLoading = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)

// Endpoint collections and ranking totals feed the panels; sequence counters
// independently reject superseded requests for each request family.
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
const rankingTotalActualCost = ref(0)
const rankingTotalRequests = ref(0)
const rankingTotalTokens = ref(0)
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
const rankingLimit = 12

// formatLocalDate returns a calendar date using the browser's local timezone.
const formatLocalDate = (date: Date): string => {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

// getLast24HoursRangeDates returns local calendar dates spanning the previous 24 hours.
// The result truncates clock time because the date controls and dashboard endpoints
// receive date strings for their selected range.
const getLast24HoursRangeDates = (): {
  start: string // first local date
  end: string // last local date
} => {
  const end = new Date()
  // Subtract one fixed 24-hour interval before reducing the endpoints to local
  // calendar dates for the date picker.
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: formatLocalDate(start),
    end: formatLocalDate(end),
  }
}

// Date inputs start with the previous 24 hours represented as local calendar dates and
// hourly chart granularity.
const granularity = ref<'day' | 'hour'>('hour')
const defaultRange = getLast24HoursRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)

// The granularity selector maps supported day and hour bucket values to translated
// labels for chart requests.
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') },
])

// Theme state feeds computed chart colors so changing the application theme updates
// tick and grid colors reactively.
const { isDark } = useChartTheme()
// chartColors supplies the dashboard palette used by chart legends, axis ticks, and
// grid lines in either theme.
const chartColors = computed(() => ({
  text: isDark.value ? '#D4D4D8' : '#2D4F68',
  grid: isDark.value ? '#27272A' : '#DDF4FC',
}))

// User-trend chart options use the external tooltip renderer and sort tooltip items by
// descending token values.
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const,
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: CHART_LEGEND_FONT_SIZE,
        },
      },
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      itemSort: (a: any, b: any) => {
        const aValue = typeof a?.raw === 'number' ? a.raw : Number(a?.parsed?.y ?? 0)
        const bValue = typeof b?.raw === 'number' ? b.raw : Number(b?.parsed?.y ?? 0)
        return bValue - aValue
      },
      callbacks: {
        label: (context: any) => {
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        },
      },
    },
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid,
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: CHART_TICK_FONT_SIZE,
        },
      },
    },
    y: {
      grid: {
        color: chartColors.value.grid,
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: CHART_TICK_FONT_SIZE,
        },
        callback: (value: string | number) => formatTokens(Number(value)),
      },
    },
  },
}))

// userTrendChartData groups token observations by user identifier and aligns every
// dataset to the same sorted date labels. Accounts with duplicate names remain
// separate because labels do not identify groups. Every dataset receives one value
// per shared date, with zero for missing observations, so dates cannot shift between
// users. An empty series produces null for the template to select its empty state.
const userTrendChartData = computed(() => {
  if (!userTrend.value?.length) return null

  // getDisplayName selects a trimmed username, then email, then translated user
  // identifier. The label is for display; chart grouping continues to use user_id so
  // duplicate names remain separate datasets.
  const getDisplayName = (point: UserUsageTrendPoint): string => {
    const username = point.username?.trim()
    if (username) {
      return username
    }

    const email = point.email?.trim()
    if (email) {
      return email
    }

    return t('admin.redeem.userPrefix', { id: point.user_id })
  }

  // Group by stable user identifiers so accounts sharing a display name cannot merge
  // their observations into one chart dataset.
  const userGroups = new Map<
    number,
    {
      name: string // displayed name text
      data: Map<string, number> // date-indexed token observations
    }
  >()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = point.user_id
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: getDisplayName(point), data: new Map() })
    }
    userGroups.get(key)!.data.set(point.date, point.tokens)
  })

  const sortedDates = Array.from(allDates).sort()
  const colors = [
    '#3b82f6',
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#ec4899',
    '#00D2FF',
    '#f97316',
    '#6366f1',
    '#84cc16',
    '#06b6d4',
    '#a855f7',
  ]

  const datasets = Array.from(userGroups.values()).map((group, idx) => ({
    label: group.name,
    // Fill missing observations against the common date axis so each dataset keeps
    // its values aligned with the displayed date labels.
    data: sortedDates.map((date) => group.data.get(date) || 0),
    borderColor: colors[idx % colors.length],
    backgroundColor: `${colors[idx % colors.length]}20`,
    fill: false,
    tension: 0.3,
  }))

  return {
    labels: sortedDates,
    datasets,
  }
})

// toFiniteNumber converts its input to a finite number or zero. Formatting helpers use
// zero for nullish or nonfinite values so malformed numeric data cannot render as NaN
// or Infinity.
const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  // Use zero for nonfinite conversions so all dashboard number formatters receive a
  // finite value even when a response field is malformed.
  return Number.isFinite(numberValue) ? numberValue : 0
}

// formatNumber formats a finite numeric value with browser locale separators. Nullish
// or nonfinite inputs pass through toFiniteNumber and become zero before the displayed
// statistic is formatted for the dashboard.
const formatNumber = (value: number | null | undefined): string => {
  return toFiniteNumber(value).toLocaleString()
}

// costDigits selects two to four decimal places from the amount's magnitude. Values
// below one receive more precision, including four places for amounts below one
// hundredth and invalid values normalized to zero.
const costDigits = (value: number | null | undefined): number => {
  const safeValue = toFiniteNumber(value)
  // Large amounts use two decimals; smaller displayed costs gain precision below one
  // and one hundredth.
  if (safeValue >= 1000) return 2
  if (safeValue >= 1) return 2
  if (safeValue >= 0.01) return 3
  return 4
}

// formatDuration displays milliseconds below one second and seconds above that
// threshold. Milliseconds are rounded to an integer, while seconds retain two decimal
// places for the average-duration statistic shown on the dashboard.
const formatDuration = (ms: number): string => {
  // Convert one thousand milliseconds to seconds for the duration label.
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}

// goToUserUsage navigates to usage records for a ranked user and date range.
const goToUserUsage = (item: UserSpendingRankingItem) => {
  void router.push({
    path: '/admin/usage',
    query: {
      user_id: String(item.user_id),
      start_date: startDate.value,
      end_date: endDate.value,
    },
  })
}

// onDateRangeChange chooses hourly granularity for ranges of at most one day. Longer
// ranges select daily buckets, and all chart request families reload using the date
// refs updated by the picker.
const onDateRangeChange = (range: {
  startDate: string // first selected date
  endDate: string // last selected date
  preset: string | null // selected date preset
}) => {
  const start = new Date(range.startDate)
  const end = new Date(range.endDate)
  // Round the selected interval upward to whole days before choosing the hourly or
  // daily chart bucket size.
  const daysDiff = Math.ceil((end.getTime() - start.getTime()) / (1000 * 60 * 60 * 24))

  if (daysDiff <= 1) {
    granularity.value = 'hour'
  } else {
    granularity.value = 'day'
  }

  loadChartData()
}

// loadDashboardSnapshot requests usage charts and optionally the summary statistics.
// Each call increments the snapshot sequence before dispatch, so a later request
// supersedes earlier successes, failures, and loading cleanup. Existing statistics
// remain visible during refreshes, and chart responses with omitted collections become
// empty arrays. Only the currently selected sequence can publish the snapshot or clear
// its loading flags, even when requests complete out of order.
const loadDashboardSnapshot = async (includeStats: boolean) => {
  // Advance snapshot ownership before awaiting the endpoint so every older response,
  // error, and finalizer can recognize that it was superseded.
  const currentSeq = ++chartLoadSeq
  if (includeStats && !stats.value) {
    loading.value = true
  }
  chartsLoading.value = true
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: includeStats,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false,
    })
    // The selected range may have changed while awaiting this request; publish or
    // report only the still-current snapshot generation.
    if (currentSeq !== chartLoadSeq) return
    if (includeStats && response.stats) {
      stats.value = response.stats
    }
    trendData.value = response.trend || []
    modelStats.value = response.models || []
  } catch (error) {
    // The selected range may have changed while awaiting this request; publish or
    // report only the still-current snapshot generation.
    if (currentSeq !== chartLoadSeq) return
    appStore.showError(t('admin.dashboard.failedToLoad'))
    console.error('Error loading dashboard snapshot:', error)
  } finally {
    if (currentSeq === chartLoadSeq) {
      loading.value = false
      chartsLoading.value = false
    }
  }
}

// loadUsersTrend requests the selected range's leading user token series. It increments
// an independent sequence so overlapping user-trend requests publish only the newest
// completion. An accepted failure clears the chart series and logs the error, while
// superseded failures remain silent. Loading cleanup checks the same sequence,
// preventing an older completion from hiding the indicator for a newer request that is
// still pending.
const loadUsersTrend = async () => {
  // User trends progress separately from snapshots, so their own generation rejects
  // old results without invalidating the other dashboard panels.
  const currentSeq = ++usersTrendLoadSeq
  userTrendLoading.value = true
  try {
    const response = await adminAPI.dashboard.getUserUsageTrend({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      limit: 12,
    })
    // Ignore superseded user-trend work because a later request already represents
    // the selected range and owns this panel state.
    if (currentSeq !== usersTrendLoadSeq) return
    userTrend.value = response.trend || []
  } catch (error) {
    // Ignore superseded user-trend work because a later request already represents
    // the selected range and owns this panel state.
    if (currentSeq !== usersTrendLoadSeq) return
    console.error('Error loading users trend:', error)
    // A failed current trend request has no series to display; clear old observations
    // so they cannot describe the newly selected dates.
    userTrend.value = []
  } finally {
    if (currentSeq === usersTrendLoadSeq) {
      userTrendLoading.value = false
    }
  }
}

// loadUserSpendingRanking requests ranked users and their aggregate costs, requests,
// and tokens. A ranking-specific sequence decides which overlapping response may update
// the panel. The accepted failure clears both rows and totals and exposes rankingError,
// keeping failed data distinct from a successful empty ranking. Superseded results and
// errors are ignored, and only the current request releases the ranking loading flag
// when it settles.
const loadUserSpendingRanking = async () => {
  // Rankings have independent request timing; advance their generation before
  // dispatch so late completions cannot replace a newer ranking selection.
  const currentSeq = ++rankingLoadSeq
  rankingLoading.value = true
  rankingError.value = false
  try {
    const response = await adminAPI.dashboard.getUserSpendingRanking({
      start_date: startDate.value,
      end_date: endDate.value,
      limit: rankingLimit,
    })
    // Ignore superseded ranking work because a later request already owns the
    // displayed ranking and its aggregate totals.
    if (currentSeq !== rankingLoadSeq) return
    rankingItems.value = response.ranking || []
    rankingTotalActualCost.value = response.total_actual_cost || 0
    rankingTotalRequests.value = response.total_requests || 0
    rankingTotalTokens.value = response.total_tokens || 0
  } catch (error) {
    // Ignore superseded ranking work because a later request already owns the
    // displayed ranking and its aggregate totals.
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    // Clear rows and all matching totals together after an accepted failure so
    // previous-range aggregates cannot remain beside an empty failed ranking.
    rankingItems.value = []
    rankingTotalActualCost.value = 0
    rankingTotalRequests.value = 0
    rankingTotalTokens.value = 0
    rankingError.value = true
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

// loadDashboardStats starts the snapshot, user trend, and ranking requests together. It
// includes summary statistics and waits for all three request wrappers, each of which
// handles its own errors and loading state.
const loadDashboardStats = async () => {
  await Promise.all([loadDashboardSnapshot(true), loadUsersTrend(), loadUserSpendingRanking()])
}

// loadChartData reloads the chart, user trend, and ranking panels together. The
// snapshot request omits summary statistics, allowing date or granularity changes to
// refresh charts while the existing summary remains displayed.
const loadChartData = async () => {
  await Promise.all([loadDashboardSnapshot(false), loadUsersTrend(), loadUserSpendingRanking()])
}

// Load the summary and charts when the dashboard becomes visible.
onMounted(() => {
  loadDashboardStats()
})
</script>

<style scoped></style>

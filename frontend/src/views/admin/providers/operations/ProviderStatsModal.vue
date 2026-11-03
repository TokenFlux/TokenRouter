<template>
  <BaseDialog
    :show="show"
    :title="t('admin.providers.usageStatistics')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <!-- Provider Info Header -->
      <div
        v-if="provider"
        class="flex items-center justify-between rounded-surface border border-primary-200 bg-gradient-to-r from-primary-50 to-primary-100 p-3 dark:border-primary-700/50 dark:from-primary-900/20 dark:to-primary-800/20"
      >
        <div class="flex items-center gap-3">
          <div
            class="flex h-10 w-10 items-center justify-center rounded-control bg-gradient-to-br from-primary-500 to-primary-600"
          >
            <Icon name="chartBar" size="md" class="text-white" />
          </div>
          <div>
            <div class="font-semibold text-gray-900 dark:text-gray-100">{{ provider.name }}</div>
            <div class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.providers.last30DaysUsage') }}
            </div>
          </div>
        </div>
        <span
          :class="[
            'rounded-full px-2.5 py-1 text-xs font-semibold',
            provider.status === 'active'
              ? 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
              : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300',
          ]"
        >
          {{ provider.status }}
        </span>
      </div>

      <!-- Loading State -->
      <div
        v-if="loading"
        class="space-y-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div class="grid grid-cols-2 lg:grid-cols-4 gap-4" aria-hidden="true">
          <div v-for="card in 4" :key="card" class="card min-w-0 space-y-3 p-4">
            <Skeleton width="55%" :height="12" />
            <Skeleton width="75%" :height="28" />
            <Skeleton width="65%" :height="12" />
          </div>
        </div>
        <div class="grid grid-cols-1 lg:grid-cols-3 gap-4" aria-hidden="true">
          <div v-for="card in 6" :key="card" class="card min-w-0 space-y-3 p-4">
            <Skeleton width="55%" :height="12" />
            <Skeleton width="75%" :height="28" />
            <Skeleton width="65%" :height="12" />
          </div>
        </div>
        <div class="card space-y-4 p-4">
          <Skeleton :width="128" :height="16" />
          <ChartSkeleton height="256px" />
        </div>
      </div>

      <template v-else-if="stats">
        <!-- Row 1: Main Stats Cards -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- 30-Day Total Cost -->
          <div
            class="card border-emerald-200 bg-gradient-to-br from-emerald-50 to-white p-4 dark:border-emerald-800/30 dark:from-emerald-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.providers.stats.totalCost')
              }}</span>
              <div class="rounded-control bg-emerald-100 p-1.5 dark:bg-emerald-900/30">
                <BalanceIcon svg="" size="sm" class="text-emerald-600 dark:text-emerald-400" />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatUsdSummary(stats.summary.total_cost) }}
            </p>
            <p v-if="stats.summary.incomplete_cost_requests" class="mt-1 text-xs text-gray-500">{{
              t('usage.incompleteCosts', { count: stats.summary.incomplete_cost_requests })
            }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.providers.stats.accumulatedCost') }}
              <span class="text-gray-400 dark:text-gray-500">
                ({{ t('usage.userBilled') }}:
                {{ formatBalanceSummary(stats.summary.total_user_cost) }} ·
                {{ t('admin.providers.stats.standardCost') }}:
                {{ formatUsdSummary(stats.summary.total_standard_cost) }})
              </span>
            </p>
          </div>

          <!-- 30-Day Total Requests -->
          <div
            class="card border-blue-200 bg-gradient-to-br from-blue-50 to-white p-4 dark:border-blue-800/30 dark:from-blue-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.providers.stats.totalRequests')
              }}</span>
              <div class="rounded-control bg-blue-100 p-1.5 dark:bg-blue-900/30">
                <Icon name="bolt" size="sm" class="text-blue-600 dark:text-blue-400" />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatNumber(stats.summary.total_requests) }}
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.providers.stats.totalCalls') }}
            </p>
          </div>

          <!-- Daily Average Cost -->
          <div
            class="card border-amber-200 bg-gradient-to-br from-amber-50 to-white p-4 dark:border-amber-800/30 dark:from-amber-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.providers.stats.avgDailyCost')
              }}</span>
              <div class="rounded-control bg-amber-100 p-1.5 dark:bg-amber-900/30">
                <Icon name="calculator" size="sm" class="text-amber-600 dark:text-amber-400" />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatUsdSummary(stats.summary.avg_daily_cost) }}
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{
                t('admin.providers.stats.basedOnActualDays', {
                  days: stats.summary.actual_days_used,
                })
              }}
              <span class="text-gray-400 dark:text-gray-500">
                ({{ t('usage.userBilled') }}:
                {{ formatBalanceSummary(stats.summary.avg_daily_user_cost) }})
              </span>
            </p>
          </div>

          <!-- Daily Average Requests -->
          <div
            class="card border-purple-200 bg-gradient-to-br from-purple-50 to-white p-4 dark:border-purple-800/30 dark:from-purple-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.providers.stats.avgDailyRequests')
              }}</span>
              <div class="rounded-control bg-purple-100 p-1.5 dark:bg-purple-900/30">
                <Icon name="chart" size="sm" class="h-4 w-4 text-purple-600 dark:text-purple-400" />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatNumber(Math.round(stats.summary.avg_daily_requests)) }}
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.providers.stats.avgDailyUsage') }}
            </p>
          </div>
        </div>

        <!-- Row 2: Today, Highest Cost, Highest Requests -->
        <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <!-- Today Overview -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-control bg-cyan-100 p-1.5 dark:bg-cyan-900/30">
                <Icon name="clock" size="sm" class="text-cyan-600 dark:text-cyan-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.providers.stats.todayOverview')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.providerBilled')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatUsdSummary(stats.summary.today?.cost || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.userBilled')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatBalanceSummary(stats.summary.today?.user_cost || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.requests')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatNumber(stats.summary.today?.requests || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.tokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(stats.summary.today?.tokens || 0)
                }}</span>
              </div>
            </div>
          </div>

          <!-- Highest Cost Day -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-control bg-orange-100 p-1.5 dark:bg-orange-900/30">
                <Icon name="fire" size="sm" class="text-orange-600 dark:text-orange-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.providers.stats.highestCostDay')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.date')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  stats.summary.highest_cost_day?.label || '-'
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.providerBilled')
                }}</span>
                <span class="text-sm font-semibold text-orange-600 dark:text-orange-400">{{
                  formatUsdSummary(stats.summary.highest_cost_day?.cost || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.userBilled')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatBalanceSummary(stats.summary.highest_cost_day?.user_cost || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.requests')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatNumber(stats.summary.highest_cost_day?.requests || 0)
                }}</span>
              </div>
            </div>
          </div>

          <!-- Highest Request Day -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-control bg-indigo-100 p-1.5 dark:bg-indigo-900/30">
                <Icon name="trendingUp" size="sm" class="text-indigo-600 dark:text-indigo-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.providers.stats.highestRequestDay')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.date')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  stats.summary.highest_request_day?.label || '-'
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.requests')
                }}</span>
                <span class="text-sm font-semibold text-indigo-600 dark:text-indigo-400">{{
                  formatNumber(stats.summary.highest_request_day?.requests || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.providerBilled')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatUsdSummary(stats.summary.highest_request_day?.cost || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.userBilled')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatBalanceSummary(stats.summary.highest_request_day?.user_cost || 0)
                }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Row 3: Token Stats -->
        <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <!-- Accumulated Tokens -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-control bg-teal-100 p-1.5 dark:bg-teal-900/30">
                <Icon name="cube" size="sm" class="text-teal-600 dark:text-teal-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.providers.stats.accumulatedTokens')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.totalTokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(stats.summary.total_tokens)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.dailyAvgTokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(Math.round(stats.summary.avg_daily_tokens))
                }}</span>
              </div>
            </div>
          </div>

          <!-- Performance -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-control bg-rose-100 p-1.5 dark:bg-rose-900/30">
                <Icon name="bolt" size="sm" class="text-rose-600 dark:text-rose-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.providers.stats.performance')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.avgResponseTime')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatDuration(stats.summary.avg_duration_ms)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.daysActive')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >{{ stats.summary.actual_days_used }} / {{ stats.summary.days }}</span
                >
              </div>
            </div>
          </div>

          <!-- Recent Activity -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-control bg-lime-100 p-1.5 dark:bg-lime-900/30">
                <Icon name="clipboard" size="sm" class="text-lime-600 dark:text-lime-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.providers.stats.recentActivity')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.todayRequests')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatNumber(stats.summary.today?.requests || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.todayTokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(stats.summary.today?.tokens || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.providers.stats.todayCost')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatUsdSummary(stats.summary.today?.cost || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('usage.userBilled')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatBalanceSummary(stats.summary.today?.user_cost || 0)
                }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Usage Trend Chart -->
        <div class="card p-4">
          <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.providers.stats.usageTrend') }}
          </h3>
          <div class="h-64">
            <Line v-if="trendChartData" :data="trendChartData" :options="lineChartOptions" />
            <div
              v-else
              class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
            >
              {{ t('admin.dashboard.noDataAvailable') }}
            </div>
          </div>
        </div>

        <!-- Model Distribution -->
        <ModelDistributionChart :model-stats="stats.models" :loading="false" />

        <EndpointDistributionChart
          :endpoint-stats="stats.endpoints || []"
          :loading="false"
          :title="t('usage.inboundEndpoint')"
        />

        <EndpointDistributionChart
          :endpoint-stats="stats.upstream_endpoints || []"
          :loading="false"
          :title="t('usage.upstreamEndpoint')"
        />
      </template>

      <!-- No Data State -->
      <div
        v-else-if="!loading"
        class="flex flex-col items-center justify-center py-12 text-gray-500 dark:text-gray-400"
      >
        <Icon name="chartBar" size="xl" class="mb-4 h-12 w-12" />
        <p class="text-sm">{{ t('admin.providers.stats.noData') }}</p>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button @click="handleClose" class="btn btn-secondary">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import { ref, watch, computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { adminAPI } from '@/api/admin'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import type { Provider, ProviderUsageStatsResponse } from '@/api/admin/providers.types'
import { formatTokens } from '@/utils/format'
import {
  useChartTheme,
  CHART_TICK_FONT_SIZE,
  CHART_LEGEND_FONT_SIZE,
} from '@/composables/useChartTheme'

/**
 * ProviderStatsModal.vue loads thirty days of provider accounting statistics and renders
 * summary values, request trends, and distributions. Provider-billed amounts are formatted in
 * USD, while user-billed amounts use the configured balance display unit. The chart shares a
 * cost axis for both series and a separate request-count axis, with themed external tooltips.
 */

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler,
)

// The shared external tooltip outlives the chart canvas unless explicitly hidden, so remove its
// visible presentation before this dialog unmounts.
onBeforeUnmount(hideExternalTooltip)

/**
 * Translation and currency helpers separate provider USD charges from user balance-unit charges
 * in statistics summaries and chart tooltips.
 */
const { t } = useI18n()
const { balanceUnitName, usdUnitName, formatBalanceAmount, formatUsdAmount } = useBalanceDisplay()

/**
 * props supplies dialog visibility and the selected provider; opening loads statistics while
 * hiding clears the displayed response.
 */
const props = defineProps<{
  show: boolean // parent-controlled statistics dialog visibility
  provider: Provider | null // selected provider or no selection
}>()

/**
 * emit requests closure from the owning provider page, which controls visibility and the
 * provider selected for statistics inspection.
 */
const emit = defineEmits<{
  /**
   * close requests that the parent hide the provider statistics dialog.
   */
  (e: 'close'): void
}>()

/**
 * loading and stats represent the current statistics request and its optional response used by
 * summaries, trend data, and distribution charts.
 */
const loading = ref(false)
const stats = ref<ProviderUsageStatsResponse | null>(null)

/**
 * chartColors follows the active theme so chart labels, axes, and grid colors update when the
 * application theme changes.
 */
const { colors: chartColors } = useChartTheme()

/**
 * trendChartData combines provider USD cost, user balance-unit cost, and request counts by the
 * server history labels, using separate cost and count axes.
 */
const trendChartData = computed(() => {
  if (!stats.value?.history?.length) return null

  return {
    labels: stats.value.history.map((h) => h.label),
    datasets: [
      {
        label: `${t('usage.providerBilled')} (${usdUnitName})`,
        data: stats.value.history.map((h) => h.actual_cost),
        borderColor: '#3b82f6',
        backgroundColor: 'rgba(59, 130, 246, 0.1)',
        fill: true,
        tension: 0.3,
        yAxisID: 'y',
      },
      {
        label: `${t('usage.userBilled')} (${balanceUnitName.value})`,
        data: stats.value.history.map((h) => h.user_cost),
        borderColor: '#10b981',
        backgroundColor: 'rgba(16, 185, 129, 0.08)',
        fill: false,
        tension: 0.3,
        borderDash: [5, 5],
        yAxisID: 'y',
      },
      {
        label: t('admin.providers.stats.requests'),
        data: stats.value.history.map((h) => h.requests),
        borderColor: '#f97316',
        backgroundColor: 'rgba(249, 115, 22, 0.1)',
        fill: false,
        tension: 0.3,
        yAxisID: 'y1',
      },
    ],
  }
})

/**
 * lineChartOptions configures shared-index external tooltips, theme-aware axes, and unit-aware
 * cost labels while requests use the separate right-hand count axis.
 */
const lineChartOptions = computed(() => ({
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
      callbacks: {
        label: (context: any) => {
          const label = context.dataset.label || ''
          const value = context.raw
          if (label.startsWith(t('usage.providerBilled'))) {
            return `${label}: ${formatUsdAmount(value, { fractionDigits: 4 })}`
          }
          if (label.startsWith(t('usage.userBilled'))) {
            return `${label}: ${formatBalanceAmount(value, { fractionDigits: 4 })}`
          }
          return `${label}: ${formatNumber(value)}`
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
        maxRotation: 45,
        minRotation: 0,
      },
    },
    y: {
      type: 'linear' as const,
      display: true,
      position: 'left' as const,
      grid: {
        color: chartColors.value.grid,
      },
      ticks: {
        color: '#3b82f6',
        font: {
          size: CHART_TICK_FONT_SIZE,
        },
        callback: (value: string | number) => formatCost(Number(value)),
      },
      title: {
        display: true,
        text: t('usage.cost'),
        color: '#3b82f6',
        font: {
          size: CHART_LEGEND_FONT_SIZE,
        },
      },
    },
    y1: {
      type: 'linear' as const,
      display: true,
      position: 'right' as const,
      grid: {
        drawOnChartArea: false,
      },
      ticks: {
        color: '#f97316',
        font: {
          size: CHART_TICK_FONT_SIZE,
        },
        callback: (value: string | number) => formatNumber(Number(value)),
      },
      title: {
        display: true,
        text: t('admin.providers.stats.requests'),
        color: '#f97316',
        font: {
          size: CHART_LEGEND_FONT_SIZE,
        },
      },
    },
  },
}))

// Opening fetches statistics for the selected provider; hiding clears the display while the
// request itself follows its normal completion path.
watch(
  () => props.show,
  async (newVal) => {
    if (newVal && props.provider) {
      await loadStats()
    } else {
      stats.value = null
    }
  },
)

/**
 * loadStats fetches thirty days of statistics for the selected provider. Failure clears the
 * previous response and logs the error; loading state clears after either successful or failed
 * settlement.
 */
const loadStats = async () => {
  if (!props.provider) return

  loading.value = true
  try {
    // Keep the provider history window at thirty days.
    stats.value = await adminAPI.providers.getStats(props.provider.id, 30)
  } catch (error) {
    console.error('Failed to load provider stats:', error)
    stats.value = null
  } finally {
    loading.value = false
  }
}

/**
 * handleClose requests that the parent hide the provider statistics dialog.
 */
const handleClose = () => {
  emit('close')
}

/**
 * formatBalanceSummary renders user charges in the configured balance unit with two fractional
 * digits.
 */
const formatBalanceSummary = (value: number): string =>
  formatBalanceAmount(value, { fractionDigits: 2 })

/**
 * formatUsdSummary renders provider cost in USD with two fractional digits.
 */
const formatUsdSummary = (value: number): string => formatUsdAmount(value, { fractionDigits: 2 })

/**
 * formatCost formats a numeric cost-axis value with magnitude-dependent precision. Thousands
 * use a K suffix, while subunit amounts retain additional fractional digits so small plotted
 * values remain distinguishable.
 */
const formatCost = (value: number): string => {
  // Compact large axis labels while retaining precision for subunit costs.
  if (value >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  } else if (value >= 1) {
    return value.toFixed(2)
  } else if (value >= 0.01) {
    return value.toFixed(3)
  }
  return value.toFixed(4)
}

/**
 * formatNumber formats counts using million or thousand suffixes at those thresholds. Smaller
 * counts use browser-local numeric formatting, keeping count-axis labels separate from cost and
 * currency formatting.
 */
const formatNumber = (value: number): string => {
  // Count suffixes keep large request axes readable.
  if (value >= 1_000_000) {
    return (value / 1_000_000).toFixed(2) + 'M'
  } else if (value >= 1_000) {
    return (value / 1_000).toFixed(2) + 'K'
  }
  return value.toLocaleString()
}

/**
 * formatDuration renders milliseconds as seconds once the value reaches one thousand. Smaller
 * values are rounded to whole milliseconds, keeping the unit visible in response-duration
 * summaries.
 */
const formatDuration = (ms: number): string => {
  // Change display units once duration reaches a whole second.
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}
</script>

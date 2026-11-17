<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import { computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Chart as ChartJS,
  CategoryScale,
  Filler,
  Legend,
  LineElement,
  LinearScale,
  PointElement,
  Title,
  Tooltip,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import type { OpsThroughputTrendPoint } from '@/api/admin/ops'
import type { ChartState } from '../types'
import { formatHistoryLabel, sumNumbers } from '../utils/opsFormatters'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import {
  useChartTheme,
  CHART_TICK_FONT_SIZE,
  CHART_LEGEND_FONT_SIZE,
} from '@/composables/useChartTheme'

// OpsSwitchRateTrendChart.vue plots provider switch counts divided by request
// counts for each history bucket. Buckets with no requests use zero, and the
// chart keeps the ratio in switches per request rather than converting it into a
// percentage.

ChartJS.register(
  Title,
  Tooltip,
  Legend,
  LineElement,
  LinearScale,
  PointElement,
  CategoryScale,
  Filler,
)

// Hide the shared external tooltip when this chart leaves the page so its
// detached overlay does not outlive the displayed dataset.
onBeforeUnmount(hideExternalTooltip)

// Props supplies measured history and display controls for this Ops chart.
interface Props {
  points: OpsThroughputTrendPoint[] // ordered API history buckets
  loading: boolean // parent request in progress
  timeRange: string // selected history range token
  fullscreen?: boolean // expanded dashboard layout flag
}

// Component bindings read dashboard-owned measurements and translate labels for
// the rendered chart and its controls.
const props = defineProps<Props>()
const { t } = useI18n()

// The chart palette uses the current theme to select contrasting grid and label
// colors.
const { isDark } = useChartTheme()
const colors = computed(() => ({
  primary: '#00D2FF',
  primaryAlpha: '#00D2FF26',
  grid: isDark.value ? '#27272A' : '#DDF4FC',
  text: isDark.value ? '#D4D4D8' : '#2D4F68',
}))

// totalRequests sums finite request counts to suppress charts whose history
// contains no observed traffic.
const totalRequests = computed(() => sumNumbers(props.points.map((p) => p.request_count)))

// chartData combines the supplied bucket labels and values into datasets
// consumed by the chart renderer.
const chartData = computed(() => {
  if (!props.points.length || totalRequests.value <= 0) return null
  return {
    labels: props.points.map((p) => formatHistoryLabel(p.bucket_start, props.timeRange)),
    datasets: [
      {
        label: t('admin.ops.switchRate'),
        data: props.points.map((p) => {
          const requests = p.request_count ?? 0
          const switches = p.switch_count ?? 0
          // Buckets without requests use zero instead of dividing by zero.
          if (requests <= 0) return 0
          return switches / requests
        }),
        borderColor: colors.value.primary,
        backgroundColor: colors.value.primaryAlpha,
        fill: true,
        tension: 0.35,
        pointRadius: 0,
        pointHitRadius: 10,
      },
    ],
  }
})

// state prefers a populated chart during background refresh, then selects
// loading or empty presentation.
const state = computed<ChartState>(() => {
  if (chartData.value) return 'ready'
  if (props.loading) return 'loading'
  return 'empty'
})

// options configures responsive chart axes and the shared external tooltip used
// for point inspection.
const options = computed(() => {
  const c = colors.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { intersect: false, mode: 'index' as const },
    plugins: {
      legend: {
        position: 'top' as const,
        align: 'end' as const,
        labels: {
          color: c.text,
          usePointStyle: true,
          boxWidth: 6,
          font: { size: CHART_LEGEND_FONT_SIZE },
        },
      },
      tooltip: {
        enabled: false,
        external: externalTooltipHandler,
        callbacks: {
          label: (context: any) => {
            const value = typeof context?.parsed?.y === 'number' ? context.parsed.y : 0
            return `${t('admin.ops.switchRate')}: ${value.toFixed(3)}`
          },
        },
      },
    },
    scales: {
      x: {
        type: 'category' as const,
        grid: { display: false },
        ticks: {
          color: c.text,
          font: { size: CHART_TICK_FONT_SIZE },
          maxTicksLimit: 8,
          autoSkip: true,
          autoSkipPadding: 10,
        },
      },
      y: {
        type: 'linear' as const,
        display: true,
        position: 'left' as const,
        grid: { color: c.grid, borderDash: [4, 4] },
        ticks: {
          color: c.text,
          font: { size: CHART_TICK_FONT_SIZE },
          callback: (value: any) => Number(value).toFixed(3),
        },
      },
    },
  }
})
</script>

<template>
  <div
    class="flex h-full flex-col rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <div class="mb-4 flex shrink-0 items-center justify-between">
      <h3 class="flex items-center gap-2 text-sm font-bold text-gray-900 dark:text-white">
        <Icon name="filter" size="sm" class="h-4 w-4 text-primary-500" />
        {{ t('admin.ops.switchRateTrend') }}
        <HelpTooltip v-if="!props.fullscreen" :content="t('admin.ops.tooltips.switchRateTrend')" />
      </h3>
    </div>

    <div class="min-h-0 flex-1">
      <Line v-if="state === 'ready' && chartData" :data="chartData" :options="options" />
      <ChartSkeleton v-else-if="state === 'loading'" height="100%" />
      <div v-else class="flex h-full items-center justify-center">
        <EmptyState :title="t('common.noData')" :description="t('admin.ops.charts.emptyRequest')" />
      </div>
    </div>
  </div>
</template>

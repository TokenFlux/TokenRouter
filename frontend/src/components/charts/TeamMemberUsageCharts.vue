<template>
  <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
    <div class="card p-4">
      <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('usage.teamMemberTrend') }}
      </h3>
      <ChartSkeleton v-if="loading" variant="plot" />
      <div v-else-if="lineData" class="h-48">
        <Line :data="lineData" :options="lineOptions" />
      </div>
      <div
        v-else
        class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
      >
        {{ t('admin.dashboard.noDataAvailable') }}
      </div>
    </div>

    <div class="card p-4">
      <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('usage.teamMemberComparison') }}
      </h3>
      <ChartSkeleton v-if="loading" variant="plot" />
      <div v-else-if="comparisonData" class="h-48">
        <Doughnut :data="comparisonData" :options="comparisonOptions" />
      </div>
      <div
        v-else
        class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
      >
        {{ t('admin.dashboard.noDataAvailable') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArcElement,
  CategoryScale,
  Chart as ChartJS,
  Legend,
  LinearScale,
  LineElement,
  PointElement,
  Tooltip,
} from 'chart.js'
import { Doughnut, Line } from 'vue-chartjs'
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import {
  useChartTheme,
  CHART_TICK_FONT_SIZE,
  CHART_LEGEND_FONT_SIZE,
} from '@/composables/useChartTheme'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import type { TeamUsageSummary } from '@/api/team'

// TeamMemberUsageCharts.vue compares team member spending over time and across members.
// Parent summaries may contain different dates, so the line chart uses the sorted union
// of supplied dates and inserts zero for a member missing a date. Both charts omit
// members whose request count and total spending are nonpositive.
//
// Colors follow each member's position in the filtered series. The line and doughnut charts
// share that order and palette, so they agree during a render; changing membership or
// ordering can change a member's color. Theme colors govern labels and grids, and the
// external tooltip uses the configured balance formatter.
ChartJS.register(ArcElement, CategoryScale, Legend, LinearScale, LineElement, PointElement, Tooltip)

// Hide the body-level tooltip when this component unmounts so its last member label does
// not outlive either chart.
onBeforeUnmount(hideExternalTooltip)

// MemberUsageSeries pairs a member identity with aggregated spending history.
interface MemberUsageSeries {
  userID: number // Account user identifier
  label: string // Visible member label
  summary: TeamUsageSummary // Member usage summary
}

// props provides ordered member summaries and the parent loading state, which switches
// both cards between skeletons and computed chart content.
const props = defineProps<{
  series: MemberUsageSeries[] // Ordered member summaries
  loading?: boolean // Parent loading state
}>()

// Translations and the balance formatter label chart headings and monetary values using
// the application language and configured currency display.
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// palette assigns matching colors by filtered member position in both charts; membership
// or ordering changes can reassign a member's color.
const palette = [
  '#2563eb',
  '#059669',
  '#d97706',
  '#dc2626',
  '#7c3aed',
  '#0891b2',
  '#db2777',
  '#4d7c0f',
  '#475569',
  '#ea580c',
]
// Theme text and grid colors update both chart presentations when the application theme
// changes, independently of the member palette.
const { colors: themeColors } = useChartTheme()
const textColor = computed(() => themeColors.value.text)
const gridColor = computed(() => themeColors.value.grid)
// labels collects and sorts the union of supplied daily dates so every member dataset
// uses the same horizontal axis positions.
const labels = computed(() =>
  Array.from(
    new Set(props.series.flatMap((item) => item.summary.daily.map((point) => point.date))),
  ).sort(),
)
// seriesWithUsage retains members with positive requests or spending, providing the same
// member order to the line and comparison chart datasets.
const seriesWithUsage = computed(() =>
  props.series.filter((item) => item.summary.request_count > 0 || item.summary.actual_cost > 0),
)

// lineData aligns daily spending across the filtered members. Missing dates become zero,
// and duplicate dates use the last supplied value because each member's daily points are
// indexed by date. The chart is absent when dates or active members are empty.
//
// The sorted union includes dates from all supplied summaries, while datasets include
// members with requests or spending. This lets values occupy matching date positions
// across lines even when the summaries contain different daily coverage; color
// assignment uses the same filtered order as the comparison chart.
const lineData = computed(() => {
  if (labels.value.length === 0 || seriesWithUsage.value.length === 0) return null
  return {
    labels: labels.value,
    datasets: seriesWithUsage.value.map((item, index) => {
      const daily = new Map(item.summary.daily.map((point) => [point.date, point.actual_cost]))
      const color = palette[index % palette.length]
      return {
        label: item.label,
        // Fill absent member dates with zero so later dates keep their shared positions
        // and cannot slide into another date's column.
        data: labels.value.map((date) => daily.get(date) ?? 0),
        borderColor: color,
        backgroundColor: `${color}20`,
        pointRadius: 2,
        tension: 0.3,
      }
    }),
  }
})

// comparisonData displays total actual spending in the filtered member order and returns
// null when there are no members with usage.
const comparisonData = computed(() => {
  if (seriesWithUsage.value.length === 0) return null
  return {
    labels: seriesWithUsage.value.map((item) => item.label),
    datasets: [
      {
        data: seriesWithUsage.value.map((item) => item.summary.actual_cost),
        backgroundColor: seriesWithUsage.value.map((_, index) => palette[index % palette.length]),
        borderWidth: 0,
      },
    ],
  }
})

// legend supplies theme-aware member labels and shared font sizing for both charts,
// matching each member through its line or slice color.
const legend = computed(() => ({
  position: 'top' as const,
  labels: {
    color: textColor.value,
    usePointStyle: true,
    pointStyle: 'circle' as const,
    padding: 14,
    font: { size: CHART_LEGEND_FONT_SIZE },
  },
}))

// lineOptions pairs shared date interactions with monetary axes and tooltips, using
// finer tooltip precision than axis labels for small daily charges.
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' as const },
  plugins: {
    legend: legend.value,
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: (context: any) =>
          `${context.dataset.label}: ${formatBalanceAmount(Number(context.raw), { fractionDigits: 4 })}`,
      },
    },
  },
  scales: {
    x: {
      grid: { color: gridColor.value },
      ticks: { color: textColor.value, font: { size: CHART_TICK_FONT_SIZE } },
    },
    y: {
      beginAtZero: true,
      grid: { color: gridColor.value },
      ticks: {
        color: textColor.value,
        callback: (value: string | number) =>
          formatBalanceAmount(Number(value), { fractionDigits: 2 }),
      },
    },
  },
}))

// comparisonOptions identifies member slices through the shared legend and formats
// spending with its share of all displayed members in the external tooltip.
const comparisonOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: legend.value,
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: (context: any) => {
          const value = Number(context.raw)
          const total = seriesWithUsage.value.reduce(
            (sum, item) => sum + item.summary.actual_cost,
            0,
          )
          // Round displayed shares without changing slice proportions; free usage has zero share.
          const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
          return `${context.label}: ${formatBalanceAmount(value, { fractionDigits: 4 })} (${percentage}%)`
        },
      },
    },
  },
}))
</script>

<template>
  <div class="space-y-4" :aria-busy="loading">
    <!-- Metric cards select the trend while showing totals and comparison changes for the selected interval. -->
    <div
      class="dash-rise-item grid grid-cols-2 gap-4 lg:grid-cols-4"
      :style="{ '--rise-i': 0 }"
      role="tablist"
      :aria-label="t('dashboard.usageChart.metricsLabel')"
    >
      <button
        v-for="item in metricTabs"
        :key="item.key"
        type="button"
        role="tab"
        :data-testid="`usage-metric-${item.key}`"
        :aria-selected="metric === item.key"
        class="metric-card card flex min-w-0 flex-col gap-1 overflow-hidden p-4 text-left transition-colors duration-fast sm:gap-3"
        :class="
          metric === item.key
            ? 'border-primary-600 dark:border-primary-500/70'
            : 'hover:border-black/20 dark:hover:border-dark-500'
        "
        @click="selectMetric(item.key)"
      >
        <div class="flex min-w-0 items-center gap-2">
          <span
            class="flex h-5 w-5 shrink-0 items-center justify-center rounded-compact sm:h-7 sm:w-7"
            :class="[METRIC_TONES[item.key].tile, METRIC_TONES[item.key].icon]"
          >
            <Icon :name="item.icon" size="sm" />
          </span>
          <span class="truncate text-xs font-medium text-gray-500 dark:text-dark-400">{{
            item.label
          }}</span>
        </div>
        <div class="flex min-w-0 items-end justify-between gap-3">
          <Skeleton v-if="!loaded" :width="96" :height="32" />
          <span
            v-else
            class="min-w-0 truncate text-2xl font-semibold tracking-tight tabular-nums text-gray-900 dark:text-dark-50"
          >
            <!-- Keep animated digits visual and expose the settled value to screen readers. -->
            <span aria-hidden="true">{{ item.animatedValue }}</span>
            <span class="sr-only">{{ item.value }}</span>
          </span>
          <!-- Use the brand color for the selected metric's small trend. -->
          <UsageSparkline
            v-if="loaded"
            class="metric-sparkline h-8 w-20 shrink-0"
            :class="
              metric === item.key
                ? 'text-primary-600 dark:text-primary-500'
                : 'text-gray-400 dark:text-dark-500'
            "
            :values="item.series"
            :data-testid="`usage-sparkline-${item.key}`"
          />
        </div>
        <Skeleton v-if="!loaded" :width="120" :height="20" />
        <span
          v-else
          class="flex min-w-0 flex-wrap items-center gap-1 text-xs text-gray-500 dark:text-dark-400 sm:gap-2"
          :data-testid="`usage-delta-${item.key}`"
        >
          <template v-if="item.delta">
            <span
              class="inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 font-medium tabular-nums"
              :class="deltaToneClasses[item.key === 'cacheHitRate' ? item.delta.tone : 'flat']"
            >
              <Icon :name="deltaIcons[item.delta.tone]" size="xs" :animate-on-hover="false" />
              {{ item.delta.text }}
            </span>
            <span class="sm:hidden">{{ t('dashboard.usageChart.vsPreviousShort') }}</span>
            <span class="hidden sm:inline">{{ t('dashboard.usageChart.vsPrevious') }}</span>
          </template>
          <span v-else class="truncate">{{ t('dashboard.usageChart.noPrevious') }}</span>
        </span>
      </button>
    </div>

    <!-- Place the ranking below the chart until the xl layout has room for both. -->
    <div class="dash-rise-item grid grid-cols-1 gap-4 xl:grid-cols-3" :style="{ '--rise-i': 1 }">
      <div
        ref="chartCardRef"
        class="card flex min-w-0 flex-col p-4 xl:col-span-2"
        data-testid="usage-chart-card"
      >
        <div class="mb-4 flex min-h-7 flex-wrap items-center justify-between gap-2">
          <div class="flex items-baseline gap-2">
            <h3
              class="text-sm font-semibold text-gray-900 dark:text-dark-50"
              data-testid="usage-chart-title"
            >
              {{ chartTitle }}
            </h3>
            <span class="text-xs text-gray-500 dark:text-dark-400">{{ granularityHint }}</span>
          </div>
          <!-- Legend controls toggle individual token categories or the previous-period comparison for other metrics. -->
          <div class="flex flex-wrap gap-2">
            <template v-if="metric === 'tokens'">
              <button
                v-for="series in tokenSeries"
                :key="series.key"
                type="button"
                class="legend-chip"
                :class="
                  hiddenTokenSeries.includes(series.key) ? 'legend-chip-off' : 'legend-chip-on'
                "
                :aria-pressed="!hiddenTokenSeries.includes(series.key)"
                :data-testid="`usage-series-${series.key}`"
                @click="toggleTokenSeries(series.key)"
              >
                <span
                  class="h-2 w-2 rounded-full"
                  :class="{ 'opacity-30': hiddenTokenSeries.includes(series.key) }"
                  :style="{ backgroundColor: series.color }"
                ></span>
                {{ series.label }}
              </button>
            </template>
            <template v-else>
              <span class="legend-chip legend-chip-on" data-testid="usage-legend-current">
                <span class="h-0.5 w-3 rounded-full" :style="{ backgroundColor: mainColor }"></span>
                {{ t('dashboard.usageChart.currentPeriod') }}
              </span>
              <button
                type="button"
                class="legend-chip"
                :class="showPrevious ? 'legend-chip-on' : 'legend-chip-off'"
                :aria-pressed="showPrevious"
                data-testid="usage-series-previous"
                @click="showPrevious = !showPrevious"
              >
                <span
                  class="w-3 border-t-2 border-dashed"
                  :style="{ borderColor: themeColors.muted }"
                  :class="{ 'opacity-30': !showPrevious }"
                ></span>
                {{ t('dashboard.usageChart.previousPeriod') }}
              </button>
            </template>
          </div>
        </div>

        <div class="relative h-64 sm:h-80">
          <ChartSkeleton v-if="!loaded" variant="plot" height="100%" />
          <div
            v-else-if="!hasUsage"
            data-testid="usage-chart-empty"
            class="flex h-full flex-col items-center justify-center gap-2 text-sm text-gray-500 dark:text-dark-400"
          >
            <Icon name="chart" size="lg" class="text-gray-300 dark:text-dark-600" />
            {{ t('dashboard.usageChart.empty') }}
          </div>
          <template v-else>
            <!-- Recreate the chart after metric or data changes so the entrance reveal starts from the beginning. -->
            <Line
              ref="lineRef"
              :key="chartKey"
              :data="chartData"
              :options="chartOptions"
              :plugins="chartPlugins"
            />
            <!-- The drawing plugin publishes the incomplete bucket position for this decorative endpoint after each redraw. -->
            <span
              v-if="livePoint"
              data-testid="usage-live-point"
              class="pointer-events-none absolute"
              :style="{ left: `${livePoint.x}px`, top: `${livePoint.y}px`, color: mainColor }"
              aria-hidden="true"
            >
              <span
                class="live-point-ping absolute -left-1.5 -top-1.5 h-3 w-3 animate-ping rounded-full bg-current opacity-60 motion-reduce:animate-none"
              ></span>
              <span
                class="absolute -left-1 -top-1 h-2 w-2 rounded-full bg-current ring-2 ring-white dark:ring-dark-900"
              ></span>
            </span>
          </template>
        </div>
      </div>

      <UserDashboardTopModels class="xl:col-span-1" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePreferredReducedMotion } from '@vueuse/core'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  type Chart,
  type ChartDataset,
  type Plugin,
  type ScriptableLineSegmentContext,
  type TooltipItem,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import ChartSkeleton from '@/components/common/ChartSkeleton.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import UsageSparkline from './UsageSparkline.vue'
import UserDashboardTopModels from './UserDashboardTopModels.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import {
  useChartTheme,
  CHART_SERIES_COLORS,
  CHART_TICK_FONT_SIZE,
} from '@/composables/useChartTheme'
import { useCountUp } from '@/composables/useCountUp'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { formatNumberLocaleString as formatNumber, formatTokensK } from '@/utils/format'
import type { TrendDataPoint } from '@/api/usage.types'
import { cacheHitRateOf } from './usageChartData'
import { CHART_REVEAL_MS, COUNT_UP_MS } from './dashboardMotion'
import { METRIC_TONES } from './metricTones'
import { injectUsageChartState, type UsageMetric } from './usageChartState'

// UserDashboardUsageChart.vue renders selectable usage totals and their time
// series from the dashboard's shared query state. Each card presents the selected
// period alongside its previous period comparison. Request, token, and cost
// changes use neutral colors because increases alone do not establish a better
// result; cache hit rate changes retain directional status colors.
//
// Selecting a metric changes the chart and model ranking together. Token series
// can be hidden independently, while other metrics support a previous period
// overlay. Incomplete time buckets use dashed segments and an animated endpoint,
// with custom drawing plugins positioning peak and average annotations relative
// to the chart scales.

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip)

// TokenSeriesKey identifies one separately toggleable token category in the usage trend.
type TokenSeriesKey =
  'input_tokens' | 'output_tokens' | 'cache_creation_tokens' | 'cache_read_tokens'

// UsageDataset extends Chart.js line data with a comparison-period tooltip marker.
type UsageDataset = ChartDataset<'line', Array<number | null>> & {
  // previous marks the comparison dataset so tooltip labels can include its own
  // date while the horizontal axis remains aligned to current-period bucket positions.
  previous?: boolean
}

// BRAND_LINE_COLOR supplies the theme-specific request and spending line color shared with
// selected metric controls.
const BRAND_LINE_COLOR = { light: '#12A7E8', dark: '#00D2FF' }
// CARD_SURFACE matches card backgrounds so hover points and peak rings appear hollow against
// the surrounding plot.
const CARD_SURFACE = { light: '#FFFFFF', dark: '#0F0F10' }
// OPEN_SEGMENT_DASH and PREVIOUS_DASH distinguish incomplete live data and comparison
// periods from completed current-period line segments.
const OPEN_SEGMENT_DASH = [4, 4]
const PREVIOUS_DASH = [4, 4]
// LABEL_HEIGHT, LABEL_PADDING_X, and LABEL_GAP size annotation capsules and separate their
// text from the plotted peak.
const LABEL_HEIGHT = 18
const LABEL_PADDING_X = 6
const LABEL_GAP = 10

// Translations, balance formatting, chart theme, and injected usage state supply the metric
// cards and their shared trend selection.
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()
const { colors: themeColors, isDark } = useChartTheme()
const reducedMotion = usePreferredReducedMotion()
const {
  metric,
  activeRange,
  loading,
  loaded,
  current,
  previous,
  previousTotals,
  totals,
  selectMetric,
} = injectUsageChartState()

// hiddenTokenSeries and showPrevious retain local legend choices; chart element refs and
// livePoint connect canvas drawing with positioned HTML decorations.
const hiddenTokenSeries = ref<TokenSeriesKey[]>([])
const showPrevious = ref(true)
const lineRef = ref<{ chart?: Chart /* Mounted Chart.js drawing instance. */ } | null>(null)
const chartCardRef = ref<HTMLElement | null>(null)
const livePoint = ref<{
  x: number // Canvas endpoint horizontal pixels.
  y: number // Canvas endpoint vertical pixels.
} | null>(null)

// chartTitle and granularityHint label the selected metric and bucket precision using the
// current dashboard query state.
const chartTitle = computed(() =>
  t('dashboard.usageChart.trendTitle', {
    metric: t(`dashboard.usageChart.metrics.${metric.value}`),
  }),
)

const granularityHint = computed(() =>
  activeRange.value.granularity === 'hour'
    ? t('dashboard.usageChart.byHour')
    : t('dashboard.usageChart.byDay'),
)

// deltaToneClasses and deltaIcons describe increase, decrease, and unchanged comparisons;
// non-cache metrics use neutral badge colors regardless of direction.
const deltaToneClasses = {
  up: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
  down: 'bg-red-500/10 text-red-600 dark:text-red-400',
  flat: 'bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-dark-300',
}

const deltaIcons: Record<keyof typeof deltaToneClasses, IconName> = {
  up: 'trendingUp',
  down: 'trendingDown',
  flat: 'minus',
}

// Delta pairs formatted comparison text with its directional icon and tone category.
type Delta = {
  text: string // Formatted change amount.
  tone: keyof typeof deltaToneClasses // Rounded comparison direction.
}

// percentDelta computes the rounded percentage change from a positive comparison value.
// Missing or nonpositive prior values return null because a relative percentage cannot
// describe their comparison baseline.
const percentDelta = (currentValue: number, previousValue: number | undefined): Delta | null => {
  if (previousValue === undefined || previousValue <= 0) return null
  const change = ((currentValue - previousValue) / previousValue) * 100
  const percent = Math.round(change)
  if (percent === 0) return { text: '0%', tone: 'flat' }
  return { text: `${percent > 0 ? '+' : ''}${percent}%`, tone: percent > 0 ? 'up' : 'down' }
}

// pointDelta computes the cache-hit change in percentage points to one decimal place.
// Missing rates return null, while a rounded zero produces the neutral badge and unchanged-
// direction icon.
const pointDelta = (
  currentValue: number | null,
  previousValue: number | null | undefined,
): Delta | null => {
  if (currentValue === null || previousValue === null || previousValue === undefined) return null
  const change = Math.round((currentValue - previousValue) * 10) / 10
  if (change === 0) return { text: '0 pt', tone: 'flat' }
  return {
    text: `${change > 0 ? '+' : ''}${change.toFixed(1)} pt`,
    tone: change > 0 ? 'up' : 'down',
  }
}

// formatPercent renders one decimal percentage or a dash for an undefined rate. Null
// represents missing input-side usage, which the card distinguishes from a measured zero
// cache-hit percentage.
const formatPercent = (value: number | null): string =>
  value === null ? '—' : `${value.toFixed(1)}%`

// formatCostTotal retains four decimal places for spending below one balance unit.
const formatCostTotal = (value: number): string =>
  formatBalanceAmount(value, { fractionDigits: value >= 1 ? 2 : 4 })

// metricValueOf selects the requested metric from one usage bucket. Cache-hit rate delegates
// input-side calculation and returns null when the bucket has no positive input total for a
// defined percentage.
const metricValueOf = (point: TrendDataPoint, key: UsageMetric): number | null => {
  if (key === 'tokens') return point.total_tokens
  if (key === 'cost') return point.actual_cost
  if (key === 'cacheHitRate')
    return cacheHitRateOf(point.input_tokens, point.cache_creation_tokens, point.cache_read_tokens)
  return point.requests
}

// Animated card values follow totals while the template exposes final values separately for
// screen readers and rounds displayed request counts.
const animatedRequests = useCountUp(() => totals.value.requests, COUNT_UP_MS)
const animatedTokens = useCountUp(() => totals.value.tokens, COUNT_UP_MS)
const animatedCost = useCountUp(() => totals.value.cost, COUNT_UP_MS)
const animatedHitRate = useCountUp(() => totals.value.cacheHitRate ?? 0, COUNT_UP_MS)

// metricTabs pairs each total with its animated value, period comparison, and bucket series
// for the dashboard metric selector.
const metricTabs = computed(() => {
  const now = totals.value
  const prev = previousTotals.value
  // seriesOf collects one metric across current buckets for its small trend.
  const seriesOf = (key: UsageMetric) => current.value.map((point) => metricValueOf(point, key))
  return [
    {
      key: 'requests' as const,
      icon: 'swap' as IconName,
      label: t('dashboard.usageChart.metrics.requests'),
      value: formatNumber(now.requests),
      animatedValue: formatNumber(Math.round(animatedRequests.value)),
      delta: percentDelta(now.requests, prev?.requests),
      series: seriesOf('requests'),
    },
    {
      key: 'tokens' as const,
      icon: 'layers' as IconName,
      label: t('dashboard.usageChart.metrics.tokens'),
      value: formatTokensK(now.tokens),
      animatedValue: formatTokensK(animatedTokens.value),
      delta: percentDelta(now.tokens, prev?.tokens),
      series: seriesOf('tokens'),
    },
    {
      key: 'cost' as const,
      icon: 'dollar' as IconName,
      label: t('dashboard.usageChart.metrics.cost'),
      value: formatCostTotal(now.cost),
      animatedValue: formatCostTotal(animatedCost.value),
      delta: percentDelta(now.cost, prev?.cost),
      series: seriesOf('cost'),
    },
    {
      key: 'cacheHitRate' as const,
      icon: 'database' as IconName,
      label: t('dashboard.usageChart.metrics.cacheHitRate'),
      value: formatPercent(now.cacheHitRate),
      animatedValue: now.cacheHitRate === null ? '—' : formatPercent(animatedHitRate.value),
      delta: pointDelta(now.cacheHitRate, prev?.cacheHitRate),
      series: seriesOf('cacheHitRate'),
    },
  ]
})

// tokenSeries assigns labels and colors to the four independently displayed input, output,
// cache-creation, and cache-read quantities.
const tokenSeries = computed(() => [
  {
    key: 'input_tokens' as const,
    label: t('dashboard.usageChart.series.input'),
    color: CHART_SERIES_COLORS.input,
  },
  {
    key: 'output_tokens' as const,
    label: t('dashboard.usageChart.series.output'),
    color: CHART_SERIES_COLORS.output,
  },
  {
    key: 'cache_creation_tokens' as const,
    label: t('dashboard.usageChart.series.cacheCreation'),
    color: CHART_SERIES_COLORS.cacheCreation,
  },
  {
    key: 'cache_read_tokens' as const,
    label: t('dashboard.usageChart.series.cacheRead'),
    color: CHART_SERIES_COLORS.cacheRead,
  },
])

// hasUsage selects the plot when any additive total is positive, otherwise the chart card
// displays its empty state.
const hasUsage = computed(
  () => totals.value.requests > 0 || totals.value.tokens > 0 || totals.value.cost > 0,
)

// shortLabel abbreviates a bucket timestamp to hours or month-day according to query
// precision. The tooltip title retains the full timestamp so abbreviated axis labels remain
// distinguishable.
const shortLabel = (date: string): string =>
  activeRange.value.granularity === 'hour' ? date.slice(11, 16) : date.slice(5, 10)
// axisLabels follows current bucket order, while theme colors below supply the plot line and
// annotation surface colors.
const axisLabels = computed(() => current.value.map((point) => shortLabel(point.date)))

const brandColor = computed(() => (isDark.value ? BRAND_LINE_COLOR.dark : BRAND_LINE_COLOR.light))
const surfaceColor = computed(() => (isDark.value ? CARD_SURFACE.dark : CARD_SURFACE.light))

// mainColor uses the cache-rate series color for that metric and the theme-specific brand
// color for other single lines.
const mainColor = computed(() =>
  metric.value === 'cacheHitRate' ? CHART_SERIES_COLORS.cacheHitRate : brandColor.value,
)

// openBucketIndex identifies the final still-accumulating bucket when the interval includes
// the current instant and contains multiple points.
const openBucketIndex = computed(() =>
  activeRange.value.endAt.getTime() > Date.now() && current.value.length > 1
    ? current.value.length - 1
    : null,
)

// segmentStyle marks the line segment entering the incomplete final bucket, distinguishing
// its provisional value from completed bucket observations.
const segmentStyle = {
  borderDash: (context: ScriptableLineSegmentContext) =>
    context.p1DataIndex === openBucketIndex.value ? OPEN_SEGMENT_DASH : undefined,
}

// lineDataset builds a current-period dataset with gap spanning and monotone interpolation.
// Missing values connect surrounding observations, and shared segment styling identifies an
// incomplete final bucket in the active interval.
const lineDataset = (label: string, data: Array<number | null>, color: string): UsageDataset => ({
  label,
  data,
  borderColor: color,
  backgroundColor: color,
  borderWidth: 2,
  fill: false,
  cubicInterpolationMode: 'monotone' as const,
  segment: segmentStyle,
  pointRadius: 0,
  pointHoverRadius: 5,
  pointHoverBorderWidth: 2,
  pointHoverBorderColor: surfaceColor.value,
  pointHoverBackgroundColor: color,
  spanGaps: true,
})

// chartData renders visible token categories or one selected metric with an optional
// comparison line. Comparison points align by index with the current period, so its axis
// labels continue to describe current dates.
const chartData = computed(() => {
  const points = current.value
  if (metric.value === 'tokens') {
    const visible = tokenSeries.value.filter(
      (series) => !hiddenTokenSeries.value.includes(series.key),
    )
    return {
      labels: axisLabels.value,
      datasets: visible.map((series) => ({
        ...lineDataset(
          series.label,
          points.map((point) => point[series.key]),
          series.color,
        ),
        borderWidth: 1.75,
      })),
    }
  }
  const datasets: UsageDataset[] = [
    lineDataset(
      metric.value === 'cost'
        ? t('dashboard.usageChart.series.actualCost')
        : t(`dashboard.usageChart.metrics.${metric.value}`),
      points.map((point) => metricValueOf(point, metric.value)),
      mainColor.value,
    ),
  ]
  if (showPrevious.value && previous.value.length > 0) {
    // Align comparison data by bucket position and truncate to available current
    // buckets; the higher order draws that comparison beneath the current line.
    datasets.push({
      label: t('dashboard.usageChart.previousPeriod'),
      data: previous.value
        .slice(0, points.length)
        .map((point) => metricValueOf(point, metric.value)),
      borderColor: themeColors.value.muted,
      backgroundColor: themeColors.value.muted,
      borderWidth: 1.5,
      borderDash: PREVIOUS_DASH,
      fill: false,
      cubicInterpolationMode: 'monotone' as const,
      pointRadius: 0,
      pointHoverRadius: 3,
      pointHoverBorderWidth: 0,
      pointHoverBackgroundColor: themeColors.value.muted,
      spanGaps: true,
      order: 1,
      previous: true,
    })
  }
  return { labels: axisLabels.value, datasets }
})

// formatAxisValue formats vertical ticks according to the selected metric. Cost ticks omit
// the currency symbol to save axis space, while token and request ticks use their shared
// display formatters.
const formatAxisValue = (value: number): string => {
  if (metric.value === 'tokens') return formatTokensK(value)
  if (metric.value === 'cacheHitRate') return `${value}%`
  if (metric.value === 'cost') return String(Math.round(value * 10000) / 10000)
  return formatNumber(value)
}

// formatValue formats tooltip and annotation amounts for the selected metric. Missing values
// use a dash; request averages retain one decimal below one hundred and round to whole
// requests above it.
const formatValue = (value: number | null): string => {
  if (value === null) return '—'
  if (metric.value === 'tokens') return formatTokensK(value)
  if (metric.value === 'cacheHitRate') return formatPercent(value)
  if (metric.value === 'cost') return formatBalanceAmount(value, { fractionDigits: 4 })
  // Request averages can be fractional; retain tenths below one hundred requests.
  return formatNumber(value >= 100 ? Math.round(value) : Math.round(value * 10) / 10)
}

// annotation derives a peak from present values and a mean from completed buckets; token
// breakdowns and empty usage do not receive these single-series annotations.
const annotation = computed(() => {
  if (metric.value === 'tokens' || !hasUsage.value) return null
  const values = current.value.map((point) => metricValueOf(point, metric.value))
  let peakIndex = -1
  values.forEach((value, index) => {
    if (value !== null && (peakIndex < 0 || value > (values[peakIndex] as number)))
      peakIndex = index
  })
  if (peakIndex < 0 || !values[peakIndex]) return null
  const closed = values.filter(
    (value, index): value is number => value !== null && index !== openBucketIndex.value,
  )
  const average =
    closed.length > 0 ? closed.reduce((sum, value) => sum + value, 0) / closed.length : null
  return { peakIndex, peakValue: values[peakIndex] as number, average }
})

// chartOptions combines theme-aware axes with external tooltip rendering and metric-specific
// formatting, ordering, and totals for the visible datasets.
const chartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  // Draw points at their final heights from the first frame so the reveal plugin
  // exposes the settled curve instead of a line rising from the baseline.
  animation: false as const,
  interaction: { intersect: false, mode: 'index' as const },
  // Annotation capsules can extend above the plot, so reserve half their height
  // in the chart layout before the canvas clips the outer drawing area.
  layout: { padding: { top: LABEL_HEIGHT / 2 } },
  plugins: {
    legend: { display: false },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      // Sort token categories by their hovered amounts for comparison; single-metric
      // tooltips retain dataset order so the current period precedes its comparison.
      itemSort: (a: TooltipItem<'line'>, b: TooltipItem<'line'>) =>
        metric.value === 'tokens'
          ? ((b.raw as number | null) ?? 0) - ((a.raw as number | null) ?? 0)
          : a.datasetIndex - b.datasetIndex,
      callbacks: {
        title: (items: TooltipItem<'line'>[]) => current.value[items[0]?.dataIndex]?.date ?? '',
        label: (context: TooltipItem<'line'>) => {
          const dataset = context.dataset as UsageDataset
          const value = formatValue(context.raw as number | null)
          if (!dataset.previous) return `${dataset.label}: ${value}`
          const previousDate = previous.value[context.dataIndex]?.date
          return `${dataset.label}${previousDate ? ` (${shortLabel(previousDate)})` : ''}: ${value}`
        },
        footer: (items: TooltipItem<'line'>[]) => {
          if (metric.value !== 'tokens' || items.length < 2) return ''
          const sum = items.reduce((total, item) => total + ((item.raw as number | null) ?? 0), 0)
          return `${t('dashboard.usageChart.series.total')}: ${formatTokensK(sum)}`
        },
      },
    },
  },
  scales: {
    x: {
      grid: { display: false },
      ticks: {
        color: themeColors.value.text,
        autoSkip: true,
        maxTicksLimit: 8,
        maxRotation: 0,
        font: { size: CHART_TICK_FONT_SIZE },
      },
    },
    y: {
      beginAtZero: true,
      // Percentages share a fixed upper bound; additive metrics reserve headroom
      // so peak labels can sit near their points without crowding the plot edge.
      ...(metric.value === 'cacheHitRate' ? { max: 100 } : { grace: '15%' }),
      grid: { color: themeColors.value.grid, drawTicks: false },
      border: { display: false, dash: [3, 3] },
      ticks: {
        color: themeColors.value.text,
        padding: 8,
        maxTicksLimit: 5,
        font: { size: CHART_TICK_FONT_SIZE },
        callback: (value: string | number) => formatAxisValue(Number(value)),
      },
    },
  },
}))

// crosshairPlugin draws the active bucket guide beneath datasets so the hover position
// remains visible without covering the plotted points.
const crosshairPlugin: Plugin<'line'> = {
  id: 'usageCrosshair',
  // beforeDatasetsDraw paints a guide for the active hover position before chart datasets
  // render. It saves and restores canvas drawing state so guide styling does not leak into
  // the lines.
  beforeDatasetsDraw(chart) {
    const active = chart.getActiveElements()
    if (!active.length) return
    const { ctx, chartArea } = chart
    const x = active[0].element.x
    ctx.save()
    ctx.strokeStyle = themeColors.value.muted
    ctx.globalAlpha = 0.5
    ctx.lineWidth = 1
    ctx.setLineDash([3, 3])
    ctx.beginPath()
    ctx.moveTo(x, chartArea.top)
    ctx.lineTo(x, chartArea.bottom)
    ctx.stroke()
    ctx.restore()
  },
}

// drawLabel paints an annotation capsule at the supplied canvas position using measured text
// width. The caller supplies drawing font and baseline; canvases without roundRect receive a
// rectangular capsule.
const drawLabel = (
  ctx: CanvasRenderingContext2D,
  text: string,
  x: number,
  y: number,
  fill: string,
  color: string,
) => {
  const width = ctx.measureText(text).width + LABEL_PADDING_X * 2
  ctx.fillStyle = fill
  ctx.beginPath()
  if (typeof ctx.roundRect === 'function')
    ctx.roundRect(x, y, width, LABEL_HEIGHT, LABEL_HEIGHT / 2)
  else ctx.rect(x, y, width, LABEL_HEIGHT)
  ctx.fill()
  ctx.fillStyle = color
  ctx.fillText(text, x + LABEL_PADDING_X, y + LABEL_HEIGHT / 2)
}

// revealProgress and revealFrame track the active left-to-right reveal; a completed or
// reduced-motion reveal uses progress one and no scheduled frame.
let revealProgress = 1
let revealFrame = 0

// revealPlugin brackets dataset drawing with a saved canvas clip, then restores it after
// annotations and updates the positioned live endpoint.
const revealPlugin: Plugin<'line'> = {
  id: 'usageReveal',
  // beforeDatasetsDraw saves canvas state and clips the active reveal region.
  // afterDatasetsDraw owns the matching restore so current lines and annotations share
  // the same reveal progress while subsequent chart drawing regains the full canvas.
  beforeDatasetsDraw(chart) {
    const { ctx, chartArea } = chart
    ctx.save()
    if (revealProgress < 1) {
      ctx.beginPath()
      ctx.rect(
        chartArea.left - LABEL_GAP,
        0,
        (chartArea.width + LABEL_GAP * 2) * revealProgress,
        chart.height,
      )
      ctx.clip()
    }
  },
  // afterDatasetsDraw paints annotations inside the saved reveal region and restores canvas
  // state. It pairs with this plugin's beforeDatasetsDraw so later chart drawing is not left
  // clipped.
  afterDatasetsDraw(chart) {
    drawAnnotations(chart)
    chart.ctx.restore()
  },
  // afterDraw refreshes the HTML endpoint position after the canvas layout and datasets
  // settle.
  afterDraw(chart) {
    updateLivePoint(chart)
  },
}

// drawAnnotations paints a closed-bucket average line and a peak ring for the current
// dataset. Missing, hidden, or nonfinite points skip their annotations, and label placement
// uses chart-area measurements to limit overflow.
const drawAnnotations = (chart: Chart) => {
  const info = annotation.value
  const meta = chart.getDatasetMeta(0)
  if (!info || !meta || meta.hidden) return
  const { ctx, chartArea, scales } = chart
  ctx.font = `500 ${CHART_TICK_FONT_SIZE + 1}px ${ChartJS.defaults.font.family}`
  ctx.textBaseline = 'middle'

  if (info.average !== null) {
    const y = scales.y.getPixelForValue(info.average)
    ctx.save()
    ctx.strokeStyle = themeColors.value.muted
    ctx.globalAlpha = 0.6
    ctx.lineWidth = 1
    ctx.setLineDash([2, 4])
    ctx.beginPath()
    ctx.moveTo(chartArea.left, y)
    ctx.lineTo(chartArea.right, y)
    ctx.stroke()
    ctx.restore()
    const text = `${t('dashboard.usageChart.average')} · ${formatValue(info.average)}`
    const width = ctx.measureText(text).width + LABEL_PADDING_X * 2
    drawLabel(
      ctx,
      text,
      chartArea.right - width,
      y - LABEL_HEIGHT / 2,
      surfaceColor.value,
      themeColors.value.muted,
    )
  }

  const point = meta.data[info.peakIndex]
  if (!point || !Number.isFinite(point.x) || !Number.isFinite(point.y)) return
  ctx.save()
  ctx.strokeStyle = mainColor.value
  ctx.fillStyle = surfaceColor.value
  ctx.lineWidth = 2
  ctx.beginPath()
  ctx.arc(point.x, point.y, 4.5, 0, Math.PI * 2)
  ctx.fill()
  ctx.stroke()
  ctx.restore()
  const text = `${t('dashboard.usageChart.peak')} · ${formatValue(info.peakValue)}`
  const width = ctx.measureText(text).width + LABEL_PADDING_X * 2
  const x = Math.min(Math.max(point.x - width / 2, chartArea.left), chartArea.right - width)
  // Move the capsule below a high peak when the upper position would exceed the
  // annotation margin, keeping its text within the chart's reserved drawing area.
  const above = point.y - LABEL_GAP - LABEL_HEIGHT
  const y = above >= chartArea.top - LABEL_HEIGHT / 2 ? above : point.y + LABEL_GAP
  drawLabel(ctx, text, x, y, `${mainColor.value}26`, mainColor.value)
}

// updateLivePoint publishes the canvas location of the incomplete current bucket for its
// HTML marker. Token breakdowns, unfinished reveals, hidden datasets, and missing points
// clear the marker until a drawable single-series endpoint exists.
const updateLivePoint = (chart: Chart) => {
  const index = openBucketIndex.value
  const meta = chart.getDatasetMeta(0)
  const point =
    index === null || metric.value === 'tokens' || revealProgress < 1 ? null : meta?.data[index]
  if (!point || meta.hidden || !Number.isFinite(point.x) || !Number.isFinite(point.y)) {
    if (livePoint.value) livePoint.value = null
    return
  }
  if (livePoint.value?.x === point.x && livePoint.value?.y === point.y) return
  livePoint.value = { x: point.x, y: point.y }
}

// chartPlugins preserves crosshair drawing before reveal clipping so the guide and datasets
// use their intended canvas layering.
const chartPlugins = [crosshairPlugin, revealPlugin]

// cancelReveal cancels the scheduled reveal frame and clears its handle. Repeated calls are
// safe during replacement animation setup and component disposal, when a frame may already
// have completed.
const cancelReveal = () => {
  if (revealFrame) cancelAnimationFrame(revealFrame)
  revealFrame = 0
}

// playReveal restarts the left-to-right chart reveal using the current layout for every
// frame. It first cancels the previous frame chain so metric and data changes cannot animate
// the same chart concurrently. Reduced-motion preference or missing animation-frame support
// immediately selects full progress. Otherwise a cubic easing curve advances a clipped
// redraw until completion, with the component owning cancellation of the latest frame handle
// during disposal.
const playReveal = () => {
  cancelReveal()
  if (reducedMotion.value === 'reduce' || typeof requestAnimationFrame !== 'function') {
    revealProgress = 1
    return
  }
  revealProgress = 0
  const startedAt = performance.now()
  // tick advances reveal progress and redraws the current chart at this animation timestamp.
  // It schedules another frame while incomplete and clears the frame handle when the reveal
  // finishes.
  const tick = (now: number) => {
    const progress = Math.min((now - startedAt) / CHART_REVEAL_MS, 1)
    // Cubic easing advances quickly and slows toward completion.
    revealProgress = 1 - Math.pow(1 - progress, 3)
    lineRef.value?.chart?.draw()
    revealFrame = progress < 1 ? requestAnimationFrame(tick) : 0
  }
  revealFrame = requestAnimationFrame(tick)
}

// chartVersion increments when current data changes; chartKey also includes the metric so
// either change recreates the chart for a fresh entrance.
const chartVersion = ref(0)
const chartKey = computed(() => `${metric.value}-${chartVersion.value}`)

watch(current, () => {
  chartVersion.value += 1
  playReveal()
})
watch(metric, playReveal)

// Cancel the owned animation frame and hide the shared external tooltip before navigation
// removes this chart from the document.
onBeforeUnmount(() => {
  cancelReveal()
  hideExternalTooltip()
})

// toggleTokenSeries adds or removes a token category from the local hidden list.
const toggleTokenSeries = (key: TokenSeriesKey) => {
  hiddenTokenSeries.value = hiddenTokenSeries.value.includes(key)
    ? hiddenTokenSeries.value.filter((item) => item !== key)
    : [...hiddenTokenSeries.value, key]
}

// DashboardView uses chartCardRef to scroll this card into view after a heatmap date selects
// hourly details.
defineExpose({ chartCardRef })
</script>

<style scoped>
/* Show the sparkline when its card has enough inline space for both numbers and the trend, preserving numeric readability in narrower cards. */
.metric-card {
  container-type: inline-size;
}

.metric-sparkline {
  display: none;
}

@container (min-width: 200px) {
  .metric-sparkline {
    display: block;
  }
}

/* Legend capsules are also dataset visibility controls. */
.legend-chip {
  @apply inline-flex items-center gap-2 rounded-full border border-gray-200 px-3 py-1 text-xs transition-colors duration-fast dark:border-dark-600;
}

.legend-chip-on {
  @apply text-gray-700 dark:text-dark-100;
}

.legend-chip-off {
  @apply text-gray-400 dark:text-dark-500;
}
</style>

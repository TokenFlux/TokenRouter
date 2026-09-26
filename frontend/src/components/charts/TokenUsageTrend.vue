<template>
  <div class="card p-4">
    <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('admin.dashboard.tokenUsageTrend') }}
    </h3>
    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <div v-else-if="trendData.length > 0 && chartData" class="h-48">
      <Line :data="chartData" :options="lineOptions" />
    </div>
    <div
      v-else
      class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
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
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useChartTheme, CHART_SERIES_COLORS, CHART_TICK_FONT_SIZE, CHART_LEGEND_FONT_SIZE } from '@/composables/useChartTheme'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import type { TrendDataPoint } from '@/types'
import { formatTokens } from '@/utils/format'

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
)

onBeforeUnmount(hideExternalTooltip)

const { t } = useI18n()
const { formatBalanceAmount, formatUsdAmount } = useBalanceDisplay()

const props = withDefaults(defineProps<{
  trendData: TrendDataPoint[]
  loading?: boolean
  granularity?: 'day' | 'hour'
  showStandardCost?: boolean
}>(), {
  granularity: 'day',
  showStandardCost: true,
})

// 文字/网格走主题档位,序列色用共享语义色板(数值与迁移前逐项相同)。
const { colors: themeColors } = useChartTheme()
const chartColors = computed(() => ({
  text: themeColors.value.text,
  grid: themeColors.value.grid,
  ...CHART_SERIES_COLORS
}))

// 小时粒度只在坐标轴展示时分，完整时间仍由 tooltip 标题保留。
const formatHourLabel = (value: string): string => {
  const match = value.match(/(?:T|\s)(\d{2}:\d{2})/)
  return match?.[1] || value
}

// 日粒度只展示月日，避免年份重复占用横轴空间。
const formatDayLabel = (value: string): string => {
  const match = value.match(/^(?:\d{4}[-/])?(\d{1,2})[-/](\d{1,2})/)
  if (!match) return value
  return `${match[1].padStart(2, '0')}-${match[2].padStart(2, '0')}`
}

const chartLabels = computed(() => props.trendData.map((point) => (
  props.granularity === 'hour' ? formatHourLabel(point.date) : formatDayLabel(point.date)
)))

const chartData = computed(() => {
  if (!props.trendData?.length) return null

  return {
    labels: chartLabels.value,
    datasets: [
      {
        label: 'Input',
        data: props.trendData.map((d) => d.input_tokens),
        borderColor: chartColors.value.input,
        backgroundColor: `${chartColors.value.input}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: 'Output',
        data: props.trendData.map((d) => d.output_tokens),
        borderColor: chartColors.value.output,
        backgroundColor: `${chartColors.value.output}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: 'Cache Creation',
        data: props.trendData.map((d) => d.cache_creation_tokens),
        borderColor: chartColors.value.cacheCreation,
        backgroundColor: `${chartColors.value.cacheCreation}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: 'Cache Read',
        data: props.trendData.map((d) => d.cache_read_tokens),
        borderColor: chartColors.value.cacheRead,
        backgroundColor: `${chartColors.value.cacheRead}20`,
        fill: true,
        tension: 0.3
      },
      {
        label: 'Cached Input %',
        data: props.trendData.map((d) => {
          // 后端的 input_tokens 已扣除 cache_read，这里用输入侧总 token 作为命中率分母。
          const inputSideTokens = d.input_tokens + d.cache_creation_tokens + d.cache_read_tokens
          return inputSideTokens > 0 ? (d.cache_read_tokens / inputSideTokens) * 100 : 0
        }),
        borderColor: chartColors.value.cacheHitRate,
        backgroundColor: `${chartColors.value.cacheHitRate}20`,
        fill: false,
        tension: 0.3,
        yAxisID: 'yPercent'
      }
    ]
  }
})

const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
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
          size: CHART_LEGEND_FONT_SIZE
        }
      }
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        title: (tooltipItems: any[]) => {
          const dataIndex = tooltipItems[0]?.dataIndex
          return dataIndex !== undefined ? props.trendData[dataIndex]?.date || '' : ''
        },
        label: (context: any) => {
          if (context.dataset.yAxisID === 'yPercent') {
            return `${context.dataset.label}: ${formatPercent(context.raw)}`
          }
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        },
        footer: (tooltipItems: any) => {
          const dataIndex = tooltipItems[0]?.dataIndex
          if (dataIndex !== undefined && props.trendData[dataIndex]) {
            const data = props.trendData[dataIndex]
            const actual = formatBalanceAmount(data.actual_cost, { fractionDigits: 4 })
            if (!props.showStandardCost) return `Actual: ${actual}`
            return `Actual: ${actual} | Standard: ${formatUsdAmount(data.cost, { fractionDigits: 4 })}`
          }
          return ''
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        autoSkip: true,
        maxRotation: props.granularity === 'hour' ? 0 : 50,
        minRotation: props.granularity === 'hour' ? 0 : 0,
        font: {
          size: CHART_TICK_FONT_SIZE
        }
      }
    },
    y: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: CHART_TICK_FONT_SIZE
        },
        callback: (value: string | number) => formatTokens(Number(value))
      }
    },
    yPercent: {
      position: 'right' as const,
      min: 0,
      max: 100,
      grid: {
        drawOnChartArea: false
      },
      ticks: {
        color: chartColors.value.cacheHitRate,
        font: {
          size: CHART_TICK_FONT_SIZE
        },
        callback: (value: string | number) => `${value}%`
      }
    }
  }
}))

const formatPercent = (value: number): string => {
  const displayValue = value < 100 ? Math.floor(value * 100) / 100 : value
  return `${displayValue.toFixed(2)}%`
}

</script>

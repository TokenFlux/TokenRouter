<template>
  <div class="card p-4">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ title || t('usage.endpointDistribution') }}
      </h3>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <div
          v-if="showSourceToggle"
          class="inline-flex rounded-control border border-gray-200 bg-gray-50 p-0.5 dark:border-gray-700 dark:bg-dark-800"
        >
          <button
            type="button"
            class="rounded-control px-2.5 py-1 text-xs font-medium transition-colors"
            :class="source === 'inbound'
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="emit('update:source', 'inbound')"
          >
            {{ t('usage.inbound') }}
          </button>
          <button
            type="button"
            class="rounded-control px-2.5 py-1 text-xs font-medium transition-colors"
            :class="source === 'upstream'
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="emit('update:source', 'upstream')"
          >
            {{ t('usage.upstream') }}
          </button>
          <button
            type="button"
            class="rounded-control px-2.5 py-1 text-xs font-medium transition-colors"
            :class="source === 'path'
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="emit('update:source', 'path')"
          >
            {{ t('usage.path') }}
          </button>
        </div>

        <div
          v-if="showMetricToggle"
          class="inline-flex rounded-control border border-gray-200 bg-gray-50 p-0.5 dark:border-gray-700 dark:bg-dark-800"
        >
          <button
            type="button"
            class="rounded-control px-2.5 py-1 text-xs font-medium transition-colors"
            :class="metric === 'tokens'
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="emit('update:metric', 'tokens')"
          >
            {{ t('admin.dashboard.metricTokens') }}
          </button>
          <button
            type="button"
            class="rounded-control px-2.5 py-1 text-xs font-medium transition-colors"
            :class="metric === 'actual_cost'
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="emit('update:metric', 'actual_cost')"
          >
            {{ t('admin.dashboard.metricActualCost') }}
          </button>
        </div>
      </div>
    </div>
    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <!-- 桌面端顶部对齐，避免数据较少时表格被圆环图垂直居中。 -->
    <div v-else-if="displayEndpointStats.length > 0 && chartData" class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6">
      <div class="h-48 w-48 shrink-0">
        <Bar v-if="chartType === 'bar'" :data="chartData" :options="barOptions" />
        <Doughnut v-else :data="doughnutChartData" :options="doughnutOptions" />
      </div>
      <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
        <table class="w-full text-xs">
          <thead>
            <tr class="text-gray-500 dark:text-gray-400">
              <th class="pb-2 text-left">{{ t('usage.endpoint') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.requests') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.tokens') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.actual') }}</th>
              <th v-if="showStandardCost" class="pb-2 text-right">{{ t('admin.dashboard.standard') }}</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="item in displayEndpointStats" :key="item.endpoint">
              <tr
                class="border-t border-gray-100 transition-colors dark:border-gray-700"
                :class="enableBreakdown ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/40' : ''"
                @click="enableBreakdown && toggleBreakdown(item.endpoint)"
              >
                <td class="max-w-[180px] truncate py-1.5 font-medium" :class="enableBreakdown ? 'text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300' : 'text-gray-900 dark:text-white'" :title="item.endpoint">
                  <span class="inline-flex items-center gap-1">
                    <svg v-if="enableBreakdown && expandedKey === item.endpoint" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
                    <svg v-else-if="enableBreakdown" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/></svg>
                    {{ item.endpoint }}
                  </span>
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatNumber(item.requests) }}
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatTokens(item.total_tokens) }}
                </td>
                <td class="py-1.5 text-right text-green-600 dark:text-green-400">
                  {{ balanceUnitSymbol }}{{ formatCost(item.actual_cost) }}
                </td>
                <td v-if="showStandardCost" class="py-1.5 text-right text-gray-400 dark:text-gray-500">
                  {{ usdUnitSymbol }}{{ formatCost(item.cost) }}
                </td>
              </tr>
              <tr v-if="expandedKey === item.endpoint">
                <td :colspan="distributionColspan" class="p-0">
                  <UserBreakdownSubTable
                    :items="breakdownItems"
                    :loading="breakdownLoading"
                  />
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>
    <div v-else class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, BarElement, CategoryScale, LogarithmicScale, Tooltip, Legend } from 'chart.js'
import { Bar, Doughnut } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import UserBreakdownSubTable from './UserBreakdownSubTable.vue'
import { toLogarithmicDisplayValues } from '@/utils/chartDisplayScale'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE } from '@/composables/useChartTheme'
import type { EndpointStat, UserBreakdownItem } from '@/types'
import { getUserBreakdown } from '@/api/admin/dashboard'
import { formatTokens } from '@/utils/format'

ChartJS.register(ArcElement, BarElement, CategoryScale, LogarithmicScale, Tooltip, Legend)

onBeforeUnmount(hideExternalTooltip)

const { t } = useI18n()
const { balanceUnitSymbol, usdUnitSymbol } = useBalanceDisplay()

type DistributionMetric = 'tokens' | 'actual_cost'
type EndpointSource = 'inbound' | 'upstream' | 'path'
// 图表形态：默认圆环；用户用量页传 bar，避免同页多个卡片都是圆环图。
type EndpointChartType = 'doughnut' | 'bar'

const props = withDefaults(
  defineProps<{
    endpointStats: EndpointStat[]
    upstreamEndpointStats?: EndpointStat[]
    endpointPathStats?: EndpointStat[]
    loading?: boolean
    title?: string
    metric?: DistributionMetric
    chartType?: EndpointChartType
    source?: EndpointSource
    showMetricToggle?: boolean
    showSourceToggle?: boolean
    enableBreakdown?: boolean
    showStandardCost?: boolean
    startDate?: string
    endDate?: string
    filters?: Record<string, any>
  }>(),
  {
    upstreamEndpointStats: () => [],
    endpointPathStats: () => [],
    loading: false,
    title: '',
    metric: 'tokens',
    chartType: 'doughnut',
    source: 'inbound',
    showMetricToggle: false,
    showSourceToggle: false,
    enableBreakdown: true,
    showStandardCost: true,
  }
)

const emit = defineEmits<{
  'update:metric': [value: DistributionMetric]
  'update:source': [value: EndpointSource]
}>()

const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)
const showStandardCost = computed(() => props.showStandardCost)
const distributionColspan = computed(() => 4 + (showStandardCost.value ? 1 : 0))

const toggleBreakdown = async (endpoint: string) => {
  if (expandedKey.value === endpoint) {
    expandedKey.value = null
    return
  }
  expandedKey.value = endpoint
  breakdownLoading.value = true
  breakdownItems.value = []
  try {
    const res = await getUserBreakdown({
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      endpoint,
      endpoint_type: props.source,
    })
    breakdownItems.value = res.users || []
  } catch {
    breakdownItems.value = []
  } finally {
    breakdownLoading.value = false
  }
}

const chartColors = CHART_PALETTE

const displayEndpointStats = computed(() => {
  const sourceStats = props.source === 'upstream'
    ? props.upstreamEndpointStats
    : props.source === 'path'
      ? props.endpointPathStats
      : props.endpointStats
  if (!sourceStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...sourceStats].sort((a, b) => b[metricKey] - a[metricKey])
})

// 原始指标值：柱状图按原值走对数轴，圆环图按 log 压缩渲染，tooltip 始终按原始值计算真实占比。
const chartValues = computed(() =>
  displayEndpointStats.value.map((item) =>
    props.metric === 'actual_cost' ? item.actual_cost : item.total_tokens
  )
)

const chartData = computed(() => {
  if (!displayEndpointStats.value?.length) return null

  return {
    labels: displayEndpointStats.value.map((item) => item.endpoint),
    datasets: [
      {
        data: chartValues.value,
        backgroundColor: chartColors.slice(0, displayEndpointStats.value.length),
        borderWidth: 0
      }
    ]
  }
})

// 与 chartData 同生命周期（无数据时模板分支不会渲染），保持非空类型以通过图表组件的 data 校验。
const doughnutChartData = computed(() => ({
  labels: displayEndpointStats.value.map((item) => item.endpoint),
  datasets: [
    {
      data: toLogarithmicDisplayValues(chartValues.value),
      backgroundColor: chartColors.slice(0, displayEndpointStats.value.length),
      borderWidth: 0
    }
  ]
}))

// tooltip 统一展示「名称: 数值 (占比)」，圆环图与柱状图共用；
// 圆环扇区可能被 log 压缩，数值与占比一律按原始值（dataIndex 回查）计算。
const tooltipLabel = (context: any) => {
  const value = chartValues.value[context.dataIndex] ?? 0
  const total = chartValues.value.reduce((a: number, b: number) => a + b, 0)
  const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
  const formattedValue = props.metric === 'actual_cost'
    ? `${balanceUnitSymbol.value}${formatCost(value)}`
    : formatTokens(value)
  return `${context.label}: ${formattedValue} (${percentage}%)`
}

const doughnutOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: {
      display: false
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: tooltipLabel
      }
    }
  }
}))

const barOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  scales: {
    // 端点名通常较长，交给 tooltip 与右侧表格展示，坐标轴只保留柱形本身。
    x: {
      ticks: {
        display: false
      },
      grid: {
        display: false
      }
    },
    y: {
      // 端点用量常差几个数量级，对数刻度保证小用量端点也可见。
      type: 'logarithmic' as const,
      ticks: {
        display: false
      },
      // log 轴默认会为每个次要刻度画网格线，视觉上挤成一团，整体关闭。
      grid: {
        display: false
      }
    }
  },
  plugins: {
    legend: {
      display: false
    },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: tooltipLabel
      }
    }
  }
}))

const formatNumber = (value: number): string => {
  return value.toLocaleString()
}

const formatCost = (value: number): string => {
  if (value >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  } else if (value >= 1) {
    return value.toFixed(2)
  } else if (value >= 0.01) {
    return value.toFixed(3)
  }
  return value.toFixed(4)
}
</script>

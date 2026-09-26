<template>
  <div class="card p-4">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('admin.dashboard.groupDistribution') }}
      </h3>
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
    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <!-- 桌面端顶部对齐，避免数据较少时表格被圆环图垂直居中。 -->
    <div v-else-if="displayGroupStats.length > 0 && chartData" class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6">
      <div class="h-48 w-48 shrink-0">
        <Bar v-if="chartType === 'bar'" :data="chartData" :options="barOptions" />
        <Doughnut v-else :data="doughnutChartData" :options="doughnutOptions" />
      </div>
      <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
        <table class="w-full text-xs">
          <thead>
            <tr class="text-gray-500 dark:text-gray-400">
              <th class="pb-2 text-left">{{ t('admin.dashboard.group') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.requests') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.tokens') }}</th>
              <th class="pb-2 text-right">{{ t('admin.dashboard.actual') }}</th>
              <th v-if="showAccountCost" class="pb-2 text-right">{{ t('admin.dashboard.accountCost') }}</th>
              <th v-if="showStandardCost" class="pb-2 text-right">{{ t('admin.dashboard.standard') }}</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="group in displayGroupStats" :key="group.group_id">
              <tr
                class="border-t border-gray-100 transition-colors dark:border-gray-700"
                :class="enableBreakdown && group.group_id > 0 ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700/40' : ''"
                @click="enableBreakdown && group.group_id > 0 && toggleBreakdown('group', group.group_id)"
              >
                <td
                  class="max-w-[100px] truncate py-1.5 font-medium"
                  :class="enableBreakdown && group.group_id > 0 ? 'text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300' : 'text-gray-900 dark:text-white'"
                  :title="group.group_name || String(group.group_id)"
                >
                  <span class="inline-flex items-center gap-1">
                    <svg v-if="enableBreakdown && group.group_id > 0 && expandedKey === `group-${group.group_id}`" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
                    <svg v-else-if="enableBreakdown && group.group_id > 0" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/></svg>
                    {{ group.group_name || t('admin.dashboard.noGroup') }}
                  </span>
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatNumber(group.requests) }}
                </td>
                <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">
                  {{ formatTokens(group.total_tokens) }}
                </td>
                <td class="py-1.5 text-right text-green-600 dark:text-green-400">
                  {{ balanceUnitSymbol }}{{ formatCost(group.actual_cost) }}
                </td>
                <td v-if="showAccountCost" class="py-1.5 text-right text-orange-500 dark:text-orange-400">
                  {{ usdUnitSymbol }}{{ formatCost(group.account_cost) }}
                </td>
                <td v-if="showStandardCost" class="py-1.5 text-right text-gray-400 dark:text-gray-500">
                  {{ usdUnitSymbol }}{{ formatCost(group.cost) }}
                </td>
              </tr>
              <!-- User breakdown sub-rows -->
              <tr v-if="expandedKey === `group-${group.group_id}`">
                <td :colspan="distributionColspan" class="p-0">
                  <UserBreakdownSubTable
                    :items="breakdownItems"
                    :loading="breakdownLoading"
                    :show-account-cost="showAccountCost"
                    :show-standard-cost="showStandardCost"
                  />
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
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
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, BarElement, CategoryScale, LogarithmicScale, Tooltip, Legend } from 'chart.js'
import { Bar, Doughnut } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import UserBreakdownSubTable from './UserBreakdownSubTable.vue'
import { toLogarithmicDisplayValues } from '@/utils/chartDisplayScale'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE, CHART_TICK_FONT_SIZE } from '@/composables/useChartTheme'
import type { GroupStat, UserBreakdownItem } from '@/types'
import { getUserBreakdown } from '@/api/admin/dashboard'
import { formatTokens } from '@/utils/format'

ChartJS.register(ArcElement, BarElement, CategoryScale, LogarithmicScale, Tooltip, Legend)

onBeforeUnmount(hideExternalTooltip)

const { t } = useI18n()
const { balanceUnitSymbol, usdUnitSymbol } = useBalanceDisplay()

type DistributionMetric = 'tokens' | 'actual_cost'
// 图表形态：默认圆环；用户用量页传 bar（水平条形图），避免同页多个卡片都是圆环图。
type GroupChartType = 'doughnut' | 'bar'

const props = withDefaults(defineProps<{
  groupStats: GroupStat[]
  loading?: boolean
  metric?: DistributionMetric
  chartType?: GroupChartType
  showMetricToggle?: boolean
  enableBreakdown?: boolean
  showAccountCost?: boolean
  showStandardCost?: boolean
  startDate?: string
  endDate?: string
  filters?: Record<string, any>
}>(), {
  loading: false,
  metric: 'tokens',
  chartType: 'doughnut',
  showMetricToggle: false,
  enableBreakdown: true,
  showAccountCost: true,
  showStandardCost: true,
})

const emit = defineEmits<{
  'update:metric': [value: DistributionMetric]
}>()

const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)
const showAccountCost = computed(() => props.showAccountCost)
const showStandardCost = computed(() => props.showStandardCost)
const distributionColspan = computed(() => 4 + (showAccountCost.value ? 1 : 0) + (showStandardCost.value ? 1 : 0))

const toggleBreakdown = async (type: string, id: number | string) => {
  const key = `${type}-${id}`
  if (expandedKey.value === key) {
    expandedKey.value = null
    return
  }
  expandedKey.value = key
  breakdownLoading.value = true
  breakdownItems.value = []
  try {
    const res = await getUserBreakdown({
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      group_id: Number(id),
    })
    breakdownItems.value = res.users || []
  } catch {
    breakdownItems.value = []
  } finally {
    breakdownLoading.value = false
  }
}

const chartColors = CHART_PALETTE // 原 10 色拷贝前缀逐项一致,补齐 11/12 色

const displayGroupStats = computed(() => {
  if (!props.groupStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...props.groupStats].sort((a, b) => toFiniteNumber(b[metricKey]) - toFiniteNumber(a[metricKey]))
})

// 图表标签与表格行保持一致：无分组（group_id=0）回退为「No Group」，而不是显示 0。
const groupLabel = (g: GroupStat): string =>
  g.group_name || (g.group_id > 0 ? String(g.group_id) : t('admin.dashboard.noGroup'))

// 原始指标值：条形图按原值走对数轴，圆环图按 log 压缩渲染，tooltip 始终按原始值计算真实占比。
const chartValues = computed(() =>
  displayGroupStats.value.map((g) => toFiniteNumber(props.metric === 'actual_cost' ? g.actual_cost : g.total_tokens))
)

const chartData = computed(() => {
  if (!props.groupStats?.length) return null

  return {
    labels: displayGroupStats.value.map(groupLabel),
    datasets: [
      {
        data: chartValues.value,
        backgroundColor: chartColors.slice(0, displayGroupStats.value.length),
        borderWidth: 0
      }
    ]
  }
})

// 与 chartData 同生命周期（无数据时模板分支不会渲染），保持非空类型以通过图表组件的 data 校验。
const doughnutChartData = computed(() => ({
  labels: displayGroupStats.value.map(groupLabel),
  datasets: [
    {
      data: toLogarithmicDisplayValues(chartValues.value),
      backgroundColor: chartColors.slice(0, displayGroupStats.value.length),
      borderWidth: 0
    }
  ]
}))

// tooltip 统一展示「名称: 数值 (占比)」，圆环图与条形图共用；
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
  indexAxis: 'y' as const,
  responsive: true,
  maintainAspectRatio: false,
  scales: {
    // 分组用量常差几个数量级，对数刻度保证小用量分组也可见；
    // 精确数值由 tooltip 与右侧表格呈现，x 轴不再显示刻度。
    x: {
      type: 'logarithmic' as const,
      ticks: {
        display: false
      },
      // log 轴默认会为每个次要刻度画网格线，视觉上挤成一团，整体关闭。
      grid: {
        display: false
      }
    },
    y: {
      grid: {
        display: false
      },
      ticks: {
        autoSkip: false,
        font: {
          size: CHART_TICK_FONT_SIZE
        },
        // 分组名可能较长，y 轴标签截断展示，全名见 tooltip 与表格。
        callback(this: any, value: any) {
          const label = String(this.getLabelForValue(value) ?? '')
          return label.length > 12 ? `${label.slice(0, 12)}…` : label
        }
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
  return toFiniteNumber(value).toLocaleString()
}

const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

const formatCost = (value: number | null | undefined): string => {
  const safeValue = toFiniteNumber(value)
  if (safeValue >= 1000) {
    return (safeValue / 1000).toFixed(2) + 'K'
  } else if (safeValue >= 1) {
    return safeValue.toFixed(2)
  } else if (safeValue >= 0.01) {
    return safeValue.toFixed(3)
  }
  return safeValue.toFixed(4)
}
</script>

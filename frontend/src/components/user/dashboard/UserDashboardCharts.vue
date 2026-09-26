<template>
  <div class="space-y-6">
    <!-- Date Range Filter -->
    <div class="card p-4">
      <div class="time-controls flex flex-wrap items-center justify-between gap-2">
        <div class="flex min-w-0 items-center gap-2">
          <span class="time-control-label text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('dashboard.timeRange') }}:</span>
          <DateRangePicker :start-date="startDate" :end-date="endDate" @update:startDate="$emit('update:startDate', $event)" @update:endDate="$emit('update:endDate', $event)" @change="$emit('dateRangeChange', $event)" />
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <span class="time-control-label text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('dashboard.granularity') }}:</span>
          <div class="w-28">
            <Select :model-value="granularity" :options="[{value:'day', label:t('dashboard.day')}, {value:'hour', label:t('dashboard.hour')}]" @update:model-value="$emit('update:granularity', $event)" @change="$emit('granularityChange')" />
          </div>
        </div>
      </div>
    </div>

    <!-- Charts Grid -->
    <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
      <!-- Model Distribution Chart -->
      <div class="card relative overflow-hidden p-4">
        <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/50 backdrop-blur-sm dark:bg-dark-800/50">
          <LoadingSpinner size="md" />
        </div>
        <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{ t('dashboard.modelDistribution') }}</h3>
        <!-- 桌面端顶部对齐，避免数据较少时表格被圆环图垂直居中。 -->
        <div v-if="modelData" class="flex flex-col items-center gap-4 sm:flex-row sm:items-start sm:gap-6" data-testid="model-distribution-content">
          <div class="h-48 w-48 shrink-0">
            <Doughnut :data="modelData" :options="doughnutOptions" />
          </div>
          <div class="max-h-48 w-full min-w-0 flex-1 overflow-auto">
            <table class="w-full text-xs">
              <thead>
                <tr class="text-gray-500 dark:text-gray-400">
                  <th class="pb-2 text-left">{{ t('dashboard.model') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.requests') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.tokens') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.actual') }}</th>
                  <th class="pb-2 text-right">{{ t('dashboard.standard') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="model in models" :key="model.model" class="border-t border-gray-100 dark:border-gray-700">
                  <td class="max-w-[100px] truncate py-1.5 font-medium text-gray-900 dark:text-white" :title="model.model">{{ model.model }}</td>
                  <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">{{ formatNumber(model.requests) }}</td>
                  <td class="py-1.5 text-right text-gray-600 dark:text-gray-400">{{ formatTokens(model.total_tokens) }}</td>
                  <td class="py-1.5 text-right text-green-600 dark:text-green-400">{{ formatBalanceAmount(model.actual_cost, { fractionDigits: 4 }) }}</td>
                  <td class="py-1.5 text-right text-gray-400 dark:text-gray-500">{{ formatUsdAmount(model.cost, { fractionDigits: 4 }) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <div v-else data-testid="model-distribution-empty" class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
          {{ t('dashboard.noDataAvailable') }}
        </div>
      </div>

      <!-- Token Usage Trend Chart -->
      <TokenUsageTrend :trend-data="trend" :loading="loading" :granularity="granularity === 'hour' ? 'hour' : 'day'" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import { Doughnut } from 'vue-chartjs'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE } from '@/composables/useChartTheme'
import type { TrendDataPoint, ModelStat } from '@/types'
import { formatNumberLocaleString as formatNumber, formatTokensK as formatTokens } from '@/utils/format'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, ArcElement, Title, Tooltip, Legend, Filler } from 'chart.js'
ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, ArcElement, Title, Tooltip, Legend, Filler)

onBeforeUnmount(hideExternalTooltip)

const props = defineProps<{ loading: boolean, startDate: string, endDate: string, granularity: string, trend: TrendDataPoint[], models: ModelStat[] }>()
defineEmits(['update:startDate', 'update:endDate', 'update:granularity', 'dateRangeChange', 'granularityChange', 'refresh'])
const { t } = useI18n()
const { formatBalanceAmount, formatUsdAmount } = useBalanceDisplay()

const modelData = computed(() => !props.models?.length ? null : {
  labels: props.models.map((m: ModelStat) => m.model),
  datasets: [{
    data: props.models.map((m: ModelStat) => m.total_tokens),
    backgroundColor: [...CHART_PALETTE]
  }]
})

const doughnutOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      enabled: false,
      external: externalTooltipHandler,
      callbacks: {
        label: (context: any) => `${context.label}: ${formatTokens(context.parsed)} tokens`
      }
    }
  }
}
</script>

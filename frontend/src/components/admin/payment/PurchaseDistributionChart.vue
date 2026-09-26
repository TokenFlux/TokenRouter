<template>
  <div class="card p-4">
    <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('payment.admin.purchaseDistribution') }}
    </h3>
    <div
      v-if="!items?.length"
      class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('payment.admin.noData') }}
    </div>
    <div v-else class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(260px,0.9fr)]">
      <div class="min-w-0">
        <p class="mb-3 text-center text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.amountShare') }}
        </p>
        <div class="mx-auto h-52 max-w-52">
          <Doughnut :data="amountChartData" :options="amountChartOptions" />
        </div>
      </div>
      <div class="min-w-0">
        <p class="mb-3 text-center text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.countShare') }}
        </p>
        <div class="mx-auto h-52 max-w-52">
          <Doughnut :data="countChartData" :options="countChartOptions" />
        </div>
      </div>
      <div class="max-h-64 min-w-0 overflow-y-auto">
        <table class="w-full text-xs">
          <thead>
            <tr class="text-gray-500 dark:text-gray-400">
              <th class="pb-2 text-left">{{ t('payment.admin.purchaseItem') }}</th>
              <th class="pb-2 text-right">{{ t('payment.admin.revenue') }}</th>
              <th class="pb-2 text-right">{{ t('payment.admin.orderCount') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(item, index) in sortedItems"
              :key="distributionKey(item, index)"
              class="border-t border-gray-100 dark:border-gray-700"
            >
              <td class="py-2">
                <div class="flex min-w-0 items-center gap-2">
                  <span
                    class="h-2.5 w-2.5 shrink-0 rounded-full"
                    :style="{ backgroundColor: chartColors[index % chartColors.length] }"
                  ></span>
                  <span class="truncate font-medium text-gray-900 dark:text-white" :title="displayLabel(item)">
                    {{ displayLabel(item) }}
                  </span>
                </div>
              </td>
              <td class="py-2 text-right font-medium text-gray-900 dark:text-white">
                ${{ formatMoney(item.amount) }}
              </td>
              <td class="py-2 text-right text-gray-600 dark:text-gray-400">
                {{ item.count.toLocaleString() }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, Legend, type TooltipItem } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE } from '@/composables/useChartTheme'
import type { DashboardStats } from '@/types/payment'

ChartJS.register(ArcElement, Tooltip, Legend)

onBeforeUnmount(hideExternalTooltip)

const { t } = useI18n()

type PurchaseDistributionItem = DashboardStats['purchase_distribution'][number]

const props = defineProps<{
  items: PurchaseDistributionItem[]
}>()

const chartColors = CHART_PALETTE

const sortedItems = computed(() => {
  return [...(props.items || [])].sort((a, b) => {
    if (b.amount === a.amount) return b.count - a.count
    return b.amount - a.amount
  })
})

const amountChartData = computed(() => ({
  labels: sortedItems.value.map(displayLabel),
  datasets: [
    {
      data: sortedItems.value.map((item) => item.amount),
      backgroundColor: chartColors.slice(0, sortedItems.value.length),
      borderWidth: 0
    }
  ]
}))

const countChartData = computed(() => ({
  labels: sortedItems.value.map(displayLabel),
  datasets: [
    {
      data: sortedItems.value.map((item) => item.count),
      backgroundColor: chartColors.slice(0, sortedItems.value.length),
      borderWidth: 0
    }
  ]
}))

const amountChartOptions = computed(() => makeChartOptions((value) => `$${formatMoney(value)}`))
const countChartOptions = computed(() => makeChartOptions((value) => value.toLocaleString()))

function makeChartOptions(formatValue: (value: number) => string) {
  return {
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
          label: (context: TooltipItem<'doughnut'>) => {
            const value = Number(context.raw || 0)
            const total = context.dataset.data.reduce((sum, item) => sum + Number(item || 0), 0)
            const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
            return `${context.label}: ${formatValue(value)} (${percentage}%)`
          }
        }
      }
    }
  }
}

function displayLabel(item: PurchaseDistributionItem): string {
  // 后端会把按量支付的 label 设置成 balance，这里统一翻译为用户可读文案。
  if (item.type === 'balance') return t('payment.admin.payAsYouGo')
  return item.label || t('payment.admin.subscriptionOrder')
}

function distributionKey(item: PurchaseDistributionItem, index: number): string {
  return `${item.type}-${item.plan_id || item.label || index}`
}

function formatMoney(value: number): string {
  return Number(value || 0).toFixed(2)
}
</script>

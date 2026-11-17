<template>
  <div class="card p-4" :aria-busy="loading">
    <div class="mb-4 flex flex-wrap items-end justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('payment.admin.purchaseDistribution') }}
      </h3>
      <Select
        v-if="currencyOptions.length > 0"
        v-model="selectedCurrency"
        :options="currencyOptions"
        :aria-label="t('payment.admin.purchaseCurrency')"
        class="w-32"
      />
    </div>
    <div
      v-if="!loading && !items?.length"
      class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
    >
      {{ t('payment.admin.noData') }}
    </div>
    <div v-else class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(260px,0.9fr)]">
      <div class="min-w-0">
        <p class="mb-3 text-center text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.amountShare') }}
        </p>
        <div class="mx-auto h-52 max-w-52">
          <Skeleton v-if="loading" variant="circle" :height="208" />
          <Doughnut v-else :data="amountChartData" :options="amountChartOptions" />
        </div>
      </div>
      <div class="min-w-0">
        <p class="mb-3 text-center text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.countShare') }}
        </p>
        <div class="mx-auto h-52 max-w-52">
          <Skeleton v-if="loading" variant="circle" :height="208" />
          <Doughnut v-else :data="countChartData" :options="countChartOptions" />
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
          <TableSkeletonBody v-if="loading" :columns="3" cell-class="py-2" />
          <tbody v-else>
            <tr
              v-for="(item, index) in sortedItems"
              :key="distributionKey(item, index)"
              class="border-t border-gray-100 dark:border-dark-600"
            >
              <td class="py-2">
                <div class="flex min-w-0 items-center gap-2">
                  <span
                    class="h-2.5 w-2.5 shrink-0 rounded-full"
                    :style="{ backgroundColor: chartColors[index % chartColors.length] }"
                  ></span>
                  <span
                    class="truncate font-medium text-gray-900 dark:text-white"
                    :title="displayLabel(item)"
                  >
                    {{ displayLabel(item) }}
                  </span>
                </div>
              </td>
              <td class="py-2 text-right font-medium text-gray-900 dark:text-white">
                {{ formatMoney(item.amount) }}
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
import Skeleton from '@/components/common/Skeleton.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'

import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, Legend, type TooltipItem } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import { externalTooltipHandler, hideExternalTooltip } from '@/utils/chartExternalTooltip'
import { CHART_PALETTE } from '@/composables/useChartTheme'
import type { DashboardStats } from '@/api/payment.types'
import Select from '@/components/common/Select.vue'

// PurchaseDistributionChart.vue displays payment purchase distribution as amount and count
// doughnuts alongside detail rows. Both charts derive labels, colors, and dataset
// positions from the same sorted item sequence. Amount determines ordering first, with
// count resolving equal amounts, so a row's color identifies the same purchase category in
// either chart. Currency selection filters both charts and the table before computing
// totals, so monetary percentages compare purchases in the same payment currency.
//
// Chart tooltip percentages use the total of the active dataset, which makes amount shares
// and purchase-count shares distinct. Balance purchases receive a translated label even
// when the backend label is a technical identifier. Empty items select the template's
// empty state, and the shared external tooltip is hidden when the component unmounts.
ChartJS.register(ArcElement, Tooltip, Legend)

// The shared tooltip is rendered outside the chart canvas; hide it on unmount so page
// navigation removes any visible chart tooltip.
onBeforeUnmount(hideExternalTooltip)

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()

// PurchaseDistributionItem names one purchase-category entry returned by the payment
// dashboard API.
type PurchaseDistributionItem = DashboardStats['purchase_distribution'][number]

// props supplies parent-owned records and presentation state consumed by this component
// and its event handlers.
const props = defineProps<{
  loading?: boolean // Initial distribution request pending.
  items: PurchaseDistributionItem[] /* ordered display item collection */
}>()

// currencyOptions lists observed payment currencies, requestedCurrency retains the user's
// selection, and selectedCurrency resolves the visible unit. A refreshed list that omits
// the requested selection displays its first available currency.
const currencyOptions = computed(() =>
  [...new Set(props.items.map((item) => item.currency))]
    .sort()
    .map((currency) => ({ label: currency, value: currency })),
)
const requestedCurrency = ref('')
const selectedCurrency = computed({
  get: () =>
    currencyOptions.value.some((option) => option.value === requestedCurrency.value)
      ? requestedCurrency.value
      : currencyOptions.value[0]?.value || '',
  set: (value: string) => {
    requestedCurrency.value = value
    // Currency changes replace the hovered dataset, so remove its external tooltip before
    // another chart interaction renders a value from the selected currency.
    hideExternalTooltip()
  },
})

// chartColors shares the dashboard palette across amount charts, count charts, and detail
// row markers for each sorted category.
const chartColors = CHART_PALETTE

// sortedItems filters the selected currency before sorting amounts and counts. Both chart
// totals therefore use observations in one currency while parent data remains unchanged.
const sortedItems = computed(() => {
  return props.items
    .filter((item) => item.currency === selectedCurrency.value)
    .sort((a, b) => {
      if (b.amount === a.amount) return b.count - a.count
      return b.amount - a.amount
    })
})

// amountChartData assigns purchase amounts to the shared sorted category labels and colors
// used by the count chart.
const amountChartData = computed(() => ({
  labels: sortedItems.value.map(displayLabel),
  datasets: [
    {
      data: sortedItems.value.map((item) => item.amount),
      backgroundColor: chartColors.slice(0, sortedItems.value.length),
      borderWidth: 0,
    },
  ],
}))

// countChartData assigns purchase counts to the shared sorted category labels and colors
// used by the amount chart.
const countChartData = computed(() => ({
  labels: sortedItems.value.map(displayLabel),
  datasets: [
    {
      data: sortedItems.value.map((item) => item.count),
      backgroundColor: chartColors.slice(0, sortedItems.value.length),
      borderWidth: 0,
    },
  ],
}))

// amountChartOptions formats tooltip values in the selected currency while the shared
// chart factory computes this currency's dataset percentage.
const amountChartOptions = computed(() => makeChartOptions(formatMoney))
// countChartOptions formats tooltip values as localized counts while the shared chart
// factory computes this dataset percentage.
const countChartOptions = computed(() => makeChartOptions((value) => value.toLocaleString()))

// makeChartOptions configures doughnut tooltips using the supplied value formatter.
// Percentages are calculated against the current dataset total and show zero when that
// total is not positive.
function makeChartOptions(formatValue: (value: number) => string) {
  return {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: {
        display: false,
      },
      tooltip: {
        enabled: false,
        external: externalTooltipHandler,
        callbacks: {
          label: (context: TooltipItem<'doughnut'>) => {
            const value = Number(context.raw || 0)
            const total = context.dataset.data.reduce((sum, item) => sum + Number(item || 0), 0)
            // Use the active dataset total for this share and show zero for an empty
            // total, preventing an undefined tooltip percentage.
            const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
            return `${context.label}: ${formatValue(value)} (${percentage}%)`
          },
        },
      },
    },
  }
}

// displayLabel returns a localized name for a purchase distribution row. Balance rows use
// the pay-as-you-go label, while unnamed subscription rows use the generic subscription
// label for display.
function displayLabel(item: PurchaseDistributionItem): string {
  // The API uses a technical balance category label; translate that category here so
  // detail rows and chart labels share user-readable text.
  if (item.type === 'balance') return t('payment.admin.payAsYouGo')
  return item.label || t('payment.admin.subscriptionOrder')
}

// distributionKey combines payment currency, purchase kind, and plan identity for detail
// rows. A missing plan identifier uses its label, then its index, so unnamed entries still
// receive a key within their currency.
function distributionKey(item: PurchaseDistributionItem, index: number): string {
  return `${item.currency}-${item.type}-${item.plan_id || item.label || index}`
}

// formatMoney renders purchase amounts with their payment currency code. Two decimal
// places match the dashboard aggregate precision, and the code distinguishes currencies
// that share a symbol, such as USD and HKD. Falsy numeric values display zero.
function formatMoney(value: number): string {
  return `${selectedCurrency.value} ${Number(value || 0).toFixed(2)}`
}
</script>

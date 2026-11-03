<template>
  <!-- Top alignment keeps icons level across cards even when currency totals or balance-unit labels occupy multiple text lines. -->
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-5">
    <div class="card p-4">
      <div class="flex items-start gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-green-100 dark:bg-green-900/30"
        >
          <Icon
            name="dollar"
            size="md"
            class="h-5 w-5 text-green-600 dark:text-green-400"
            :stroke-width="2"
          />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('payment.admin.todayRevenue')
          }}</p>
          <p
            v-for="[currency, amount] in sortedAmounts(stats.today_amount)"
            :key="currency"
            class="text-xl font-bold text-gray-900 dark:text-white"
          >
            {{ formatMoney(currency, amount) }}
          </p>
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ stats.today_count }} {{ t('payment.admin.orders') }}
          </p>
        </div>
      </div>
    </div>

    <div class="card p-4">
      <div class="flex items-start gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-blue-100 dark:bg-blue-900/30"
        >
          <Icon
            name="creditCard"
            size="md"
            class="h-5 w-5 text-blue-600 dark:text-blue-400"
            :stroke-width="2"
          />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('payment.admin.totalRevenue')
          }}</p>
          <p
            v-for="[currency, amount] in sortedAmounts(stats.total_amount)"
            :key="currency"
            class="text-xl font-bold text-gray-900 dark:text-white"
          >
            {{ formatMoney(currency, amount) }}
          </p>
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ stats.total_count }} {{ t('payment.admin.orders') }}
          </p>
        </div>
      </div>
    </div>

    <div class="card p-4">
      <div class="flex items-start gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-purple-100 dark:bg-purple-900/30"
        >
          <Icon
            name="chart"
            size="md"
            class="h-5 w-5 text-purple-600 dark:text-purple-400"
            :stroke-width="2"
          />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('payment.admin.todayOrders')
          }}</p>
          <p class="text-xl font-bold text-gray-900 dark:text-white">{{ stats.today_count }}</p>
        </div>
      </div>
    </div>

    <div class="card p-4">
      <div class="flex items-start gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-amber-100 dark:bg-amber-900/30"
        >
          <Icon
            name="chart"
            size="md"
            class="h-5 w-5 text-amber-600 dark:text-amber-400"
            :stroke-width="2"
          />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('payment.admin.avgAmount')
          }}</p>
          <p
            v-for="[currency, amount] in sortedAmounts(stats.avg_amount)"
            :key="currency"
            class="text-xl font-bold text-gray-900 dark:text-white"
          >
            {{ formatMoney(currency, amount) }}
          </p>
        </div>
      </div>
    </div>

    <div class="card p-4">
      <div class="flex items-start gap-3">
        <div
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-sky-100 dark:bg-sky-900/30"
        >
          <Icon
            name="brain"
            size="md"
            class="h-5 w-5 text-sky-600 dark:text-sky-400"
            :stroke-width="2"
          />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('payment.admin.avgBalanceUnitPurchaseUnitPrice', { unitName: balanceUnitName })
          }}</p>
          <p
            v-for="[currency, amount] in sortedAmounts(
              stats.avg_reasoning_point_purchase_unit_price,
            )"
            :key="currency"
            class="text-xl font-bold text-gray-900 dark:text-white"
            >{{ formatUnitPrice(currency, amount)
            }}<span class="inline-block text-sm font-medium">/{{ balanceUnitName }}</span></p
          >
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ stats.reasoning_point_purchase_order_count }} {{ t('payment.admin.orders') }}
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import Icon from '@/components/icons/Icon.vue'
import type { CurrencyAmounts, DashboardStats } from '@/api/payment.types'
import { DEFAULT_PAYMENT_CURRENCY } from '@/utils/payment/currency'

// OrderStatsCards.vue displays payment totals, order counts, and the average purchase
// price of balance units. Currency totals remain separate, empty amount collections render
// a default currency zero, and the balance unit label follows the application's configured
// display name.

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()
// balanceUnitName supplies the configured unit label displayed beside average balance
// purchase prices and contributing order counts.
const { balanceUnitName } = useBalanceDisplay()

defineProps<{
  stats: DashboardStats /* payment dashboard aggregate values */
}>()

// sortedAmounts orders currency amount entries alphabetically for consistent display
// across cards. Empty collections produce a default-currency zero entry so the statistics
// cards retain an amount when no orders exist.
function sortedAmounts(amounts: CurrencyAmounts): [string, number][] {
  const entries = Object.entries(amounts || {}).sort(([left], [right]) => left.localeCompare(right))
  // An empty dashboard has no currency keys, so render a default-currency zero entry to
  // keep its amount cards populated and comparable.
  return entries.length > 0 ? entries : [[DEFAULT_PAYMENT_CURRENCY, 0]]
}

// formatMoney formats a numeric amount as a localized currency string for display.
function formatMoney(currency: string, amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
}

// formatUnitPrice identifies the payment currency for each four-decimal balance purchase
// price. Currency codes distinguish payments that share a displayed monetary symbol.
function formatUnitPrice(currency: string, value: number): string {
  // Unit purchase prices retain four decimal places for fractional balance-unit costs.
  return `${currency} ${value.toFixed(4)}`
}
</script>

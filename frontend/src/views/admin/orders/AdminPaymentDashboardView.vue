<template>
  <AppLayout>
    <template #page-heading-actions>
      <div class="flex items-center justify-end gap-2">
        <DateRangePicker
          v-model:start-date="startDate"
          v-model:end-date="endDate"
          @change="onDateRangeChange"
        />
        <button
          @click="loadDashboard"
          :disabled="loading"
          class="btn btn-secondary shrink-0 btn-icon"
          :title="t('common.refresh')"
        >
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
        </button>
      </div>
    </template>
    <div class="space-y-4">
      <div
        v-if="loading && !stats"
        class="space-y-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-5 gap-4" aria-hidden="true">
          <div v-for="card in 5" :key="card" class="card p-4">
            <div class="flex items-start gap-3">
              <Skeleton :width="36" :height="36" class="shrink-0 rounded-control" />
              <div class="min-w-0 flex-1 space-y-1.5">
                <Skeleton :width="92" :height="12" class="max-w-full" />
                <Skeleton :width="76" :height="24" class="max-w-full" />
                <Skeleton v-if="card <= 2" :width="54" :height="12" />
              </div>
            </div>
          </div>
        </div>
        <DailyRevenueChart :data="[]" :loading="true" />
        <PurchaseDistributionChart :items="[]" loading />
        <div class="grid gap-4 lg:grid-cols-2">
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('payment.admin.paymentDistribution') }}
            </h3>
            <ContentSkeleton :rows="3" />
          </div>
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('payment.admin.topUsers') }}
            </h3>
            <ContentSkeleton :rows="3" />
          </div>
        </div>
      </div>
      <template v-else-if="stats">
        <OrderStatsCards :stats="stats" />
        <DailyRevenueChart :data="stats.daily_series || []" />
        <PurchaseDistributionChart :items="stats.purchase_distribution || []" />
        <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('payment.admin.paymentDistribution') }}
            </h3>
            <div
              v-if="!stats.payment_methods?.length"
              class="flex h-32 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
            >
              {{ t('payment.admin.noData') }}
            </div>
            <div v-else class="space-y-3">
              <div
                v-for="method in stats.payment_methods"
                :key="method.type"
                class="flex items-center justify-between"
              >
                <div class="flex items-center gap-2">
                  <span
                    :class="['inline-block h-3 w-3 rounded-full', methodColor(method.type)]"
                  ></span>
                  <span class="text-sm text-gray-700 dark:text-gray-300">{{
                    t('payment.methods.' + method.type, method.type)
                  }}</span>
                </div>
                <div class="space-y-1 text-right">
                  <span
                    v-for="[currency, amount] in sortedAmounts(method.amount)"
                    :key="currency"
                    class="block text-sm font-medium text-gray-900 dark:text-white"
                  >
                    {{ formatMoney(currency, amount) }}
                  </span>
                  <span class="ml-2 text-xs text-gray-500 dark:text-gray-400"
                    >({{ method.count }})</span
                  >
                </div>
              </div>
            </div>
          </div>
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('payment.admin.topUsers') }}
            </h3>
            <div
              v-if="!hasTopUsers(stats.top_users)"
              class="flex h-32 items-center justify-center text-sm text-gray-500 dark:text-gray-400"
            >
              {{ t('payment.admin.noData') }}
            </div>
            <div v-else class="space-y-2">
              <div
                v-for="[currency, users] in sortedTopUsers(stats.top_users)"
                :key="currency"
                class="space-y-2"
              >
                <p class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ currency }}</p>
                <div
                  v-for="(user, idx) in users"
                  :key="user.user_id"
                  class="flex items-center justify-between rounded-control px-3 py-2 hover:bg-gray-50 dark:hover:bg-dark-700"
                >
                  <div class="flex items-center gap-3">
                    <span
                      :class="[
                        'flex h-6 w-6 items-center justify-center rounded-full text-xs font-bold',
                        rankClass(idx),
                      ]"
                      >{{ idx + 1 }}</span
                    >
                    <span class="text-sm text-gray-700 dark:text-gray-300">{{ user.email }}</span>
                  </div>
                  <span class="text-sm font-medium text-gray-900 dark:text-white">{{
                    formatMoney(currency, user.amount)
                  }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
      <LoadErrorState v-else class="card" :message="dashboardError" @retry="loadDashboard" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { CurrencyAmounts, DashboardStats, TopUserPaymentStats } from '@/api/payment.types'
import LoadErrorState from '@/components/common/LoadErrorState.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Icon from '@/components/icons/Icon.vue'
import OrderStatsCards from '@/components/admin/payment/OrderStatsCards.vue'
import DailyRevenueChart from '@/components/admin/payment/DailyRevenueChart.vue'
import PurchaseDistributionChart from '@/components/admin/payment/PurchaseDistributionChart.vue'

// AdminPaymentDashboardView.vue loads payment analytics for a local calendar date range
// and passes results to revenue, totals, and distribution panels. Currency amounts and
// payer rankings remain grouped by currency, and the initial range includes today and the
// preceding twenty-nine days.

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()
// appStore provides page feedback for completed operations and request failures while
// handlers retain their local view state.
const appStore = useAppStore()

// loading tracks dashboard request progress for loading presentation and clears after the
// request success or error handling finishes.
const loading = ref(true)
const dashboardError = ref('')

// dashboardRequest identifies the latest selected payment analytics range.
let dashboardRequest = 0
// stats holds the latest successful dashboard response; null represents the period before
// any response has populated the page.
const stats = ref<DashboardStats | null>(null)

// formatLocalDate formats local calendar fields as a zero-padded API date string.
function formatLocalDate(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

// getLast30DaysRange returns local date strings covering today and the preceding twenty-
// nine days. Calendar subtraction includes today's date in the thirty-day range sent to
// the dashboard API.
function getLast30DaysRange(): {
  start: string // range start date text
  end: string // range end date text
} {
  const end = new Date()
  const start = new Date()
  // Subtract twenty-nine calendar days because the date range includes today.
  start.setDate(start.getDate() - 29)
  return {
    start: formatLocalDate(start),
    end: formatLocalDate(end),
  }
}

// defaultRange initializes local date inputs once during setup so the first dashboard
// request covers the selected default period.
const defaultRange = getLast30DaysRange()
// startDate stores the inclusive range start sent to the dashboard API and is updated by
// date-picker changes.
const startDate = ref(defaultRange.start)
// endDate stores the range end sent to the dashboard API and is updated together with the
// start date.
const endDate = ref(defaultRange.end)

// methodColor returns the dashboard color class for a payment method code. Known provider
// aliases have dedicated colors, while unrecognized methods use a neutral class to remain
// displayable.
function methodColor(type: string): string {
  const c: Record<string, string> = {
    alipay: 'bg-blue-500',
    wxpay: 'bg-green-500',
    alipay_direct: 'bg-blue-400',
    wxpay_direct: 'bg-green-400',
    stripe: 'bg-purple-500',
  }
  // Unknown or newly configured payment methods lack dedicated palette entries; use the
  // neutral color so their dashboard rows remain visually identifiable.
  return c[type] || 'bg-gray-400'
}

// rankClass selects the badge classes for a zero-based payer ranking position. The first
// three positions receive distinct medal colors, while subsequent positions use the shared
// neutral style.
function rankClass(idx: number): string {
  if (idx === 0) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  if (idx === 1) return 'bg-gray-200 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
  if (idx === 2) return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
  return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'
}

// sortedAmounts sorts separate currency totals alphabetically for consistent dashboard
// presentation.
function sortedAmounts(amounts: CurrencyAmounts): [string, number][] {
  return Object.entries(amounts).sort(([left], [right]) => left.localeCompare(right))
}

// sortedTopUsers sorts currency-specific payer ranking groups by their currency
// identifiers.
function sortedTopUsers(
  usersByCurrency: Record<string, TopUserPaymentStats[]>,
): [string, TopUserPaymentStats[]][] {
  return Object.entries(usersByCurrency).sort(([left], [right]) => left.localeCompare(right))
}

// hasTopUsers reports whether any currency group contains a ranked payer.
function hasTopUsers(usersByCurrency: Record<string, TopUserPaymentStats[]>): boolean {
  return Object.values(usersByCurrency).some((users) => users.length > 0)
}

// formatMoney formats a numeric amount as a localized currency string for display.
function formatMoney(currency: string, amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
}

// loadDashboard reads analytics for the chosen start and end dates. Only the
// latest range request publishes results, errors, and loading cleanup. A first
// failure shows a retry; a refresh failure can retain existing analytics with
// a notification.
async function loadDashboard() {
  // Capture ownership before dispatch so an earlier range cannot end the
  // loading state or replace the results for a newer date selection.
  const request = ++dashboardRequest
  loading.value = true
  dashboardError.value = ''
  try {
    const res = await adminPaymentAPI.getDashboard({
      start_date: startDate.value,
      end_date: endDate.value,
    })
    if (request !== dashboardRequest) return
    stats.value = res.data
  } catch (err: unknown) {
    if (request !== dashboardRequest) return
    dashboardError.value = extractI18nErrorMessage(
      err,
      t,
      'payment.errors',
      t('payment.dashboardUnavailable'),
    )
    if (stats.value) appStore.showError(dashboardError.value)
  } finally {
    if (request === dashboardRequest) loading.value = false
  }
}

// onDateRangeChange adopts the date picker's start and end values before reloading
// analytics. The preset label is presentation metadata; the API query uses the concrete
// dates supplied by the picker.
function onDateRangeChange(range: {
  startDate: string // selected range start date
  endDate: string // selected range end date
  preset: string | null // date picker preset identifier
}) {
  startDate.value = range.startDate
  endDate.value = range.endDate
  void loadDashboard()
}

onMounted(() => loadDashboard())
</script>

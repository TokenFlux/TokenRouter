<template>
  <AppLayout fit-viewport>
    <!-- Keep the main content stretched within the flex-column layout. Narrow screens stack redemption, history, and subscriptions; wide screens reserve the left column for benefits and let history scroll across both rows. -->
    <div
      class="grid gap-4 lg:min-h-0 lg:flex-1 lg:grid-cols-[22.5rem_minmax(0,1fr)] lg:grid-rows-[auto_minmax(0,1fr)]"
    >
      <section data-testid="redeem-panel" class="card p-4 sm:p-6">
        <div class="flex flex-col gap-4">
          <RedeemCelebration
            :sequence="celebration?.sequence ?? 0"
            :title="celebration?.title ?? ''"
            :detail="celebration?.detail ?? ''"
          >
            <dl class="grid grid-cols-2 gap-4">
              <div>
                <dt class="text-xs font-medium text-gray-500 dark:text-dark-400">
                  {{ t('redeem.currentBalance') }}
                </dt>
                <dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
                  {{ formatBalanceAmount(user?.balance, { fractionDigits: 2 }) }}
                </dd>
              </div>
              <div>
                <dt class="text-xs font-medium text-gray-500 dark:text-dark-400">
                  {{ t('redeem.concurrency') }}
                </dt>
                <dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
                  {{ user?.concurrency || 0 }}
                  <span class="text-sm font-normal text-gray-500 dark:text-dark-400">{{
                    t('redeem.requests')
                  }}</span>
                </dd>
              </div>
            </dl>
          </RedeemCelebration>

          <!-- Medium widths place input and submit beside each other; the narrower wide-layout column stacks them again. -->
          <form
            ref="redeemForm"
            class="flex flex-col gap-3 border-t border-gray-100 pt-4 dark:border-dark-700 sm:flex-row lg:flex-col"
            @submit.prevent="handleRedeem"
          >
            <label for="code" class="sr-only">{{ t('redeem.redeemCodeLabel') }}</label>
            <div class="input-icon-wrap min-w-0 flex-1">
              <div class="input-icon">
                <Icon name="gift" size="md" class="text-gray-400 dark:text-dark-500" />
              </div>
              <input
                id="code"
                ref="codeInput"
                v-model="redeemCode"
                type="text"
                required
                autocomplete="off"
                spellcheck="false"
                :placeholder="t('redeem.redeemCodePlaceholder')"
                :disabled="submitting"
                class="input input-has-icon"
              />
            </div>
            <button
              type="submit"
              :disabled="!redeemCode || submitting"
              class="btn btn-primary w-full sm:w-auto sm:px-6 lg:w-full"
            >
              <Icon
                v-if="submitting"
                name="loader"
                size="sm"
                :animate-on-hover="false"
                class="animate-spin"
              />
              {{ submitting ? t('redeem.redeeming') : t('redeem.redeemButton') }}
            </button>
          </form>
        </div>
      </section>

      <!-- Separators divide recent activity while rows share the card padding on narrow screens. -->
      <section
        data-testid="redeem-history"
        class="card flex min-w-0 flex-col overflow-hidden lg:col-start-2 lg:row-span-2 lg:row-start-1 lg:min-h-0"
      >
        <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-6 sm:py-4">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('redeem.recentActivity') }}
          </h2>
        </div>

        <div class="lg:min-h-0 lg:flex-1 lg:overflow-y-auto">
          <!-- Initial loading uses a skeleton; pagination dims existing rows to retain their occupied space while another page loads. -->
          <ContentSkeleton
            v-if="loadingHistory && history.length === 0"
            variant="list"
            :rows="4"
            class="p-6"
          />

          <ul
            v-else-if="history.length > 0"
            :class="[
              'divide-y divide-gray-100 transition-opacity dark:divide-dark-700',
              loadingHistory ? 'opacity-60' : '',
            ]"
          >
            <li
              v-for="item in history"
              :key="item.id"
              class="flex items-center gap-3 px-4 py-3 sm:gap-4 sm:px-6"
            >
              <div
                :class="[
                  'flex h-9 w-9 shrink-0 items-center justify-center rounded-compact',
                  getHistoryTone(item).bg,
                ]"
              >
                <BalanceIcon
                  v-if="isBalanceType(item.type)"
                  size="sm"
                  :class="getHistoryTone(item).text"
                />
                <Icon
                  v-else-if="isSubscriptionType(item.type)"
                  name="badge"
                  size="sm"
                  :class="getHistoryTone(item).text"
                />
                <Icon v-else name="bolt" size="sm" :class="getHistoryTone(item).text" />
              </div>

              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium text-gray-900 dark:text-white">
                  {{ getHistoryItemTitle(item) }}
                </p>
                <p
                  class="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400"
                >
                  <span v-if="item.used_at" class="shrink-0">{{
                    formatDateTime(item.used_at)
                  }}</span>
                  <template v-if="getHistoryItemMeta(item)">
                    <span v-if="item.used_at" aria-hidden="true">·</span>
                    <span
                      :class="['truncate', isAdjustment(item.type) ? '' : 'font-mono']"
                      :title="getHistoryItemMeta(item)"
                    >
                      {{ getHistoryItemMeta(item) }}
                    </span>
                  </template>
                </p>
              </div>

              <p
                :class="[
                  'max-w-40 shrink-0 truncate text-right text-sm font-semibold tabular-nums',
                  getHistoryTone(item).text,
                ]"
              >
                {{ formatHistoryValue(item) }}
              </p>
            </li>
          </ul>

          <div v-else class="empty-state lg:h-full">
            <div
              class="mb-4 flex h-12 w-12 items-center justify-center rounded-surface bg-gray-100 dark:bg-dark-800"
            >
              <Icon name="clock" size="lg" class="text-gray-400 dark:text-dark-500" />
            </div>
            <p class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('redeem.historyWillAppear') }}
            </p>
          </div>
        </div>

        <Pagination
          v-if="historyTotal > HISTORY_PAGE_SIZE"
          class="shrink-0"
          :total="historyTotal"
          :page="historyPage"
          :page-size="HISTORY_PAGE_SIZE"
          :show-page-size-selector="false"
          @update:page="fetchHistory"
        />
      </section>

      <!-- Subscriptions fill the remaining wide-layout space below redemption and follow history in the narrow document layout. -->
      <section
        data-testid="redeem-subscriptions"
        class="card flex min-w-0 flex-col overflow-hidden lg:col-start-1 lg:row-start-2 lg:min-h-0"
      >
        <div
          class="flex items-center justify-between gap-4 border-b border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-6 sm:py-4"
        >
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('subscriptionProgress.title') }}
          </h2>
          <router-link to="/subscriptions" class="shrink-0 text-sm text-link">
            {{ t('subscriptionProgress.viewAll') }}
          </router-link>
        </div>

        <SubscriptionUsageList
          v-if="activeSubscriptions.length > 0"
          class="lg:min-h-0 lg:flex-1 lg:overflow-y-auto"
          :subscriptions="activeSubscriptions"
          item-class="border-b border-gray-100 px-4 py-3 last:border-b-0 dark:border-dark-700 sm:px-6"
        />
        <p
          v-else
          class="flex items-center justify-center px-4 py-8 text-sm text-gray-500 dark:text-dark-400 lg:flex-1"
        >
          {{ t('subscriptionProgress.noSubscriptions') }}
        </p>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { redeemAPI, type RedeemHistoryItem } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'
import AppLayout from '@/components/layout/AppLayout.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import Pagination from '@/components/common/Pagination.vue'
import SubscriptionUsageList from '@/components/common/SubscriptionUsageList.vue'
import Icon from '@/components/icons/Icon.vue'
import RedeemCelebration from '@/components/user/RedeemCelebration.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateTime } from '@/utils/format'

// RedeemView.vue redeems benefit codes and displays account benefits alongside paginated
// redemption history. Successful redemption produces its own sequence-tagged message from
// the returned benefit, while profile, history, and subscription refreshes run afterward and
// can fail independently.
//
// History requests use increasing identities so rapid pagination publishes only the current
// page. Disposal invalidates that identity and suppresses page-owned redemption feedback.
// The form retains its focus intent across disabled controls but restores focus only when
// the document body still owns it, leaving later user navigation intact. Celebration timing
// belongs to its child component rather than the submission flag.

// Account, subscription, and app stores supply current benefits and notification feedback;
// balance formatting uses the configured account unit throughout redemption history.
const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()
const subscriptionStore = useSubscriptionStore()
const { formatBalanceAmount } = useBalanceDisplay()

// user exposes the shared profile used by the balance and concurrency summary beside the
// redemption form.
const user = computed(() => authStore.user)

// Form refs and submitting control redemption input; celebrationSequence identifies
// successful results while disposed suppresses page-owned UI updates after navigation.
const redeemCode = ref('')
const redeemForm = ref<HTMLFormElement | null>(null)
const codeInput = ref<HTMLInputElement | null>(null)
const submitting = ref(false)
const celebration = ref<{
  sequence: number // Unique successful redemption identity.
  title: string // Localized success heading text.
  detail: string // Response-derived granted benefit text.
} | null>(null)
let celebrationSequence = 0
let disposed = false

// HISTORY_PAGE_SIZE bounds each history request; page and total values track server
// pagination while the history card owns its scroll area.
const HISTORY_PAGE_SIZE = 10
const history = ref<RedeemHistoryItem[]>([])
const historyPage = ref(1)
const historyTotal = ref(0)
const loadingHistory = ref(false)
// historyRequestId identifies the latest history request, preventing earlier page responses
// from replacing a newer navigation selection.
let historyRequestId = 0

// The subscription store supplies active benefits; redeemErrorMap converts known redemption
// rejection reasons into page-specific localized error messages.
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)
const redeemErrorMap = computed<Record<string, string>>(() => ({
  REDEEM_PAYMENT_REQUIRED: t('redeem.paymentRequired'),
  REDEEM_CODE_EXPIRED: t('redeem.codeExpired'),
  REDEEM_CODE_MAX_USED: t('redeem.codeMaxUsed'),
  REDEEM_CODE_ALREADY_USED: t('redeem.codeAlreadyUsed'),
  REDEEM_CODE_USED: t('redeem.codeMaxUsed'),
}))

// isBalanceType identifies history entries expressed in the configured balance unit.
const isBalanceType = (type: string) => {
  return (
    type === 'balance' ||
    type === 'admin_balance' ||
    type === 'plugin_balance' ||
    type === 'affiliate_balance'
  )
}

// isSubscriptionType identifies history entries that represent a granted subscription.
const isSubscriptionType = (type: string) => {
  return type === 'subscription'
}

// isAdjustment identifies administrator and plugin entries whose metadata contains a
// user-visible adjustment reason instead of a redeemable credential.
const isAdjustment = (type: string) => {
  return type === 'admin_balance' || type === 'admin_concurrency' || type === 'plugin_balance'
}

// getHistoryTone selects entry colors using benefit type and adjustment direction. Balance,
// subscription, and concurrency entries use distinct palettes, with negative adjustments
// differentiated from granted benefits.
const getHistoryTone = (item: RedeemHistoryItem) => {
  if (isBalanceType(item.type)) {
    return item.value >= 0
      ? {
          bg: 'bg-emerald-100 dark:bg-emerald-900/30',
          text: 'text-emerald-600 dark:text-emerald-400',
        }
      : { bg: 'bg-red-100 dark:bg-red-900/30', text: 'text-red-600 dark:text-red-400' }
  }
  if (isSubscriptionType(item.type)) {
    return {
      bg: 'bg-purple-100 dark:bg-purple-900/30',
      text: 'text-purple-600 dark:text-purple-400',
    }
  }
  return item.value >= 0
    ? { bg: 'bg-blue-100 dark:bg-blue-900/30', text: 'text-blue-600 dark:text-blue-400' }
    : { bg: 'bg-orange-100 dark:bg-orange-900/30', text: 'text-orange-600 dark:text-orange-400' }
}

// getHistoryItemTitle returns the localized history action for its benefit type and
// direction. Unrecognized types use the shared unknown label, while administrator
// and plugin adjustments distinguish additions from deductions.
const getHistoryItemTitle = (item: RedeemHistoryItem) => {
  if (item.type === 'balance') {
    return t('redeem.balanceAddedRedeem')
  } else if (item.type === 'plugin_balance') {
    return item.value >= 0 ? t('redeem.balanceAddedPlugin') : t('redeem.balanceDeductedPlugin')
  } else if (item.type === 'admin_balance') {
    return item.value >= 0 ? t('redeem.balanceAddedAdmin') : t('redeem.balanceDeductedAdmin')
  } else if (item.type === 'concurrency') {
    return t('redeem.concurrencyAddedRedeem')
  } else if (item.type === 'admin_concurrency') {
    return item.value >= 0 ? t('redeem.concurrencyAddedAdmin') : t('redeem.concurrencyReducedAdmin')
  } else if (item.type === 'subscription') {
    return t('redeem.subscriptionAssigned')
  } else if (item.type === 'affiliate_balance') {
    return t('redeem.affiliateBalance')
  }
  return t('common.unknown')
}

// getHistoryItemMeta returns visible notes for administrator and plugin adjustments.
// Plugin notes include the plugin ID and operation reason. Other entries show their
// redemption code, and missing adjustment notes leave the metadata empty.
const getHistoryItemMeta = (item: RedeemHistoryItem) => {
  if (isAdjustment(item.type)) {
    return item.notes || ''
  }
  return item.code
}

// formatHistoryValue formats signed balance changes, subscription names, or concurrency
// amounts by entry type. Subscription names fall back to a plan ID and then the generic
// assigned label when metadata is incomplete.
const formatHistoryValue = (item: RedeemHistoryItem) => {
  if (isBalanceType(item.type)) {
    return formatSignedBalanceAmount(item.value, 2)
  } else if (isSubscriptionType(item.type)) {
    return (
      item.plan?.name || (item.plan_id ? `Plan #${item.plan_id}` : t('redeem.subscriptionAssigned'))
    )
  } else {
    const sign = item.value >= 0 ? '+' : ''
    return `${sign}${item.value} ${t('redeem.requests')}`
  }
}

// formatSignedBalanceAmount formats an absolute amount with an explicit positive or negative
// sign.
const formatSignedBalanceAmount = (value: number, fractionDigits: number) => {
  const sign = value >= 0 ? '+' : '-'
  return `${sign}${formatBalanceAmount(Math.abs(value), { fractionDigits })}`
}

// fetchHistory requests the selected page and publishes it only while its request ID remains
// current and the page is mounted. Superseded successful responses resolve true after being
// ignored, while request failures log the cause and return false. Redemption refresh uses
// that boolean to report a partial refresh failure. Only the current request clears the
// loading flag, preventing an older completion from hiding a newer page load.
const fetchHistory = async (page = historyPage.value) => {
  const requestId = ++historyRequestId
  loadingHistory.value = true
  try {
    const result = await redeemAPI.getHistory(page, HISTORY_PAGE_SIZE)
    // Ignore successful responses owned by an older page or request; returning
    // true keeps discarded results from reporting a redemption refresh failure.
    if (disposed || requestId !== historyRequestId) return true
    history.value = result.items
    historyTotal.value = result.total
    historyPage.value = page
    return true
  } catch (error) {
    // Convert history errors to a boolean so redemption can report its partial
    // refresh failure after the successful redemption response has already arrived.
    console.error('Failed to fetch history:', error)
    return false
  } finally {
    // A newer history request owns the shared spinner, so older completions
    // leave it active until that request finishes.
    if (requestId === historyRequestId) {
      loadingHistory.value = false
    }
  }
}

// getCelebrationDetail formats the benefit granted by the successful redemption response. It
// uses that response amount directly, so subsequent profile refresh timing or another
// balance change cannot alter the success description.
const getCelebrationDetail = (result: RedeemHistoryItem) => {
  if (result.type === 'balance') {
    return t('redeem.balanceReceived', { amount: formatSignedBalanceAmount(result.value, 2) })
  }
  if (result.type === 'concurrency') {
    const count = `${result.value >= 0 ? '+' : ''}${result.value}`
    return t('redeem.concurrencyReceived', { count })
  }
  return t('redeem.subscriptionReceived')
}

// handleRedeem validates and submits the entered code, then separates granted benefits from
// subsequent account refresh work. A successful response clears input and publishes
// sequence-tagged feedback before profile, history, and optional subscription refreshes
// settle independently. Refresh failures produce a warning while retaining redemption
// success. Disposal suppresses page notifications and feedback, and focus returns only if
// the form previously owned focus and disabled controls left it on the document body.
const handleRedeem = async () => {
  if (submitting.value) return
  if (!redeemCode.value.trim()) {
    appStore.showError(t('redeem.pleaseEnterCode'))
    return
  }

  const restoreInputFocus = redeemForm.value?.contains(document.activeElement) ?? false
  submitting.value = true
  celebration.value = null

  try {
    let result: RedeemHistoryItem
    try {
      result = await redeemAPI.redeem(redeemCode.value.trim())
    } catch (error) {
      if (!disposed) {
        appStore.showError(
          extractApiErrorMessage(error, t('redeem.failedToRedeem'), redeemErrorMap.value),
        )
      }
      return
    }
    if (disposed) return

    redeemCode.value = ''
    celebration.value = {
      sequence: ++celebrationSequence,
      title: t('redeem.codeRedeemSuccess'),
      detail: getCelebrationDetail(result),
    }

    // The benefit is already granted. Refresh the first history page, profile, and
    // applicable subscription summary independently so one failure does not prevent
    // other summaries updating or replace success with a redemption error.
    const results = await Promise.allSettled([
      authStore.refreshUser(),
      fetchHistory(1),
      result.type === 'subscription'
        ? subscriptionStore.fetchActiveSubscriptions(true)
        : Promise.resolve(),
    ])
    const historyResult = results[1]
    const refreshFailed =
      results.some((entry) => entry.status === 'rejected') ||
      (historyResult.status === 'fulfilled' && historyResult.value === false)
    if (!disposed && refreshFailed) {
      appStore.showWarning(t('redeem.dataRefreshFailed'))
    }
  } finally {
    // Release submission after request work settles; celebration visibility belongs
    // to its separate component timer and animation events rather than this guard.
    submitting.value = false
    await nextTick()
    // Disabled form controls can leave focus on body. Restore the input only in
    // that case so a user who moved elsewhere retains their chosen focus.
    if (!disposed && restoreInputFocus && document.activeElement === document.body) {
      codeInput.value?.focus({ preventScroll: true })
    }
  }
}

// Mark the page disposed and invalidate history ownership before late responses arrive so
// old page data and success feedback cannot publish after navigation.
onBeforeUnmount(() => {
  disposed = true
  historyRequestId++
})

onMounted(() => {
  fetchHistory()
  subscriptionStore.fetchActiveSubscriptions().catch((error) => {
    console.error('Failed to load subscriptions on redeem page:', error)
  })
})
</script>

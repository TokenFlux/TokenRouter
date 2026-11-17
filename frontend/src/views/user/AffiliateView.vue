<template>
  <AppLayout>
    <div class="space-y-4">
      <div
        v-if="loading"
        class="space-y-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" aria-hidden="true">
          <div v-for="card in 4" :key="card" class="card p-4">
            <Skeleton :width="110" :height="14" />
            <Skeleton :width="72" :height="28" class="mt-2" />
            <Skeleton v-if="card === 1" :width="120" :height="12" class="mt-1" />
          </div>
        </div>
        <div class="card p-6" aria-hidden="true">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{
            t('affiliate.title')
          }}</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{
            t('affiliate.description')
          }}</p>
          <div class="mt-5 grid min-w-0 gap-4 md:grid-cols-2">
            <div
              v-for="title in [t('affiliate.yourCode'), t('affiliate.inviteLink')]"
              :key="title"
              class="min-w-0 space-y-2"
            >
              <p class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ title }}</p>
              <div
                class="flex min-w-0 flex-col gap-2 rounded-surface border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900 sm:flex-row sm:items-center"
              >
                <Skeleton :width="120" :height="16" class="min-w-0 sm:mr-auto" />
                <Skeleton :width="0" :height="28" class="w-full shrink-0 sm:w-24" />
              </div>
            </div>
          </div>
          <div
            class="mt-5 space-y-2 rounded-surface border border-gray-200 p-4 dark:border-dark-700"
          >
            <Skeleton :width="84" :height="16" />
            <Skeleton
              v-for="line in 3"
              :key="line"
              :width="line === 3 ? '54%' : '82%'"
              :height="14"
            />
          </div>
        </div>
        <div class="card p-6" aria-hidden="true">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div class="min-w-0 flex-1 space-y-2">
              <Skeleton :width="142" :height="18" />
              <Skeleton :width="278" :height="14" class="max-w-full" />
            </div>
            <Skeleton :width="0" :height="36" class="w-full shrink-0 sm:w-28" />
          </div>
        </div>
      </div>

      <template v-else-if="detail">
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div class="card p-4">
            <p class="flex items-center gap-1.5 text-sm text-gray-500 dark:text-dark-400">
              <Icon name="gift" size="sm" class="text-primary-500" />
              {{ t('affiliate.stats.rebateRate') }}
            </p>
            <p class="mt-2 text-2xl font-semibold text-primary-600 dark:text-primary-400">
              {{ formattedRebateRate }}<span class="ml-0.5 text-base font-medium">%</span>
            </p>
            <p class="mt-1 text-xs text-gray-400 dark:text-dark-500">
              {{ t('affiliate.stats.rebateRateHint') }}
            </p>
          </div>
          <div class="card p-4">
            <p class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('affiliate.stats.invitedUsers') }}
            </p>
            <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">
              {{ formatCount(detail.aff_count) }}
            </p>
          </div>
          <div class="card p-4">
            <p class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('affiliate.stats.availableQuota') }}
            </p>
            <p class="mt-2 text-2xl font-semibold text-emerald-600 dark:text-emerald-400">
              {{ formatAffiliateAmount(detail.aff_quota) }}
            </p>
          </div>
          <div class="card p-4">
            <p class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('affiliate.stats.totalQuota') }}
            </p>
            <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">
              {{ formatAffiliateAmount(detail.aff_history_quota) }}
            </p>
            <p
              v-if="detail.aff_frozen_quota > 0"
              class="mt-1 text-xs text-amber-600 dark:text-amber-400"
            >
              {{ t('affiliate.stats.frozenQuota') }}:
              {{ formatAffiliateAmount(detail.aff_frozen_quota) }}
            </p>
          </div>
        </div>

        <div class="card p-6">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('affiliate.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
            {{ t('affiliate.description') }}
          </p>

          <!-- Allow each flex item to shrink before text truncation takes over, preventing intrinsic minimum widths from expanding the mobile grid. -->
          <div class="mt-5 grid min-w-0 gap-4 md:grid-cols-2">
            <div class="min-w-0 space-y-2">
              <p class="text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('affiliate.yourCode') }}
              </p>
              <div
                data-testid="affiliate-code-row"
                class="flex min-w-0 flex-col items-stretch gap-2 rounded-surface border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900 sm:flex-row sm:items-center"
              >
                <code
                  data-testid="affiliate-code"
                  class="min-w-0 break-all text-sm font-semibold text-gray-900 dark:text-white sm:flex-1 sm:truncate"
                  >{{ detail.aff_code }}</code
                >
                <button
                  data-testid="affiliate-copy-code"
                  class="btn btn-secondary btn-sm w-full sm:w-auto sm:shrink-0"
                  @click="copyCode"
                >
                  <Icon name="copy" size="sm" />
                  <span>{{ t('affiliate.copyCode') }}</span>
                </button>
              </div>
            </div>

            <div class="min-w-0 space-y-2">
              <p class="text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('affiliate.inviteLink') }}
              </p>
              <div
                data-testid="affiliate-link-row"
                class="flex min-w-0 flex-col items-stretch gap-2 rounded-surface border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900 sm:flex-row sm:items-center"
              >
                <code
                  data-testid="affiliate-link"
                  class="min-w-0 break-all text-sm text-gray-700 dark:text-gray-300 sm:flex-1 sm:truncate"
                  >{{ inviteLink }}</code
                >
                <button
                  data-testid="affiliate-copy-link"
                  class="btn btn-secondary btn-sm w-full sm:w-auto sm:shrink-0"
                  @click="copyInviteLink"
                >
                  <Icon name="copy" size="sm" />
                  <span>{{ t('affiliate.copyLink') }}</span>
                </button>
              </div>
            </div>
          </div>

          <div
            class="mt-5 rounded-surface border border-primary-200 bg-primary-50 p-4 dark:border-primary-900/40 dark:bg-primary-900/20"
          >
            <p class="text-sm font-medium text-primary-800 dark:text-primary-200">
              {{ t('affiliate.tips.title') }}
            </p>
            <ul class="mt-2 space-y-1 text-sm text-primary-700 dark:text-primary-300">
              <li>1. {{ t('affiliate.tips.line1') }}</li>
              <li>2. {{ t('affiliate.tips.line2', { rate: `${formattedRebateRate}%` }) }}</li>
              <li>3. {{ t('affiliate.tips.line3') }}</li>
              <li v-if="detail.aff_frozen_quota > 0">4. {{ t('affiliate.tips.line4') }}</li>
            </ul>
          </div>
        </div>

        <div class="card p-6">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('affiliate.transfer.title') }}
              </h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
                {{ t('affiliate.transfer.description') }}
              </p>
            </div>
            <button
              class="btn btn-primary"
              :disabled="transferring || detail.aff_quota <= 0"
              @click="transferQuota"
            >
              <Icon
                v-if="transferring"
                name="refresh"
                size="sm"
                class="animate-spin"
                :animate-on-hover="false"
              />
              <Icon v-else name="swap" size="sm" />
              <span>{{
                transferring ? t('affiliate.transfer.transferring') : t('affiliate.transfer.button')
              }}</span>
            </button>
          </div>
          <p v-if="detail.aff_quota <= 0" class="mt-3 text-sm text-amber-600 dark:text-amber-400">
            {{ t('affiliate.transfer.empty') }}
          </p>
        </div>

        <div class="card p-6">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('affiliate.invitees.title') }}
          </h3>
          <div
            v-if="detail.invitees.length === 0"
            class="mt-4 rounded-surface border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400"
          >
            {{ t('affiliate.invitees.empty') }}
          </div>
          <div v-else class="mt-4 overflow-x-auto">
            <table class="w-full min-w-[560px] text-left text-sm">
              <thead>
                <tr
                  class="border-b border-gray-200 text-gray-500 dark:border-dark-700 dark:text-dark-400"
                >
                  <th class="px-3 py-2 font-medium">{{ t('affiliate.invitees.columns.email') }}</th>
                  <th class="px-3 py-2 font-medium">
                    {{ t('affiliate.invitees.columns.username') }}
                  </th>
                  <th class="px-3 py-2 font-medium text-right">
                    {{ t('affiliate.invitees.columns.rebate', { unitName: balanceUnitName }) }}
                  </th>
                  <th class="px-3 py-2 font-medium">
                    {{ t('affiliate.invitees.columns.joinedAt') }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="item in detail.invitees"
                  :key="item.user_id"
                  class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
                >
                  <td class="px-3 py-3 text-gray-900 dark:text-white">{{ item.email || '-' }}</td>
                  <td class="px-3 py-3 text-gray-700 dark:text-gray-300">
                    {{ item.username || '-' }}
                  </td>
                  <td
                    class="px-3 py-3 text-right font-medium text-emerald-600 dark:text-emerald-400"
                  >
                    {{ formatAffiliateAmount(item.total_rebate) }}
                  </td>
                  <td class="px-3 py-3 text-gray-700 dark:text-gray-300">
                    {{ formatDateTime(item.created_at) || '-' }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { getLocale } from '@/i18n'
import Skeleton from '@/components/common/Skeleton.vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import userAPI from '@/api/user'
import type { UserAffiliateDetail } from '@/api/user.types'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useClipboard } from '@/composables/useClipboard'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'

// AffiliateView.vue shows affiliate codes, referral activity, and quota available for
// transfer into account balance. Registration links encode the current code, while money
// amounts use the same configured balance unit displayed elsewhere in the account.
//
// Transfers report the amount returned by the mutation before refreshing both affiliate
// detail and the auth profile. Those refreshes can fail after funds have moved, so their
// outcome is separated from the transfer result. A local guard prevents a second submission
// during this sequence, and ordinary detail loading retains the last successful response
// when a request fails.

// Page stores, clipboard support, and configured balance formatting provide affiliate
// summaries, transfer feedback, and copy actions for the current account.
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { copyToClipboard } = useClipboard()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()

// loading controls the initial summary placeholder, transferring prevents duplicate
// transfers, and detail retains the latest successfully fetched affiliate record.
const loading = ref(true)
const transferring = ref(false)
const detail = ref<UserAffiliateDetail | null>(null)

// inviteLink encodes the account affiliate code into registration navigation, using the
// browser origin when available and a relative link outside the browser.
const inviteLink = computed(() => {
  if (!detail.value) return ''
  if (typeof window === 'undefined')
    return `/register?aff=${encodeURIComponent(detail.value.aff_code)}`
  return `${window.location.origin}/register?aff=${encodeURIComponent(detail.value.aff_code)}`
})

// formattedRebateRate rounds the returned rebate percentage to two decimal places and
// removes trailing fractional zeros for its displayed label.
const formattedRebateRate = computed(() => {
  const v = detail.value?.effective_rebate_rate_percent ?? 0
  // Round the displayed percentage, leaving the server's rebate rate intact.
  const roundedValue = Math.round(v * 100) / 100
  return Number.isInteger(roundedValue) ? String(roundedValue) : roundedValue.toString()
})

// formatCount displays referral counts using the current application locale.
function formatCount(value: number): string {
  return value.toLocaleString(getLocale())
}

// formatAffiliateAmount delegates affiliate amounts to the configured account balance
// formatter, matching the units used when quota is transferred into the balance.
function formatAffiliateAmount(value: number | null | undefined): string {
  return formatBalanceAmount(value)
}

// loadAffiliateDetail loads the current affiliate record and reports failures through the
// app store. Silent refreshes retain the existing loading presentation, while ordinary loads
// show the initial summary placeholder.
async function loadAffiliateDetail(silent = false): Promise<void> {
  if (!silent) {
    loading.value = true
  }
  try {
    detail.value = await userAPI.getAffiliateDetail()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('affiliate.loadFailed')))
  } finally {
    if (!silent) {
      loading.value = false
    }
  }
}

// copyCode copies a present affiliate code with a localized success message. Missing detail
// or an empty code leaves the clipboard untouched, and the shared clipboard helper owns
// failure feedback.
async function copyCode(): Promise<void> {
  if (!detail.value?.aff_code) return
  await copyToClipboard(detail.value.aff_code, t('affiliate.codeCopied'))
}

// copyInviteLink copies the generated registration link with localized success feedback. An
// empty link prevents the operation, while the shared clipboard helper handles browser
// clipboard failures for the page.
async function copyInviteLink(): Promise<void> {
  if (!inviteLink.value) return
  await copyToClipboard(inviteLink.value, t('affiliate.linkCopied'))
}

// transferQuota moves available affiliate quota into account balance and prevents concurrent
// transfer submissions. After success it reports the transferred amount from the API
// response and refreshes affiliate and account summaries together. Affiliate refresh handles
// its own error, while account-refresh failure is suppressed because transfer persistence
// has already succeeded. The transfer guard resets after those refreshes settle, so another
// attempt uses the latest available detail.
async function transferQuota(): Promise<void> {
  if (!detail.value || detail.value.aff_quota <= 0 || transferring.value) return
  transferring.value = true
  try {
    const resp = await userAPI.transferAffiliateQuota()
    appStore.showSuccess(
      t('affiliate.transfer.success', { amount: formatAffiliateAmount(resp.transferred_quota) }),
    )
    // Transfer persistence has already completed, so refresh failures must not relabel that
    // result; affiliate refresh reports its own error and account refresh resolves quietly.
    await Promise.all([loadAffiliateDetail(true), authStore.refreshUser().catch(() => undefined)])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('affiliate.transferFailed')))
  } finally {
    transferring.value = false
  }
}

onMounted(() => {
  void loadAffiliateDetail()
})
</script>

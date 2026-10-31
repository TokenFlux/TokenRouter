<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.balanceHistoryTitle')"
    width="wide"
    :close-on-click-outside="true"
    @close="$emit('close')"
  >
    <div v-if="user" class="space-y-4">
      <!-- User header: two-row layout with full user info -->
      <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-700">
        <!-- Row 1: avatar + email/username/created_at (left) + current balance (right) -->
        <div class="flex items-center gap-3">
          <UserAvatar
            :avatar-url="user.avatar_url || ''"
            :user-id="user.id"
            :alt="user.email"
            size-class="h-10 w-10 flex-shrink-0"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <p class="truncate font-medium text-gray-900 dark:text-white">{{ user.email }}</p>
              <span
                v-if="user.deleted_at"
                class="flex-shrink-0 inline-flex items-center rounded-compact px-1 py-px text-xs font-medium leading-tight bg-rose-100 text-rose-600 ring-1 ring-inset ring-rose-200 dark:bg-rose-500/20 dark:text-rose-400 dark:ring-rose-500/30"
              >
                {{ t('admin.usage.userDeletedBadge') }}
              </span>
              <span
                v-if="user.username"
                class="flex-shrink-0 rounded-compact bg-primary-50 px-1.5 py-0.5 text-xs text-primary-600 dark:bg-primary-900/20 dark:text-primary-400"
              >
                {{ user.username }}
              </span>
            </div>
            <p class="text-xs text-gray-400 dark:text-dark-500">
              {{ t('admin.users.createdAt') }}: {{ formatDateTime(user.created_at) }}
            </p>
          </div>
          <!-- Current balance: prominent display on the right -->
          <div class="flex-shrink-0 text-right">
            <p class="text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.users.currentBalance') }}
            </p>
            <p class="text-xl font-bold text-gray-900 dark:text-white">
              {{ formatBalanceAmount(user.balance || 0) }}
            </p>
          </div>
        </div>
        <!-- Row 2: notes + total recharged -->
        <div
          class="mt-2.5 flex items-center justify-between border-t border-gray-200/60 pt-2.5 dark:border-dark-600/60"
        >
          <p
            class="min-w-0 flex-1 truncate text-xs text-gray-500 dark:text-dark-400"
            :title="user.notes || ''"
          >
            <template v-if="user.notes">{{ t('admin.users.notes') }}: {{ user.notes }}</template>
            <template v-else>&nbsp;</template>
          </p>
          <p class="ml-4 flex-shrink-0 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.users.totalRecharged') }}:
            <span class="font-semibold text-emerald-600 dark:text-emerald-400">{{
              formatBalanceAmount(totalRecharged)
            }}</span>
          </p>
        </div>
      </div>

      <!-- Type filter + Action buttons -->
      <div class="flex items-center gap-2">
        <Select v-model="typeFilter" :options="typeOptions" class="w-56" @change="loadHistory(1)" />
        <!-- Deposit button - matches menu style -->
        <button
          v-if="!hideActions"
          @click="emit('deposit')"
          class="flex items-center gap-2 rounded-control border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700"
        >
          <Icon name="plus" size="sm" class="text-emerald-500" :stroke-width="2" />
          {{ t('admin.users.deposit') }}
        </button>
        <!-- Withdraw button - matches menu style -->
        <button
          v-if="!hideActions"
          @click="emit('withdraw')"
          class="flex items-center gap-2 rounded-control border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700"
        >
          <Icon name="minus" size="sm" class="h-4 w-4 text-amber-500" />
          {{ t('admin.users.withdraw') }}
        </button>
      </div>

      <!-- Loading -->
      <ContentSkeleton v-if="loading" variant="list" :rows="3" class="py-4" />

      <!-- Empty state -->
      <div v-else-if="history.length === 0" class="py-8 text-center">
        <p class="text-sm text-gray-500">{{ t('admin.users.noBalanceHistory') }}</p>
      </div>

      <!-- History list -->
      <div v-else class="max-h-[28rem] space-y-3 overflow-y-auto">
        <div
          v-for="item in history"
          :key="item.id"
          class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="flex items-start justify-between">
            <!-- Left: type icon + description -->
            <div class="flex items-start gap-3">
              <div
                :class="[
                  'flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-control',
                  getIconBg(item),
                ]"
              >
                <BalanceIcon
                  v-if="isBalanceType(item.type)"
                  size="sm"
                  :class="getIconColor(item)"
                />
                <Icon v-else :name="getIconName(item)" size="sm" :class="getIconColor(item)" />
              </div>
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ getItemTitle(item) }}
                </p>
                <!-- Long notes show a shortened preview while the title retains the complete adjustment reason. -->
                <p
                  v-if="item.notes"
                  class="mt-0.5 text-xs text-gray-500 dark:text-dark-400"
                  :title="item.notes"
                >
                  {{ item.notes.length > 60 ? item.notes.substring(0, 55) + '...' : item.notes }}
                </p>
                <p class="mt-0.5 text-xs text-gray-400 dark:text-dark-500">
                  {{ formatDateTime(item.used_at || item.created_at) }}
                </p>
              </div>
            </div>
            <!-- Right: value -->
            <div class="text-right">
              <p :class="['text-sm font-semibold', getValueColor(item)]">
                {{ formatValue(item) }}
              </p>
              <p v-if="isAdminType(item.type)" class="text-xs text-gray-400 dark:text-dark-500">
                {{ t('redeem.adminAdjustment') }}
              </p>
              <p
                v-else-if="item.type === 'plugin_balance'"
                class="text-xs text-gray-400 dark:text-dark-500"
              >
                {{ t('redeem.pluginAdjustment') }}
              </p>
              <!-- Show eight code characters to keep history rows compact. -->
              <p v-else class="font-mono text-xs text-gray-400 dark:text-dark-500">
                {{ item.code.slice(0, 8) }}...
              </p>
            </div>
          </div>
        </div>
      </div>

      <!-- Pagination -->
      <div v-if="totalPages > 1" class="flex items-center justify-center gap-2 pt-2">
        <button
          :disabled="currentPage <= 1"
          class="btn btn-secondary px-3 py-1 text-sm"
          @click="loadHistory(currentPage - 1)"
        >
          {{ t('pagination.previous') }}
        </button>
        <span class="text-sm text-gray-500 dark:text-dark-400">
          {{ currentPage }} / {{ totalPages }}
        </span>
        <button
          :disabled="currentPage >= totalPages"
          class="btn btn-secondary px-3 py-1 text-sm"
          @click="loadHistory(currentPage + 1)"
        >
          {{ t('pagination.next') }}
        </button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI, type BalanceHistoryItem } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser } from '@/api/admin/users.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

// UserBalanceHistoryModal.vue displays paginated balance, concurrency, and subscription
// history for a selected user. A type filter changes the server query, while local
// helpers choose labels, icons, and formatted values for the returned entries. Deposit
// and withdrawal events let the parent open the appropriate balance editor.
//
// History type and value sign jointly determine the monetary or concurrency colors and
// the source-specific action label. Subscription entries use their own badge and duration
// representation. Monetary values retain an explicit plus or minus prefix before the
// formatted magnitude, including plugin credits and debits. The
// displayed recharge total comes from the history response rather than a sum of the
// visible page, and a parent-supplied refresh key reloads the first page after an edit
// made above the dialog.

// props and emit connect the history subject, optional action visibility, the parent's
// edit-refresh signal, and parent-owned close, deposit, and withdrawal flows displayed by
// the modal.
const props = withDefaults(
  defineProps<{
    show: boolean // Parent-controlled dialog visibility.
    // user supplies the selected account; null leaves identity-dependent operations waiting
    // until the parent chooses an account.
    user: AdminUser | null
    // hideActions suppresses both deposit and withdrawal controls while leaving the
    // history query and table available.
    hideActions?: boolean
    // refreshKey reports balance edits completed above this dialog; each new value
    // reloads the first record page while the dialog stays open.
    refreshKey?: number
  }>(),
  {
    refreshKey: 0,
  },
)
const emit = defineEmits(['close', 'deposit', 'withdraw'])
// The translation and balance-display services provide history labels and monetary text
// in the configured balance unit used by this modal.
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// history, loading, currentPage, and total hold the displayed server page and its
// pending state; totalRecharged separately displays the server's aggregate recharge
// amount.
const history = ref<BalanceHistoryItem[]>([])
const loading = ref(false)
const currentPage = ref(1)
const total = ref(0)
const totalRecharged = ref(0)
// pageSize fixes the history request size, while typeFilter supplies an optional
// server-side category for the next history page load.
const pageSize = 15
const typeFilter = ref('')
// historyGeneration numbers history requests so a late response from an earlier page,
// filter, or refresh cannot replace newer results.
let historyGeneration = 0

// totalPages rounds the item count into history pages and keeps at least one page
// available when the server returns no records.
const totalPages = computed(() => Math.ceil(total.value / pageSize) || 1)

// typeOptions supplies the server history categories and an empty choice that removes
// category filtering from subsequent history requests.
const typeOptions = computed(() => [
  {
    value: '', // Stored option value.
    label: t('admin.users.allTypes'), // Displayed option label.
  },
  {
    value: 'balance', // Stored option value.
    label: t('admin.users.typeBalance'), // Displayed option label.
  },
  {
    value: 'affiliate_balance', // Stored option value.
    label: t('admin.users.typeAffiliateBalance'), // Displayed option label.
  },
  {
    value: 'admin_balance', // Stored option value.
    label: t('admin.users.typeAdminBalance'), // Displayed option label.
  },
  {
    value: 'plugin_balance', // Stored option value.
    label: t('admin.users.typePluginBalance'), // Displayed option label.
  },
  {
    value: 'concurrency', // Stored option value.
    label: t('admin.users.typeConcurrency'), // Displayed option label.
  },
  {
    value: 'admin_concurrency', // Stored option value.
    label: t('admin.users.typeAdminConcurrency'), // Displayed option label.
  },
  {
    value: 'subscription', // Stored option value.
    label: t('admin.users.typeSubscription'), // Displayed option label.
  },
])

// The dialog displays one account's history at a time. Opening, or switching accounts
// while it stays open, clears the previous type filter and loads the first page; closing
// or losing the account discards in-flight responses and the pending state so nothing
// commits for a hidden or replaced account.
watch([() => props.show, () => props.user?.id], ([open, userId], [wasOpen, previousUserId]) => {
  const accountChanged = userId !== previousUserId
  // A closing dialog releases its account instead of switching to another one, so the
  // departing panel keeps its last rows while it fades out.
  const closing = wasOpen && !open
  // Rows, totals, and the page belong to the previous account. Keeping them while the
  // replacement request runs would show its header over the old ledger, and a failed
  // request would leave that ledger visible under the new account.
  if (accountChanged && !closing) {
    history.value = []
    total.value = 0
    totalRecharged.value = 0
    currentPage.value = 1
  }
  if (!open || !userId) {
    historyGeneration += 1
    loading.value = false
    return
  }
  if (!wasOpen || accountChanged) {
    typeFilter.value = ''
    loadHistory(1)
  }
})

// A new refresh key follows an adjustment made in the editor above this dialog. The first
// page reloads so the new entry and updated totals appear under the open dialog, while a
// key change delivered while the dialog is closed starts no request.
watch(
  () => props.refreshKey,
  () => {
    if (props.show) loadHistory(1)
  },
)

// loadHistory requests one page for the selected user and optional type filter. Missing
// items and totals become empty or zero values, while request errors are logged and
// retain the previously displayed history.
//
// Each request takes the next generation number, and only the newest generation may
// commit items, totals, and the loading flag. A page click, filter change, reopening for
// another user, or an edit refresh can therefore overlap an earlier request without the
// late response replacing fresher history.
const loadHistory = async (page: number) => {
  if (!props.user) return
  const generation = ++historyGeneration
  loading.value = true
  currentPage.value = page
  try {
    const res = await adminAPI.users.getUserBalanceHistory(
      props.user.id,
      page,
      pageSize,
      typeFilter.value || undefined,
    )
    if (generation !== historyGeneration) return
    history.value = res.items || []
    total.value = res.total || 0
    totalRecharged.value = res.total_recharged || 0
  } catch (error) {
    // History failures retain the last response and recharge total while recording the
    // error; pending feedback still ends when the newest request settles.
    console.error('Failed to load balance history:', error)
  } finally {
    if (generation === historyGeneration) loading.value = false
  }
}

// isAdminType identifies administrator-issued balance or concurrency history entries
// for their labels.
const isAdminType = (type: string) => type === 'admin_balance' || type === 'admin_concurrency'

// isBalanceType identifies monetary history entries including affiliate transfers and
// administrator or plugin adjustments.
const isBalanceType = (type: string) =>
  type === 'balance' ||
  type === 'admin_balance' ||
  type === 'plugin_balance' ||
  type === 'affiliate_balance'

// isSubscriptionType identifies subscription assignment entries for their plan-duration
// display.
const isSubscriptionType = (type: string) => type === 'subscription'

// getIconName selects the subscription badge or the concurrency bolt icon. Monetary
// entries use their separate balance rendering path in the template, which determines
// when this helper is called.
const getIconName = (item: BalanceHistoryItem) => {
  if (isSubscriptionType(item.type)) return 'badge'
  return 'bolt'
}

// getIconBg combines history type and amount sign to choose the icon background
// classes. Balance entries use green for nonnegative values and red for deductions;
// subscription entries use purple regardless of sign. Remaining concurrency entries use
// blue or orange according to the amount. The returned classes contain both light and
// dark variants so the same classification remains visible when the application theme
// changes.
const getIconBg = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) {
    return item.value >= 0
      ? 'bg-emerald-100 dark:bg-emerald-900/30'
      : 'bg-red-100 dark:bg-red-900/30'
  }
  if (isSubscriptionType(item.type)) return 'bg-purple-100 dark:bg-purple-900/30'
  return item.value >= 0 ? 'bg-blue-100 dark:bg-blue-900/30' : 'bg-orange-100 dark:bg-orange-900/30'
}

// getIconColor combines history type and amount sign to choose the icon text classes.
// Balance entries use green for nonnegative values and red for deductions; subscription
// entries use purple regardless of sign. Remaining concurrency entries use blue or
// orange according to the amount. Both theme variants are returned together so the
// icon's meaning stays consistent with its background when dark mode changes.
const getIconColor = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) {
    return item.value >= 0
      ? 'text-emerald-600 dark:text-emerald-400'
      : 'text-red-600 dark:text-red-400'
  }
  if (isSubscriptionType(item.type)) return 'text-purple-600 dark:text-purple-400'
  return item.value >= 0
    ? 'text-blue-600 dark:text-blue-400'
    : 'text-orange-600 dark:text-orange-400'
}

// getValueColor combines history type and amount sign to choose value text colors.
// Balance entries use green for nonnegative values and red for deductions; subscription
// entries use purple regardless of sign. Remaining concurrency entries use blue or
// orange according to the amount. The returned light and dark classes keep the number's
// classification consistent with the matching icon while the displayed unit varies by
// history type.
const getValueColor = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) {
    return item.value >= 0
      ? 'text-emerald-600 dark:text-emerald-400'
      : 'text-red-600 dark:text-red-400'
  }
  if (isSubscriptionType(item.type)) return 'text-purple-600 dark:text-purple-400'
  return item.value >= 0
    ? 'text-blue-600 dark:text-blue-400'
    : 'text-orange-600 dark:text-orange-400'
}

// getItemTitle resolves a translated action label from history type and the sign of
// administrator or plugin adjustments. Redeemed balance, affiliate balance, concurrency, and
// subscription records each have a fixed source label. Administrator and plugin
// adjustment records choose increase or decrease labels from their signed values.
// Unknown types use the common unknown label so an unfamiliar server record still
// receives a heading in the history list.
const getItemTitle = (item: BalanceHistoryItem) => {
  switch (item.type) {
    case 'balance':
      return t('redeem.balanceAddedRedeem')
    case 'plugin_balance':
      return item.value >= 0 ? t('redeem.balanceAddedPlugin') : t('redeem.balanceDeductedPlugin')
    case 'admin_balance':
      return item.value >= 0 ? t('redeem.balanceAddedAdmin') : t('redeem.balanceDeductedAdmin')
    case 'affiliate_balance':
      return t('redeem.affiliateBalance')
    case 'concurrency':
      return t('redeem.concurrencyAddedRedeem')
    case 'admin_concurrency':
      return item.value >= 0
        ? t('redeem.concurrencyAddedAdmin')
        : t('redeem.concurrencyReducedAdmin')
    case 'subscription':
      return t('redeem.subscriptionAssigned')
    default:
      return t('common.unknown')
  }
}

// formatValue renders signed balance amounts, subscription days, or signed concurrency
// values according to the entry type. Subscription duration uses validity_days when
// truthy and otherwise rounds value; the balance path prefixes its formatted magnitude
// with the original adjustment direction.
const formatValue = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) {
    const sign = item.value >= 0 ? '+' : '-'
    return `${sign}${formatBalanceAmount(Math.abs(item.value))}`
  }
  if (isSubscriptionType(item.type)) {
    // Subscription history uses an explicit truthy validity period when present, then
    // rounds the generic value into whole displayed days.
    const days = item.validity_days || Math.round(item.value)
    const groupName = item.group?.name || ''
    return groupName ? `${days}d - ${groupName}` : `${days}d`
  }
  // concurrency types
  const sign = item.value >= 0 ? '+' : ''
  return `${sign}${item.value}`
}
</script>

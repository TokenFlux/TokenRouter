<template>
  <BaseDialog
    :show="show"
    :title="t('payment.admin.refundOrder')"
    width="normal"
    @close="emit('cancel')"
  >
    <form id="refund-form" @submit.prevent="handleSubmit" class="space-y-4">
      <div
        v-if="order?.refund_requested_at || order?.refund_request_reason"
        class="rounded-control border border-violet-200 bg-violet-50 p-3 dark:border-violet-800 dark:bg-violet-900/20"
      >
        <div
          class="flex items-center gap-2 text-sm font-medium text-violet-700 dark:text-violet-300"
        >
          <Icon name="infoCircle" size="sm" class="h-4 w-4" />
          {{ t('payment.admin.refundRequestInfo') }}
        </div>
        <div v-if="order?.refund_requested_at" class="mt-2 flex justify-between text-sm">
          <span class="text-violet-600 dark:text-violet-400">{{
            t('payment.admin.refundRequestedAt')
          }}</span>
          <span class="text-violet-800 dark:text-violet-200">{{
            formatDateTime(order.refund_requested_at)
          }}</span>
        </div>
        <div v-if="order?.refund_request_reason" class="mt-1 text-sm">
          <span class="text-violet-600 dark:text-violet-400"
            >{{ t('payment.admin.refundRequestReason') }}:</span
          >
          <span class="ml-1 text-violet-800 dark:text-violet-200">{{
            order.refund_request_reason
          }}</span>
        </div>
      </div>

      <div class="rounded-control bg-gray-50 p-3 dark:bg-dark-700">
        <div class="flex justify-between text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
          <span class="font-mono text-gray-900 dark:text-white">#{{ order?.id }}</span>
        </div>
        <div class="mt-1 flex justify-between text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{
            t('payment.orders.creditedAmount')
          }}</span>
          <span class="font-medium text-gray-900 dark:text-white">{{
            order ? formatOrderAmount(order.amount, order) : '-'
          }}</span>
        </div>
        <div class="mt-1 flex justify-between text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
          <span class="font-medium text-gray-900 dark:text-white">{{
            order ? formatGatewayAmount(order.pay_amount, order.currency) : '-'
          }}</span>
        </div>
        <div v-if="actuallyRefunded > 0" class="mt-1 flex justify-between text-sm">
          <span class="text-gray-500 dark:text-gray-400">{{
            t('payment.admin.alreadyRefunded')
          }}</span>
          <span class="font-medium text-red-600 dark:text-red-400">{{
            order ? formatOrderAmount(actuallyRefunded, order) : '-'
          }}</span>
        </div>
      </div>

      <div>
        <div class="flex items-center gap-2">
          <input
            id="deduct-balance"
            v-model="form.deduct_balance"
            type="checkbox"
            class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
          />
          <label for="deduct-balance" class="text-sm text-gray-700 dark:text-gray-300">
            {{
              t(
                isSubscription
                  ? 'payment.admin.revokeRefundSubscription'
                  : 'payment.admin.deductBalance',
              )
            }}
          </label>
          <span class="text-xs text-gray-500 dark:text-gray-400">{{
            t(
              isSubscription
                ? 'payment.admin.revokeRefundSubscriptionHint'
                : 'payment.admin.deductBalanceHint',
            )
          }}</span>
        </div>

        <div
          v-if="!isSubscription && form.deduct_balance && userBalance != null"
          class="mt-3 grid grid-cols-2 gap-3"
        >
          <div class="rounded-control bg-gray-50 p-3 text-sm dark:bg-dark-700">
            <div class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.userBalance') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">
              {{ formatBalanceAmount(userBalance, { fractionDigits: 2 }) }}
            </div>
          </div>
          <div class="rounded-control bg-gray-50 p-3 text-sm dark:bg-dark-700">
            <div class="text-gray-500 dark:text-gray-400">{{ t('payment.admin.orderAmount') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">
              {{ order ? formatOrderAmount(order.amount, order) : '-' }}
            </div>
          </div>
        </div>

        <div
          v-if="!isSubscription && form.deduct_balance && balanceInsufficient"
          class="mt-2 rounded-control bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t('payment.admin.insufficientBalance') }}
        </div>

        <div
          v-if="!form.deduct_balance"
          class="mt-2 rounded-control bg-blue-50 p-3 text-sm text-blue-700 dark:bg-blue-900/20 dark:text-blue-300"
        >
          {{
            t(isSubscription ? 'payment.admin.keepRefundSubscription' : 'payment.admin.noDeduction')
          }}
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('payment.admin.refundAmount') }}</label>
        <div v-if="isSubscription && order" class="input" data-testid="subscription-refund-amount">
          {{ formatGatewayAmount(order.pay_amount, order.currency) }}
        </div>
        <div v-else class="relative">
          <span class="input-icon text-gray-500">{{ balanceUnitSymbol }}</span>
          <input
            v-model.number="form.amount"
            type="number"
            step="0.01"
            min="0.01"
            :max="maxRefundable"
            class="input input-has-icon input-icon-text"
            required
          />
        </div>
        <p v-if="!isSubscription" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('payment.admin.maxRefundable') }}:
          {{ order ? formatOrderAmount(maxRefundable, order) : '-' }}
        </p>
      </div>

      <div>
        <label class="input-label">{{ t('payment.admin.refundReason') }}</label>
        <textarea
          v-model="form.reason"
          rows="3"
          class="input"
          :placeholder="t('payment.admin.refundReasonPlaceholder')"
          required
        ></textarea>
      </div>

      <div
        v-if="warning"
        class="rounded-control bg-yellow-50 p-3 text-sm text-yellow-700 dark:bg-yellow-900/20 dark:text-yellow-300"
      >
        {{ warning }}
      </div>

      <div v-if="requireForce" class="flex items-center gap-2">
        <input
          id="force-refund"
          v-model="form.force"
          type="checkbox"
          class="h-4 w-4 rounded-compact border-gray-300 text-red-600 focus:ring-red-500"
        />
        <label for="force-refund" class="text-sm font-medium text-red-600 dark:text-red-400">
          {{
            t(
              isSubscription
                ? 'payment.admin.forceSubscriptionRefund'
                : 'payment.admin.forceRefund',
            )
          }}
        </label>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" @click="emit('cancel')" class="btn btn-secondary">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="refund-form"
          :disabled="submitting || form.amount <= 0 || (requireForce && !form.force)"
          class="btn btn-danger"
        >
          {{ submitting ? t('common.processing') : t('payment.admin.confirmRefund') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import { reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { PaymentOrder } from '@/api/payment.types'
import { formatOrderDateTime } from '@/utils/payment/orderUtils'
import { formatPaymentAmount } from '@/utils/payment/currency'

// AdminRefundDialog.vue collects the administrator's refund and entitlement choices.
// Balance refunds accept an amount in entitlement units. Subscription refunds display
// the original channel charge and submit the full order price in its own currency.
// The subscription checkbox controls whether to pause and later revoke the grant.
//
// Opening the dialog initializes the draft from the selected order and clears force
// consent. A server response requiring force leaves submission disabled until the
// administrator selects the confirmation checkbox. The component emits a copied draft,
// while its parent owns the API request and the decision to close or retain the dialog.

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()
// balanceUnitSymbol and formatBalanceAmount use the configured balance display for refund
// units and balance purchase amounts.
const { balanceUnitSymbol, formatBalanceAmount } = useBalanceDisplay()

// props supplies parent-owned records and presentation state consumed by this component
// and its event handlers.
const props = defineProps<{
  show: boolean /* parent-controlled dialog visibility */
  // order supplies the amount, status, and currency for the draft; null leaves derived
  // amounts at zero until a record is selected.
  order: PaymentOrder | null /* selected payment order record */
  submitting?: boolean /* parent refund submission flag */
  // userBalance enables the balance warning when known; omitted or null balances skip that
  // advisory comparison in the refund dialog.
  userBalance?: number | null /* known user balance amount */
  // requireForce reflects the server response and blocks confirmation until the
  // administrator selects force consent in the current draft.
  requireForce?: boolean /* server force confirmation requirement */
  warning?: string /* server refund guidance text */
}>()

// emit defines requests and completion signals that the parent uses to coordinate this
// component with persistence.
const emit = defineEmits<{
  // confirm sends the copied refund draft after amount and force-consent validation. The
  // parent performs persistence and decides whether the current dialog remains open for
  // further confirmation.
  (
    e: 'confirm',
    data: {
      amount: number // requested refund amount
      reason: string // administrator operation reason
      deduct_balance: boolean // entitlement deduction request flag
      force: boolean // administrator force confirmation flag
    },
  ): void
  // cancel asks the parent to dismiss the refund editor and reset its response warning
  // state.
  (e: 'cancel'): void
}>()

// form owns the editable dialog values emitted on confirmation; opening or loading an
// entity initializes these values.
const form = reactive({
  amount: 0, // refund amount in order units
  reason: '', // administrator refund explanation
  deduct_balance: true, // deduct credited entitlement choice
  force: false, // current force confirmation choice
})

// isSubscription selects fixed full refunds and subscription entitlement copy.
const isSubscription = computed(() => props.order?.order_type === 'subscription')

// actuallyRefunded counts refund_amount only in completed refund states; requested and
// pending amounts remain proposals and do not reduce the available refund.
const actuallyRefunded = computed(() => {
  if (!props.order) return 0
  const s = props.order.status
  if (s === 'PARTIALLY_REFUNDED' || s === 'REFUNDED') return props.order.refund_amount || 0
  return 0
})

// maxRefundable subtracts completed refunds from the order amount and supplies the amount
// input limit and submission check.
const maxRefundable = computed(() => {
  if (!props.order) return 0
  return props.order.amount - actuallyRefunded.value
})

// balanceInsufficient compares known user balance with the whole order amount; absent
// balance data leaves this advisory warning hidden.
const balanceInsufficient = computed(() => {
  if (props.userBalance == null || !props.order) return false
  return props.userBalance < props.order.amount
})

// Opening the dialog resets the draft and force consent for this order; requested refunds
// preferentially hydrate the user-requested amount before editing.
watch(
  () => props.show,
  (val) => {
    if (val && props.order) {
      if (isSubscription.value) {
        form.amount = props.order.amount
      } else if (props.order.status === 'REFUND_REQUESTED' && props.order.refund_amount) {
        form.amount = props.order.refund_amount
      } else {
        form.amount = maxRefundable.value
      }
      form.reason = props.order.refund_request_reason || ''
      form.deduct_balance = true
      form.force = false
    }
  },
  { immediate: true },
)

// formatDateTime formats an order timestamp through the shared payment date formatter.
function formatDateTime(dateStr: string): string {
  return formatOrderDateTime(dateStr)
}

// formatOrderAmount formats an amount according to the selected order's purchase kind.
// Balance purchases use configured balance units; subscription purchases use the order
// price currency through the monetary formatter, with payment currency for older responses.
function formatOrderAmount(amount: number, order: PaymentOrder): string {
  return order.order_type === 'balance'
    ? formatBalanceAmount(amount, { fractionDigits: 2 })
    : formatGatewayAmount(amount, order.amount_currency || order.currency)
}

// formatGatewayAmount renders an amount through the shared payment currency formatter.
// Omitted, empty, or malformed currency labels normalize to CNY; valid labels use the
// currency precision resolved by Intl, and nonfinite amounts display as zero.
function formatGatewayAmount(amount: number, currency?: string | null): string {
  return formatPaymentAmount(amount, currency)
}

// handleSubmit emits a copied refund draft after checking its amount and force consent.
// The parent handles persistence, and invalid amounts or an unconfirmed force requirement
// emit nothing.
function handleSubmit() {
  if (form.amount <= 0 || form.amount > maxRefundable.value) return
  if (props.requireForce && !form.force) return
  emit('confirm', { ...form })
}
</script>

<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex items-center gap-2 sm:justify-between">
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
            <div class="min-w-0 flex-1 sm:flex-none sm:w-64">
              <input
                v-model="orderSearch"
                type="text"
                :placeholder="t('payment.admin.searchOrders')"
                class="input"
                @input="debounceLoadOrders"
              />
            </div>
            <FilterDropdown :active-count="activeFilterCount" @reset="resetOrderFilters">
              <FilterField :label="t('payment.orders.status')" icon="checkCircle">
                <FilterOptions
                  v-model="orderFilters.status"
                  :options="statusFilterOptions"
                  @change="loadOrders"
                />
              </FilterField>
              <FilterField :label="t('payment.orders.paymentMethod')" icon="creditCard">
                <FilterOptions
                  v-model="orderFilters.payment_type"
                  :options="paymentTypeFilterOptions"
                  @change="loadOrders"
                >
                  <template #icon="{ value }">
                    <img
                      :src="paymentMethodIconUrl(String(value))"
                      alt=""
                      class="h-4 w-4 object-contain"
                    />
                  </template>
                </FilterOptions>
              </FilterField>
              <FilterField :label="t('payment.orders.orderType')" icon="order">
                <FilterOptions
                  v-model="orderFilters.order_type"
                  :options="orderTypeFilterOptions"
                  @change="loadOrders"
                />
              </FilterField>
            </FilterDropdown>
          </div>
          <button
            @click="loadOrders"
            :disabled="ordersLoading"
            class="btn btn-secondary shrink-0 btn-icon"
            :title="t('common.refresh')"
          >
            <Icon name="refresh" size="sm" :class="ordersLoading ? 'animate-spin' : ''" />
          </button>
        </div>
      </template>

      <template #table>
        <OrderTable :orders="orders" :loading="ordersLoading" show-user>
          <template #actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                @click="showOrderDetail(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-700"
              >
                <Icon name="eye" size="sm" />
                {{ t('common.view') }}
              </button>
              <button
                v-if="canOpenInvoice(row)"
                @click="openInvoice(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-500 dark:hover:bg-primary-500/8"
              >
                <Icon name="document" size="sm" />
                {{ t('payment.orders.invoice') }}
              </button>
              <button
                v-if="row.status === 'PENDING'"
                @click="handleCancelOrder(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20"
              >
                <Icon name="x" size="sm" />
                {{ t('payment.orders.cancel') }}
              </button>
              <button
                v-if="row.status === 'PENDING'"
                @click="openForceExpireDialog(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
              >
                <Icon name="exclamationTriangle" size="sm" />
                {{ t('payment.admin.forceExpire') }}
              </button>
              <button
                v-if="row.status === 'FAILED'"
                @click="handleRetryOrder(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-500 dark:hover:bg-primary-500/8"
              >
                <Icon name="refresh" size="sm" />
                {{ t('payment.admin.retry') }}
              </button>
              <template v-if="row.status === 'REFUND_REQUESTED'">
                <span
                  v-if="row.refund_amount"
                  class="rounded-full bg-purple-100 px-1.5 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300"
                  >{{ formatOrderAmount(row.refund_amount, row) }}</span
                >
                <button
                  @click="openRefundDialog(row)"
                  class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20"
                >
                  <Icon name="check" size="sm" :animate-on-hover="false" />
                  {{ t('payment.admin.approveRefund') }}
                </button>
              </template>
              <button
                v-else-if="row.status === 'REFUND_FAILED'"
                @click="openRefundDialog(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20"
              >
                <Icon name="refresh" size="sm" />
                {{ t('payment.admin.retryRefund') }}
              </button>
              <button
                v-else-if="row.status === 'REFUND_PENDING' || row.status === 'REFUNDING'"
                :disabled="refundQueryingIds.has(row.id)"
                @click="handleQueryRefund(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-orange-600 hover:bg-orange-50 disabled:opacity-60 dark:text-orange-400 dark:hover:bg-orange-900/20"
              >
                <Icon
                  name="refresh"
                  size="sm"
                  :class="refundQueryingIds.has(row.id) ? 'animate-spin' : ''"
                />
                {{ t('payment.admin.queryRefundStatus') }}
              </button>
              <button
                v-else-if="row.status === 'COMPLETED'"
                @click="openRefundDialog(row)"
                class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
              >
                <Icon name="dollar" size="sm" />
                {{ t('payment.admin.refund') }}
              </button>
            </div>
          </template>
        </OrderTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="orderPagination.total > 0"
          :page="orderPagination.page"
          :total="orderPagination.total"
          :page-size="orderPagination.page_size"
          @update:page="handleOrderPageChange"
          @update:pageSize="handleOrderPageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showDetailDialog"
      :title="t('payment.admin.orderDetail')"
      width="wide"
      @close="showDetailDialog = false"
    >
      <div v-if="selectedOrder" class="space-y-4">
        <div class="grid grid-cols-2 gap-4">
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.orders.orderId')
            }}</p
            ><p class="font-mono text-sm font-medium text-gray-900 dark:text-white"
              >#{{ selectedOrder.id }}</p
            ></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.orders.orderNo')
            }}</p
            ><p class="text-sm font-medium text-gray-900 dark:text-white">{{
              selectedOrder.out_trade_no
            }}</p></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.status') }}</p
            ><OrderStatusBadge :status="selectedOrder.status"
          /></div>
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</p
            ><p class="text-sm font-medium text-gray-900 dark:text-white">{{
              formatOrderAmount(selectedOrder.amount, selectedOrder)
            }}</p></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.orders.payAmount')
            }}</p
            ><p class="text-sm font-medium text-gray-900 dark:text-white">{{
              formatGatewayAmount(selectedOrder.pay_amount, selectedOrder.currency)
            }}</p></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.orders.paymentMethod')
            }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300">{{
              t('payment.methods.' + selectedOrder.payment_type, selectedOrder.payment_type)
            }}</p></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.fee') }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300"
              >{{ formatGatewayAmount(selectedOrderFeeAmount, selectedOrder.currency) }}
              <span v-if="selectedOrder.fee_rate > 0">({{ selectedOrder.fee_rate }}%)</span></p
            ></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.orders.createdAt')
            }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300">{{
              formatDateTime(selectedOrder.created_at)
            }}</p></div
          >
          <div
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.admin.expiresAt')
            }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300">{{
              formatDateTime(selectedOrder.expires_at)
            }}</p></div
          >
          <div v-if="selectedOrder.paid_at"
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidAt') }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300">{{
              formatDateTime(selectedOrder.paid_at)
            }}</p></div
          >
          <div v-if="selectedOrder.refund_amount"
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.admin.refundAmount')
            }}</p
            ><p class="text-sm font-medium text-red-600 dark:text-red-400">{{
              formatOrderAmount(selectedOrder.refund_amount, selectedOrder)
            }}</p></div
          >
          <div v-if="selectedOrder.refund_reason" class="col-span-2"
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.admin.refundReason')
            }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300">{{
              selectedOrder.refund_reason
            }}</p></div
          >
          <div v-if="selectedOrder.payment_invoice_id" class="col-span-2"
            ><p class="text-xs text-gray-500 dark:text-gray-400">{{
              t('payment.orders.invoice')
            }}</p
            ><p class="text-sm text-gray-700 dark:text-gray-300">{{
              selectedOrder.payment_invoice_id
            }}</p></div
          >
          <div
            v-if="selectedOrder.refund_requested_at"
            class="col-span-2 border-t border-gray-200 pt-3 dark:border-dark-600"
          >
            <p class="mb-2 text-xs font-medium text-purple-600 dark:text-purple-400">{{
              t('payment.admin.refundRequestInfo')
            }}</p>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('payment.admin.refundRequestedAt')
                }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{
                  formatDateTime(selectedOrder.refund_requested_at)
                }}</p>
              </div>
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('payment.admin.refundRequestedBy')
                }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300"
                  >#{{ selectedOrder.refund_requested_by }}</p
                >
              </div>
              <div class="col-span-2">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('payment.admin.refundRequestReason')
                }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{
                  selectedOrder.refund_request_reason
                }}</p>
              </div>
            </div>
          </div>
        </div>
        <div class="flex justify-end border-t border-gray-200 pt-4 dark:border-dark-600">
          <button
            v-if="canOpenInvoice(selectedOrder)"
            class="btn btn-secondary"
            @click="openInvoice(selectedOrder)"
          >
            <Icon name="document" size="sm" />
            {{ t('payment.orders.invoice') }}
          </button>
        </div>
        <div
          v-if="orderAuditLogs.length > 0"
          class="border-t border-gray-200 pt-4 dark:border-dark-600"
        >
          <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{
            t('payment.admin.auditLogs')
          }}</p>
          <div class="max-h-48 space-y-2 overflow-y-auto">
            <div
              v-for="log in orderAuditLogs"
              :key="log.id"
              class="rounded-control border border-gray-100 bg-gray-50 p-2.5 dark:border-dark-600 dark:bg-dark-800"
            >
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{
                  log.action
                }}</span>
                <span class="text-xs text-gray-400">{{ formatDateTime(log.created_at) }}</span>
              </div>
              <div
                v-if="log.detail"
                class="mt-1 break-all text-xs text-gray-500 dark:text-gray-400"
                >{{ log.detail }}</div
              >
              <div v-if="log.operator" class="mt-1 text-xs text-gray-400"
                >{{ t('payment.admin.operator') }}: {{ log.operator }}</div
              >
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>

    <BaseDialog
      :show="showForceExpireDialog"
      :title="t('payment.admin.forceExpireOrder')"
      width="normal"
      @close="closeForceExpireDialog"
    >
      <form id="force-expire-form" class="space-y-4" @submit.prevent="handleForceExpireOrder">
        <div
          class="rounded-control border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-200"
        >
          {{ t('payment.admin.forceExpireWarning') }}
        </div>
        <div
          v-if="forceExpireTarget"
          class="rounded-control bg-gray-50 p-3 text-sm dark:bg-dark-700"
        >
          <div class="flex justify-between gap-3">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ forceExpireTarget.id }}</span>
          </div>
          <div class="mt-1 flex justify-between gap-3">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
            <span class="break-all text-right text-gray-900 dark:text-white">{{
              forceExpireTarget.out_trade_no
            }}</span>
          </div>
        </div>
        <div>
          <label for="force-expire-reason" class="input-label">{{
            t('payment.admin.forceExpireReason')
          }}</label>
          <textarea
            id="force-expire-reason"
            v-model="forceExpireReason"
            rows="3"
            maxlength="500"
            class="input"
            :placeholder="t('payment.admin.forceExpireReasonPlaceholder')"
            required
          ></textarea>
        </div>
        <label class="flex items-start gap-2 text-sm text-red-700 dark:text-red-300">
          <input
            id="force-expire-confirm"
            v-model="forceExpireConfirmed"
            type="checkbox"
            class="mt-0.5 h-4 w-4 rounded-compact border-gray-300 text-red-600 focus:ring-red-500"
          />
          <span>{{ t('payment.admin.forceExpireAcknowledge') }}</span>
        </label>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeForceExpireDialog">{{
            t('common.cancel')
          }}</button>
          <button
            type="submit"
            form="force-expire-form"
            :disabled="forceExpireSubmitting || !forceExpireConfirmed || !forceExpireReason.trim()"
            class="btn btn-danger"
          >
            {{
              forceExpireSubmitting ? t('common.processing') : t('payment.admin.confirmForceExpire')
            }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="refundReferenceOrder !== null"
      :title="t('payment.admin.verifyStripeRefund')"
      width="narrow"
      @close="closeRefundReferenceDialog"
    >
      <form id="refund-reference-form" class="space-y-3" @submit.prevent="submitRefundReference">
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ t('payment.admin.verifyStripeRefundHint', { order: refundReferenceOrder?.id }) }}
        </p>
        <div>
          <label class="input-label" for="stripe-refund-reference">
            {{ t('payment.admin.stripeRefundReference') }}
          </label>
          <input
            id="stripe-refund-reference"
            v-model.trim="refundReference"
            class="input font-mono"
            type="text"
            placeholder="re_"
            autocomplete="off"
            spellcheck="false"
            required
          />
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="closeRefundReferenceDialog">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="refund-reference-form"
            class="btn btn-primary"
            :disabled="!refundReference || refundReferenceSubmitting"
          >
            {{ t('payment.admin.verifyStripeRefund') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <AdminRefundDialog
      :show="showRefundDialog"
      :order="selectedOrder"
      :submitting="refundSubmitting"
      :require-force="refundRequireForce"
      :warning="refundWarning"
      @confirm="handleRefund"
      @cancel="closeRefundDialog"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/utils/payment/orderUtils'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { PaymentOrder } from '@/api/payment.types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import { paymentMethodIconUrl } from '@/utils/payment/paymentMethods'
import type { FilterOption } from '@/components/common/filterPanel'
import Icon from '@/components/icons/Icon.vue'
import AdminRefundDialog from '@/components/admin/payment/AdminRefundDialog.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import { formatPaymentAmount } from '@/utils/payment/currency'

// AdminOrdersView.vue lists payment orders and coordinates detail, cancellation, recovery,
// invoice, force-expiration, and refund actions. List filters and pagination feed the
// order query, while detail dialogs initially display the selected row before requesting
// richer data. Balance amounts use configured balance units and gateway amounts use the
// order currency.
//
// Refund responses determine whether the dialog closes or remains open for force consent.
// A successful, pending, or unconfirmed refund refreshes the list; require_force retains the draft
// and exposes the server warning. Force expiration requires a reason and confirmation, and
// a status conflict closes its stale confirmation before reloading orders. Per-order
// refund-query indicators track which row has an outstanding query.

// AuditLog represents an order action record displayed in the administrator detail dialog.
interface AuditLog {
  id: number /* persistent record identifier */
  action: string /* order audit action code */
  detail: string | null /* nullable audit detail text */
  operator: string | null /* nullable audit operator identity */
  created_at: string /* audit creation timestamp text */
}

// t resolves the locale labels used by payment controls, validation messages, and
// displayed order information.
const { t } = useI18n()
// appStore provides page feedback for completed operations and request failures while
// handlers retain their local view state.
const appStore = useAppStore()
// formatBalanceAmount renders balance order values using the configured balance unit while
// gateway amounts retain their own currency.
const { formatBalanceAmount } = useBalanceDisplay()

// ordersLoading reflects list request progress and is cleared when the active invocation
// finishes its success or error handling.
const ordersLoading = ref(false)
// orders holds the latest successful list response; request failures retain these rows
// while displaying an error message.
const orders = ref<PaymentOrder[]>([])
// orderSearch holds keyword text independently of categorical filters and is omitted from
// list requests when empty.
const orderSearch = ref('')
// orderFilters owns status, payment method, and purchase-kind selections; empty values are
// omitted from the order query.
const orderFilters = reactive({ status: '', payment_type: '', order_type: '' })
// activeFilterCount counts populated categorical filters for the filter indicator and does
// not count the separate keyword input.
const activeFilterCount = computed(
  () =>
    [orderFilters.status, orderFilters.payment_type, orderFilters.order_type].filter(Boolean)
      .length,
)
// orderPagination stores current page, page size, and server total; filter resets and size
// changes return to page one.
const orderPagination = reactive({ page: 1, page_size: 20, total: 0 })
// selectedOrder identifies the row used by detail and refund dialogs and may be replaced
// by a detailed API response.
const selectedOrder = ref<PaymentOrder | null>(null)
// showDetailDialog controls the detail modal while cached row data and asynchronously
// fetched audit logs populate its content.
const showDetailDialog = ref(false)
// showRefundDialog controls refund editing; response handlers close it for success or
// pending results and retain it for force consent.
const showRefundDialog = ref(false)
// showForceExpireDialog controls the manual expiration confirmation whose target, reason,
// and consent are stored separately from order details.
const showForceExpireDialog = ref(false)
// forceExpireTarget captures the order for manual expiration and is cleared when the
// confirmation dialog closes.
const forceExpireTarget = ref<PaymentOrder | null>(null)
// forceExpireReason retains the administrator explanation until closing and is trimmed
// before the manual expiration request is submitted.
const forceExpireReason = ref('')
// forceExpireConfirmed records consent for the current expiration draft and is reset on
// both opening and closing.
const forceExpireConfirmed = ref(false)
// forceExpireSubmitting tracks manual expiration request progress and remains set through
// a status-conflict list refresh when that occurs.
const forceExpireSubmitting = ref(false)
// refundSubmitting tracks refund submission progress so the dialog can disable actions
// while the API request is being handled.
const refundSubmitting = ref(false)
// refundRequireForce exposes the server requirement for another administrator confirmation
// and is reset whenever the refund dialog opens or closes.
const refundRequireForce = ref(false)
// refundWarning retains server guidance while the refund dialog awaits force consent;
// opening or closing clears the previous warning.
const refundWarning = ref('')
// refundQueryingIds tracks rows with outstanding refund status queries; each completion
// removes its identifier from the latest set.
const refundQueryingIds = ref(new Set<number>())
// refundReferenceOrder and refundReference retain the order and draft for
// Stripe verification. Only a lookup that explicitly asks for a reference
// opens this form. Closing it clears the input before another order uses it.
const refundReferenceOrder = ref<PaymentOrder | null>(null)
const refundReference = ref('')
// refundReferenceSubmitting reuses the per-order query state so an active
// verification cannot be submitted twice from the reference dialog.
const refundReferenceSubmitting = computed(
  () =>
    refundReferenceOrder.value !== null &&
    refundQueryingIds.value.has(refundReferenceOrder.value.id),
)
// orderAuditLogs belongs to the detail dialog and is cleared before fetching another
// selected order to remove previous entries.
const orderAuditLogs = ref<AuditLog[]>([])

// resetOrderFilters clears the three categorical filters and returns pagination to its
// first page. It immediately starts a list reload while retaining the separate keyword
// search input.
function resetOrderFilters() {
  orderFilters.status = ''
  orderFilters.payment_type = ''
  orderFilters.order_type = ''
  orderPagination.page = 1
  loadOrders()
}

// debounceTimer identifies the pending keyword-search timer; subsequent input clears that
// timer before scheduling a new order-list request.
let debounceTimer: ReturnType<typeof setTimeout> | null = null
// debounceLoadOrders replaces the pending search timer with a delayed list reload.
// Repeated calls before the delay expires clear the previous timer, so the eventual
// request uses current filters.
function debounceLoadOrders() {
  // Replace the pending search delay on further input so typing several characters
  // dispatches the request after the latest input settles.
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => loadOrders(), 300)
}

// loadOrders requests the current search, filter, and pagination values from the
// administrator API. Success replaces rows and total, while failure displays an error and
// retains the previous list.
async function loadOrders() {
  ordersLoading.value = true
  try {
    const res = await adminPaymentAPI.getOrders({
      page: orderPagination.page,
      page_size: orderPagination.page_size,
      keyword: orderSearch.value || undefined,
      status: orderFilters.status || undefined,
      payment_type: orderFilters.payment_type || undefined,
      order_type: orderFilters.order_type || undefined,
    })
    orders.value = res.data.items || []
    orderPagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    ordersLoading.value = false
  }
}

// handleOrderPageChange selects a page and starts loading its order rows. The asynchronous
// reload owns the loading indicator and error reporting while this event handler returns
// immediately.
function handleOrderPageChange(page: number) {
  orderPagination.page = page
  loadOrders()
}
// handleOrderPageSizeChange updates page size and returns to the first page before
// loading. Resetting the page keeps a size change from retaining an offset based on the
// previous size.
function handleOrderPageSizeChange(size: number) {
  orderPagination.page_size = size
  orderPagination.page = 1
  loadOrders()
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

// selectedOrderFeeAmount prefers a positive recorded fee, otherwise derives the fee
// included in pay_amount from the stored percentage.
const selectedOrderFeeAmount = computed(() => {
  const order = selectedOrder.value
  if (!order) return 0
  if ((order.fee_amount || 0) > 0) return order.fee_amount
  if ((order.fee_rate || 0) <= 0) return 0
  // Some stored orders lack a positive fee amount; derive the fee included in the paid
  // total using the recorded percentage rate.
  return order.pay_amount - order.pay_amount / (1 + order.fee_rate / 100)
})

// statusFilterOptions supplies translated order statuses plus the empty all-statuses
// option used when constructing list query parameters.
const statusFilterOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('payment.admin.allStatuses') },
  { value: 'PENDING', label: t('payment.status.pending'), dot: 'yellow' },
  { value: 'PROCESSING', label: t('payment.status.processing'), dot: 'cyan' },
  { value: 'PAID', label: t('payment.status.paid'), dot: 'blue' },
  { value: 'COMPLETED', label: t('payment.status.completed'), dot: 'green' },
  { value: 'EXPIRED', label: t('payment.status.expired'), dot: 'gray' },
  { value: 'CANCELLED', label: t('payment.status.cancelled'), dot: 'gray' },
  { value: 'FAILED', label: t('payment.status.failed'), dot: 'red' },
  { value: 'REFUNDED', label: t('payment.status.refunded'), dot: 'purple' },
  { value: 'REFUND_REQUESTED', label: t('payment.status.refund_requested'), dot: 'orange' },
  { value: 'REFUNDING', label: t('payment.status.refunding'), dot: 'orange' },
  { value: 'REFUND_PENDING', label: t('payment.status.refund_pending'), dot: 'orange' },
  { value: 'REFUND_FAILED', label: t('payment.status.refund_failed'), dot: 'red' },
])

// paymentTypeFilterOptions supplies supported payment-method filters and an empty option
// that omits the method restriction from requests.
const paymentTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allPaymentTypes') },
  { value: 'alipay', label: t('payment.methods.alipay') },
  { value: 'wxpay', label: t('payment.methods.wxpay') },
  { value: 'stripe', label: t('payment.methods.stripe') },
  { value: 'airwallex', label: t('payment.methods.airwallex') },
])

// orderTypeFilterOptions offers balance and subscription purchase filters with an empty
// value that keeps both purchase kinds visible.
const orderTypeFilterOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('payment.admin.allOrderTypes') },
  { value: 'balance', label: t('payment.admin.balanceOrder'), icon: 'recharge' },
  { value: 'subscription', label: t('payment.admin.subscriptionOrder'), icon: 'calendar' },
])

// showOrderDetail opens with the selected list row and requests current order details and
// audit logs. It clears previous logs immediately, accepts either audit-log property
// spelling, and retains cached data on failure.
async function showOrderDetail(order: PaymentOrder) {
  selectedOrder.value = order
  orderAuditLogs.value = []
  showDetailDialog.value = true
  try {
    const res = await adminPaymentAPI.getOrder(order.id)
    const data = res.data as unknown as Record<string, unknown>
    if (data.order) selectedOrder.value = data.order as PaymentOrder
    // Order details may expose audit logs in camelCase or snake_case; accept either
    // representation so both response shapes populate the same timeline.
    orderAuditLogs.value = (data.auditLogs || data.audit_logs || []) as unknown as AuditLog[]
  } catch (_err: unknown) {
    // The selected list row already supplies order details; retain that cached record if
    // enrichment fails so the opened dialog remains usable.
  }
}

// handleCancelOrder asks the API to cancel the supplied order and reports its result. A
// successful response starts a list refresh; failures display the translated payment
// error.
async function handleCancelOrder(order: PaymentOrder) {
  try {
    await adminPaymentAPI.cancelOrder(order.id)
    appStore.showSuccess(t('payment.admin.orderCancelled'))
    loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
}

// openForceExpireDialog selects the target order and opens a fresh confirmation draft. The
// reason and consent are cleared for each opening so a previous operation's confirmation
// is not reused.
function openForceExpireDialog(order: PaymentOrder) {
  forceExpireTarget.value = order
  forceExpireReason.value = ''
  forceExpireConfirmed.value = false
  showForceExpireDialog.value = true
}

// closeForceExpireDialog closes the confirmation and clears its target, reason, and
// consent.
function closeForceExpireDialog() {
  showForceExpireDialog.value = false
  forceExpireTarget.value = null
  forceExpireReason.value = ''
  forceExpireConfirmed.value = false
}

// handleForceExpireOrder submits a confirmed force-expiration request with a trimmed
// reason. Missing target, reason, or confirmation leaves the dialog unchanged. Success
// closes the dialog and starts reloading orders. A status-conflict response also closes
// the stale confirmation and awaits a refresh, because the target may have completed
// payment since the row was selected. Other failures retain the draft for inspection, and
// the submitting indicator clears after either outcome.
async function handleForceExpireOrder() {
  const order = forceExpireTarget.value
  const reason = forceExpireReason.value.trim()
  if (!order || !reason || !forceExpireConfirmed.value) return

  forceExpireSubmitting.value = true
  try {
    await adminPaymentAPI.forceExpireOrder(order.id, { reason })
    appStore.showSuccess(t('payment.admin.forceExpireSuccess'))
    closeForceExpireDialog()
    loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
    // A concurrent payment or status change invalidates the expiration draft; dismiss it
    // and refresh the list before further administrator action.
    if (extractApiErrorCode(err) === 'ORDER_STATUS_CHANGED') {
      closeForceExpireDialog()
      await loadOrders()
    }
  } finally {
    forceExpireSubmitting.value = false
  }
}

// handleRetryOrder requests another fulfillment attempt for the supplied order. Successful
// completion shows a message and reloads the list; the API remains responsible for
// deciding whether retry is allowed.
async function handleRetryOrder(order: PaymentOrder) {
  try {
    await adminPaymentAPI.retryRecharge(order.id)
    appStore.showSuccess(t('payment.admin.retrySuccess'))
    loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
}

// canOpenInvoice reports whether a Stripe order has a status eligible for document
// retrieval. Null orders, other payment types, and statuses outside the listed paid or
// refunded states return false.
function canOpenInvoice(order: PaymentOrder | null): boolean {
  if (!order || (order.payment_type || '').trim() !== 'stripe') return false
  return ['PAID', 'RECHARGING', 'COMPLETED', 'PARTIALLY_REFUNDED', 'REFUNDED'].includes(
    order.status,
  )
}

// openInvoice requests the available payment document for an order and opens its URL. It
// prefers the general URL, hosted invoice, PDF, then receipt, and displays an error when
// none exists.
async function openInvoice(order: PaymentOrder) {
  try {
    const res = await adminPaymentAPI.getOrderInvoice(order.id)
    const doc = res.data
    // Document responses can provide different provider URL fields; prefer the general
    // document URL and use available invoice or receipt alternatives.
    const url = doc.url || doc.hosted_invoice_url || doc.invoice_pdf || doc.receipt_url
    if (!url) {
      appStore.showError(t('payment.errors.PAYMENT_DOCUMENT_NOT_FOUND'))
      return
    }
    window.open(url, '_blank', 'noopener,noreferrer')
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  }
}

// openRefundDialog selects an order and opens its refund editor with cleared warning
// state. Previous force requirements are reset so another order starts with its own server
// validation.
function openRefundDialog(order: PaymentOrder) {
  selectedOrder.value = order
  refundRequireForce.value = false
  refundWarning.value = ''
  showRefundDialog.value = true
}

// closeRefundDialog closes the refund editor and clears the server warning and force
// requirement.
function closeRefundDialog() {
  showRefundDialog.value = false
  refundRequireForce.value = false
  refundWarning.value = ''
}

// handleRefund submits the dialog draft for the currently selected order and interprets
// the refund response. Success closes the editor and starts a list refresh. A pending
// result follows the same close-and-refresh path with a warning message. An
// unconfirmed outcome closes the editor and refreshes the claimed refund state. A
// force requirement keeps the editor open and exposes server guidance so the administrator
// can consent before resubmitting. Other failures retain the draft, and the submitting
// indicator clears after response handling.
async function handleRefund(data: {
  amount: number /* requested refund amount */
  reason: string /* administrator operation reason */
  deduct_balance: boolean /* entitlement deduction request flag */
  force: boolean /* administrator force confirmation flag */
}) {
  if (!selectedOrder.value) return
  refundSubmitting.value = true
  try {
    const res = await adminPaymentAPI.refundOrder(selectedOrder.value.id, {
      amount: data.amount,
      reason: data.reason,
      deduct_balance: data.deduct_balance,
      force: data.force,
    })
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (res.data.status === 'pending') {
      appStore.showWarning(t('payment.admin.refundPending'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (res.data.status === 'requires_force' || res.data.require_force) {
      // The server requires fresh force consent, so retain the open refund draft and
      // expose its warning before allowing the administrator to resubmit.
      refundRequireForce.value = true
      refundWarning.value =
        selectedOrder.value?.order_type === 'subscription'
          ? t('payment.admin.subscriptionRefundForceHint')
          : res.data.warning || ''
      return
    }
    appStore.showError(res.data.warning || t('common.error'))
  } catch (err: unknown) {
    if (extractApiErrorCode(err) === 'REFUND_RECOVERY_REQUIRED') {
      // The external refund may have completed. Close the stale submission form and
      // reload its claimed state before another action can be selected for this order.
      closeRefundDialog()
      await loadOrders()
      appStore.showWarning(t('payment.errors.REFUND_RECOVERY_REQUIRED'))
      return
    }
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    refundSubmitting.value = false
  }
}

// closeRefundReferenceDialog clears the current order and typed reference.
function closeRefundReferenceDialog() {
  refundReferenceOrder.value = null
  refundReference.value = ''
}

// submitRefundReference asks the server to verify the entered ID against the
// captured order. A blank draft leaves the dialog open without querying.
async function submitRefundReference() {
  const order = refundReferenceOrder.value
  if (order && refundReference.value) {
    await handleQueryRefund(order, refundReference.value)
  }
}

// handleQueryRefund asks the API to resolve a refund already associated with the supplied
// order. The order ID enters the busy set until the request finishes, and successful
// responses refresh the list after reporting success, pending, or failure guidance.
// A missing Stripe reference opens the verification dialog when another order
// is not already being edited. Completion clears only this order's busy state.
async function handleQueryRefund(order: PaymentOrder, refundID?: string) {
  if (refundQueryingIds.value.has(order.id)) return
  // Track this row independently of other queries; replacing the set publishes its busy
  // indicator while preserving identifiers for outstanding requests.
  refundQueryingIds.value = new Set(refundQueryingIds.value).add(order.id)
  try {
    const res = await adminPaymentAPI.queryRefund(order.id, refundID)
    if (refundID && refundReferenceOrder.value?.id === order.id) {
      closeRefundReferenceDialog()
    }
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
    } else if (res.data.status === 'pending') {
      appStore.showWarning(t('payment.admin.refundPending'))
    } else {
      appStore.showError(res.data.warning || t('common.error'))
    }
    loadOrders()
  } catch (err: unknown) {
    const code = extractApiErrorCode(err)
    if (code === 'REFUND_REFERENCE_REQUIRED') {
      if (!refundReferenceOrder.value) {
        refundReferenceOrder.value = order
      } else if (refundReferenceOrder.value.id !== order.id) {
        appStore.showWarning(t('payment.errors.REFUND_REFERENCE_REQUIRED'))
      }
      return
    }
    if (code === 'REFUND_RECOVERY_REQUIRED') {
      appStore.showWarning(t('payment.errors.REFUND_RECOVERY_REQUIRED'))
      await loadOrders()
      return
    }
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    // Read the latest set at completion so clearing this row does not discard indicators
    // added by other queries during the request.
    const next = new Set(refundQueryingIds.value)
    next.delete(order.id)
    refundQueryingIds.value = next
  }
}

// formatDateTime formats an order timestamp through the shared payment date formatter.
function formatDateTime(dateStr: string): string {
  return formatOrderDateTime(dateStr)
}

onMounted(() => {
  loadOrders()
})
</script>

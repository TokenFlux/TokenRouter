<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
      <!-- 搜索和筛选工具栏 -->
      <div class="flex items-center gap-2 sm:justify-between">
        <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
          <div class="min-w-0 flex-1 sm:flex-none sm:w-64">
            <input v-model="orderSearch" type="text" :placeholder="t('payment.admin.searchOrders')" class="input" @input="debounceLoadOrders" />
          </div>
          <div ref="filterDropdownRef" class="relative shrink-0">
            <button type="button" class="btn btn-secondary relative h-9 w-9 p-0" :aria-expanded="showFilterDropdown" :aria-label="t('common.filter')" :title="t('common.filter')" @click="showFilterDropdown = !showFilterDropdown">
              <Icon name="filter" size="sm" />
              <span v-if="activeFilterCount > 0" class="absolute -right-1 -top-1 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary-100 px-1.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ activeFilterCount }}</span>
            </button>
            <div v-if="showFilterDropdown" class="absolute left-auto right-0 top-full z-[60] mt-2 w-80 rounded-surface border border-gray-200 bg-white p-4 shadow-xl dark:border-dark-600 dark:bg-dark-900 sm:left-0 sm:right-auto" @click.stop>
              <div class="mb-3 flex items-center justify-between">
                <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('common.filter') }}</div>
                <button v-if="activeFilterCount > 0" type="button" class="text-xs font-medium text-primary-600 dark:text-primary-400" @click="resetOrderFilters">{{ t('common.reset') }}</button>
              </div>
              <div class="space-y-3">
                <Select v-model="orderFilters.status" :options="statusFilterOptions" @change="loadOrders" />
                <Select v-model="orderFilters.payment_type" :options="paymentTypeFilterOptions" @change="loadOrders" />
                <Select v-model="orderFilters.order_type" :options="orderTypeFilterOptions" @change="loadOrders" />
              </div>
            </div>
          </div>
        </div>
        <button
          @click="loadOrders"
          :disabled="ordersLoading"
          class="btn btn-secondary h-9 w-9 shrink-0 p-0"
          :title="t('common.refresh')"
        >
          <Icon name="refresh" size="md" :class="ordersLoading ? 'animate-spin' : ''" />
        </button>
      </div>
      </template>

      <template #table>
        <!-- 订单表格复用与 API Keys 相同的 TablePageLayout/DataTable 容器，订单字段和操作仍由本组件定制。 -->
        <OrderTable :orders="orders" :loading="ordersLoading" show-user>
        <template #actions="{ row }">
          <div class="flex items-center gap-1">
            <button @click="showOrderDetail(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-600">
              <Icon name="eye" size="sm" />
              {{ t('common.view') }}
            </button>
            <button v-if="canOpenInvoice(row)" @click="openInvoice(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon name="document" size="sm" />
              {{ t('payment.orders.invoice') }}
            </button>
            <button v-if="row.status === 'PENDING'" @click="handleCancelOrder(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-yellow-600 hover:bg-yellow-50 dark:text-yellow-400 dark:hover:bg-yellow-900/20">
              <Icon name="x" size="sm" />
              {{ t('payment.orders.cancel') }}
            </button>
            <button v-if="row.status === 'PENDING'" @click="openForceExpireDialog(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20">
              <Icon name="exclamationTriangle" size="sm" />
              {{ t('payment.admin.forceExpire') }}
            </button>
            <button v-if="row.status === 'FAILED'" @click="handleRetryOrder(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retry') }}
            </button>
            <template v-if="row.status === 'REFUND_REQUESTED'">
              <span v-if="row.refund_amount" class="rounded-full bg-purple-100 px-1.5 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">{{ formatOrderAmount(row.refund_amount, row) }}</span>
              <button @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
                <Icon name="check" size="sm" />
                {{ t('payment.admin.approveRefund') }}
              </button>
            </template>
            <button v-else-if="row.status === 'REFUND_FAILED'" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-purple-600 hover:bg-purple-50 dark:text-purple-400 dark:hover:bg-purple-900/20">
              <Icon name="refresh" size="sm" />
              {{ t('payment.admin.retryRefund') }}
            </button>
            <button v-else-if="row.status === 'REFUND_PENDING' || row.status === 'REFUNDING'" :disabled="refundQueryingIds.has(row.id)" @click="handleQueryRefund(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-orange-600 hover:bg-orange-50 disabled:opacity-60 dark:text-orange-400 dark:hover:bg-orange-900/20">
              <Icon name="refresh" size="sm" :class="refundQueryingIds.has(row.id) ? 'animate-spin' : ''" />
              {{ t('payment.admin.queryRefundStatus') }}
            </button>
            <button v-else-if="row.status === 'COMPLETED' || row.status === 'PARTIALLY_REFUNDED'" @click="openRefundDialog(row)" class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20">
              <Icon name="dollar" size="sm" />
              {{ t('payment.admin.refund') }}
            </button>
          </div>
        </template>
        </OrderTable>
      </template>

      <template #pagination>
        <Pagination v-if="orderPagination.total > 0" :page="orderPagination.page" :total="orderPagination.total" :page-size="orderPagination.page_size" @update:page="handleOrderPageChange" @update:pageSize="handleOrderPageSizeChange" />
      </template>
    </TablePageLayout>

    <!-- Order Detail Dialog -->
    <BaseDialog :show="showDetailDialog" :title="t('payment.admin.orderDetail')" width="wide" @close="showDetailDialog = false">
      <div v-if="selectedOrder" class="space-y-4">
        <div class="grid grid-cols-2 gap-4">
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</p><p class="font-mono text-sm font-medium text-gray-900 dark:text-white">#{{ selectedOrder.id }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ selectedOrder.out_trade_no }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.status') }}</p><OrderStatusBadge :status="selectedOrder.status" /></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ formatOrderAmount(selectedOrder.amount, selectedOrder) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</p><p class="text-sm font-medium text-gray-900 dark:text-white">{{ formatGatewayAmount(selectedOrder.pay_amount, selectedOrder.currency) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.paymentMethod') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ t('payment.methods.' + selectedOrder.payment_type, selectedOrder.payment_type) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.fee') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatGatewayAmount(selectedOrderFeeAmount, selectedOrder.currency) }} <span v-if="selectedOrder.fee_rate > 0">({{ selectedOrder.fee_rate }}%)</span></p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.createdAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.created_at) }}</p></div>
          <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.expiresAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.expires_at) }}</p></div>
          <div v-if="selectedOrder.paid_at"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.paidAt') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.paid_at) }}</p></div>
          <div v-if="selectedOrder.refund_amount"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundAmount') }}</p><p class="text-sm font-medium text-red-600 dark:text-red-400">{{ formatOrderAmount(selectedOrder.refund_amount, selectedOrder) }}</p></div>
          <div v-if="selectedOrder.refund_reason" class="col-span-2"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundReason') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_reason }}</p></div>
          <div v-if="selectedOrder.payment_invoice_id" class="col-span-2"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.orders.invoice') }}</p><p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.payment_invoice_id }}</p></div>
          <!-- Refund request info -->
          <div v-if="selectedOrder.refund_requested_at" class="col-span-2 border-t border-gray-200 pt-3 dark:border-dark-600">
            <p class="mb-2 text-xs font-medium text-purple-600 dark:text-purple-400">{{ t('payment.admin.refundRequestInfo') }}</p>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestedAt') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{ formatDateTime(selectedOrder.refund_requested_at) }}</p>
              </div>
              <div>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestedBy') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">#{{ selectedOrder.refund_requested_by }}</p>
              </div>
              <div class="col-span-2">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.refundRequestReason') }}</p>
                <p class="text-sm text-gray-700 dark:text-gray-300">{{ selectedOrder.refund_request_reason }}</p>
              </div>
            </div>
          </div>
        </div>
        <div class="flex justify-end border-t border-gray-200 pt-4 dark:border-dark-600">
          <button v-if="canOpenInvoice(selectedOrder)" class="btn btn-secondary" @click="openInvoice(selectedOrder)">
            <Icon name="document" size="sm" />
            {{ t('payment.orders.invoice') }}
          </button>
        </div>
        <!-- Audit Logs -->
        <div v-if="orderAuditLogs.length > 0" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <p class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('payment.admin.auditLogs') }}</p>
          <div class="max-h-48 space-y-2 overflow-y-auto">
            <div v-for="log in orderAuditLogs" :key="log.id" class="rounded-control border border-gray-100 bg-gray-50 p-2.5 dark:border-dark-600 dark:bg-dark-800">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ log.action }}</span>
                <span class="text-xs text-gray-400">{{ formatDateTime(log.created_at) }}</span>
              </div>
              <div v-if="log.detail" class="mt-1 break-all text-xs text-gray-500 dark:text-gray-400">{{ log.detail }}</div>
              <div v-if="log.operator" class="mt-1 text-xs text-gray-400">{{ t('payment.admin.operator') }}: {{ log.operator }}</div>
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>

    <BaseDialog :show="showForceExpireDialog" :title="t('payment.admin.forceExpireOrder')" width="normal" @close="closeForceExpireDialog">
      <form id="force-expire-form" class="space-y-4" @submit.prevent="handleForceExpireOrder">
        <div class="rounded-control border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-200">
          {{ t('payment.admin.forceExpireWarning') }}
        </div>
        <div v-if="forceExpireTarget" class="rounded-control bg-gray-50 p-3 text-sm dark:bg-dark-700">
          <div class="flex justify-between gap-3">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ forceExpireTarget.id }}</span>
          </div>
          <div class="mt-1 flex justify-between gap-3">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderNo') }}</span>
            <span class="break-all text-right text-gray-900 dark:text-white">{{ forceExpireTarget.out_trade_no }}</span>
          </div>
        </div>
        <div>
          <label for="force-expire-reason" class="input-label">{{ t('payment.admin.forceExpireReason') }}</label>
          <textarea id="force-expire-reason" v-model="forceExpireReason" rows="3" maxlength="500" class="input" :placeholder="t('payment.admin.forceExpireReasonPlaceholder')" required></textarea>
        </div>
        <label class="flex items-start gap-2 text-sm text-red-700 dark:text-red-300">
          <input id="force-expire-confirm" v-model="forceExpireConfirmed" type="checkbox" class="mt-0.5 h-4 w-4 rounded-compact border-gray-300 text-red-600 focus:ring-red-500" />
          <span>{{ t('payment.admin.forceExpireAcknowledge') }}</span>
        </label>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeForceExpireDialog">{{ t('common.cancel') }}</button>
          <button type="submit" form="force-expire-form" :disabled="forceExpireSubmitting || !forceExpireConfirmed || !forceExpireReason.trim()" class="btn btn-danger">
            {{ forceExpireSubmitting ? t('common.processing') : t('payment.admin.confirmForceExpire') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <AdminRefundDialog :show="showRefundDialog" :order="selectedOrder" :submitting="refundSubmitting" :require-force="refundRequireForce" :warning="refundWarning" @confirm="handleRefund" @cancel="closeRefundDialog" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import AdminRefundDialog from '@/components/admin/payment/AdminRefundDialog.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import { formatPaymentAmount } from '@/components/payment/currency'

interface AuditLog {
  id: number
  action: string
  detail: string | null
  operator: string | null
  created_at: string
}

const { t } = useI18n()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

const ordersLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const orderSearch = ref('')
const orderFilters = reactive({ status: '', payment_type: '', order_type: '' })
const showFilterDropdown = ref(false)
const filterDropdownRef = ref<HTMLElement | null>(null)
const activeFilterCount = computed(() => [orderFilters.status, orderFilters.payment_type, orderFilters.order_type].filter(Boolean).length)
const orderPagination = reactive({ page: 1, page_size: 20, total: 0 })
const selectedOrder = ref<PaymentOrder | null>(null)
const showDetailDialog = ref(false)
const showRefundDialog = ref(false)
const showForceExpireDialog = ref(false)
const forceExpireTarget = ref<PaymentOrder | null>(null)
const forceExpireReason = ref('')
const forceExpireConfirmed = ref(false)
const forceExpireSubmitting = ref(false)
const refundSubmitting = ref(false)
const refundRequireForce = ref(false)
const refundWarning = ref('')
const refundQueryingIds = ref(new Set<number>())
const orderAuditLogs = ref<AuditLog[]>([])

function resetOrderFilters() {
  orderFilters.status = ''
  orderFilters.payment_type = ''
  orderFilters.order_type = ''
  orderPagination.page = 1
  loadOrders()
}

function handleOrderClickOutside(event: MouseEvent) {
  const target = event.target
  if (target instanceof Node && filterDropdownRef.value?.contains(target)) return
  showFilterDropdown.value = false
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debounceLoadOrders() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => loadOrders(), 300)
}

async function loadOrders() {
  ordersLoading.value = true
  try {
    const res = await adminPaymentAPI.getOrders({
      page: orderPagination.page, page_size: orderPagination.page_size,
      keyword: orderSearch.value || undefined, status: orderFilters.status || undefined,
      payment_type: orderFilters.payment_type || undefined, order_type: orderFilters.order_type || undefined,
    })
    orders.value = res.data.items || []
    orderPagination.total = res.data.total || 0
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally { ordersLoading.value = false }
}

function handleOrderPageChange(page: number) { orderPagination.page = page; loadOrders() }
function handleOrderPageSizeChange(size: number) { orderPagination.page_size = size; orderPagination.page = 1; loadOrders() }

function formatOrderAmount(amount: number, order: PaymentOrder): string {
  return order.order_type === 'balance' ? formatBalanceAmount(amount, { fractionDigits: 2 }) : formatGatewayAmount(amount, order.currency)
}

function formatGatewayAmount(amount: number, currency?: string | null): string {
  return formatPaymentAmount(amount, currency)
}

const selectedOrderFeeAmount = computed(() => {
  const order = selectedOrder.value
  if (!order) return 0
  if ((order.fee_amount || 0) > 0) return order.fee_amount
  if ((order.fee_rate || 0) <= 0) return 0
  return order.pay_amount - order.pay_amount / (1 + order.fee_rate / 100)
})

const statusFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allStatuses') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'PROCESSING', label: t('payment.status.processing') },
  { value: 'PAID', label: t('payment.status.paid') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'EXPIRED', label: t('payment.status.expired') },
  { value: 'CANCELLED', label: t('payment.status.cancelled') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
  { value: 'REFUND_REQUESTED', label: t('payment.status.refund_requested') },
  { value: 'REFUND_PENDING', label: t('payment.status.refund_pending') },
  { value: 'REFUND_FAILED', label: t('payment.status.refund_failed') },
])

const paymentTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allPaymentTypes') },
  { value: 'alipay', label: t('payment.methods.alipay') },
  { value: 'wxpay', label: t('payment.methods.wxpay') },
  { value: 'stripe', label: t('payment.methods.stripe') },
  { value: 'airwallex', label: t('payment.methods.airwallex') },
])

const orderTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allOrderTypes') },
  { value: 'balance', label: t('payment.admin.balanceOrder') },
  { value: 'subscription', label: t('payment.admin.subscriptionOrder') },
])

async function showOrderDetail(order: PaymentOrder) {
  selectedOrder.value = order
  orderAuditLogs.value = []
  showDetailDialog.value = true
  try {
    const res = await adminPaymentAPI.getOrder(order.id)
    const data = res.data as unknown as Record<string, unknown>
    if (data.order) selectedOrder.value = data.order as PaymentOrder
    orderAuditLogs.value = ((data.auditLogs || data.audit_logs || []) as unknown) as AuditLog[]
  } catch (_err: unknown) { /* keep cached order data */ }
}

async function handleCancelOrder(order: PaymentOrder) {
  try { await adminPaymentAPI.cancelOrder(order.id); appStore.showSuccess(t('payment.admin.orderCancelled')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

function openForceExpireDialog(order: PaymentOrder) {
  forceExpireTarget.value = order
  forceExpireReason.value = ''
  forceExpireConfirmed.value = false
  showForceExpireDialog.value = true
}

function closeForceExpireDialog() {
  showForceExpireDialog.value = false
  forceExpireTarget.value = null
  forceExpireReason.value = ''
  forceExpireConfirmed.value = false
}

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
    if (extractApiErrorCode(err) === 'ORDER_STATUS_CHANGED') {
      closeForceExpireDialog()
      await loadOrders()
    }
  } finally {
    forceExpireSubmitting.value = false
  }
}

async function handleRetryOrder(order: PaymentOrder) {
  try { await adminPaymentAPI.retryRecharge(order.id); appStore.showSuccess(t('payment.admin.retrySuccess')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

function canOpenInvoice(order: PaymentOrder | null): boolean {
  if (!order || (order.payment_type || '').trim() !== 'stripe') return false
  return ['PAID', 'RECHARGING', 'COMPLETED', 'PARTIALLY_REFUNDED', 'REFUNDED'].includes(order.status)
}

async function openInvoice(order: PaymentOrder) {
  try {
    const res = await adminPaymentAPI.getOrderInvoice(order.id)
    const doc = res.data
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

function openRefundDialog(order: PaymentOrder) {
  selectedOrder.value = order
  refundRequireForce.value = false
  refundWarning.value = ''
  showRefundDialog.value = true
}

function closeRefundDialog() {
  showRefundDialog.value = false
  refundRequireForce.value = false
  refundWarning.value = ''
}

function isRefundPendingWarning(warning: string | undefined): boolean {
  return /pending|处理中|待/.test(String(warning || '').toLowerCase())
}

async function handleRefund(data: { amount: number; reason: string; deduct_balance: boolean; force: boolean }) {
  if (!selectedOrder.value) return
  refundSubmitting.value = true
  try {
    const res = await adminPaymentAPI.refundOrder(selectedOrder.value.id, { amount: data.amount, reason: data.reason, deduct_balance: data.deduct_balance, force: data.force })
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (isRefundPendingWarning(res.data.warning)) {
      appStore.showWarning(t('payment.admin.refundPending'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (res.data.require_force) {
      // 后端要求管理员显式确认强制退款时，保留弹窗并展示警告与确认项。
      refundRequireForce.value = true
      refundWarning.value = res.data.warning || ''
      return
    }
    appStore.showError(res.data.warning || t('common.error'))
  } catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { refundSubmitting.value = false }
}

async function handleQueryRefund(order: PaymentOrder) {
  refundQueryingIds.value = new Set(refundQueryingIds.value).add(order.id)
  try {
    const res = await adminPaymentAPI.queryRefund(order.id)
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
    } else if (isRefundPendingWarning(res.data.warning)) {
      appStore.showWarning(t('payment.admin.refundPending'))
    } else {
      appStore.showError(res.data.warning || t('common.error'))
    }
    loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    const next = new Set(refundQueryingIds.value)
    next.delete(order.id)
    refundQueryingIds.value = next
  }
}

function formatDateTime(dateStr: string): string { return formatOrderDateTime(dateStr) }

onMounted(() => {
  loadOrders()
  document.addEventListener('click', handleOrderClickOutside)
})

onUnmounted(() => document.removeEventListener('click', handleOrderClickOutside))
</script>

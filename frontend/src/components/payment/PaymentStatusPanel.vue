<template>
  <!-- Payment instructions use a centered single column with a bounded width. -->
  <div class="mx-auto w-full max-w-md">
    <!-- Terminal results remain visible until the user confirms dismissal. -->

    <!-- Payment success. -->
    <div v-if="outcome === 'success'" class="card p-6 text-center sm:p-8">
      <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-green-500/10">
        <Icon
          name="check"
          size="lg"
          class="text-green-600 dark:text-green-400"
          :animate-on-hover="false"
        />
      </div>
      <p class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">
        {{
          props.orderType === 'subscription'
            ? t('payment.result.subscriptionSuccess')
            : t('payment.result.success')
        }}
      </p>
      <dl
        v-if="paidOrder"
        class="mt-6 divide-y divide-gray-100 border-y border-gray-100 text-left text-sm dark:divide-dark-700 dark:border-dark-700"
      >
        <div class="flex justify-between gap-4 py-2.5">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.orders.orderId') }}</dt>
          <dd class="font-medium text-gray-900 dark:text-white">#{{ paidOrder.id }}</dd>
        </div>
        <div v-if="paidOrder.out_trade_no" class="flex justify-between gap-4 py-2.5">
          <dt class="shrink-0 text-gray-500 dark:text-dark-400">
            {{ t('payment.orders.orderNo') }}
          </dt>
          <dd
            class="min-w-0 break-all text-right font-mono text-xs leading-5 text-gray-900 dark:text-white"
          >
            {{ paidOrder.out_trade_no }}
          </dd>
        </div>
        <div class="flex justify-between gap-4 py-2.5">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.orders.amount') }}</dt>
          <dd class="font-medium tabular-nums text-gray-900 dark:text-white">
            {{
              formatOrderAmount(paidOrder.amount, paidOrder.order_type, paidOrder.amount_currency)
            }}
          </dd>
        </div>
        <div class="flex justify-between gap-4 py-2.5">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.orders.payAmount') }}</dt>
          <dd class="font-medium tabular-nums text-gray-900 dark:text-white">
            {{ formatGatewayAmount(paidOrder.pay_amount) }}
          </dd>
        </div>
      </dl>
      <button class="btn btn-primary mt-6 w-full" @click="handleDone">
        {{ t('common.confirm') }}
      </button>
    </div>

    <!-- Cancelled payment. -->
    <div v-else-if="outcome === 'cancelled'" class="card p-6 text-center sm:p-8">
      <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-gray-500/10">
        <Icon name="x" size="lg" class="text-gray-500 dark:text-dark-400" />
      </div>
      <p class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('payment.qr.cancelled') }}
      </p>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
        {{ t('payment.qr.cancelledDesc') }}
      </p>
      <button class="btn btn-primary mt-6 w-full" @click="handleDone">
        {{ t('common.confirm') }}
      </button>
    </div>

    <!-- Expired or failed payment. -->
    <div v-else-if="outcome === 'expired'" class="card p-6 text-center sm:p-8">
      <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-orange-500/10">
        <Icon name="clock" size="lg" class="text-orange-600 dark:text-orange-400" />
      </div>
      <p class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('payment.qr.expired') }}
      </p>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{
        isEPUSDT ? t('payment.crypto.expiredHint') : t('payment.qr.expiredDesc')
      }}</p>
      <button class="btn btn-primary mt-6 w-full" @click="handleDone">
        {{ t('common.confirm') }}
      </button>
    </div>

    <!-- The gateway accepted processing and the panel waits for an asynchronous terminal status. -->
    <div v-else-if="isProcessing" class="card p-6 text-center sm:p-8">
      <Icon
        name="loader"
        size="xl"
        class="mx-auto animate-spin text-primary-500"
        :animate-on-hover="false"
      />
      <p class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('payment.result.processing') }}
      </p>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
        {{ t('payment.result.processingHint') }}
      </p>
    </div>

    <!-- Pending payment uses QR instructions or an external checkout window. -->

    <!-- Mobile Alipay handoff hides QR fallback until the launch deadline, leaving time for the application switch to finish. -->
    <template v-else-if="isMobileAlipayDeepLink">
      <div v-if="!deepLinkFallbackVisible" class="card overflow-hidden">
        <div class="p-6 text-center sm:p-8">
          <Icon
            v-if="deepLinkState === 'launching'"
            name="loader"
            size="xl"
            class="mx-auto animate-spin text-[#00AEEF]"
            :animate-on-hover="false"
          />
          <div
            v-else
            class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-[#00AEEF]/10"
          >
            <Icon name="checkCircle" size="lg" class="text-[#00AEEF]" :animate-on-hover="false" />
          </div>
          <p class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">
            {{
              deepLinkState === 'backgrounded'
                ? t('payment.qr.alipayContinueInApp')
                : t('payment.qr.alipayOpening')
            }}
          </p>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
            {{ t('payment.qr.alipayWaitingHint') }}
          </p>
          <button
            v-if="deepLinkState === 'backgrounded'"
            data-test="reopen-alipay"
            class="btn btn-alipay mt-5 inline-flex items-center gap-2 text-sm"
            @click="reopenAlipay"
          >
            <Icon name="externalLink" size="sm" />
            {{ t('payment.qr.reopenAlipay') }}
          </button>
        </div>
        <div
          class="flex items-center justify-between gap-4 border-t border-gray-100 px-6 py-3 text-sm dark:border-dark-700"
        >
          <span class="flex items-center gap-2 text-gray-500 dark:text-dark-400">
            <span
              class="h-2 w-2 animate-pulse rounded-full bg-primary-500"
              aria-hidden="true"
            ></span>
            {{ t('payment.qr.waitingPayment') }}
          </span>
          <span class="text-gray-500 dark:text-dark-400">
            {{ t('payment.qr.expiresIn') }}
            <span class="ml-1 font-semibold tabular-nums text-gray-900 dark:text-white">{{
              countdownDisplay
            }}</span>
          </span>
        </div>
      </div>
      <div v-else data-test="alipay-qr-fallback" class="card p-6">
        <div class="text-center">
          <p class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('payment.qr.alipayFallbackTitle') }}
          </p>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
            {{ t('payment.qr.alipayFallbackHint') }}
          </p>
        </div>
        <dl
          class="mt-5 divide-y divide-gray-100 border-y border-gray-100 text-sm dark:divide-dark-700 dark:border-dark-700"
        >
          <div class="flex items-start justify-between gap-4 py-2.5">
            <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.orders.payAmount') }}</dt>
            <dd class="font-semibold tabular-nums text-gray-900 dark:text-white">
              {{ displayPaymentAmount }}
            </dd>
          </div>
          <div class="flex items-start justify-between gap-4 py-2.5">
            <dt class="shrink-0 text-gray-500 dark:text-dark-400">
              {{ t('payment.orders.orderNo') }}
            </dt>
            <dd
              class="min-w-0 break-all text-right font-mono text-xs leading-5 text-gray-900 dark:text-white"
            >
              {{ displayOrderNumber }}
            </dd>
          </div>
          <div class="flex items-start justify-between gap-4 py-2.5">
            <dt class="text-gray-500 dark:text-dark-400">{{ t('payment.qr.expiresIn') }}</dt>
            <dd class="font-semibold tabular-nums text-gray-900 dark:text-white">
              {{ countdownDisplay }}
            </dd>
          </div>
        </dl>
        <div class="mt-5 flex justify-center">
          <div :class="['relative rounded-control border bg-white p-3', qrBorderClass]">
            <canvas ref="qrCanvas" class="mx-auto"></canvas>
            <div class="pointer-events-none absolute inset-0 flex items-center justify-center">
              <span :class="['rounded-full p-2 shadow ring-2 ring-white', qrLogoBgClass]">
                <img :src="qrLogoIcon" alt="" class="h-5 w-5 brightness-0 invert" />
              </span>
            </div>
          </div>
        </div>
        <p class="mt-4 text-center text-sm leading-6 text-gray-600 dark:text-dark-300">
          {{ t('payment.qr.alipaySaveAndScanHint') }}
        </p>
        <div class="mt-5 grid gap-2 sm:grid-cols-2">
          <button
            data-test="reopen-alipay"
            class="btn btn-alipay inline-flex items-center justify-center gap-2"
            @click="reopenAlipay"
          >
            <Icon name="externalLink" size="sm" />
            {{ t('payment.qr.reopenAlipay') }}
          </button>
          <button
            data-test="save-alipay-qr"
            class="btn btn-secondary inline-flex items-center justify-center gap-2"
            @click="saveQRCode"
          >
            <Icon name="download" size="sm" />
            {{ t('payment.qr.saveQRCode') }}
          </button>
        </div>
        <button class="btn btn-secondary mt-2 w-full" @click="handleDone">
          {{ t('payment.result.backToRecharge') }}
        </button>
      </div>
    </template>

    <!-- QR payment instructions. -->
    <div v-else-if="showQRCode" class="card overflow-hidden">
      <div class="p-6 text-center">
        <p class="text-sm font-medium text-gray-500 dark:text-dark-400">{{ scanTitle }}</p>
        <p class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
          {{ displayPaymentAmount }}
        </p>
        <!-- The QR retains a white background in both themes so its dark modules remain recognizable by scanning applications. -->
        <div
          :class="[
            'relative mx-auto mt-5 inline-block rounded-control border bg-white p-3',
            qrBorderClass,
          ]"
        >
          <canvas ref="qrCanvas" class="mx-auto"></canvas>
          <div class="pointer-events-none absolute inset-0 flex items-center justify-center">
            <span :class="['rounded-full p-2 shadow ring-2 ring-white', qrLogoBgClass]">
              <img :src="qrLogoIcon" alt="" class="h-5 w-5 brightness-0 invert" />
            </span>
          </div>
        </div>
        <p v-if="scanHint" class="mt-4 text-sm text-gray-500 dark:text-dark-400">{{ scanHint }}</p>
        <button v-if="payUrl" class="btn btn-secondary mt-4 text-sm" @click="reopenPopup">
          {{ t('payment.qr.openPayWindow') }}
        </button>
      </div>
      <div
        class="flex items-center justify-between gap-4 border-t border-gray-100 px-6 py-3 text-sm dark:border-dark-700"
      >
        <span class="flex items-center gap-2 text-gray-500 dark:text-dark-400">
          <span class="h-2 w-2 animate-pulse rounded-full bg-primary-500" aria-hidden="true"></span>
          {{ t('payment.qr.waitingPayment') }}
        </span>
        <span class="text-gray-500 dark:text-dark-400">
          {{ t('payment.qr.expiresIn') }}
          <span class="ml-1 font-semibold tabular-nums text-gray-900 dark:text-white">{{
            countdownDisplay
          }}</span>
        </span>
      </div>
      <div class="border-t border-gray-100 p-4 dark:border-dark-700">
        <button class="btn btn-secondary w-full" :disabled="cancelling" @click="handleCancel">
          {{ cancelling ? t('common.processing') : t('payment.qr.cancelOrder') }}
        </button>
      </div>
    </div>

    <!-- GMPay confirms fiat order amounts while the hosted cashier owns token selection and transfer instructions. -->
    <div v-else-if="isEPUSDT" class="card p-6 sm:p-8">
      <div class="text-center">
        <Icon
          name="recharge"
          size="xl"
          class="mx-auto text-primary-600"
          :animate-on-hover="false"
        />
        <h2 class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">{{
          payUrl ? t('payment.crypto.waiting') : t('payment.result.processing')
        }}</h2>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-300">{{
          payUrl ? t('payment.crypto.waitingHint') : t('payment.crypto.creationUnconfirmed')
        }}</p>
        <p class="mt-6 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">{{
          displayPaymentAmount
        }}</p>
      </div>
      <div class="mt-6 grid gap-2 sm:grid-cols-2">
        <button v-if="payUrl" class="btn btn-primary" @click="reopenPopup"
          >{{ t('payment.crypto.continue')
          }}<Icon name="externalLink" size="sm" :animate-on-hover="false"
        /></button>
        <button class="btn btn-secondary" @click="pollStatus">{{
          t('payment.crypto.checkStatus')
        }}</button>
      </div>
      <dl class="mt-6 space-y-3 border-t border-gray-100 pt-4 text-xs dark:border-dark-700">
        <div class="flex justify-between gap-4"
          ><dt class="shrink-0 text-gray-500 dark:text-dark-300">{{
            t('payment.orders.orderNo')
          }}</dt
          ><dd class="min-w-0 break-all text-right font-mono text-gray-900 dark:text-white">{{
            displayOrderNumber
          }}</dd></div
        >
        <div class="flex justify-between gap-4"
          ><dt class="text-gray-500 dark:text-dark-300">{{ t('payment.qr.expiresIn') }}</dt
          ><dd class="font-semibold tabular-nums text-gray-900 dark:text-white">{{
            countdownDisplay
          }}</dd></div
        >
      </dl>
      <p
        class="mt-4 rounded-control bg-gray-50 p-4 text-xs leading-6 text-gray-500 dark:bg-dark-950 dark:text-dark-300"
        >{{ t('payment.crypto.confirmationHint') }}</p
      >
      <button class="btn btn-secondary mt-4 w-full" :disabled="cancelling" @click="handleCancel">{{
        cancelling ? t('common.processing') : t('payment.qr.cancelOrder')
      }}</button>
    </div>

    <!-- Popup or redirected checkout. -->
    <div v-else class="card overflow-hidden">
      <div class="p-6 text-center sm:p-8">
        <Icon
          name="loader"
          size="xl"
          class="mx-auto animate-spin text-primary-500"
          :animate-on-hover="false"
        />
        <p class="mt-4 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
          {{ displayPaymentAmount }}
        </p>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('payment.qr.payInNewWindowHint') }}
        </p>
        <button v-if="payUrl" class="btn btn-secondary mt-5 text-sm" @click="reopenPopup">
          {{ t('payment.qr.openPayWindow') }}
        </button>
      </div>
      <div
        class="flex items-center justify-between gap-4 border-t border-gray-100 px-6 py-3 text-sm dark:border-dark-700"
      >
        <span class="flex items-center gap-2 text-gray-500 dark:text-dark-400">
          <span class="h-2 w-2 animate-pulse rounded-full bg-primary-500" aria-hidden="true"></span>
          {{ t('payment.qr.waitingPayment') }}
        </span>
        <span class="font-semibold tabular-nums text-gray-900 dark:text-white">{{
          countdownDisplay
        }}</span>
      </div>
      <div class="border-t border-gray-100 p-4 dark:border-dark-700">
        <button class="btn btn-secondary w-full" :disabled="cancelling" @click="handleCancel">
          {{ cancelling ? t('common.processing') : t('payment.qr.cancelOrder') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePaymentStore } from '@/stores/payment'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import {
  getPaymentPopupFeatures,
  isBuiltInAlipayMethod,
  isBuiltInWxpayMethod,
} from '@/utils/payment/paymentMethods'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatPaymentAmount, normalizePaymentCurrency } from '@/utils/payment/currency'
import type { PaymentOrder } from '@/api/payment.types'
import Icon from '@/components/icons/Icon.vue'
import QRCode from 'qrcode'
import alipayIcon from '@/assets/icons/alipay.svg'
import wxpayIcon from '@/assets/icons/wxpay.svg'
import paymentIcon from '@/assets/icons/payment.svg'
import {
  createAlipayDeepLinkLauncher,
  type AlipayDeepLinkLauncher,
  type AlipayDeepLinkState,
} from '@/utils/payment/alipayDeepLink'

/**
 * PaymentStatusPanel.vue owns the waiting state for a created checkout order, including QR
 * rendering, popup reopening, mobile Alipay handoff, cancellation, and status recovery.
 * Its parent receives settlement and success events and decides when to leave the purchase
 * page or clear stored checkout data.
 *
 * Polling distinguishes gateway PROCESSING from terminal payment outcomes and slows its
 * cadence while processing. Pending wallet orders can request bounded upstream
 * verification when callbacks have not updated local state. Cancellation that reports an
 * already-paid order resolves that order before choosing a terminal presentation. Timer
 * and deep-link cleanup is shared by settlement, dismissal, and component teardown, while
 * repeated matching outcome publication is suppressed.
 */

const props = defineProps<{
  orderId: number // internal payment order identifier

  /**
   * amount and payAmount supply the waiting charge display. A truthy payAmount takes
   * precedence, then a truthy amount, with zero displayed when both values are falsy.
   */
  amount?: number // selected purchase amount

  payAmount?: number // gateway charge amount

  qrCode: string // gateway QR payment payload

  expiresAt: string // gateway expiry timestamp

  paymentType: string // visible payment method code

  outTradeNo?: string // gateway order trade reference

  payUrl?: string // hosted payment browser destination

  orderType?: string // balance or subscription purchase

  /**
   * currency seeds charge formatting until a fetched paidOrder supplies a truthy currency,
   * which takes precedence before shared currency normalization.
   */
  currency?: string

  /**
   * mobileAlipayDeepLink enables handoff only when true, the payment method is an exact
   * built-in Alipay alias, and the QR payload is nonempty.
   */
  mobileAlipayDeepLink?: boolean
}>()

/**
 * PaymentOutcome describes the terminal panel presentation emitted to its parent.
 */
type PaymentOutcome = 'success' | 'cancelled' | 'expired'

const emit = defineEmits<{
  done: [] // waiting panel dismissal event

  success: [] // resolved payment success event

  settled: [outcome: PaymentOutcome] // terminal panel outcome event
}>()

const i18n = useI18n()
const { t } = i18n
const paymentStore = usePaymentStore()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

// The waiting panel owns its QR canvas, countdown display, cancellation state, resolved order,
// and mobile handoff presentation until settlement or teardown.
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const qrUrl = ref('')
const remainingSeconds = ref(0)
const cancelling = ref(false)
const isProcessing = ref(false)
const paidOrder = ref<PaymentOrder | null>(null)
const deepLinkState = ref<AlipayDeepLinkState>('idle')
const deepLinkFallbackVisible = ref(false)
const paymentCurrency = computed(() =>
  normalizePaymentCurrency(paidOrder.value?.currency || props.currency),
)
const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String(
      (
        raw as {
          value?: string // reactive locale string value
        }
      ).value || '',
    )
  }
  return undefined
})

// Terminal outcome: null = still active, 'success' | 'cancelled' | 'expired'
const outcome = ref<PaymentOutcome | null>(null)

// Polling, countdown, and the Alipay launcher belong to this panel instance; cleanup releases
// their resources when the panel settles or unmounts.
let pollTimer: ReturnType<typeof setInterval> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null
let pollIntervalMs = 0
let verifyAttempts = 0
let lastVerifyAt = 0
let alipayLauncher: AlipayDeepLinkLauncher | null = null

// Polling and verification intervals are milliseconds; processing slows local checks, while
// upstream recovery also has a bounded attempt budget.
const PENDING_POLL_INTERVAL_MS = 3000
const PROCESSING_POLL_INTERVAL_MS = 15000
const VERIFY_RETRY_INTERVAL_MS = 15000
const VERIFY_RETRY_MAX_ATTEMPTS = 6

// Wallet identity controls instruction text and artwork, while the deep-link marker decides
// when a mobile Alipay QR becomes visible.
const isEPUSDT = computed(() => props.paymentType === 'epusdt')
const isAlipay = computed(() => isBuiltInAlipayMethod(props.paymentType))
const isWxpay = computed(() => isBuiltInWxpayMethod(props.paymentType))
const isMobileAlipayDeepLink = computed(
  () => props.mobileAlipayDeepLink === true && isAlipay.value && !!qrUrl.value,
)
const showQRCode = computed(
  () => !!qrUrl.value && (!isMobileAlipayDeepLink.value || deepLinkFallbackVisible.value),
)

const qrBorderClass = computed(() => {
  if (isAlipay.value) return 'border-[#00AEEF]/40'
  if (isWxpay.value) return 'border-[#2BB741]/40'
  return 'border-gray-200 dark:border-dark-600'
})

const qrLogoBgClass = computed(() => {
  if (isAlipay.value) return 'bg-[#00AEEF]'
  if (isWxpay.value) return 'bg-[#2BB741]'
  return 'bg-gray-400'
})

const qrLogoIcon = computed(() => {
  if (isAlipay.value) return alipayIcon
  if (isWxpay.value) return wxpayIcon
  return paymentIcon
})

const scanTitle = computed(() => {
  if (isAlipay.value) return t('payment.qr.scanAlipay')
  if (isWxpay.value) return t('payment.qr.scanWxpay')
  return t('payment.qr.scanToPay')
})

const scanHint = computed(() => {
  if (isAlipay.value) return t('payment.qr.scanAlipayHint')
  if (isWxpay.value) return t('payment.qr.scanWxpayHint')
  return ''
})

const countdownDisplay = computed(() => {
  const m = Math.floor(remainingSeconds.value / 60)
  const s = remainingSeconds.value % 60
  return m.toString().padStart(2, '0') + ':' + s.toString().padStart(2, '0')
})

const displayPaymentAmount = computed(() =>
  formatGatewayAmount(props.payAmount || props.amount || 0),
)
const displayOrderNumber = computed(() => props.outTradeNo || `#${props.orderId}`)

/**
 * formatOrderAmount formats recharge quantities in balance units and subscription amounts
 * in their saved price currency. An absent price currency uses the resolved payment
 * currency, allowing older order responses to retain their monetary labels.
 */
function formatOrderAmount(amount: number, orderType: string, amountCurrency?: string): string {
  return orderType === 'balance'
    ? formatBalanceAmount(amount, { fractionDigits: 2 })
    : formatGatewayAmount(amount, amountCurrency)
}

/**
 * formatGatewayAmount renders a charge using a truthy supplied currency or the panel's
 * resolved currency. Shared formatting normalizes malformed labels to CNY, follows the
 * currency's Intl precision, and displays nonfinite amounts as zero.
 */
function formatGatewayAmount(value: number, currency?: string | null): string {
  return formatPaymentAmount(value, currency || paymentCurrency.value, localeCode.value)
}

/**
 * isSuccessStatus recognizes completed, paid, and recharging payment-success states.
 */
function isSuccessStatus(status: string | null | undefined): boolean {
  return status === 'COMPLETED' || status === 'PAID' || status === 'RECHARGING'
}

/**
 * upstreamVerificationOutTradeNo supplies the trade reference for supported direct
 * verification paths. Stripe, WeChat, and mobile Alipay precreate can use it; other
 * payment presentations return an empty reference.
 */
function upstreamVerificationOutTradeNo(): string {
  if (props.paymentType !== 'stripe' && !isWxpay.value && !isMobileAlipayDeepLink.value) return ''
  return props.outTradeNo || ''
}

/**
 * reopenPopup opens the configured checkout destination for the current order.
 * Mobile GMPay checkout navigates in the current browser. Other presentations use
 * the shared payment popup, with current-page navigation when that popup is blocked.
 */
function reopenPopup() {
  if (props.payUrl) {
    if (isEPUSDT.value && isMobileDevice()) {
      // Mobile buyers continue in the current browser so returning from their wallet
      // retains the existing checkout recovery record and its merchant order identity.
      window.location.href = props.payUrl
      return
    }
    const win = window.open(props.payUrl, 'paymentPopup', getPaymentPopupFeatures())
    // A popup can be blocked after an earlier asynchronous checkout step; current-window
    // navigation keeps the same order payable.
    if (!win || win.closed) {
      window.location.href = props.payUrl
    }
  }
}

/**
 * setOutcome publishes a terminal payment outcome and emits its settlement event.
 * Repeating the current outcome is ignored, while a different outcome replaces the
 * displayed terminal state and emits again.
 */
function setOutcome(next: PaymentOutcome) {
  if (outcome.value === next) return
  outcome.value = next
  emit('settled', next)
}

/**
 * renderQR waits for Vue to expose the canvas before drawing payment instructions. Hidden
 * mobile deep-link fallback and missing QR data skip drawing, keeping application handoff
 * separate from QR presentation.
 */
async function renderQR() {
  await nextTick()
  if (!showQRCode.value || !qrCanvas.value || !qrUrl.value) return
  await QRCode.toCanvas(qrCanvas.value, qrUrl.value, {
    width: 220,
    margin: 2,
    errorCorrectionLevel: 'M',
  })
}

/**
 * updateDeepLinkState applies a launcher update to the mobile Alipay presentation.
 * Fallback reveals and renders the QR, while backgrounding hides it after an observed
 * application handoff; other states retain its current visibility.
 */
function updateDeepLinkState(state: AlipayDeepLinkState) {
  deepLinkState.value = state
  if (state === 'fallback') {
    deepLinkFallbackVisible.value = true
    renderQR()
  } else if (state === 'backgrounded') {
    deepLinkFallbackVisible.value = false
  }
}

/**
 * reopenAlipay asks the active mobile launcher to retry application handoff.
 */
function reopenAlipay() {
  alipayLauncher?.launch()
}

/**
 * saveQRCode downloads the current QR canvas as a PNG named for the order. An absent
 * canvas is ignored; the temporary anchor is attached for the click and removed
 * immediately afterward.
 */
function saveQRCode() {
  const canvas = qrCanvas.value
  if (!canvas) return
  const link = document.createElement('a')
  link.href = canvas.toDataURL('image/png')
  link.download = `alipay-${props.outTradeNo || props.orderId}.png`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

// The request guard spans local lookup and optional upstream recovery, preventing another
// interval tick from issuing overlapping work for this panel.
let pollInFlight = false
/**
 * pollStatus refreshes this order while preventing overlapping requests and updates after
 * a known outcome. Stripe can verify its trade reference upstream directly; other paths
 * first query the local order and may then recover a pending wallet order upstream. The
 * outcome is checked after each await before publishing the result, because cancellation
 * or another operation may have settled the panel while the request was in progress. The
 * in-flight flag is released in finally.
 */
async function pollStatus() {
  if (!props.orderId || outcome.value || pollInFlight) return
  pollInFlight = true
  try {
    // Stripe verifies upstream directly; pending WeChat and Alipay precreate orders throttle
    // upstream checks to recover settlement when callbacks have not updated local state.
    const upstreamOutTradeNo = isProcessing.value ? '' : upstreamVerificationOutTradeNo()
    let order =
      upstreamOutTradeNo && props.paymentType === 'stripe'
        ? await verifyOrderWithUpstream(upstreamOutTradeNo)
        : await paymentStore.pollOrderStatus(props.orderId)
    if (outcome.value) return
    order = await tryRecoverPendingOrder(order)
    if (outcome.value) return
    applyResolvedOrderStatus(order)
  } finally {
    pollInFlight = false
  }
}

/**
 * applyResolvedOrderStatus updates the panel from a fetched order and reports whether it
 * handled the status. PROCESSING keeps the order active and lowers polling frequency.
 * Payment success stores the resolved order, cleans resources, and emits settlement before
 * success navigation is requested. Cancellation, expiry, and failure clean resources and
 * publish their terminal outcome; absent orders and unrecognized pending statuses leave
 * the current waiting state available for later polls.
 */
function applyResolvedOrderStatus(order: PaymentOrder | null): boolean {
  if (!order) return false
  if (order.status === 'PROCESSING') {
    enterProcessingState()
    return true
  }
  if (isSuccessStatus(order.status)) {
    cleanup()
    paidOrder.value = order
    setOutcome('success')
    emit('success')
    return true
  } else if (order.status === 'CANCELLED') {
    cleanup()
    setOutcome('cancelled')
    return true
  } else if (order.status === 'EXPIRED' || order.status === 'FAILED') {
    cleanup()
    setOutcome('expired')
    return true
  }
  return false
}

/**
 * enterProcessingState stops the expiry countdown and selects slower order polling. The
 * gateway has accepted processing, so a local countdown cannot mark the still-settling
 * payment as expired while server status is pending.
 */
function enterProcessingState() {
  isProcessing.value = true
  // Releasing the countdown handle prevents another expiry tick from competing with processing
  // or terminal-state updates for the same order.
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  startPolling(PROCESSING_POLL_INTERVAL_MS)
}

/**
 * verifyOrderWithUpstream requests provider verification by trade reference. When
 * verification fails it logs the error and queries the local order, allowing an already
 * recorded webhook result to complete the panel.
 */
async function verifyOrderWithUpstream(outTradeNo: string): Promise<PaymentOrder | null> {
  try {
    const response = await paymentAPI.verifyOrder(outTradeNo)
    return response.data
  } catch (err: unknown) {
    console.warn('[payment] Failed to verify order upstream:', err)
    // Failed upstream verification can still have a recorded webhook result locally, so
    // query that order before leaving the panel waiting.
    return paymentStore.pollOrderStatus(props.orderId)
  }
}

/**
 * tryRecoverPendingOrder supplements a local pending wallet order with upstream
 * verification. It accepts Alipay, WeChat, and GMPay methods with a trade reference, applies both
 * a time interval and a maximum attempt count, and records the attempt before awaiting the
 * provider. A failed or empty verification retains the local order. This bounds recovery
 * traffic while allowing a missing callback to be observed through provider status without
 * discarding the server state already fetched.
 */
async function tryRecoverPendingOrder(order: PaymentOrder | null): Promise<PaymentOrder | null> {
  if (!order || (!isWxpay.value && !isAlipay.value && !isEPUSDT.value)) return order
  const outTradeNo = String(order.out_trade_no || props.outTradeNo || '').trim()
  if (!outTradeNo) return order
  if (
    String(order.status || '')
      .trim()
      .toUpperCase() !== 'PENDING'
  )
    return order
  const now = Date.now()
  if (
    verifyAttempts >= VERIFY_RETRY_MAX_ATTEMPTS ||
    now - lastVerifyAt < VERIFY_RETRY_INTERVAL_MS
  ) {
    return order
  }

  // Record the attempt before awaiting verification so overlapping status work cannot reuse the
  // same retry interval or attempt count.
  lastVerifyAt = now
  verifyAttempts += 1
  try {
    const response = await paymentAPI.verifyOrder(outTradeNo)
    return response.data ?? order
  } catch {
    // Verification failure retains the locally fetched order so the panel can continue polling
    // within its existing recovery attempt budget.
    return order
  }
}

/**
 * startCountdown starts or replaces the expiry display using seconds. PROCESSING
 * suppresses countdown work, and reaching zero triggers a status poll instead of declaring
 * expiry locally. This allows a payment accepted near its deadline to resolve from server
 * status. Replacing a running countdown clears its interval first, while the polling owner
 * continues to choose terminal outcomes from order responses and cancellation results.
 */
function startCountdown(seconds: number) {
  if (isProcessing.value) return
  remainingSeconds.value = Math.max(0, seconds)
  if (remainingSeconds.value <= 0) {
    void pollStatus()
    return
  }
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  countdownTimer = setInterval(() => {
    if (isProcessing.value) return
    remainingSeconds.value--
    if (remainingSeconds.value <= 0) {
      remainingSeconds.value = 0
      if (countdownTimer) clearInterval(countdownTimer)
      countdownTimer = null
      void pollStatus()
    }
  }, 1000)
}

/**
 * handleCancel sends one cancellation request while guarding repeated clicks. A successful
 * cancellation stops timers, but an already-paid response first resolves the order through
 * the available verification path. If that lookup still has no handled terminal or
 * processing state, countdown and polling restart with their remaining state. Other
 * successful cancellations publish cancelled; API failures keep the panel available and
 * show the translated error, and the cancelling flag clears in finally.
 */
async function handleCancel() {
  if (!props.orderId || cancelling.value) return
  cancelling.value = true
  try {
    const response = await paymentAPI.cancelOrder(props.orderId)
    cleanup()
    if (
      (
        response.data as
          | {
              message?: string // gateway response message text
            }
          | undefined
      )?.message === 'already_paid'
    ) {
      const upstreamOutTradeNo = upstreamVerificationOutTradeNo()
      const order = upstreamOutTradeNo
        ? await verifyOrderWithUpstream(upstreamOutTradeNo)
        : await paymentStore.pollOrderStatus(props.orderId)
      if (!applyResolvedOrderStatus(order)) {
        startCountdown(remainingSeconds.value)
        startPolling()
      }
    } else {
      setOutcome('cancelled')
    }
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    cancelling.value = false
  }
}

/**
 * handleDone clears polling and countdown timers and disposes the Alipay launcher before
 * emitting the parent's dismissal event. Already-running requests continue and can still
 * apply their results when the panel has no terminal outcome.
 */
function handleDone() {
  cleanup()
  emit('done')
}

/**
 * cleanup cancels polling and countdown intervals and disposes the mobile Alipay launcher.
 * It clears handles for later reuse; an already running network request continues through
 * the poll function's outcome checks.
 */
function cleanup() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  pollIntervalMs = 0
  alipayLauncher?.dispose()
  alipayLauncher = null
}

/**
 * startPolling sets the status interval in milliseconds and replaces a different cadence.
 * Reusing the active cadence leaves its timer untouched, while cleanup releases the
 * interval on settlement or unmount.
 */
function startPolling(intervalMs = PENDING_POLL_INTERVAL_MS) {
  if (pollTimer && pollIntervalMs === intervalMs) return
  if (pollTimer) clearInterval(pollTimer)
  pollIntervalMs = intervalMs
  pollTimer = setInterval(pollStatus, intervalMs)
}

// Initialize timers during setup so status and countdown updates start for this order; mounted
// QR and deep-link work follows once DOM resources exist.
qrUrl.value = props.qrCode
isProcessing.value = false
verifyAttempts = 0
lastVerifyAt = 0
// An absent gateway deadline uses thirty minutes of countdown display until server order
// status supplies its terminal result.
let seconds = 30 * 60
if (props.expiresAt) {
  // Convert the gateway expiry difference from milliseconds to whole seconds for the countdown
  // display, discarding the partial second.
  seconds = Math.floor((new Date(props.expiresAt).getTime() - Date.now()) / 1000)
}
startCountdown(seconds)
startPolling()
renderQR()

watch([() => qrUrl.value, showQRCode], () => renderQR())
// The mobile launcher needs browser lifecycle sources after mount; panel cleanup disposes its
// visibility listeners and fallback deadline together.
onMounted(() => {
  if (!isMobileAlipayDeepLink.value) return
  alipayLauncher = createAlipayDeepLinkLauncher({
    qrCode: qrUrl.value,
    document,
    lifecycleTarget: window,
    userAgent: window.navigator.userAgent,
    assignLocation: (url) => window.location.assign(url),
    onStateChange: updateDeepLinkState,
  })
  alipayLauncher.launch()
})
onUnmounted(() => cleanup())
</script>

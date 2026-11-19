<template>
  <div class="flex min-h-screen items-center justify-center bg-slate-50 p-4 dark:bg-slate-950">
    <div
      v-content-reveal="route.path"
      class="w-full max-w-md space-y-4 rounded-surface border border-slate-200 bg-white p-6 shadow-lg dark:border-dark-600 dark:bg-slate-900"
    >
      <!-- Amount + Order ID -->
      <div v-if="amount" class="text-center">
        <p class="text-3xl font-bold" :style="{ color: methodColor }">¥{{ amount }}</p>
        <p v-if="orderId" class="mt-1 text-sm text-gray-500 dark:text-slate-400">
          {{ t('payment.orders.orderId') }}: {{ orderId }}
        </p>
      </div>

      <!-- Error -->
      <div v-if="error" class="space-y-3">
        <div
          class="rounded-control border border-red-200 bg-red-50 p-3 text-sm text-red-600 dark:border-red-700 dark:bg-red-900/30 dark:text-red-400"
        >
          {{ error }}
        </div>
        <button
          class="w-full text-sm underline dark:text-blue-400 dark:hover:text-blue-300"
          :style="{ color: methodColor }"
          @click="closeWindow"
        >
          {{ t('common.close') }}
        </button>
      </div>

      <!-- Success -->
      <div v-else-if="success" class="space-y-3 py-4 text-center">
        <Icon
          name="checkCircle"
          size="xl"
          :animate-on-hover="false"
          class="mx-auto h-12 w-12 text-green-600 dark:text-green-400"
        />
        <p class="text-sm text-gray-500 dark:text-dark-300">{{ t('payment.result.success') }}</p>
        <button
          class="text-sm underline dark:text-blue-400 dark:hover:text-blue-300"
          :style="{ color: methodColor }"
          @click="closeWindow"
        >
          {{ t('common.close') }}
        </button>
      </div>

      <!-- Loading / Redirecting -->
      <div v-else class="flex items-center justify-center py-8">
        <div class="spinner h-8 w-8" :style="{ color: methodColor }" />
        <span class="ml-3 text-sm text-gray-500 dark:text-dark-300">{{ hint }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import Icon from '@/components/icons/Icon.vue'

import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import { paymentAPI } from '@/api/payment'

/**
 * StripePopupView.vue initializes a wallet checkout after a same-origin opener supplies
 * the Stripe client secret and publishable key. The popup announces readiness, accepts one
 * initialization message, and displays an error when the opener does not initialize it
 * before the deadline.
 *
 * Alipay delegates navigation to Stripe; WeChat may return a succeeded intent or continue
 * through authenticated order polling. A shared in-flight flag prevents interval ticks
 * from overlapping, and PROCESSING lowers the polling cadence. Teardown releases the
 * polling timer, initialization timeout, and remaining message listener. The independent
 * short window-close delays execute after accepted success and are not retained in the
 * cleanup state.
 */

/** StripeWithWechatPay describes the wallet confirmation method used by this popup. */
interface StripeWithWechatPay {
  /**
   * confirmWechatPayPayment requests Stripe WeChat confirmation using this order's
   * secret and client options. Its response can contain an error, a completed intent,
   * or QR continuation data for the owning page.
   */
  confirmWechatPayPayment(
    clientSecret: string,
    options: Record<string, unknown>,
  ): Promise<{
    error?: {
      message?: string // gateway response message text
    } // Stripe confirmation failure details

    paymentIntent?: {
      status: string // Stripe intent lifecycle status
    } // Stripe confirmation result data
  }>
}

// Wallet colors identify the chosen Stripe method; unrecognized values use the default Stripe
// accent in the popup presentation.
const METHOD_COLORS: Record<string, string> = {
  alipay: '#00AEEF',
  wechat_pay: '#07C160',
}
const DEFAULT_METHOD_COLOR = '#635bff'

const { t } = useI18n()
const route = useRoute()

// The popup reads order identity and its selected wallet from route data before waiting for
// the opener to supply SDK credentials.
const orderId = String(route.query.order_id || '')
const method = String(route.query.method || 'alipay')
const amount = String(route.query.amount || '')

const methodColor = computed(() => METHOD_COLORS[method] || DEFAULT_METHOD_COLOR)

// Popup error, success, and hint state remain local to this window while its opener keeps the
// purchase-page checkout state.
const error = ref('')
const success = ref(false)
const hint = ref(t('payment.stripePopup.redirecting'))

// The popup owns polling, initialization deadline, and message listener handles; unmount
// releases those resources while in-flight confirmations complete independently.
let pollTimer: ReturnType<typeof setInterval> | null = null
let pollIntervalMs = 0
let pollInFlight = false
let initTimeoutTimer: ReturnType<typeof setTimeout> | null = null
let messageHandler: ((event: MessageEvent) => void) | null = null

// Polling cadence values are milliseconds and distinguish pending wallet payment from the
// slower checks used while the gateway is processing.
const PENDING_POLL_INTERVAL_MS = 3000
const PROCESSING_POLL_INTERVAL_MS = 15000

/**
 * closeWindow asks the browser to close the current checkout popup.
 */
function closeWindow() {
  window.close()
}

/**
 * clearInitTimeout cancels the opener initialization deadline and clears its handle. It is
 * called after accepting an initialization message and during teardown, allowing payment
 * to continue beyond the initialization period.
 */
function clearInitTimeout() {
  if (initTimeoutTimer) {
    clearTimeout(initTimeoutTimer)
    initTimeoutTimer = null
  }
}

onMounted(() => {
  messageHandler = (event: MessageEvent) => {
    if (event.origin !== window.location.origin) return
    if (event.data?.type !== 'STRIPE_POPUP_INIT') return
    // Receiving INIT cancels the initialization deadline so a wallet QR can remain open while
    // the user completes payment.
    clearInitTimeout()
    if (messageHandler) {
      window.removeEventListener('message', messageHandler)
      messageHandler = null
    }
    initStripe(event.data.clientSecret, event.data.publishableKey)
  }
  window.addEventListener('message', messageHandler)

  if (window.opener) {
    window.opener.postMessage({ type: 'STRIPE_POPUP_READY' }, window.location.origin)
  }

  // The deadline covers an opener that never supplies STRIPE_POPUP_INIT; accepted
  // initialization cancels it before starting SDK work.
  initTimeoutTimer = setTimeout(() => {
    if (!error.value && !success.value) {
      error.value = t('payment.stripePopup.timeout')
    }
  }, 15000)
})

onUnmounted(() => {
  stopPolling()
  clearInitTimeout()
  if (messageHandler) {
    window.removeEventListener('message', messageHandler)
    messageHandler = null
  }
})

/**
 * initStripe validates received credentials, loads Stripe, and starts the selected wallet
 * flow. Missing credentials or SDK initialization failures remain visible in the popup.
 * Alipay confirmation supplies a result URL and lets Stripe navigate; WeChat confirmation
 * either reports an error, schedules closing for a succeeded intent, or starts local order
 * polling. The initialization message listener is removed before this function is called,
 * so another opener message cannot initialize the same popup again.
 */
async function initStripe(clientSecret: string, publishableKey: string) {
  if (!clientSecret || !publishableKey) {
    error.value = t('payment.stripeMissingParams')
    return
  }
  try {
    const { loadStripe } = await import('@stripe/stripe-js/pure')
    const stripe = await loadStripe(publishableKey)
    if (!stripe) {
      error.value = t('payment.stripeLoadFailed')
      return
    }

    const returnUrl =
      window.location.origin + '/payment/result?order_id=' + orderId + '&status=success'

    if (method === 'alipay') {
      // Alipay confirmation navigates this popup to the gateway while the opener retains the
      // purchase page and its recovery state.
      const { error: err } = await stripe.confirmAlipayPayment(clientSecret, {
        return_url: returnUrl,
      })
      if (err) error.value = err.message || t('payment.result.failed')
    } else if (method === 'wechat_pay') {
      // Stripe presents its WeChat QR flow, then local order polling continues after
      // confirmation returns to observe payment settlement.
      hint.value = t('payment.stripePopup.loadingQr')
      const result = await (stripe as unknown as StripeWithWechatPay).confirmWechatPayPayment(
        clientSecret,
        {
          payment_method_options: {
            wechat_pay: { client: isMobileDevice() ? 'mobile_web' : 'web' },
          },
        },
      )
      if (result.error) {
        error.value = result.error.message || t('payment.result.failed')
      } else if (result.paymentIntent?.status === 'succeeded') {
        success.value = true
        setTimeout(closeWindow, 2000)
      } else {
        startPolling()
      }
    }
  } catch (err: unknown) {
    error.value = extractI18nErrorMessage(err, t, 'payment.errors', t('payment.stripeLoadFailed'))
  }
}

/**
 * stopPolling cancels the current order polling interval and clears its remembered
 * cadence. An already running fetch continues through its normal response handling;
 * clearing the timer prevents further scheduled requests.
 */
function stopPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = null
  pollIntervalMs = 0
}

/**
 * pollOrderStatus fetches the order through paymentAPI using the shared client's
 * authentication, token refresh, and locale interceptors. It rejects overlapping ticks,
 * switches PROCESSING to the slower cadence, and stops polling for payment-success or
 * terminal failure statuses. HTTP failures and exceptions leave the next interval
 * available to retry. PAID and RECHARGING display payment success, while the backend
 * remains responsible for fulfillment; the popup schedules closing after accepting
 * that success state.
 */
async function pollOrderStatus() {
  // Polling can outlast its interval, so the in-flight guard keeps subsequent ticks from
  // issuing overlapping requests for this order.
  if (pollInFlight) return
  pollInFlight = true
  try {
    const res = await paymentAPI.getOrder(orderId)
    const status = res.data?.status
    if (status === 'PROCESSING') {
      hint.value = t('payment.result.processing')
      startPolling(PROCESSING_POLL_INTERVAL_MS)
    } else if (status === 'COMPLETED' || status === 'PAID' || status === 'RECHARGING') {
      stopPolling()
      success.value = true
      setTimeout(closeWindow, 2000)
    } else if (status === 'EXPIRED' || status === 'CANCELLED' || status === 'FAILED') {
      stopPolling()
      error.value = status === 'CANCELLED' ? t('payment.qr.cancelled') : t('payment.qr.expired')
    }
  } catch {
    /* A failed lookup leaves the interval running so the next scheduled tick can observe this order again. */
  } finally {
    pollInFlight = false
  }
}

/**
 * startPolling installs or replaces the order polling interval in milliseconds. Repeated
 * requests for the current cadence reuse its timer, and callers release the timer on
 * terminal status or component teardown.
 */
function startPolling(intervalMs = PENDING_POLL_INTERVAL_MS) {
  if (pollTimer && pollIntervalMs === intervalMs) return
  if (pollTimer) clearInterval(pollTimer)
  pollIntervalMs = intervalMs
  pollTimer = setInterval(pollOrderStatus, intervalMs)
}
</script>

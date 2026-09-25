<template>
  <div class="flex min-h-screen items-center justify-center bg-slate-50 p-4 dark:bg-slate-950">
    <div
      class="w-full max-w-md space-y-4 rounded-surface border border-slate-200 bg-white p-6 shadow-lg dark:border-slate-700 dark:bg-slate-900"
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
        <div class="text-5xl text-green-600 dark:text-green-400">✓</div>
        <p class="text-sm text-gray-500 dark:text-slate-400">{{ t('payment.result.success') }}</p>
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
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-t-transparent"
          :style="{ borderColor: methodColor, borderTopColor: 'transparent' }"
        />
        <span class="ml-3 text-sm text-gray-500 dark:text-slate-400">{{ hint }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import { buildApiUrl } from '@/api/client'

interface StripeWithWechatPay {
  confirmWechatPayPayment(clientSecret: string, options: Record<string, unknown>): Promise<{ error?: { message?: string }; paymentIntent?: { status: string } }>
}

const METHOD_COLORS: Record<string, string> = {
  alipay: '#00AEEF',
  wechat_pay: '#07C160',
}
const DEFAULT_METHOD_COLOR = '#635bff'

const { t } = useI18n()
const route = useRoute()

const orderId = String(route.query.order_id || '')
const method = String(route.query.method || 'alipay')
const amount = String(route.query.amount || '')

const methodColor = computed(() => METHOD_COLORS[method] || DEFAULT_METHOD_COLOR)

const error = ref('')
const success = ref(false)
const hint = ref(t('payment.stripePopup.redirecting'))

let pollTimer: ReturnType<typeof setInterval> | null = null
let pollIntervalMs = 0
let pollInFlight = false
let initTimeoutTimer: ReturnType<typeof setTimeout> | null = null
let messageHandler: ((event: MessageEvent) => void) | null = null

const PENDING_POLL_INTERVAL_MS = 3000
const PROCESSING_POLL_INTERVAL_MS = 15000

function closeWindow() { window.close() }

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
    // INIT 已到达，取消兜底超时，避免长时间的扫码支付被误判为超时。
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

  // 仅兜底“父窗口始终未发 STRIPE_POPUP_INIT”的场景。
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

async function initStripe(clientSecret: string, publishableKey: string) {
  if (!clientSecret || !publishableKey) {
    error.value = t('payment.stripeMissingParams')
    return
  }
  try {
    const { loadStripe } = await import('@stripe/stripe-js/pure')
    const stripe = await loadStripe(publishableKey)
    if (!stripe) { error.value = t('payment.stripeLoadFailed'); return }

    const returnUrl = window.location.origin + '/payment/result?order_id=' + orderId + '&status=success'

    if (method === 'alipay') {
      // 支付宝在当前弹窗中跳转到支付页面。
      const { error: err } = await stripe.confirmAlipayPayment(clientSecret, { return_url: returnUrl })
      if (err) error.value = err.message || t('payment.result.failed')
    } else if (method === 'wechat_pay') {
      // 微信支付由 Stripe 展示内置二维码，关闭二维码后继续查询本地订单。
      hint.value = t('payment.stripePopup.loadingQr')
      const result = await (stripe as unknown as StripeWithWechatPay).confirmWechatPayPayment(clientSecret, {
        payment_method_options: { wechat_pay: { client: isMobileDevice() ? 'mobile_web' : 'web' } },
      })
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

function stopPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = null
  pollIntervalMs = 0
}

async function pollOrderStatus() {
  // 防重入：接口响应慢于轮询间隔时避免并发重叠请求。
  if (pollInFlight) return
  pollInFlight = true
  try {
    // 认证令牌固定存储在 auth_token，弹窗查询也必须携带它。
    const token = localStorage.getItem('auth_token') || ''
    const res = await fetch(buildApiUrl(`/payment/orders/${orderId}`), {
      headers: token ? { Authorization: 'Bearer ' + token } : {},
      credentials: 'include',
    })
    if (!res.ok) return
    const data = await res.json()
    const status = data?.data?.status
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
  } catch { /* 查询失败时由下一轮重试。 */ } finally {
    pollInFlight = false
  }
}

function startPolling(intervalMs = PENDING_POLL_INTERVAL_MS) {
  if (pollTimer && pollIntervalMs === intervalMs) return
  if (pollTimer) clearInterval(pollTimer)
  pollIntervalMs = intervalMs
  pollTimer = setInterval(pollOrderStatus, intervalMs)
}
</script>

import { computed, ref, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePaymentStore } from '@/stores/payment'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { useAppStore } from '@/stores'
import type {
  BillingInfo,
  CheckoutInfoResponse,
  CreateOrderResult,
  OrderType,
  SubscriptionPlan,
} from '@/api/payment.types'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import { getPaymentPopupFeatures } from '@/utils/payment/paymentMethods'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  buildCreateOrderPayload,
  clearPaymentRecoverySnapshot,
  decidePaymentLaunch,
  getVisibleMethods,
  normalizeVisibleMethod,
  readPaymentRecoverySnapshot,
  type PaymentRecoverySnapshot,
  writePaymentRecoverySnapshot,
} from '@/utils/payment/paymentFlow'
import { buildPaymentErrorToastMessage, describePaymentScenarioError } from './paymentUx'
import {
  hasWechatResumeQuery,
  parseWechatResumeRoute,
  stripWechatResumeQuery,
} from './paymentWechatResume'

/**
 * usePaymentCheckout.ts owns order creation, browser payment launch, and local
 * recovery for the purchase page. Checkout replies select hosted redirects,
 * embedded SDK pages, QR waiting, or WeChat authorization while the page keeps
 * the editable amount, plan, and billing details.
 *
 * An accepted launch writes the waiting state before opening another browser
 * destination, allowing a later visit to restore the order. WeChat authorization
 * carries selection in its return URL, and its resume query is removed before
 * another create request. Mobile gateway failures can create a desktop QR order;
 * only a QR reply becomes the displayed recovery state for that attempt.
 * Unconfirmed GMPay creation keeps the committed order in the waiting panel
 * with no cashier link until its signed callback resolves payment.
 */

/** CheckoutInputs supplies reactive selection and billing data owned by the page. */
interface CheckoutInputs {
  checkout: Ref<CheckoutInfoResponse> // loaded checkout configuration
  selectedMethod: Ref<string> // selected visible method
  selectedPlan: Ref<SubscriptionPlan | null> // selected subscription plan
  amount: Ref<number | null> // entered recharge amount
  /**
   * buildStripeBillingInfo reads the page draft at request creation time and
   * returns undefined when the selected method does not use Stripe billing.
   */
  buildStripeBillingInfo: () => BillingInfo | undefined // normalized billing details
}

/**
 * usePaymentCheckout creates the purchase page's submission and recovery state.
 * Call restorePayment after loading available methods, then await the WeChat
 * resume operation before applying ordinary plan query selection. Browser SDK
 * destinations own their own polling and cleanup after navigation.
 *
 * The returned refs drive the existing waiting panel. Completion clears stored
 * recovery before account refresh or result navigation, while a launch writes
 * recovery before leaving the page. Callers retain ownership of selection and
 * supply billing values at the moment each request is constructed.
 */
export function usePaymentCheckout(inputs: CheckoutInputs) {
  // Inputs retain page-owned purchase and billing selection; stores, router, and local refs
  // below own the corresponding submission and recovery work.
  const { checkout, selectedMethod, selectedPlan, amount, buildStripeBillingInfo } = inputs
  const { t } = useI18n()
  const route = useRoute()
  const router = useRouter()
  const authStore = useAuthStore()
  const paymentStore = usePaymentStore()
  const subscriptionStore = useSubscriptionStore()
  const appStore = useAppStore()
  const validAmount = computed(() => amount.value ?? 0)
  const visibleMethods = computed(() => getVisibleMethods(checkout.value.methods))
  const submitting = ref(false)
  const errorMessage = ref('')
  const errorHintMessage = ref('')
  const paymentPhase = ref<'select' | 'paying'>('select')

  /**
   * CreateOrderOptions carries authorization return data and retry state for order creation.
   */
  interface CreateOrderOptions {
    openid?: string // WeChat authorized user identifier
    wechatResumeToken?: string // backend resume intent token
    paymentType?: string // requested visible method override
    isResume?: boolean // authorization resume caller marker
    mobileQrFallbackAttempted?: boolean // desktop retry already attempted
  }

  /**
   * WeixinJSBridgeLike describes the payment invocation exposed by the WeChat browser.
   */
  interface WeixinJSBridgeLike {
    /** invoke starts a named WeChat action and reports its callback result. */
    invoke(
      action: string,
      payload: Record<string, unknown>,
      callback: (result: Record<string, unknown>) => void,
    ): void
  }

  /**
   * emptyPaymentState creates an unselected checkout snapshot for the waiting panel. Zero
   * identifiers and empty destination fields represent the selection phase; persistence
   * skips this state because it has no order identifier.
   */
  function emptyPaymentState(): PaymentRecoverySnapshot {
    return {
      orderId: 0,
      amount: 0,
      qrCode: '',
      expiresAt: '',
      paymentType: '',
      payUrl: '',
      outTradeNo: '',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 0,
      orderType: '',
      paymentMode: '',
      resumeToken: '',
      alipayMobilePrecreateDeepLink: false,
      createdAt: 0,
    }
  }

  /**
   * getWeixinJSBridge reads the optional WeChat bridge installed on window.
   */
  function getWeixinJSBridge(): WeixinJSBridgeLike | undefined {
    return (
      window as Window & {
        WeixinJSBridge?: WeixinJSBridgeLike // optional browser WeChat bridge
      }
    ).WeixinJSBridge
  }

  /**
   * waitForWeixinJSBridge waits for a bridge readiness event or the timeout in milliseconds.
   * An already installed bridge resolves immediately. The two supported readiness event
   * names and the timer share a completion guard, so whichever finishes first removes both
   * listeners and cancels the timer. Timeout reads the bridge again before returning null
   * because installation may have happened without an observed readiness event. Callers
   * treat null as an unavailable JSAPI environment.
   */
  function waitForWeixinJSBridge(timeoutMs = 4000): Promise<WeixinJSBridgeLike | null> {
    const existing = getWeixinJSBridge()
    if (existing) return Promise.resolve(existing)

    return new Promise((resolve) => {
      // Readiness events and the deadline can arrive together; the first completion releases
      // both listeners and the timer.
      let settled = false
      /**
       * finish accepts the first bridge readiness or timeout result and resolves the
       * waiting promise. The settled flag changes before listener removal and timer
       * cancellation, so another queued readiness callback sees completion immediately.
       * Both event names and the deadline share this operation because each can
       * observe the same installed bridge. Callers pass null when the event or
       * timeout fires before a bridge is available.
       */
      const finish = (bridge: WeixinJSBridgeLike | null) => {
        if (settled) return
        settled = true
        document.removeEventListener('WeixinJSBridgeReady', handleReady)
        document.removeEventListener('onWeixinJSBridgeReady', handleReady)
        window.clearTimeout(timer)
        resolve(bridge)
      }
      /** handleReady samples the bridge installed by the browser readiness event. */
      const handleReady = () => finish(getWeixinJSBridge() ?? null)
      const timer = window.setTimeout(() => finish(getWeixinJSBridge() ?? null), timeoutMs)
      document.addEventListener('WeixinJSBridgeReady', handleReady, false)
      document.addEventListener('onWeixinJSBridgeReady', handleReady, false)
    })
  }

  /**
   * invokeWechatJsapiPayment waits for WeChat readiness and invokes its payment action.
   * Missing readiness throws WECHAT_JSAPI_UNAVAILABLE; bridge callback values resolve as a
   * record, with a missing value represented by an empty record.
   */
  async function invokeWechatJsapiPayment(
    payload: Record<string, unknown>,
  ): Promise<Record<string, unknown>> {
    const bridge = await waitForWeixinJSBridge()
    if (!bridge) {
      throw new Error('WECHAT_JSAPI_UNAVAILABLE')
    }
    return new Promise((resolve) => {
      bridge.invoke('getBrandWCPayRequest', payload, (result) => resolve(result || {}))
    })
  }

  // The waiting snapshot belongs to this checkout instance and is replaced as one value when a launch is accepted or reset.
  const paymentState = ref<PaymentRecoverySnapshot>(emptyPaymentState())

  /**
   * persistRecoverySnapshot writes an identified order to browser local storage. Server-side
   * execution and the empty selection state are skipped; storage failures propagate to the
   * checkout operation that owns recovery.
   */
  function persistRecoverySnapshot(snapshot: PaymentRecoverySnapshot) {
    if (typeof window === 'undefined' || !snapshot.orderId) return
    writePaymentRecoverySnapshot(window.localStorage, snapshot, PAYMENT_RECOVERY_STORAGE_KEY)
  }

  /**
   * removeRecoverySnapshot removes this browser's checkout recovery entry when window
   * exists. The caller controls its ordering relative to payment state changes, account
   * refresh, and navigation to the result page.
   */
  function removeRecoverySnapshot() {
    if (typeof window === 'undefined') return
    clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY)
  }

  /**
   * resetPayment returns checkout to selection and discards its stored recovery entry. The
   * displayed order state is cleared before storage removal; callers copy needed order
   * identifiers before invoking this reset.
   */
  function resetPayment() {
    paymentPhase.value = 'select'
    paymentState.value = emptyPaymentState()
    removeRecoverySnapshot()
  }

  /**
   * redirectToPaymentResult navigates with the available order identifier, trade reference,
   * and resume token. Missing identifiers are omitted from query parameters, and the
   * returned promise tracks router navigation completion.
   */
  async function redirectToPaymentResult(state: PaymentRecoverySnapshot): Promise<void> {
    const query: Record<string, string | undefined> = {}
    if (state.orderId > 0) {
      query.order_id = String(state.orderId)
    }
    if (state.outTradeNo) {
      query.out_trade_no = state.outTradeNo
    }
    if (state.resumeToken) {
      query.resume_token = state.resumeToken
    }
    await router.push({
      path: '/payment/result',
      query,
    })
  }

  /**
   * buildWechatOAuthAuthorizeUrl adds the selected purchase to the OAuth return destination.
   * The outer authorization URL keeps its existing parameters, while its redirect parameter
   * carries the visible method, order type, optional plan, and positive amount. Relative
   * URLs resolve against the current origin, and the nested destination is serialized as a
   * path and query. Invalid URLs return their trimmed input so the existing browser
   * navigation path can handle the value.
   */
  function buildWechatOAuthAuthorizeUrl(
    authorizeUrl: string,
    context: {
      paymentType: string // visible payment method code

      orderType: OrderType // balance or subscription purchase

      planId?: number // selected subscription plan identifier

      orderAmount: number // requested purchase amount
    },
  ): string {
    const normalizedUrl = authorizeUrl.trim()
    if (!normalizedUrl || typeof window === 'undefined') {
      return normalizedUrl
    }

    try {
      const targetUrl = new URL(normalizedUrl, window.location.origin)
      const redirectPath = targetUrl.searchParams.get('redirect') || '/purchase'
      const redirectUrl = new URL(redirectPath, window.location.origin)
      const paymentType =
        normalizeVisibleMethod(context.paymentType) || context.paymentType.trim() || 'wxpay'

      redirectUrl.searchParams.set('payment_type', paymentType)
      redirectUrl.searchParams.set('order_type', context.orderType)

      if (context.planId) {
        redirectUrl.searchParams.set('plan_id', String(context.planId))
      } else {
        redirectUrl.searchParams.delete('plan_id')
      }

      if (context.orderAmount > 0) {
        redirectUrl.searchParams.set('amount', String(context.orderAmount))
      } else {
        redirectUrl.searchParams.delete('amount')
      }

      targetUrl.searchParams.set('redirect', `${redirectUrl.pathname}${redirectUrl.search}`)
      return targetUrl.toString()
    } catch {
      // Invalid URL parsing leaves the supplied authorization destination intact so navigation
      // can still follow the gateway response.
      return normalizedUrl
    }
  }

  /**
   * onPaymentDone clears checkout selection after the waiting panel is dismissed.
   * Subscription orders also refresh active subscriptions in the background; a failed
   * refresh leaves the store's current list available.
   */
  function onPaymentDone() {
    const wasSubscription = paymentState.value.orderType === 'subscription'
    resetPayment()
    selectedPlan.value = null
    if (wasSubscription) {
      // Refresh failure leaves the current subscription store visible while dismissal or
      // result navigation continues independently.
      subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
    }
  }

  /**
   * onPaymentSuccess copies the completed order before clearing recovery and refreshing
   * the account. The copy retains the result destination even if subsequent page state
   * changes while navigation is pending. Subscription purchases request their active-
   * subscription refresh independently, and a rejected refresh leaves that store
   * unchanged. Recovery removal happens before these follow-up actions so returning to the
   * purchase page does not restore the order after the waiting panel has already reported
   * success.
   */
  async function onPaymentSuccess() {
    const completedPayment = { ...paymentState.value }
    removeRecoverySnapshot()
    authStore.refreshUser()
    if (paymentState.value.orderType === 'subscription') {
      // Refresh failure leaves the current subscription store visible while dismissal or
      // result navigation continues independently.
      subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
    }
    await redirectToPaymentResult(completedPayment)
  }

  /**
   * onPaymentSettled discards recovery after the waiting panel reports settlement. The
   * displayed order remains available until success navigation or dismissal handles the rest
   * of the purchase page state.
   */
  function onPaymentSettled() {
    removeRecoverySnapshot()
  }

  /**
   * createOrder creates an order and executes the launch action chosen from its reply.
   * Callers validate editable billing data before calling; resume options can supply an
   * authorization token or OpenID and override the selected method. The waiting state is
   * published and persisted before opening a payment destination.
   *
   * WeChat cancellation returns to selection, successful JSAPI replies navigate to result
   * verification, and supported mobile failures can retry with desktop QR parameters. Error-
   * specific messages take precedence over generic API errors. The submitting flag is
   * cleared in finally across every launch, redirect, handled failure, and thrown error
   * path.
   */
  async function createOrder(
    orderAmount: number,
    orderType: OrderType,
    planId?: number,
    options: CreateOrderOptions = {},
  ) {
    submitting.value = true
    errorMessage.value = ''
    errorHintMessage.value = ''
    const requestType =
      normalizeVisibleMethod(options.paymentType || selectedMethod.value) ||
      options.paymentType ||
      selectedMethod.value
    try {
      const payload = buildCreateOrderPayload({
        amount: orderAmount,
        paymentType: requestType,
        orderType,
        planId,
        origin: typeof window !== 'undefined' ? window.location.origin : '',
        isMobile: isMobileDevice(),
        isWechatBrowser:
          typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
        billingInfo: buildStripeBillingInfo(),
        forceQRCode: !!(
          checkout.value.alipay_force_qrcode && normalizeVisibleMethod(requestType) === 'alipay'
        ),
        mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      })
      if (options.openid) {
        payload.openid = options.openid
      }
      if (options.wechatResumeToken) {
        payload.wechat_resume_token = options.wechatResumeToken
      }

      const result = (await paymentStore.createOrder(payload)) as CreateOrderResult & {
        resume_token?: string // public order recovery token
      }
      /**
       * openWindow opens the payment popup with the shared checkout dimensions.
       * A blocked or already closed popup redirects the current window so the user
       * still reaches the gateway destination after an accepted order.
       */
      const openWindow = (url: string) => {
        const win = window.open(url, 'paymentPopup', getPaymentPopupFeatures())
        // Browsers can block a popup after the asynchronous create response; use current-
        // window navigation so this order remains payable.
        if (!win || win.closed) {
          window.location.href = url
        }
      }
      const visibleMethod = normalizeVisibleMethod(requestType) || requestType
      // The Stripe button leaves wallet selection to Payment Element; a wallet button supplies
      // its wallet identifier to the embedded page.
      const stripeMethod =
        visibleMethod === 'stripe' ? '' : visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay'
      // A client secret needs an embedded payment route, while hosted Checkout replies retain
      // their supplied URL in the launch decision.
      const stripeRouteUrl =
        result.client_secret && visibleMethod !== 'airwallex'
          ? router.resolve({
              path: '/payment/stripe',
              query: {
                order_id: String(result.order_id),
                client_secret: result.client_secret,
                method: stripeMethod || undefined,
                resume_token: result.resume_token || undefined,
              },
            }).href
          : ''
      const airwallexRouteUrl =
        result.client_secret && result.intent_id
          ? router.resolve({
              path: '/payment/airwallex',
              query: {
                order_id: String(result.order_id),
                out_trade_no: result.out_trade_no || undefined,
                resume_token: result.resume_token || undefined,
              },
            }).href
          : ''
      const decision = decidePaymentLaunch(result, {
        visibleMethod,
        orderType,
        isMobile: isMobileDevice(),
        isWechatBrowser:
          typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
        forceQRCode: !!(checkout.value.alipay_force_qrcode && visibleMethod === 'alipay'),
        mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
        stripePopupUrl: stripeRouteUrl,
        stripeRouteUrl,
        airwallexRouteUrl,
      })

      if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
        window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
          paymentType: visibleMethod,
          orderType,
          planId,
          orderAmount,
        })
        return
      }

      if (decision.kind === 'unhandled') {
        applyScenarioError({ reason: 'UNHANDLED_PAYMENT_SCENARIO' }, visibleMethod)
        return
      }

      // Publish and persist the accepted order before navigation so returning to checkout can
      // recover the same waiting state.
      paymentState.value = decision.paymentState
      paymentPhase.value = 'paying'
      persistRecoverySnapshot(decision.recovery)

      if (decision.kind === 'stripe_popup') {
        openWindow(decision.paymentState.payUrl)
        return
      }
      if (decision.kind === 'stripe_route') {
        window.location.href = decision.paymentState.payUrl
        return
      }
      if (decision.kind === 'airwallex_route') {
        window.location.href = decision.paymentState.payUrl
        return
      }
      if (decision.kind === 'wechat_jsapi' && decision.jsapi) {
        try {
          const jsapiResult = await invokeWechatJsapiPayment(
            decision.jsapi as Record<string, unknown>,
          )
          const errMsg = String(jsapiResult.err_msg || '').toLowerCase()
          if (errMsg.includes('cancel')) {
            appStore.showInfo(t('payment.qr.cancelled'))
            resetPayment()
          } else if (errMsg && !errMsg.includes('ok')) {
            resetPayment()
            const fallbackApplied = await attemptMobileQrFallback(
              { reason: 'WECHAT_JSAPI_FAILED', message: errMsg },
              {
                orderAmount,
                orderType,
                planId,
                paymentType: visibleMethod,
                attempted: options.mobileQrFallbackAttempted === true,
              },
            )
            if (!fallbackApplied) {
              applyScenarioError({ reason: 'WECHAT_JSAPI_FAILED', message: errMsg }, visibleMethod)
            }
          } else {
            const resultState = { ...decision.paymentState }
            resetPayment()
            await redirectToPaymentResult(resultState)
          }
        } catch (err: unknown) {
          resetPayment()
          const fallbackApplied = await attemptMobileQrFallback(err, {
            orderAmount,
            orderType,
            planId,
            paymentType: visibleMethod,
            attempted: options.mobileQrFallbackAttempted === true,
          })
          if (!fallbackApplied) {
            throw err
          }
        }
        return
      }
      if (decision.kind === 'redirect_waiting' && decision.paymentState.payUrl) {
        if (isMobileDevice()) {
          window.location.href = decision.paymentState.payUrl
          return
        }
        openWindow(decision.paymentState.payUrl)
      }
    } catch (err: unknown) {
      const apiErr = err as Record<string, unknown>
      if (apiErr.reason === 'TOO_MANY_PENDING') {
        const metadata = apiErr.metadata as Record<string, unknown> | undefined
        errorMessage.value = t('payment.errors.tooManyPending', { max: metadata?.max || '' })
        errorHintMessage.value = ''
      } else if (apiErr.reason === 'PAYMENT_CREATE_UNCONFIRMED') {
        const metadata = apiErr.metadata as Record<string, unknown> | undefined
        const orderId = Number(metadata?.order_id)
        errorMessage.value = t('payment.crypto.creationUnconfirmed')
        errorHintMessage.value = ''
        if (Number.isSafeInteger(orderId) && orderId > 0) {
          // An uncertain gateway reply still names a committed local order. Enter its
          // waiting panel instead of submitting another checkout for the same purchase.
          paymentState.value = {
            ...emptyPaymentState(),
            orderId,
            amount: Number(metadata?.amount) || orderAmount,
            payAmount: Number(metadata?.pay_amount) || orderAmount,
            currency: String(metadata?.currency || ''),
            outTradeNo: String(metadata?.out_trade_no || ''),
            expiresAt: String(metadata?.expires_at || ''),
            paymentType: 'epusdt',
            orderType,
            paymentMode: 'popup',
            createdAt: Date.now(),
          }
          persistRecoverySnapshot(paymentState.value)
          paymentPhase.value = 'paying'
          return
        }
      } else if (apiErr.reason === 'CANCEL_RATE_LIMITED') {
        errorMessage.value = t('payment.errors.cancelRateLimited')
        errorHintMessage.value = ''
      } else if (
        await attemptMobileQrFallback(err, {
          orderAmount,
          orderType,
          planId,
          paymentType: requestType,
          attempted: options.mobileQrFallbackAttempted === true,
        })
      ) {
        return
      } else {
        const handled = applyScenarioError(
          err,
          normalizeVisibleMethod(options.paymentType || selectedMethod.value) ||
            selectedMethod.value,
        )
        if (!handled) {
          errorMessage.value = extractI18nErrorMessage(
            err,
            t,
            'payment.errors',
            extractApiErrorMessage(err, t('payment.result.failed')),
          )
          errorHintMessage.value = ''
        }
        if (handled) {
          return
        }
      }
      appStore.showError(buildPaymentErrorToastMessage(errorMessage.value, errorHintMessage.value))
    } finally {
      submitting.value = false
    }
  }

  /**
   * MobileQrFallbackContext retains the purchase identity during a mobile QR retry.
   */
  interface MobileQrFallbackContext {
    orderAmount: number // requested purchase amount
    orderType: OrderType // balance or subscription purchase
    planId?: number // selected subscription plan identifier
    paymentType: string // requested visible method override
    attempted: boolean // desktop retry already attempted
  }

  /**
   * shouldFallbackToDesktopQr reports whether a mobile failure permits a desktop QR retry.
   * An already attempted retry is rejected, and only listed WeChat or Alipay failures
   * qualify; unrelated providers keep their normal error handling.
   */
  function shouldFallbackToDesktopQr(
    err: unknown,
    paymentMethod: string,
    attempted: boolean,
  ): boolean {
    if (attempted || !isMobileDevice()) {
      return false
    }

    const normalizedMethod = normalizeVisibleMethod(paymentMethod) || paymentMethod
    const reason =
      typeof err === 'object' && err && 'reason' in err && typeof err.reason === 'string'
        ? err.reason
        : ''
    const message =
      err instanceof Error
        ? err.message
        : typeof err === 'object' && err && 'message' in err && typeof err.message === 'string'
          ? err.message
          : ''
    const normalizedMessage = message.toLowerCase()

    if (normalizedMethod === 'wxpay') {
      return (
        reason === 'WECHAT_H5_NOT_AUTHORIZED' ||
        reason === 'WECHAT_PAYMENT_MP_NOT_CONFIGURED' ||
        reason === 'WECHAT_JSAPI_FAILED' ||
        reason === 'PAYMENT_GATEWAY_ERROR' ||
        reason === 'UNHANDLED_PAYMENT_SCENARIO' ||
        normalizedMessage.includes('weixinjsbridge is unavailable') ||
        normalizedMessage.includes('wechat_jsapi_unavailable')
      )
    }

    if (normalizedMethod === 'alipay') {
      return reason === 'PAYMENT_GATEWAY_ERROR' || reason === 'UNHANDLED_PAYMENT_SCENARIO'
    }

    return false
  }

  /**
   * attemptMobileQrFallback requests a desktop-style order after an eligible mobile failure.
   * It receives the original amount, order type, and plan so the replacement checkout refers
   * to the same intended purchase. Only a QR waiting decision with a nonempty QR payload is
   * accepted.
   *
   * The retry clears current error text, replaces the waiting state, and persists recovery
   * before warning the user. Unsupported replies and retry errors return false, leaving the
   * caller to report the initiating failure through its normal scenario or API message
   * handling.
   */
  async function attemptMobileQrFallback(
    err: unknown,
    context: MobileQrFallbackContext,
  ): Promise<boolean> {
    if (!shouldFallbackToDesktopQr(err, context.paymentType, context.attempted)) {
      return false
    }

    try {
      const visibleMethod = normalizeVisibleMethod(context.paymentType) || context.paymentType
      const payload = buildCreateOrderPayload({
        amount: context.orderAmount,
        paymentType: visibleMethod,
        orderType: context.orderType,
        planId: context.planId,
        origin: typeof window !== 'undefined' ? window.location.origin : '',
        isMobile: false,
        isWechatBrowser: false,
        billingInfo: buildStripeBillingInfo(),
      })
      const result = (await paymentStore.createOrder(payload)) as CreateOrderResult & {
        resume_token?: string // public order recovery token
      }
      const stripeMethod = visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay'
      // Desktop retries still supply embedded Stripe routing data; hosted URLs are resolved by
      // decidePaymentLaunch before the QR-only acceptance check.
      const stripeRouteUrl = result.client_secret
        ? router.resolve({
            path: '/payment/stripe',
            query: {
              order_id: String(result.order_id),
              client_secret: result.client_secret,
              method: stripeMethod,
              resume_token: result.resume_token || undefined,
            },
          }).href
        : ''
      const decision = decidePaymentLaunch(result, {
        visibleMethod,
        orderType: context.orderType,
        isMobile: false,
        isWechatBrowser: false,
        stripePopupUrl: stripeRouteUrl,
        stripeRouteUrl,
      })

      if (decision.kind !== 'qr_waiting' || !decision.paymentState.qrCode) {
        return false
      }

      errorMessage.value = ''
      errorHintMessage.value = ''
      // Publish and persist the accepted order before navigation so returning to checkout can
      // recover the same waiting state.
      paymentState.value = decision.paymentState
      paymentPhase.value = 'paying'
      persistRecoverySnapshot(decision.recovery)
      appStore.showWarning(t('payment.errors.mobilePaymentFallbackToQr'))
      return true
    } catch {
      // A failed QR retry reports false so its caller retains the initiating mobile error and
      // the existing error-message precedence.
      return false
    }
  }

  /**
   * applyScenarioError publishes a recognized provider and browser-specific failure message.
   * Unrecognized failures clear these fields and return false so the caller can use its
   * generic API error path.
   */
  function applyScenarioError(err: unknown, paymentMethod: string): boolean {
    const descriptor = describePaymentScenarioError(err, {
      paymentMethod,
      isMobile: isMobileDevice(),
      isWechatBrowser:
        typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
    })
    if (!descriptor) {
      errorMessage.value = ''
      errorHintMessage.value = ''
      return false
    }
    errorMessage.value = t(descriptor.messageKey)
    errorHintMessage.value = descriptor.hintKey ? t(descriptor.hintKey) : ''
    appStore.showError(buildPaymentErrorToastMessage(errorMessage.value, errorHintMessage.value))
    return true
  }

  /**
   * resumeWechatPaymentFromQuery restores a purchase selection from WeChat return
   * parameters. It updates visible method and any available amount or plan, then removes
   * authorization parameters through router.replace before creating another order. Token-
   * based resume passes zero amount because the backend resolves the saved intent; OpenID-
   * based resume requires a positive amount. Removing the query first prevents the same
   * authorization values from being consumed again by a subsequent page initialization.
   */
  async function resumeWechatPaymentFromQuery() {
    const resume = parseWechatResumeRoute(route.query, checkout.value.plans, validAmount.value)
    if (!resume) {
      return
    }

    selectedMethod.value = resume.paymentType
    if (resume.orderType === 'balance' && resume.orderAmount > 0) {
      amount.value = resume.orderAmount
    }
    if (resume.orderType === 'subscription' && resume.planId) {
      selectedPlan.value = checkout.value.plans.find((plan) => plan.id === resume.planId) ?? null
    }

    // Consume authorization parameters before another create call, so a later route
    // initialization cannot replay these credentials from its query.
    await router.replace({ path: route.path, query: stripWechatResumeQuery(route.query) })

    if (resume.wechatResumeToken) {
      await createOrder(0, resume.orderType, resume.planId, {
        wechatResumeToken: resume.wechatResumeToken,
        paymentType: resume.paymentType,
        isResume: true,
      })
      return
    }

    if (resume.orderAmount > 0 && resume.openid) {
      await createOrder(resume.orderAmount, resume.orderType, resume.planId, {
        openid: resume.openid,
        paymentType: resume.paymentType,
        isResume: true,
      })
    }
  }

  /**
   * restorePayment applies a valid stored order after checkout methods are loaded. WeChat
   * resume parameters clear the previous snapshot before parsing, giving the authorization
   * return its own order identity. A supplied route token must match the stored entry, and
   * invalid or expired entries are removed. Accepted recovery replaces the waiting state
   * and chooses a known alias or configured custom method; an unrecognized restored method
   * leaves the existing selection unchanged while the recovered order remains displayed.
   */
  function restorePayment() {
    if (typeof window !== 'undefined') {
      if (hasWechatResumeQuery(route.query)) {
        removeRecoverySnapshot()
      }
      const routeResumeToken =
        typeof route.query.resume_token === 'string'
          ? route.query.resume_token
          : typeof route.query.wechat_resume_token === 'string'
            ? route.query.wechat_resume_token
            : undefined
      const restored = readPaymentRecoverySnapshot(
        window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY),
        { resumeToken: routeResumeToken },
      )
      if (restored) {
        paymentState.value = restored
        paymentPhase.value = 'paying'
        const restoredMethod =
          normalizeVisibleMethod(restored.paymentType) ||
          (visibleMethods.value[restored.paymentType] ? restored.paymentType : '')
        if (restoredMethod) {
          selectedMethod.value = restoredMethod
        }
      } else {
        removeRecoverySnapshot()
      }
    }
  }
  return {
    submitting,
    errorMessage,
    errorHintMessage,
    paymentPhase,
    paymentState,
    resetPayment,
    onPaymentDone,
    onPaymentSuccess,
    onPaymentSettled,
    createOrder,
    restorePayment,
    resumeWechatPaymentFromQuery,
  }
}

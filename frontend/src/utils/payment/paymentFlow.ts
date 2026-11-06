import type {
  CreateOrderRequest,
  CreateOrderResult,
  BillingInfo,
  MethodLimit,
  OrderType,
  WechatJSAPIPayload,
  WechatOAuthInfo,
} from '@/api/payment.types'

/**
 * paymentFlow.ts converts checkout requests and gateway replies into browser launch
 * decisions and serializable recovery state. Visible aliases keep the selected wallet
 * identity consistent across provider-specific responses, and recovery stores the order
 * details needed when a user returns from another payment destination.
 *
 * Launch selection gives hosted Stripe URLs precedence over embedded client secrets, then
 * handles Airwallex, WeChat authorization, JSAPI, and wallet QR or redirect modes. Mobile
 * Alipay precreate markers can override ordinary QR preferences. Recovery parsing rejects
 * malformed fields, expired timestamps, and a conflicting supplied resume token while
 * filling fields omitted by older stored snapshots.
 */

// The recovery entry and alias map keep browser storage and user-visible method identities
// consistent across checkout pages.
export const PAYMENT_RECOVERY_STORAGE_KEY = 'payment.recovery.current'

const VISIBLE_METHOD_ALIASES = {
  alipay: 'alipay',
  alipay_direct: 'alipay',
  wxpay: 'wxpay',
  wxpay_direct: 'wxpay',
  stripe: 'stripe',
  airwallex: 'airwallex',
  epusdt: 'epusdt',
} as const

/**
 * VisiblePaymentMethod names the built-in methods presented to checkout users.
 */
export type VisiblePaymentMethod = 'alipay' | 'wxpay' | 'stripe' | 'airwallex' | 'epusdt'
/**
 * StripeVisibleMethod identifies a wallet preselected inside Stripe Payment Element.
 */
export type StripeVisibleMethod = 'alipay' | 'wechat_pay'
/**
 * PaymentLaunchKind identifies the browser action needed for a gateway reply.
 */
export type PaymentLaunchKind =
  | 'qr_waiting' // wait for gateway QR payment
  | 'alipay_deep_link' // launch the Alipay mobile application
  | 'redirect_waiting' // open hosted checkout and retain waiting state
  | 'stripe_popup' // open Stripe wallet checkout in a popup
  | 'stripe_route' // navigate to embedded Stripe checkout
  | 'airwallex_route' // navigate to the Airwallex SDK page
  | 'wechat_oauth' // navigate to WeChat authorization
  | 'wechat_jsapi' // invoke the ready WeChat payment bridge
  | 'unhandled' // report an unsupported launch reply

/**
 * PaymentRecoverySnapshot stores one order and its launch data for browser recovery.
 */
export interface PaymentRecoverySnapshot {
  orderId: number // internal order identifier
  amount: number // selected purchase amount
  qrCode: string // gateway QR payload
  expiresAt: string // gateway expiry timestamp
  paymentType: string // visible payment method
  payUrl: string // browser checkout destination
  outTradeNo: string // upstream verification reference
  clientSecret: string // embedded checkout credential
  intentId: string // Airwallex intent identifier
  currency: string // payment currency code
  countryCode: string // checkout country code
  paymentEnv: string // gateway environment label
  payAmount: number // gateway charge amount
  orderType: OrderType | '' // balance or subscription purchase
  paymentMode: string // gateway launch mode
  resumeToken: string // public order recovery token
  alipayMobilePrecreateDeepLink?: boolean // mobile precreate launch marker
  createdAt: number // milliseconds since Unix epoch
}

/**
 * PaymentLaunchContext supplies selection, device flags, and prebuilt checkout destinations.
 */
export interface PaymentLaunchContext {
  visibleMethod: string // selected visible method
  orderType: OrderType // balance or subscription purchase
  isMobile: boolean // mobile device detection
  isWechatBrowser?: boolean // WeChat browser detection
  /** forceQRCode requests desktop-style Alipay QR handling unless mobile precreate has priority for this checkout. */
  forceQRCode?: boolean
  /** mobilePrecreateDeepLink retains the mobile signal when both Alipay launch settings are enabled for this request. */
  mobilePrecreateDeepLink?: boolean
  now?: number // milliseconds since Unix epoch
  stripePopupUrl?: string // resolved Stripe popup route
  stripeRouteUrl?: string // resolved Stripe checkout route
  airwallexRouteUrl?: string // resolved Airwallex checkout route
}

/**
 * PaymentLaunchDecision pairs a launch action with display and persisted order state.
 */
export interface PaymentLaunchDecision {
  kind: PaymentLaunchKind // selected browser action
  /**
   * paymentState and recovery carry the selected order data for display and storage.
   * They can share the same object, so callers replace snapshots instead of mutating one copy.
   */
  paymentState: PaymentRecoverySnapshot // waiting panel order state
  recovery: PaymentRecoverySnapshot // persisted recovery order state
  stripeMethod?: StripeVisibleMethod // preselected Stripe wallet method
  oauth?: WechatOAuthInfo // WeChat authorization response
  jsapi?: WechatJSAPIPayload // WeChat bridge request payload
}

/**
 * BuildCreateOrderPayloadInput carries editable checkout data before API field conversion.
 */
export interface BuildCreateOrderPayloadInput {
  amount: number // selected purchase amount
  paymentType: string // visible payment method
  orderType: OrderType // balance or subscription purchase
  planId?: number // selected subscription plan identifier
  origin?: string // browser origin for return URL
  isMobile: boolean // mobile device detection
  isWechatBrowser: boolean // WeChat browser detection
  billingInfo?: BillingInfo // normalized optional billing details
  /** forceQRCode reports a desktop request to the backend when the visible Alipay method needs QR output. */
  forceQRCode?: boolean
  /** mobilePrecreateDeepLink keeps the actual mobile signal so the backend can create an Alipay precreate order. */
  mobilePrecreateDeepLink?: boolean
}

/**
 * CreateOrderFlowResult includes the optional public token returned for order recovery.
 */
type CreateOrderFlowResult = CreateOrderResult & {
  resume_token?: string // public order recovery token
}

/**
 * StorageWriter exposes the browser storage operations needed by recovery persistence.
 */
type StorageWriter = Pick<Storage, 'removeItem' | 'setItem'>

/**
 * normalizeVisibleMethod returns the canonical built-in method for a trimmed alias. Unknown
 * values return an empty string so callers can choose whether custom provider method codes
 * remain admissible.
 */
export function normalizeVisibleMethod(method: string): VisiblePaymentMethod | '' {
  const normalized = VISIBLE_METHOD_ALIASES[method.trim() as keyof typeof VISIBLE_METHOD_ALIASES]
  return normalized ?? ''
}

/**
 * getVisibleMethods combines provider aliases under the visible checkout method name. Each
 * returned limit is copied, and a canonical method entry takes precedence over its alias
 * regardless of enumeration order. Custom nonempty method codes remain separate entries.
 * This prevents alias ordering from changing which limits and fee values the user sees,
 * while allowing EasyPay custom methods to keep their configured identifiers in the checkout
 * list.
 */
export function getVisibleMethods(
  methods: Record<string, MethodLimit>,
): Record<string, MethodLimit> {
  const visible: Record<string, MethodLimit> = {}

  Object.entries(methods).forEach(([type, limit]) => {
    const normalized = normalizeVisibleMethod(type) || type.trim()
    if (!normalized) return

    const isCanonical = type === normalized
    const existing = visible[normalized]
    if (!existing || isCanonical) {
      visible[normalized] = { ...limit }
    }
  })

  return visible
}

/**
 * buildCreateOrderPayload converts selection and browser context into the order API request.
 * An optional origin is trimmed before the result route is appended, and empty plan
 * identifiers or absent billing details are omitted. Alipay forced QR requests report
 * desktop mode unless mobile precreate is enabled. WeChat browser requests select the in-app
 * resume source, allowing the backend to choose authorization handling using the same
 * visible method that the page displays.
 */
export function buildCreateOrderPayload(input: BuildCreateOrderPayloadInput): CreateOrderRequest {
  const visibleMethod = normalizeVisibleMethod(input.paymentType) || input.paymentType.trim()
  const normalizedOrigin = (input.origin || '').trim().replace(/\/+$/, '')
  // Report desktop mode for forced QR checkout unless mobile precreate needs the actual device
  // signal to select its backend flow.
  const effectiveMobile =
    input.forceQRCode && !input.mobilePrecreateDeepLink && visibleMethod === 'alipay'
      ? false
      : input.isMobile
  const payload: CreateOrderRequest = {
    amount: input.amount,
    payment_type: visibleMethod,
    order_type: input.orderType,
    is_mobile: effectiveMobile,
    payment_source:
      visibleMethod === 'wxpay' && input.isWechatBrowser
        ? 'wechat_in_app_resume'
        : 'hosted_redirect',
  }

  if (input.planId) {
    payload.plan_id = input.planId
  }
  if (normalizedOrigin) {
    payload.return_url = `${normalizedOrigin}/payment/result`
  }
  if (input.billingInfo) {
    payload.billing_info = input.billingInfo
  }

  return payload
}

/**
 * decidePaymentLaunch resolves one gateway reply into a launch action and recovery snapshot.
 * The supplied route URLs must already identify this order; missing Airwallex route data
 * produces an unhandled action. Stripe hosted checkout wins over a hosted invoice and
 * embedded secret, while an invoice without a secret remains usable. Wallet selection,
 * device context, explicit payment mode, and Alipay precreate markers then determine whether
 * the caller opens a browser destination, waits for QR payment, or invokes WeChat
 * authorization.
 */
export function decidePaymentLaunch(
  result: CreateOrderFlowResult,
  context: PaymentLaunchContext,
): PaymentLaunchDecision {
  const visibleMethod = normalizeVisibleMethod(context.visibleMethod) || context.visibleMethod
  const hostedInvoiceUrl = (result.invoice_url || '').trim()
  const hostedPaymentUrl = (result.pay_url || '').trim()
  const baseState = createPaymentRecoverySnapshot(
    {
      orderId: result.order_id,
      amount: result.amount,
      qrCode: result.qr_code || '',
      expiresAt: result.expires_at || '',
      paymentType: visibleMethod,
      payUrl: hostedPaymentUrl || hostedInvoiceUrl,
      outTradeNo: result.out_trade_no || '',
      clientSecret: result.client_secret || '',
      intentId: result.intent_id || '',
      currency: result.currency || '',
      countryCode: result.country_code || '',
      paymentEnv: result.payment_env || '',
      payAmount: result.pay_amount,
      orderType: context.orderType,
      paymentMode: (result.payment_mode || '').trim(),
      resumeToken: result.resume_token || '',
      alipayMobilePrecreateDeepLink: result.alipay_mobile_precreate_deep_link === true,
    },
    context.now,
  )

  // A hosted checkout URL identifies the payable Stripe destination; an invoice URL is
  // considered later for replies without that destination.
  if (visibleMethod === 'stripe' && hostedPaymentUrl) {
    const paymentState = { ...baseState, payUrl: hostedPaymentUrl }
    return { kind: 'redirect_waiting', paymentState, recovery: paymentState }
  }

  if (hostedInvoiceUrl && !baseState.clientSecret) {
    const paymentState = { ...baseState, payUrl: hostedInvoiceUrl }
    return { kind: 'redirect_waiting', paymentState, recovery: paymentState }
  }

  if (visibleMethod === 'airwallex' && baseState.clientSecret && baseState.intentId) {
    if (!context.airwallexRouteUrl) {
      return { kind: 'unhandled', paymentState: baseState, recovery: baseState }
    }
    const paymentState = { ...baseState, payUrl: context.airwallexRouteUrl || '' }
    return { kind: 'airwallex_route', paymentState, recovery: paymentState }
  }

  if (baseState.clientSecret) {
    // The top-level Stripe button leaves method choice to Payment Element, while wallet
    // buttons retain their selected wallet inside Stripe.
    const isStripeButton = visibleMethod === 'stripe'
    const stripeMethod: StripeVisibleMethod | undefined = isStripeButton
      ? undefined
      : visibleMethod === 'wxpay'
        ? 'wechat_pay'
        : 'alipay'
    const kind: PaymentLaunchKind =
      stripeMethod === 'alipay' && !context.isMobile ? 'stripe_popup' : 'stripe_route'
    const payUrl =
      kind === 'stripe_popup'
        ? context.stripePopupUrl || context.stripeRouteUrl || ''
        : context.stripeRouteUrl || context.stripePopupUrl || ''
    const paymentState = { ...baseState, payUrl }
    return { kind, paymentState, recovery: paymentState, stripeMethod }
  }

  if (result.result_type === 'oauth_required' && result.oauth?.authorize_url) {
    return {
      kind: 'wechat_oauth',
      paymentState: baseState,
      recovery: baseState,
      oauth: result.oauth,
    }
  }

  const jsapiPayload = result.jsapi ?? result.jsapi_payload
  if (result.result_type === 'jsapi_ready' && jsapiPayload) {
    return {
      kind: 'wechat_jsapi',
      paymentState: baseState,
      recovery: baseState,
      jsapi: jsapiPayload,
    }
  }

  if (
    visibleMethod === 'alipay' &&
    context.isMobile &&
    baseState.alipayMobilePrecreateDeepLink &&
    baseState.qrCode
  ) {
    return { kind: 'alipay_deep_link', paymentState: baseState, recovery: baseState }
  }

  const normalizedPaymentMode = baseState.paymentMode.trim().toLowerCase()
  // Forced QR treats Alipay as desktop here; a marked mobile precreate order has already
  // selected the deep-link action above.
  const effectiveMobile =
    context.forceQRCode && !context.mobilePrecreateDeepLink && visibleMethod === 'alipay'
      ? false
      : context.isMobile
  const prefersRedirect =
    normalizedPaymentMode === 'redirect' ||
    normalizedPaymentMode === 'popup' ||
    (effectiveMobile && !!baseState.payUrl)
  const prefersQr =
    normalizedPaymentMode === 'qrcode' ||
    normalizedPaymentMode === 'native' ||
    (!prefersRedirect && !!baseState.qrCode)

  if (
    visibleMethod === 'wxpay' &&
    context.isWechatBrowser &&
    baseState.payUrl &&
    !baseState.qrCode
  ) {
    return { kind: 'redirect_waiting', paymentState: baseState, recovery: baseState }
  }

  if (prefersRedirect && baseState.payUrl) {
    return { kind: 'redirect_waiting', paymentState: baseState, recovery: baseState }
  }

  if (prefersQr && baseState.qrCode) {
    return { kind: 'qr_waiting', paymentState: baseState, recovery: baseState }
  }

  if (baseState.payUrl) {
    return { kind: 'redirect_waiting', paymentState: baseState, recovery: baseState }
  }

  return { kind: 'unhandled', paymentState: baseState, recovery: baseState }
}

/**
 * createPaymentRecoverySnapshot copies checkout state and adds its creation time in
 * milliseconds. The caller may supply a clock value for deterministic tests; otherwise the
 * current time is sampled when the snapshot is created.
 */
export function createPaymentRecoverySnapshot(
  state: Omit<PaymentRecoverySnapshot, 'createdAt'>,
  now = Date.now(),
): PaymentRecoverySnapshot {
  return {
    ...state,
    createdAt: now,
  }
}

/**
 * writePaymentRecoverySnapshot serializes one order under the recovery storage key. Callers
 * supply browser storage and handle any storage exception; writing replaces the previous
 * recovery entry for that key.
 */
export function writePaymentRecoverySnapshot(
  storage: StorageWriter,
  snapshot: PaymentRecoverySnapshot,
  key = PAYMENT_RECOVERY_STORAGE_KEY,
): void {
  storage.setItem(key, JSON.stringify(snapshot))
}

/**
 * clearPaymentRecoverySnapshot removes the selected recovery entry from supplied storage.
 * The default key is shared with checkout writers and readers; storage errors propagate to
 * the owning page.
 */
export function clearPaymentRecoverySnapshot(
  storage: Pick<Storage, 'removeItem'>,
  key = PAYMENT_RECOVERY_STORAGE_KEY,
): void {
  storage.removeItem(key)
}

/**
 * readPaymentRecoverySnapshot validates stored JSON and returns usable order recovery data.
 * A missing or malformed entry, expired parseable timestamp, or conflicting nonempty resume
 * token returns null. Unparseable expiry text remains accepted because the stored entry may
 * lack a usable deadline. Optional fields introduced after the initial storage shape receive
 * empty defaults, and every order type other than subscription becomes balance so the
 * waiting page receives a supported presentation mode.
 */
export function readPaymentRecoverySnapshot(
  raw: string | null | undefined,
  options: {
    now?: number // milliseconds since Unix epoch

    resumeToken?: string // public order recovery token
  } = {},
): PaymentRecoverySnapshot | null {
  if (!raw) return null

  try {
    const parsed = JSON.parse(raw) as Partial<PaymentRecoverySnapshot>
    if (
      typeof parsed.orderId !== 'number' ||
      typeof parsed.amount !== 'number' ||
      typeof parsed.qrCode !== 'string' ||
      typeof parsed.expiresAt !== 'string' ||
      typeof parsed.paymentType !== 'string' ||
      typeof parsed.payUrl !== 'string' ||
      (parsed.outTradeNo != null && typeof parsed.outTradeNo !== 'string') ||
      typeof parsed.clientSecret !== 'string' ||
      (parsed.intentId != null && typeof parsed.intentId !== 'string') ||
      (parsed.currency != null && typeof parsed.currency !== 'string') ||
      (parsed.countryCode != null && typeof parsed.countryCode !== 'string') ||
      (parsed.paymentEnv != null && typeof parsed.paymentEnv !== 'string') ||
      typeof parsed.payAmount !== 'number' ||
      typeof parsed.paymentMode !== 'string' ||
      typeof parsed.resumeToken !== 'string' ||
      (parsed.alipayMobilePrecreateDeepLink != null &&
        typeof parsed.alipayMobilePrecreateDeepLink !== 'boolean') ||
      typeof parsed.createdAt !== 'number'
    ) {
      return null
    }

    const now = options.now ?? Date.now()
    const expiresAt = Date.parse(parsed.expiresAt)
    if (Number.isFinite(expiresAt) && expiresAt <= now) {
      return null
    }
    if (options.resumeToken && parsed.resumeToken !== options.resumeToken) {
      return null
    }

    return {
      orderId: parsed.orderId,
      amount: parsed.amount,
      qrCode: parsed.qrCode,
      expiresAt: parsed.expiresAt,
      paymentType: parsed.paymentType,
      payUrl: parsed.payUrl,
      outTradeNo: parsed.outTradeNo || '',
      clientSecret: parsed.clientSecret,
      intentId: parsed.intentId || '',
      currency: parsed.currency || '',
      countryCode: parsed.countryCode || '',
      paymentEnv: parsed.paymentEnv || '',
      payAmount: parsed.payAmount,
      orderType: parsed.orderType === 'subscription' ? 'subscription' : 'balance',
      paymentMode: parsed.paymentMode,
      resumeToken: parsed.resumeToken,
      alipayMobilePrecreateDeepLink: parsed.alipayMobilePrecreateDeepLink === true,
      createdAt: parsed.createdAt,
    }
  } catch {
    // Corrupt or non-object JSON cannot restore an order, so the caller clears that entry and
    // keeps checkout available.
    return null
  }
}

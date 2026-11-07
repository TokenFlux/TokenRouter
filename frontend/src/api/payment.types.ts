import type { LocalizedUpdate } from '@/i18n/content'

// payment.types.ts describes payment orders, checkout methods, subscription plans, and
// billing reports. Checkout responses include method limits and charges, while order
// creation can return a completed initiation, a required OAuth handoff, or the payload
// needed for WeChat JSAPI payment.

// PlanCopy contains a complete language version of subscription plan marketing text.
export type PlanCopy = {
  name: string // Localized plan name.
  description: string // Localized plan description.
  features: string // Localized feature list text.
  product_name: string // Payment product display name.
}

// OrderStatus identifies the payment and refund lifecycle state of an order.
export type OrderStatus =
  | 'PENDING'
  | 'PROCESSING'
  | 'PAID'
  | 'RECHARGING'
  | 'COMPLETED'
  | 'EXPIRED'
  | 'CANCELLED'
  | 'FAILED'
  | 'REFUND_REQUESTED'
  | 'REFUNDING'
  | 'REFUND_PENDING'
  | 'PARTIALLY_REFUNDED'
  | 'REFUNDED'
  | 'REFUND_FAILED'

// PaymentType identifies a supported checkout method or payment provider integration.
export type PaymentType =
  | 'alipay'
  | 'wxpay'
  | 'alipay_direct'
  | 'wxpay_direct'
  | 'stripe'
  | 'easypay'
  | 'card'
  | 'link'
  | 'airwallex'
  | 'epusdt'

// OrderType distinguishes a wallet recharge from a subscription plan purchase.
export type OrderType = 'balance' | 'subscription'

// PaymentConfig describes payment availability, limits, fees, and public checkout
// credentials.
export interface PaymentConfig {
  payment_enabled: boolean // Payment checkout availability.
  min_amount: number // Minimum payment amount.
  max_amount: number // Maximum payment amount.
  daily_limit: number // Daily payment ceiling.
  max_pending_orders: number // Maximum pending orders.
  order_timeout_minutes: number // Order timeout minutes.
  balance_disabled: boolean // Wallet recharge disabled.
  balance_recharge_multiplier: number // Balance recharge multiplier.
  subscription_usd_to_cny_rate: number // Subscription USD to CNY rate.
  recharge_fee_rate: number // Recharge fee rate.
  method_fees: Record<string, MethodFeeConfig> // Per-method payment fees.
  enabled_payment_types: PaymentType[] // Enabled payment types.
  help_image_url: string // Help image url.
  help_text: string // Checkout assistance text.
  stripe_publishable_key: string // Stripe publishable key.
}

// MethodFeeConfig configures a payment method's fixed charge and proportional fee.
export interface MethodFeeConfig {
  enabled: boolean // Feature activation switch.
  fixed_fee: number // Fixed payment fee.
  fee_rate: number // Proportional payment fee.
}

// MethodLimit reports payment availability, amount bounds, daily quota, and applicable
// fees.
export interface MethodLimit {
  currency?: string // Payment currency code.
  display_name?: string // Human-readable display name.
  daily_limit: number // Daily payment ceiling.
  daily_used: number // Daily payment consumption.
  daily_remaining: number // Remaining daily allowance.
  single_min: number // Minimum individual payment.
  single_max: number // Maximum individual payment.
  fee_fixed: number // Fixed payment fee.
  fee_rate: number // Proportional payment fee.
  available: boolean // Current payment availability.
}

// MethodLimitsResponse combines checkout method limits with their widest shared amount
// bounds.
export interface MethodLimitsResponse {
  methods: Record<string, MethodLimit> // Available payment method limits.
  global_min: number // widest min across all methods; 0 = no minimum
  global_max: number // widest max across all methods; 0 = no maximum
}

// CheckoutInfoResponse supplies method limits, plan offerings, and public payment page
// settings.
export interface CheckoutInfoResponse {
  // default_balance_recharge_multiplier and user_balance_recharge_multiplier distinguish
  // inherited terms from explicit account overrides. Older servers omit both fields;
  // their effective rate still supplies the ordinary recharge preview.
  default_balance_recharge_multiplier?: number // Global balance recharge rate.
  user_balance_recharge_multiplier?: number | null // Personal rate, null inherits global.
  methods: Record<string, MethodLimit> // Available payment method limits.
  global_min: number // Widest method minimum.
  global_max: number // Widest method maximum.
  plans: SubscriptionPlan[] // Purchasable subscription plans.
  balance_disabled: boolean // Wallet recharge disabled.
  balance_recharge_multiplier: number // Effective balance recharge rate.
  // CNY conversion rate per USD; zero disables conversion and charges the plan price
  // directly.
  subscription_usd_to_cny_rate: number // Subscription USD to CNY rate.
  recharge_fee_rate: number // Recharge fee rate.
  method_fees: Record<string, MethodFeeConfig> // Per-method payment fees.
  help_text: string // Checkout assistance text.
  help_image_url: string // Help image url.
  stripe_publishable_key: string // Stripe publishable key.
  // When enabled, mobile Alipay checkout uses the QR-code payment flow for order
  // completion.
  alipay_force_qrcode?: boolean // Alipay force qrcode.
  // When enabled, official mobile Alipay orders use precreation and a deep link to launch
  // the Alipay client.
  alipay_mobile_precreate_deep_link?: boolean // Alipay mobile precreate deep link.
}

// PaymentOrder records a purchase, its provider payment identifiers, and refund state.
export interface PaymentOrder {
  id: number // Record numeric identifier.
  user_id: number // Owning account identifier.
  amount: number // Purchased resource face amount.
  // Subscription face amounts use their saved plan currency. Balance orders omit
  // this field because the site settings define their display unit.
  amount_currency?: string // Subscription price currency.
  pay_amount: number // Provider charge amount.
  currency?: string // Payment currency code.
  fee_rate: number // Proportional payment fee.
  fee_fixed: number // Fixed payment fee.
  fee_rate_amount: number // Calculated proportional fee.
  fee_amount: number // Total payment fee.
  payment_type: string // Selected payment method.
  out_trade_no: string // Merchant transaction identifier.
  status: OrderStatus // Current lifecycle state.
  order_type: OrderType // Purchased resource kind.
  created_at: string // Record creation timestamp.
  expires_at: string // Record expiration timestamp.
  paid_at?: string // Payment confirmation timestamp.
  completed_at?: string // Order completion timestamp.
  refund_amount: number // Refunded payment amount.
  refund_reason?: string // Recorded refund reason.
  refund_requested_at?: string // Refund requested timestamp.
  refund_requested_by?: string // Refund request actor.
  refund_request_reason?: string // Submitted refund request reason.
  plan_id?: number // Subscription plan identifier.
  provider_instance_id?: string // Payment integration instance identifier.
  payment_customer_id?: string // Payment provider customer identifier.
  payment_invoice_id?: string // Payment provider invoice identifier.
  payment_invoice_url?: string // Hosted provider invoice URL.
  payment_invoice_pdf_url?: string // Provider invoice PDF URL.
  payment_invoice_status?: string // Provider invoice lifecycle state.
  billing_snapshot?: Record<string, unknown> // Captured customer billing details.
}

// PublicPaymentOrder exposes the order fields used by public payment status consumers.
export type PublicPaymentOrder = PaymentOrder

// SubscriptionPlan describes a purchasable allowance, its group access, and localized
// presentation.
export interface SubscriptionPlan {
  product_name?: string // Payment product display name.
  localization?: LocalizedUpdate<PlanCopy> // Localized content versions.
  id: number // Record numeric identifier.
  group_id?: number // Routing group identifier.
  group_ids?: number[] // Assigned group identifiers.
  groups_restricted?: boolean // Plan group restriction switch.
  applicable_groups?: Array<{
    id: number // Record numeric identifier.
    name: string // Record display name.
  }> // Eligible routing groups.
  group_rate_multipliers?: Record<string, number> | Record<number, number> // Per-group subscription billing multipliers.
  group_name?: string // Routing group display name.
  rate_multiplier?: number // Applied billing multiplier.
  peak_rate_enabled?: boolean // Peak rate enabled.
  peak_start?: string // Peak start in HH:mm.
  peak_end?: string // Peak end in HH:mm.
  peak_rate_multiplier?: number // Peak rate multiplier.
  daily_limit_usd?: number | null // Daily limit USD.
  weekly_limit_usd?: number | null // Weekly limit USD.
  monthly_limit_usd?: number | null // Monthly limit USD.
  supported_model_scopes?: string[] // Supported model family identifiers.
  name: string // Record display name.
  description: string // Human-readable record description.
  price: number // Subscription purchase price.
  original_price?: number // Displayed comparison price.
  // Display-only ISO 4217 currency label such as NZD; an empty value hides the label.
  currency?: string // Displayed ISO 4217 currency.
  // validity_days is a count in validity_unit despite its legacy name. Issuing
  // subscriptions converts weeks to seven days and months to thirty days; other
  // unit values use the supplied count directly as days.
  validity_days: number // Configured duration count.
  validity_unit: string // Configured duration unit.
  /** Stored as JSON string in backend; API layer should parse before use */
  features: string[] // Plan feature descriptions.
  for_sale: boolean // Plan purchase availability.
  sort_order: number // Display sorting position.
}

// PaymentChannel advertises a payment channel and the models or features it supports.
export interface PaymentChannel {
  id: number // Record numeric identifier.
  group_id?: number // Routing group identifier.
  name: string // Record display name.
  platform: string // Payment channel platform.
  rate_multiplier: number // Applied billing multiplier.
  description: string // Human-readable record description.
  models: string[] // Requestable model identifiers.
  features: string[] // Channel feature descriptions.
  enabled: boolean // Feature activation switch.
}

// ProviderInstance describes an administrator-configured payment integration and its
// refund permissions.
export interface ProviderInstance {
  id: number // Record numeric identifier.
  provider_key: string // Payment integration identifier.
  name: string // Record display name.
  config: Record<string, string> // Payment provider configuration.
  supported_types: string[] // Accepted payment method identifiers.
  enabled: boolean // Feature activation switch.
  payment_mode: string // Provider checkout flow.
  refund_enabled: boolean // Provider refund support.
  allow_user_refund: boolean // User refund request permission.
  limits: string // Serialized payment method limits.
  sort_order: number // Display sorting position.
}

// CreateOrderRequest starts checkout with purchase details and optional provider resume
// context.
export interface CreateOrderRequest {
  amount: number // Monetary payment amount.
  payment_type: string // Selected payment method.
  order_type: string // Purchased resource kind.
  plan_id?: number // Subscription plan identifier.
  return_url?: string // Checkout completion destination.
  payment_source?: string // Checkout client source.
  openid?: string // WeChat account identifier.
  wechat_resume_token?: string // WeChat checkout continuation token.
  is_mobile?: boolean // Mobile checkout client flag.
  billing_info?: BillingInfo // Customer billing information.
}

// BillingAddress supplies optional postal address fields for payment customer billing
// details.
export interface BillingAddress {
  country?: string // Billing address country.
  line1?: string // Primary street address.
  line2?: string // Additional street address.
  city?: string // Geographic city name.
  state?: string // Billing address region.
  postal_code?: string // Address postal code.
}

// BillingInfo supplies customer identity, address, and tax registration for a purchase.
export interface BillingInfo {
  name?: string // Record display name.
  email?: string // Account email address.
  address?: BillingAddress // Customer billing address.
  tax_id_type?: string // Tax registration category.
  tax_id?: string // Tax registration identifier.
}

// CreateOrderResultType identifies order initiation, OAuth handoff, or WeChat JSAPI
// readiness.
export type CreateOrderResultType = 'order_created' | 'oauth_required' | 'jsapi_ready'

// WechatOAuthInfo supplies WeChat authorization and account context for resuming
// checkout.
export interface WechatOAuthInfo {
  authorize_url?: string // OAuth authorization destination.
  appid?: string // WeChat application identifier.
  openid?: string // WeChat account identifier.
  scope?: string // Requested OAuth permissions.
  state?: string // OAuth callback correlation value.
  redirect_url?: string // OAuth callback destination.
}

// WechatJSAPIPayload supplies the signed parameters consumed by the WeChat payment
// bridge.
export interface WechatJSAPIPayload {
  appId?: string // WeChat application identifier.
  timeStamp?: string // WeChat signature timestamp.
  nonceStr?: string // WeChat signature nonce.
  package?: string // WeChat prepayment package.
  signType?: string // Payment signature algorithm.
  paySign?: string // Generated payment signature.
}

// CreateOrderResult returns payment initiation details for the chosen provider checkout
// flow.
export interface CreateOrderResult {
  order_id: number // Created order identifier.
  amount: number // Credited balance or subscription face amount.
  amount_currency?: string // Persisted subscription price currency.
  pay_url?: string // Hosted checkout URL.
  qr_code?: string // QR-code payment content.
  client_secret?: string // Provider payment confirmation secret.
  customer_id?: string // Payment provider customer identifier.
  invoice_id?: string // Payment provider invoice identifier.
  invoice_url?: string // Hosted invoice URL.
  invoice_pdf?: string // Invoice PDF URL.
  invoice_status?: string // Provider invoice status.
  intent_id?: string // Provider payment intent identifier.
  currency?: string // Payment currency code.
  country_code?: string // Payment country code.
  payment_env?: string // Provider payment environment.
  pay_amount: number // Provider charge amount.
  fee_rate: number // Proportional payment fee.
  fee_fixed: number // Fixed payment fee.
  fee_rate_amount: number // Calculated proportional fee.
  fee_amount: number // Total payment fee.
  expires_at: string // Record expiration timestamp.
  result_type?: CreateOrderResultType // Checkout initiation outcome.
  payment_type?: string // Selected payment method.
  out_trade_no?: string // Merchant transaction identifier.
  payment_mode?: string // Provider checkout flow.
  resume_token?: string // Checkout continuation token.
  alipay_mobile_precreate_deep_link?: boolean // Alipay mobile precreate deep link.
  oauth?: WechatOAuthInfo // WeChat authorization handoff.
  jsapi?: WechatJSAPIPayload // WeChat payment bridge payload.
  jsapi_payload?: WechatJSAPIPayload // WeChat payment bridge payload.
}

// PaymentDocument exposes invoice or receipt locations returned for a payment order.
export interface PaymentDocument {
  type: 'invoice' | 'receipt' | string // Record kind discriminator.
  url?: string // Destination resource URL.
  hosted_invoice_url?: string // Hosted provider invoice URL.
  invoice_pdf?: string // Invoice PDF URL.
  receipt_url?: string // Provider receipt URL.
  invoice_id?: string // Payment provider invoice identifier.
  invoice_status?: string // Provider invoice status.
}

// CurrencyAmounts groups payment totals by currency for administrative billing reports.
export type CurrencyAmounts = Record<string, number>

// DailyPaymentStats records order counts and currency totals for a reporting date.
export interface DailyPaymentStats {
  date: string // Reporting calendar date.
  amount: CurrencyAmounts // Payment totals by currency.
  count: number // Counted payment orders.
}

// PaymentMethodStats groups order counts and currency totals by payment method.
export interface PaymentMethodStats {
  type: string // Payment method identifier.
  amount: CurrencyAmounts // Payment totals by currency.
  count: number // Counted payment orders.
}

// TopUserPaymentStats identifies one paying account and its aggregated purchase amount.
export interface TopUserPaymentStats {
  user_id: number // Owning account identifier.
  email: string // Account email address.
  amount: number // Aggregated user purchase amount.
}

// DashboardStats aggregates order counts, currency totals, and purchase distributions for
// payment reporting.
export interface DashboardStats {
  today_amount: CurrencyAmounts // Today's purchases by currency.
  total_amount: CurrencyAmounts // Cumulative purchases by currency.
  today_count: number // Today's completed order count.
  total_count: number // Cumulative payment order count.
  avg_amount: CurrencyAmounts // Average purchase by currency.
  avg_reasoning_point_purchase_unit_price: CurrencyAmounts // Weighted unit prices by payment currency.
  reasoning_point_purchase_order_count: number // Reasoning point purchase order count.
  daily_series: DailyPaymentStats[] // Daily payment totals.
  payment_methods: PaymentMethodStats[] // Totals by payment method.
  purchase_distribution: {
    type: OrderType // Record kind discriminator.
    currency: string // Recorded payment currency.
    label: string // Human-readable display label.
    plan_id?: number // Subscription plan identifier.
    amount: number // Monetary payment amount.
    count: number // Counted payment orders.
  }[] // Totals by purchase category.
  top_users: Record<string, TopUserPaymentStats[]> // Highest-paying users by currency.
}

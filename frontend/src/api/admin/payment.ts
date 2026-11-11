import { apiClient } from '../client'
import type {
  DashboardStats,
  PaymentOrder,
  PaymentChannel,
  SubscriptionPlan,
  ProviderInstance,
  PaymentDocument,
} from '@/api/payment.types'
import type { BasePaginationResponse } from '@/api/common.types'

/**
 * payment.ts exposes administrator payment settings, orders, plans and provider
 * operations. Its methods return shared-client response objects so consumers
 * read their data property, including refund outcomes and provider draft tests
 * that have dedicated response shapes.
 */

/**
 * AdminPaymentConfig contains current checkout limits, enabled methods and
 * payment presentation settings for administrators.
 */
export interface AdminPaymentConfig {
  enabled: boolean // Feature activation flag.
  min_amount: number // Minimum payment amount.
  max_amount: number // Maximum payment amount.
  daily_limit: number // Configured daily limit.
  order_timeout_minutes: number // Order timeout minutes.
  max_pending_orders: number // Max pending orders.
  enabled_payment_types: string[] // Enabled payment types.
  balance_disabled: boolean // Configured balance disabled.
  balance_recharge_multiplier: number // Balance recharge multiplier.
  subscription_usd_to_cny_rate: number // Subscription USD to cny rate.
  recharge_fee_rate: number // Recharge fee rate.
  load_balance_strategy: string // Load balance strategy.
  product_name_prefix: string // Product name prefix.
  product_name_suffix: string // Product name suffix.
  help_image_url: string // Help image URL.
  help_text: string // Configured help text.
}

/**
 * UpdatePaymentConfigRequest contains selected payment-setting edits; omitted
 * properties stay outside the submitted update payload.
 */
export interface UpdatePaymentConfigRequest {
  enabled?: boolean // Feature activation flag.
  min_amount?: number // Minimum payment amount.
  max_amount?: number // Maximum payment amount.
  daily_limit?: number // Requested daily limit.
  order_timeout_minutes?: number // Order timeout minutes.
  max_pending_orders?: number // Max pending orders.
  enabled_payment_types?: string[] // Enabled payment types.
  balance_disabled?: boolean // Requested balance disabled.
  balance_recharge_multiplier?: number // Balance recharge multiplier.
  subscription_usd_to_cny_rate?: number // Subscription USD to cny rate.
  recharge_fee_rate?: number // Recharge fee rate.
  load_balance_strategy?: string // Load balance strategy.
  product_name_prefix?: string // Product name prefix.
  product_name_suffix?: string // Product name suffix.
  help_image_url?: string // Help image URL.
  help_text?: string // Requested help text.
}

/**
 * RefundResult reports refund success, warnings and any requested force
 * confirmation or deducted entitlements.
 */
export interface RefundResult {
  // New servers provide this outcome so clients can refresh or ask for consent
  // independently of the wording in warning. Older servers omit the field.
  status?: 'succeeded' | 'pending' | 'failed' | 'requires_force'
  success: boolean // Operation success flag.
  warning?: string // Server advisory message.
  require_force?: boolean // Recorded require force.
  balance_deducted?: number // Recorded balance deducted.
  subscription_days_deducted?: number // Subscription days deducted.
}

/**
 * ForceExpireOrderRequest supplies the administrator's reason for force-expiring
 * a pending payment order.
 */
export interface ForceExpireOrderRequest {
  reason: string // Administrator operation reason.
}

/**
 * ProviderDraftTestRequest submits an unsaved payment-provider configuration and
 * optional existing instance identity for testing.
 */
export interface ProviderDraftTestRequest {
  provider_key: string // Payment provider implementation.
  instance_id?: number // Requested instance id.
  config: Record<string, string> // Submitted configuration values.
}

/**
 * ProviderDraftTestResult reports whether the tested payment-provider draft was
 * reachable through its connection test.
 */
export interface ProviderDraftTestResult {
  reachable: boolean // Connection reachability flag.
}

/**
 * adminPaymentAPI groups payment configuration, orders and provider operations
 * for callers using the module's named or default API object.
 */
export const adminPaymentAPI = {
  /**
   * getConfig retrieves administrator payment settings in the shared client
   * response object.
   */
  getConfig() {
    return apiClient.get<AdminPaymentConfig>('/admin/payment/config')
  },

  /**
   * updateConfig updates payment configuration and returns the server result.
   */
  updateConfig(data: UpdatePaymentConfigRequest) {
    return apiClient.put('/admin/payment/config', data)
  },

  /**
   * getDashboard retrieves payment aggregates using a day count or a date-range
   * query. Numeric input becomes the days parameter, while query objects pass
   * through; callers read the Axios response data property.
   */
  getDashboard(
    params?:
      | number
      | {
          start_date?: string // Requested start date.
          end_date?: string // Requested end date.
          days?: number // Requested interval days.
        },
  ) {
    const queryParams = typeof params === 'number' ? { days: params } : params
    return apiClient.get<DashboardStats>('/admin/payment/dashboard', {
      params: queryParams,
    })
  },

  /**
   * getOrders retrieves filtered payment order pages in the shared client
   * response object.
   */
  getOrders(params?: {
    page?: number // Requested page number.
    page_size?: number // Requested page size.
    status?: string // Current server-reported status.
    payment_type?: string // Recorded payment type.
    user_id?: number // Owning user identifier.
    keyword?: string // Search input text.
    start_date?: string // Requested start date.
    end_date?: string // Requested end date.
    order_type?: string // Recorded order type.
  }) {
    return apiClient.get<BasePaginationResponse<PaymentOrder>>('/admin/payment/orders', { params })
  },

  /**
   * getOrder retrieves a specific order by ID and returns the server result.
   */
  getOrder(id: number) {
    return apiClient.get<PaymentOrder>(`/admin/payment/orders/${id}`)
  },

  /**
   * getOrderInvoice retrieves an order's Stripe invoice or historical receipt
   * link.
   */
  getOrderInvoice(id: number) {
    return apiClient.get<PaymentDocument>(`/admin/payment/orders/${id}/invoice`)
  },

  /**
   * cancelOrder requests administrator cancellation of the selected payment
   * order.
   */
  cancelOrder(id: number) {
    return apiClient.post(`/admin/payment/orders/${id}/cancel`)
  },

  /**
   * forceExpireOrder submits the administrator's reason for expiring a pending
   * order.
   */
  forceExpireOrder(id: number, data: ForceExpireOrderRequest) {
    return apiClient.post(`/admin/payment/orders/${id}/force-expire`, data)
  },

  /**
   * retryRecharge requests another fulfillment attempt for the selected failed
   * payment order.
   */
  retryRecharge(id: number) {
    return apiClient.post(`/admin/payment/orders/${id}/retry`)
  },

  /**
   * refundOrder submits the selected order's refund amount, reason and
   * entitlement deduction options.
   */
  refundOrder(
    id: number,
    data: {
      /**
       * Nonpositive input selects the full local order amount; the server derives
       * the payment provider's refund quantity separately from this local quantity.
       */
      amount: number // Local refund quantity.
      reason: string // Administrator-submitted refund reason.
      deduct_balance?: boolean // Recorded deduct balance.
      force?: boolean // Force-refund confirmation flag.
    },
  ) {
    return apiClient.post<RefundResult>(`/admin/payment/orders/${id}/refund`, data)
  },

  /**
   * queryRefund reconciles an existing refund. An optional Stripe refund ID
   * is verified by the server before it is associated with the order.
   */
  queryRefund(id: number, refundID?: string) {
    return apiClient.post<RefundResult>(
      `/admin/payment/orders/${id}/refund/query`,
      refundID ? { refund_id: refundID } : undefined,
    )
  },

  /**
   * getChannels retrieves payment channel records in the shared client response
   * object.
   */
  getChannels() {
    return apiClient.get<PaymentChannel[]>('/admin/payment/channels')
  },

  /**
   * createChannel creates a payment channel and returns the server result.
   */
  createChannel(data: Partial<PaymentChannel>) {
    return apiClient.post<PaymentChannel>('/admin/payment/channels', data)
  },

  /**
   * updateChannel updates a payment channel and returns the server result.
   */
  updateChannel(id: number, data: Partial<PaymentChannel>) {
    return apiClient.put<PaymentChannel>(`/admin/payment/channels/${id}`, data)
  },

  /**
   * deleteChannel deletes a payment channel and returns the server result.
   */
  deleteChannel(id: number) {
    return apiClient.delete(`/admin/payment/channels/${id}`)
  },

  /**
   * getPlans retrieves administrator subscription plans in the shared client
   * response object.
   */
  getPlans() {
    return apiClient.get<SubscriptionPlan[]>('/admin/payment/plans')
  },

  /**
   * createPlan creates a subscription plan and returns the server result.
   */
  createPlan(data: Record<string, unknown>) {
    return apiClient.post<SubscriptionPlan>('/admin/payment/plans', data)
  },

  /**
   * updatePlan updates a subscription plan and returns the server result.
   */
  updatePlan(id: number, data: Record<string, unknown>) {
    return apiClient.put<SubscriptionPlan>(`/admin/payment/plans/${id}`, data)
  },

  /**
   * deletePlan deletes a subscription plan and returns the server result.
   */
  deletePlan(id: number) {
    return apiClient.delete(`/admin/payment/plans/${id}`)
  },

  /**
   * getProviders retrieves payment-provider instances in the shared client
   * response object.
   */
  getProviders() {
    return apiClient.get<ProviderInstance[]>('/admin/payment/providers')
  },

  /**
   * createProvider creates a provider instance and returns the server result.
   */
  createProvider(data: Partial<ProviderInstance>) {
    return apiClient.post<ProviderInstance>('/admin/payment/providers', data)
  },

  /**
   * updateProvider updates a provider instance and returns the server result.
   */
  updateProvider(id: number, data: Partial<ProviderInstance>) {
    return apiClient.put<ProviderInstance>(`/admin/payment/providers/${id}`, data)
  },

  /**
   * deleteProvider deletes a provider instance and returns the server result.
   */
  deleteProvider(id: number) {
    return apiClient.delete(`/admin/payment/providers/${id}`)
  },

  /**
   * testProviderDraft submits a payment provider draft and returns the shared
   * client response object containing the test result.
   */
  testProviderDraft(data: ProviderDraftTestRequest) {
    return apiClient.post<ProviderDraftTestResult>('/admin/payment/providers/test', data)
  },
}

export default adminPaymentAPI

import { apiClient } from './client'
import type {
  PaymentConfig,
  SubscriptionPlan,
  MethodLimitsResponse,
  CheckoutInfoResponse,
  CreateOrderRequest,
  CreateOrderResult,
  PaymentDocument,
  PaymentOrder,
} from '@/api/payment.types'
import type { BasePaginationResponse } from '@/api/common.types'

/**
 * payment.ts sends checkout and order operations for payment pages. Methods
 * return shared-client response objects, including authenticated order actions
 * and separate public lookups by trade number or signed recovery token for
 * payment-return flows.
 */

/**
 * PublicOrderVerifyResult contains the limited trade status and lifecycle
 * timestamps exposed by public order verification.
 */
export interface PublicOrderVerifyResult {
  out_trade_no: string // External trade identifier.
  status: string // Current server-reported status.
  paid: boolean // Payment confirmation flag.
  created_at: string // Record creation timestamp.
  expires_at: string // Recorded expiration timestamp.
  paid_at?: string // Recorded paid at.
  completed_at?: string // Operation completion timestamp.
}

/**
 * paymentAPI groups payment configuration, orders and provider operations for
 * callers using the module's named or default API object.
 */
export const paymentAPI = {
  /**
   * getConfig retrieves payment configuration (enabled types, limits, etc.).
   */
  getConfig() {
    return apiClient.get<PaymentConfig>('/payment/config')
  },

  /**
   * getPlans retrieves available subscription plans and returns the server
   * result.
   */
  getPlans() {
    return apiClient.get<SubscriptionPlan[]>('/payment/plans')
  },

  /**
   * getCheckoutInfo retrieves all checkout page data in a single call.
   */
  getCheckoutInfo() {
    return apiClient.get<CheckoutInfoResponse>('/payment/checkout-info')
  },

  /**
   * getLimits retrieves payment method limits and fee rates.
   */
  getLimits() {
    return apiClient.get<MethodLimitsResponse>('/payment/limits')
  },

  /**
   * createOrder creates a new payment order and returns the server result.
   */
  createOrder(data: CreateOrderRequest) {
    return apiClient.post<CreateOrderResult>('/payment/orders', data)
  },

  /**
   * getMyOrders retrieves current user's orders and returns the server result.
   */
  getMyOrders(params?: {
    page?: number // Requested page number.
    page_size?: number // Requested page size.
    status?: string // Current server-reported status.
  }) {
    return apiClient.get<BasePaginationResponse<PaymentOrder>>('/payment/orders/my', { params })
  },

  /**
   * getOrder retrieves a specific order by numeric or route-query ID and returns
   * the server result.
   */
  getOrder(id: number | string) {
    return apiClient.get<PaymentOrder>(`/payment/orders/${encodeURIComponent(String(id))}`)
  },

  /**
   * getOrderInvoice retrieves an order's Stripe invoice or historical receipt
   * link.
   */
  getOrderInvoice(id: number) {
    return apiClient.get<PaymentDocument>(`/payment/orders/${id}/invoice`)
  },

  /**
   * cancelOrder requests cancellation of the current user's selected pending
   * payment order.
   */
  cancelOrder(id: number) {
    return apiClient.post(`/payment/orders/${id}/cancel`)
  },

  /**
   * verifyOrder asks the payment provider to verify the selected trade number.
   */
  verifyOrder(outTradeNo: string) {
    return apiClient.post<PaymentOrder>('/payment/orders/verify', { out_trade_no: outTradeNo })
  },

  /**
   * verifyOrderPublic queries a trade number anonymously and returns the limited
   * public order status.
   */
  verifyOrderPublic(outTradeNo: string) {
    return apiClient.post<PublicOrderVerifyResult>('/payment/public/orders/verify', {
      out_trade_no: outTradeNo,
    })
  },

  /**
   * resolveOrderPublicByResumeToken retrieves payment-result details with the
   * signed recovery token, including when the browser is not logged in.
   */
  resolveOrderPublicByResumeToken(resumeToken: string) {
    return apiClient.post<PaymentOrder>('/payment/public/orders/resolve', {
      resume_token: resumeToken,
    })
  },

  /**
   * requestRefund submits the user's reason for requesting an order refund.
   */
  requestRefund(
    id: number,
    data: {
      reason: string // Buyer-submitted refund reason.
    },
  ) {
    return apiClient.post(`/payment/orders/${id}/refund-request`, data)
  },

  /**
   * getRefundEligibleProviders retrieves provider instance IDs that allow user
   * refund.
   */
  getRefundEligibleProviders() {
    return apiClient.get<{
      provider_instance_ids: string[] // Provider instance IDs.
    }>('/payment/orders/refund-eligible-providers')
  },
}

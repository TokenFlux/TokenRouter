import type { User } from '@/api/identity.types'
import type { SubscriptionPlan } from '@/api/payment.types'

// redeem.types.ts defines redemption codes and administrator requests for creating or
// editing their grants. Codes track usage limits, expiration, and optional subscription
// plans, and payment eligibility can restrict redemption to users with an earlier
// successful payment.

// RedeemCodeType identifies the resource or allowance granted by a redemption code.
export type RedeemCodeType =
  | 'balance'
  | 'admin_balance'
  | 'plugin_balance'
  | 'concurrency'
  | 'admin_concurrency'
  | 'subscription'
  | 'invitation'
  | 'affiliate_balance'

// RedeemCode describes a redeemable allowance and its usage and expiration state.
export interface RedeemCode {
  // When enabled, redemption requires the recipient to have a previous successful payment
  // record.
  requires_payment?: boolean // Prior successful payment requirement.
  id: number // Record numeric identifier.
  code: string // Redeemable grant code.
  type: RedeemCodeType // Record kind discriminator.
  value: number // Granted resource amount.
  status: 'active' | 'used' | 'expired' | 'unused' | 'disabled' // Current lifecycle state.
  max_uses: number // Maximum redemption count.
  used_count: number // Completed redemption count.
  expires_at: string | null // Record expiration timestamp.
  used_by: number | null // Redeeming account identifier.
  used_at: string | null // Latest redemption timestamp.
  created_at: string // Record creation timestamp.
  updated_at?: string // Record update timestamp.
  plan_id?: number | null // Subscription plan identifier.
  // A present day count freezes subscription duration; missing or null retains the plan default at redemption.
  validity_days?: number | null
  notes?: string | null // Visible adjustment reason.
  user?: User // Expanded account details.
  plan?: SubscriptionPlan // Granted subscription plan details.
}

// GenerateRedeemCodesRequest supplies grant amounts and eligibility settings for new
// redemption codes.
export interface GenerateRedeemCodesRequest {
  // When enabled, redemption requires the recipient to have a previous successful payment
  // record.
  requires_payment?: boolean // Prior successful payment requirement.
  code?: string // Requested redemption code text.
  count: number // Requested generation count.
  type: RedeemCodeType // Record kind discriminator.
  value: number // Granted resource amount.
  max_uses?: number // Maximum redemption count.
  expires_at?: number | null // Expiration time in Unix seconds.
  expires_in_days?: number // Requested validity days.
  plan_id?: number | null // Subscription plan identifier.
}

// UpdateRedeemCodeRequest changes the allowance and eligibility settings of a redemption
// code.
export interface UpdateRedeemCodeRequest {
  // When enabled, redemption requires the recipient to have a previous successful payment
  // record.
  requires_payment?: boolean // Prior successful payment requirement.
  value?: number // Updated resource amount.
  max_uses?: number // Maximum redemption count.
  expires_at?: number | null // Expiration time in Unix seconds.
  plan_id?: number | null // Subscription plan identifier.
}

// BatchUpdateRedeemCodeFields supplies shared changes applied to selected redemption
// codes.
export interface BatchUpdateRedeemCodeFields {
  status?: 'unused' | 'disabled' // Current lifecycle state.
  expires_at?: string | null // Record expiration timestamp.
  notes?: string // Administrator record notes.
}

// RedeemCodeRequest submits the code that the current user wants to redeem.
export interface RedeemCodeRequest {
  code: string // Submitted redemption code.
}

import { apiClient } from './client'
import type {
  ApiKey,
  ApiKeyBillingSubscriptionOption,
  CreateApiKeyRequest,
  UpdateApiKeyRequest,
} from '@/api/keys.types'
import type { PaginatedResponse } from '@/api/common.types'

/**
 * keys.ts sends personal and team API-key management requests. List queries
 * carry scope, filters and cancellation, while billing options identify
 * selectable subscriptions; credential rotation targets the existing key
 * identifier and returns its updated record to callers.
 */

/**
 * list lists all API keys for current user. Page defaults to 1, pageSize
 * defaults to 10; the caller may cancel through the supplied AbortSignal. The
 * function returns the data portion of the completed response.
 */
export async function list(
  page: number = 1,
  pageSize: number = 10,
  filters?: {
    search?: string // Free-text search query.
    status?: string // Current server-reported status.
    group_id?: number | string // Group record identifier.
    sort_by?: string // Requested sort column.
    sort_order?: 'asc' | 'desc' // Requested ordering direction.
    scope?: 'personal' | 'team' // Requested access scope.
  },
  options?: {
    signal?: AbortSignal // Caller-owned cancellation signal.
  },
): Promise<PaginatedResponse<ApiKey>> {
  const { data } = await apiClient.get<PaginatedResponse<ApiKey>>('/keys', {
    params: { page, page_size: pageSize, ...filters },
    signal: options?.signal,
  })
  return data
}

/**
 * getBillingOptions retrieves active subscriptions that can be locked to a key
 * in the current scope. Scope defaults to 'personal'. The function returns the
 * data portion of the completed response.
 */
export async function getBillingOptions(
  scope: 'personal' | 'team' = 'personal',
): Promise<ApiKeyBillingSubscriptionOption[]> {
  const { data } = await apiClient.get<ApiKeyBillingSubscriptionOption[]>('/keys/billing-options', {
    params: { scope },
  })
  return data
}

/**
 * getTokenRateMultipliers previews the highest current group Token rates for
 * the selected funding mode and payer scope. Subscription mode requires its
 * selected identifier; rejected lookups leave error handling to the caller.
 */
export async function getTokenRateMultipliers(
  scope: 'personal' | 'team',
  billingMode: string,
  subscriptionID: number | null,
): Promise<Record<number, number>> {
  const { data } = await apiClient.get<Record<number, number>>('/groups/rates', {
    params: {
      scope,
      token_rates: true,
      billing_mode: billingMode,
      subscription_id: billingMode === 'subscription' ? subscriptionID : undefined,
    },
  })
  return data
}

/**
 * createWithPayload submits the caller's assembled API-key creation payload and
 * returns the created key.
 */
export async function createWithPayload(payload: CreateApiKeyRequest): Promise<ApiKey> {
  const { data } = await apiClient.post<ApiKey>('/keys', payload)
  return data
}

/**
 * update updates API key and returns the server result.
 */
export async function update(id: number, updates: UpdateApiKeyRequest): Promise<ApiKey> {
  const { data } = await apiClient.put<ApiKey>(`/keys/${id}`, updates)
  return data
}

/**
 * rotate submits rotate and returns the server response data.
 */
export async function rotate(id: number): Promise<ApiKey> {
  const { data } = await apiClient.post<ApiKey>(`/keys/${id}/rotate`)
  return data
}

/**
 * deleteKey deletes API key and returns the server result.
 */
export async function deleteKey(id: number): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.delete<{
    message: string // Server-provided result message.
  }>(`/keys/${id}`)
  return data
}

/**
 * toggleStatus sets API key status (active/inactive) and returns the server
 * result.
 */
export async function toggleStatus(id: number, status: 'active' | 'inactive'): Promise<ApiKey> {
  return update(id, { status })
}

/**
 * keysAPI groups personal and team API-key operations for callers using the
 * module's named or default API object.
 */
export const keysAPI = {
  list,
  getBillingOptions,
  getTokenRateMultipliers,
  createWithPayload,
  update,
  rotate,
  delete: deleteKey,
  toggleStatus,
}

export default keysAPI

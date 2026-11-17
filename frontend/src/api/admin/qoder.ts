import { apiClient } from '../client'

/**
 * qoder.ts exchanges administrator Qoder authorization session data with the
 * server. URL creation selects the requested site and proxy, while callback
 * exchange and polling retain session and state identifiers so authorization
 * callers can follow their pending session.
 */

/**
 * QoderSite selects the global or China Qoder authorization site for a provider.
 */
export type QoderSite = 'global' | 'cn'

/**
 * QoderAuthUrlResponse pairs an authorization URL with session state, polling
 * guidance and optional selected site.
 */
export interface QoderAuthUrlResponse {
  auth_url: string // Authorization navigation URL.
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
  expires_in?: number // Lifetime in seconds.
  interval?: number // Polling interval seconds.
  site?: QoderSite // Provider deployment site.
}

/**
 * QoderAuthUrlRequest selects the Qoder site and optional proxy used when
 * authorization begins.
 */
export interface QoderAuthUrlRequest {
  proxy_id?: number // Selected proxy identifier.
  site?: QoderSite // Provider deployment site.
}

/**
 * QoderExchangeCodeRequest submits a session's authorization code or callback
 * URL with its correlation state.
 */
export interface QoderExchangeCodeRequest {
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
  code?: string // OAuth authorization code.
  callback_url?: string // Requested callback url.
}

/**
 * QoderTokenInfo carries returned account and machine credentials, site and
 * refresh mode with extra provider metadata.
 */
export interface QoderTokenInfo {
  security_oauth_token?: string // Security OAuth token.
  refresh_token?: string // Session rotation token.
  machine_id?: string // Recorded machine id.
  machine_token?: string // Recorded machine token.
  machine_type?: string // Recorded machine type.
  uid?: string // Qoder user identifier.
  aid?: string // Qoder account identifier.
  organization_id?: string // Recorded organization id.
  organization_name?: string // Recorded organization name.
  name?: string // Human-readable display name.
  user_type?: string // Recorded user type.
  site?: QoderSite // Provider deployment site.
  refresh_mode?: 'cosy' | 'qodercn20' | 'device_token' // Recorded refresh mode.
  expires_at?: string // Recorded expiration timestamp.
  extra?: Record<string, unknown> // Additional provider metadata.
  [key: string]: unknown // Additional token response fields.
}

/**
 * QoderPollRequest identifies the authorization session and state whose
 * completion is being polled.
 */
export interface QoderPollRequest {
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
}

/**
 * QoderPollResponse distinguishes pending authorization from completion with
 * optional returned token information.
 */
export interface QoderPollResponse {
  status: 'pending' | 'completed' // Current server-reported status.
  token_info?: QoderTokenInfo // Recorded token info.
}

/**
 * generateAuthUrl submits generate auth url and returns the server response
 * data.
 */
export async function generateAuthUrl(payload: QoderAuthUrlRequest): Promise<QoderAuthUrlResponse> {
  const { data } = await apiClient.post<QoderAuthUrlResponse>(
    '/admin/qoder/oauth/auth-url',
    payload,
  )
  return data
}

/**
 * exchangeCode submits exchange code and returns the server response data.
 */
export async function exchangeCode(payload: QoderExchangeCodeRequest): Promise<QoderTokenInfo> {
  const { data } = await apiClient.post<QoderTokenInfo>('/admin/qoder/oauth/exchange-code', payload)
  return data
}

/**
 * poll submits poll and returns the server response data.
 */
export async function poll(payload: QoderPollRequest): Promise<QoderPollResponse> {
  const { data } = await apiClient.post<QoderPollResponse>('/admin/qoder/oauth/poll', payload)
  return data
}

export default { generateAuthUrl, exchangeCode, poll }

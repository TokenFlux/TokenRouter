import { apiClient } from '../client'

/**
 * antigravity.ts sends administrator Antigravity authorization requests and
 * returns token information. Authorization URL creation and code exchange
 * forward their typed session and optional project data, while refresh includes
 * optional proxy and project identifiers when supplied.
 */

/**
 * AntigravityAuthUrlResponse pairs an authorization URL with the session and
 * state needed for code exchange.
 */
export interface AntigravityAuthUrlResponse {
  auth_url: string // Authorization navigation URL.
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
}

/**
 * AntigravityAuthUrlRequest selects an optional proxy for starting Antigravity
 * authorization through the server.
 */
export interface AntigravityAuthUrlRequest {
  proxy_id?: number // Selected proxy identifier.
}

/**
 * AntigravityExchangeCodeRequest submits the authorization code with its
 * matching session, state, optional GCP project, and optional proxy.
 */
export interface AntigravityExchangeCodeRequest {
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
  code: string // OAuth authorization code.
  project_id?: string // Optional GCP Project ID for standard-tier accounts.
  proxy_id?: number // Selected proxy identifier.
}

/**
 * AntigravityTokenInfo carries returned OAuth credentials and account metadata
 * while accepting additional provider fields.
 */
export interface AntigravityTokenInfo {
  access_token?: string // Bearer access token.
  refresh_token?: string // Session rotation token.
  token_type?: string // Authorization scheme name.
  expires_at?: number | string // Recorded expiration timestamp.
  expires_in?: number // Lifetime in seconds.
  project_id?: string // Recorded project id.
  email?: string // Account email address.
  plan_type?: string // Discovered subscription plan label.
  privacy_mode?: string // Privacy configuration observation marker.
  [key: string]: unknown // Additional token response fields.
}

/**
 * generateAuthUrl submits generate auth url and returns the server response
 * data.
 */
export async function generateAuthUrl(
  payload: AntigravityAuthUrlRequest,
): Promise<AntigravityAuthUrlResponse> {
  const { data } = await apiClient.post<AntigravityAuthUrlResponse>(
    '/admin/antigravity/oauth/auth-url',
    payload,
  )
  return data
}

/**
 * exchangeCode submits exchange code and returns the server response data.
 */
export async function exchangeCode(
  payload: AntigravityExchangeCodeRequest,
): Promise<AntigravityTokenInfo> {
  const { data } = await apiClient.post<AntigravityTokenInfo>(
    '/admin/antigravity/oauth/exchange-code',
    payload,
  )
  return data
}

/**
 * refreshAntigravityToken exchanges the supplied refresh token for token metadata.
 * Truthy proxy and nonblank project identifiers are forwarded; zero, null, blank,
 * and omission leave them out of the request.
 */
export async function refreshAntigravityToken(
  refreshToken: string,
  proxyId?: number | null,
  projectId?: string | null,
): Promise<AntigravityTokenInfo> {
  const payload: Record<string, any> = { refresh_token: refreshToken }
  if (proxyId) payload.proxy_id = proxyId
  const trimmedProjectID = projectId?.trim()
  if (trimmedProjectID) payload.project_id = trimmedProjectID

  const { data } = await apiClient.post<AntigravityTokenInfo>(
    '/admin/antigravity/oauth/refresh-token',
    payload,
  )
  return data
}

export default { generateAuthUrl, exchangeCode, refreshAntigravityToken }

import { apiClient } from '../client'

/**
 * gemini.ts starts and completes administrator Gemini OAuth authorization.
 * Request data retains the selected OAuth flow, project and tier where accepted,
 * while a capability query supplies the redirect and AI Studio availability
 * information used by authorization forms.
 */

/**
 * GeminiAuthUrlResponse pairs the Gemini authorization URL with its session and
 * correlation state.
 */
export interface GeminiAuthUrlResponse {
  auth_url: string // Authorization navigation URL.
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
}

/**
 * GeminiOAuthCapabilities reports AI Studio OAuth availability and redirect URLs
 * required by authorization configuration.
 */
export interface GeminiOAuthCapabilities {
  ai_studio_oauth_enabled: boolean // Ai studio OAuth enabled.
  required_redirect_uris: string[] // Required redirect uris.
}

/**
 * GeminiAuthUrlRequest selects the Gemini authorization flow, project, tier and
 * optional proxy.
 */
export interface GeminiAuthUrlRequest {
  proxy_id?: number // Selected proxy identifier.
  project_id?: string // Requested project id.
  oauth_type?: 'code_assist' | 'ai_studio' // Requested oauth type.
  tier_id?: string // Requested tier id.
}

/**
 * GeminiExchangeCodeRequest submits a Gemini authorization code with its
 * session, state, optional project override, and selected flow context.
 */
export interface GeminiExchangeCodeRequest {
  session_id: string // Authorization session identifier.
  state: string // OAuth correlation state.
  code: string // OAuth authorization code.
  project_id?: string // Optional GCP Project ID override for retry completion.
  proxy_id?: number // Selected proxy identifier.
  oauth_type?: 'code_assist' | 'ai_studio' // Requested oauth type.
  tier_id?: string // Requested tier id.
}

/**
 * GeminiTokenInfo carries returned Gemini credentials, project and tier metadata
 * with additional provider fields.
 */
export type GeminiTokenInfo = {
  access_token?: string // Bearer access token.
  refresh_token?: string // Session rotation token.
  token_type?: string // Authorization scheme name.
  scope?: string // Requested access scope.
  expires_in?: number // Lifetime in seconds.
  expires_at?: number // Recorded expiration timestamp.
  project_id?: string // Recorded project id.
  oauth_type?: string // Recorded oauth type.
  tier_id?: string // Recorded tier id.
  extra?: Record<string, unknown> // Additional provider metadata.
  [key: string]: unknown // Additional token response fields.
}

/**
 * generateAuthUrl submits generate auth url and returns the server response
 * data.
 */
export async function generateAuthUrl(
  payload: GeminiAuthUrlRequest,
): Promise<GeminiAuthUrlResponse> {
  const { data } = await apiClient.post<GeminiAuthUrlResponse>(
    '/admin/gemini/oauth/auth-url',
    payload,
  )
  return data
}

/**
 * exchangeCode submits exchange code and returns the server response data.
 */
export async function exchangeCode(payload: GeminiExchangeCodeRequest): Promise<GeminiTokenInfo> {
  const { data } = await apiClient.post<GeminiTokenInfo>(
    '/admin/gemini/oauth/exchange-code',
    payload,
  )
  return data
}

/**
 * getCapabilities retrieves capabilities and returns the server response data.
 */
export async function getCapabilities(): Promise<GeminiOAuthCapabilities> {
  const { data } = await apiClient.get<GeminiOAuthCapabilities>('/admin/gemini/oauth/capabilities')
  return data
}

export default { generateAuthUrl, exchangeCode, getCapabilities }

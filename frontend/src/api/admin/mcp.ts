import { apiClient } from '../client'

// mcp.ts sends administrator token and browser-confirmation requests through the
// existing authenticated client. Token metadata never includes reusable secrets;
// creation and the owner-only copy endpoint return complete credentials. Callers own cancellation for reads and
// retain failed form input while displaying the server's validation response.

// MCPOperation identifies one permission and the interaction required to execute it.
export interface MCPOperation {
  operation_id: string // Stable operation permission.
  module: string // Permission grouping category.
  title: string // Human-readable operation label.
  title_zh?: string // Optional Chinese operation label.
  description: string // Parameter and effect guidance.
  method: string // Underlying HTTP verb.
  path: string // Registered route pattern.
  read_only: boolean // Declared query behavior.
  sensitive: boolean // Browser confirmation requirement.
  step_up: boolean // Existing TOTP policy requirement.
  response_mode: string // Shared response delivery mode.
}

// MCPToken contains administrator-visible metadata for a dedicated access credential.
export interface MCPToken {
  source?: string // Empty denotes manual credentials.
  id: number // Stored credential identifier.
  user_id: number // Owning administrator identifier.
  name: string // Administrator-chosen token label.
  prefix: string // Non-secret credential prefix.
  permissions: string[] // Exact granted operation identifiers.
  enabled: boolean // Current admission switch.
  skip_browser_confirmation: boolean // Direct sensitive execution preference.
  redact_tool_results: boolean // Current response masking preference.
  expires_at: string | null // Optional absolute expiry.
  last_used_at: string | null // Last admitted request time.
  created_at: string // Initial creation time.
  updated_at: string // Last settings change.
  revoked_at: string | null // Permanent revocation time.
}

// MCPTokenInput carries editable settings for one owned MCP credential.
export interface MCPTokenInput {
  name: string // Required display name.
  permissions: string[] // Exact selected permission set.
  enabled: boolean // Requested admission switch.
  skip_browser_confirmation: boolean // Opt-in MCP confirmation bypass.
  // Omission leaves response masking off at creation and preserves the saved value on update.
  redact_tool_results?: boolean
  expires_at: string | null // Null permits no expiry.
  // expected_updated_at rejects replacement when another operation has changed the token since this snapshot.
  expected_updated_at?: string
}

// MCPCreation carries a freshly issued credential for immediate client setup.
export interface MCPCreation {
  token: MCPToken // Persisted public token metadata.
  credential: string // Complete issued access credential.
}

// MCPConfirmation describes the exact redacted action awaiting browser review.
export interface MCPConfirmation {
  id: string // Opaque confirmation identifier.
  token_id: number // Requesting owned credential.
  operation: MCPOperation // Action being confirmed.
  preview: string // Redacted JSON argument display.
  state: 'pending' | 'approved' | 'consumed' // Current shared workflow state.
  expires_at: string // Absolute confirmation expiry.
}

// listTokens reads the current administrator's token metadata with optional cancellation.
export async function listTokens(signal?: AbortSignal): Promise<MCPToken[]> {
  const { data } = await apiClient.get<MCPToken[]>('/admin/mcp/tokens', { signal })
  return data
}

// listOperations loads the same operation directory used by MCP discovery.
export async function listOperations(signal?: AbortSignal): Promise<MCPOperation[]> {
  const { data } = await apiClient.get<MCPOperation[]>('/admin/mcp/operations', { signal })
  return data
}

// getCredential reads an owned token's complete value for an explicit copy action.
// Callers keep the returned plaintext ephemeral. List responses contain metadata,
// and this endpoint requires the owner's authenticated browser session.
export async function getCredential(id: number): Promise<string> {
  const { data } = await apiClient.get<{ credential: string }>(`/admin/mcp/tokens/${id}/credential`)
  return data.credential
}

// createToken issues a credential whose plaintext the caller must keep ephemeral.
export async function createToken(input: MCPTokenInput): Promise<MCPCreation> {
  const { data } = await apiClient.post<MCPCreation>('/admin/mcp/tokens', input)
  return data
}

// updateToken replaces settings on an owned credential and returns saved metadata.
export async function updateToken(id: number, input: MCPTokenInput): Promise<MCPToken> {
  const { data } = await apiClient.put<MCPToken>(`/admin/mcp/tokens/${id}`, input)
  return data
}

// revokeToken permanently disables an owned credential through its administrator endpoint.
export async function revokeToken(id: number): Promise<void> {
  await apiClient.delete(`/admin/mcp/tokens/${id}`)
}

// getConfirmation reads a redacted review in the owner's authenticated browser session.
export async function getConfirmation(id: string, signal?: AbortSignal): Promise<MCPConfirmation> {
  const { data } = await apiClient.get<MCPConfirmation>(
    `/admin/mcp/confirmations/${encodeURIComponent(id)}`,
    { signal },
  )
  return data
}

// approveConfirmation records browser approval and leaves execution to the matching MCP retry.
export async function approveConfirmation(id: string): Promise<void> {
  await apiClient.post(`/admin/mcp/confirmations/${encodeURIComponent(id)}/approve`)
}

// MCPDownload pairs the returned bytes with the server's suggested download filename.
export interface MCPDownload {
  blob: Blob // Original downloaded result bytes.
  filename: string // Suggested local download filename.
}

// downloadFilename reads UTF-8 extended or ordinary Content-Disposition filenames.
// Malformed extended values fall back to the ordinary parameter. A basename
// keeps server suggestions compatible with file saving, with a default for absent names.
function downloadFilename(disposition: string): string {
  let name = ''
  const extended = /(?:^|;)\s*filename\*=UTF-8'[^']*'([^;]+)/i.exec(disposition)
  if (extended) {
    try {
      name = decodeURIComponent(extended[1]!.trim())
    } catch {
      // An invalid encoded filename must not prevent downloading valid bytes;
      // the ordinary filename parameter below can still provide the server's usable suggestion.
    }
  }
  if (!name) {
    const plain = /(?:^|;)\s*filename=(?:"([^"]*)"|([^;]*))/i.exec(disposition)
    name = (plain?.[1] ?? plain?.[2] ?? '').trim()
  }
  return (
    name
      .split(/[\\/]/)
      .pop()
      ?.replace(/[\r\n]/g, '') || 'tokenrouter-mcp-result'
  )
}

// downloadFile reads a protected result with the normal administrator JWT header.
// Error bytes are decoded before the shared interceptor so expiry and TOTP
// responses retain their structured reason. Successful downloads remain binary
// and use the existing archive timeout for large results.
export async function downloadFile(id: string): Promise<MCPDownload> {
  const { data, headers } = await apiClient.get<Blob>(
    `/admin/mcp/files/${encodeURIComponent(id)}`,
    {
      responseType: 'arraybuffer',
      timeout: 300000,
      transformResponse: [
        (bytes, headers, status) => {
          if (status && status >= 400) {
            try {
              return JSON.parse(new TextDecoder().decode(bytes))
            } catch {
              return {}
            }
          }
          return new Blob([bytes], {
            type: String(headers['content-type'] || 'application/octet-stream'),
          })
        },
      ],
    },
  )
  return { blob: data, filename: downloadFilename(String(headers['content-disposition'] ?? '')) }
}

// downloadSpecification reads the authenticated OpenAPI export as a downloadable document.
export async function downloadSpecification(): Promise<Blob> {
  const { data } = await apiClient.get<Blob>('/admin/mcp/openapi', { responseType: 'blob' })
  return data
}

// MCPOAuthGrant describes one application's stable permissions and latest connection lifetime.
export interface MCPOAuthGrant extends MCPToken {
  client_id: string // Exact registered application identifier.
  client_name: string // Consented application display name.
  resource: string // Canonical authorized MCP resource.
  connection_expires_at: string | null // Latest active credential expiry.
}

// MCPOAuthStatus supplies configured discovery availability without any signing credentials.
export interface MCPOAuthStatus {
  enabled: boolean // OAuth endpoint availability flag.
  reason: string // Missing or invalid frontend address.
  issuer: string // Validated frontend URL origin.
  resource: string // Canonical protected MCP URL.
}

// MCPOAuthReview supplies the browser's request details and session-bound approval proof.
export interface MCPOAuthReview {
  request: {
    id: string // Opaque pending request identifier.
    client: {
      client_id: string // Exact requested client identifier.
      client_name: string // Untrusted application display name.
    }
    expires_at: string // Absolute authorization request deadline.
  }
  nonce: string // Session-bound decision verification proof.
  grant: MCPOAuthGrant | null // Previous live application authorization.
  operations: MCPOperation[] // Mounted selectable operation catalogue.
  redirect_uri: string // Server-validated client callback address.
}

// getOAuthStatus reads the configured OAuth availability for administrator connection guidance.
export async function getOAuthStatus(signal?: AbortSignal): Promise<MCPOAuthStatus> {
  const { data } = await apiClient.get<MCPOAuthStatus>('/admin/mcp/oauth/status', { signal })
  return data
}

// listOAuthGrants reads this administrator's application grants including revoked history.
export async function listOAuthGrants(signal?: AbortSignal): Promise<MCPOAuthGrant[]> {
  const { data } = await apiClient.get<MCPOAuthGrant[]>('/admin/mcp/oauth/grants', { signal })
  return data
}

// updateOAuthGrant conditionally replaces an application's permissions and confirmation policy.
export async function updateOAuthGrant(id: number, input: MCPTokenInput): Promise<MCPOAuthGrant> {
  const { data } = await apiClient.put<MCPOAuthGrant>(`/admin/mcp/oauth/grants/${id}`, input)
  return data
}

// revokeOAuthGrant permanently revokes an owned application and all its credential families.
export async function revokeOAuthGrant(id: number): Promise<void> {
  await apiClient.delete(`/admin/mcp/oauth/grants/${id}`)
}

// getOAuthReview binds the authorization request to the current browser session.
export async function getOAuthReview(id: string, signal?: AbortSignal): Promise<MCPOAuthReview> {
  const { data } = await apiClient.get<MCPOAuthReview>(
    `/admin/mcp/oauth/requests/${encodeURIComponent(id)}`,
    { signal },
  )
  return data
}

// decideOAuthRequest consumes browser consent and returns the server-validated callback URL.
export async function decideOAuthRequest(
  id: string,
  nonce: string,
  approve: boolean,
  permissions: string[],
): Promise<string> {
  const { data } = await apiClient.post<{ redirect_uri: string }>(
    `/admin/mcp/oauth/requests/${encodeURIComponent(id)}/decision`,
    { nonce, approve, permissions },
  )
  return data.redirect_uri
}

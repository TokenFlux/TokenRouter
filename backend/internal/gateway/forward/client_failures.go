// 失败事实与安全展示标识由转发契约唯一拥有。
package forward

const (
	OpenAIRequestBodyTooLargeClientMessage     = "Request payload is too large"
	OpenAIUpstreamAccessStateReason            = GatewayFailureReason("openai_upstream_access_state")
	OpenAIHTTPContinuationUnsupportedReason    = GatewayFailureReason("openai_http_continuation_unsupported")
	AntigravityCredentialRejectedClientMessage = "Antigravity rejected the OAuth credential after refresh; reauthorize the account and verify project_id"
	AntigravityCredentialRejectedReason        = GatewayFailureReason("antigravity_oauth_credential_rejected")
)

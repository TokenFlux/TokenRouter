package httpapi

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"net/http"
	"strings"
)

// CredentialFailoverClientResponse 仅投影已经判定的凭据失败，不改变重试资格。
func CredentialFailoverClientResponse(failoverErr *forwardcore.UpstreamFailoverError) (int, string) {
	if failoverErr != nil && failoverErr.Reason == forwardcore.OpenAIUpstreamAccessStateReason && strings.TrimSpace(failoverErr.ClientMessage) != "" {
		status := failoverErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		return status, failoverErr.ClientMessage
	}
	if failoverErr != nil && failoverErr.Reason == forwardcore.AntigravityCredentialRejectedReason {
		return http.StatusBadGateway, forwardcore.AntigravityCredentialRejectedClientMessage
	}
	return http.StatusServiceUnavailable, forwardcore.GrokCredentialUnavailableClientMessage
}

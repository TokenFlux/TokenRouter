package provider

import (
	"net/http"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/tidwall/gjson"
)

func CompatContinuationEnabled(account *ExecutionAccount, model string) bool {
	if account == nil || account.Record.Type != capability.AccountTypeAPIKey {
		return false
	}
	if !accountcore.ResolveResponsesContinuationSupported(account.Record.Extra) {
		return false
	}
	return ShouldAutoInjectPromptCacheKeyForCompat(model)
}
func CompatPreviousResponseNotFound(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
	}
	check := func(s string) bool {
		lower := strings.ToLower(strings.TrimSpace(s))
		return strings.Contains(lower, "previous_response_not_found") ||
			lower == "previous_response_id is not available for this user" ||
			(strings.Contains(lower, "previous response") && strings.Contains(lower, "not found")) ||
			(strings.Contains(lower, "unsupported parameter") && strings.Contains(lower, "previous_response_id"))
	}
	if check(upstreamMsg) || check(string(upstreamBody)) {
		return true
	}
	return check(gjson.GetBytes(upstreamBody, "error.code").String()) ||
		check(gjson.GetBytes(upstreamBody, "error.message").String())
}
func CompatPreviousResponseUnsupported(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	check := func(s string) bool {
		lower := strings.ToLower(strings.TrimSpace(s))
		if !strings.Contains(lower, "previous_response_id") {
			return false
		}
		return strings.Contains(lower, "unsupported parameter") ||
			strings.Contains(lower, "only supported on responses websocket") ||
			strings.Contains(lower, "not supported") ||
			strings.Contains(lower, "requires an openai api-key account for http requests")
	}
	if check(upstreamMsg) || check(string(upstreamBody)) {
		return true
	}
	return check(gjson.GetBytes(upstreamBody, "error.code").String()) ||
		check(gjson.GetBytes(upstreamBody, "error.message").String())
}

package messageforward

import (
	"context"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

// PrepareBedrockCompatibility 保留分组映射后的清理位置和 Header 原地更新行为。
func (r *Runtime) PrepareBedrockCompatibility(ctx context.Context, headers http.Header, body []byte, model string, target *provider.ExecutionAccount, groupID *int64) []byte {
	if groupID == nil || r.dependencies.GroupPolicies == nil {
		return body
	}
	policy, err := r.dependencies.GroupPolicies.GetGroupPolicy(ctx, *groupID)
	if err != nil || policy == nil || !policy.IsBedrockCCCompatEnabled(target.Record.Platform) {
		return body
	}
	body = bedrock.SanitizeBedrockCCFields(body)
	body = bedrock.SanitizeBedrockThinking(body, model)
	body = bedrock.SanitizeBedrockToolUseIDs(body)
	body = bedrock.SanitizeBedrockCCBetaTokens(body, model)
	if beta := headers.Get("anthropic-beta"); beta != "" {
		if filtered := bedrock.ResolveBedrockBetaTokens(beta, body, model); len(filtered) > 0 {
			headers.Set("anthropic-beta", strings.Join(filtered, ", "))
		} else {
			headers.Del("anthropic-beta")
		}
	}
	return body
}

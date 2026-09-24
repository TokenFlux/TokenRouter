package httpapi

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// ApplyHTTPFailure 保留旧 HTTP 失败入口只使用首个显式模型的边界。
func (p *OpenAIResponseOutput) ApplyHTTPFailure(ctx context.Context, resp *http.Response, target *provider.ExecutionAccount, body []byte, models ...string) account.UpstreamErrorDecision {
	if len(models) > 0 {
		return provider.ApplyOpenAIResponseHealth(ctx, p.Health, target, resp.StatusCode, resp.Header, body, false, models[0])
	}
	return provider.ApplyOpenAIResponseHealth(ctx, p.Health, target, resp.StatusCode, resp.Header, body, false)
}

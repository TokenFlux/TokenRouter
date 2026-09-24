//go:build unit

package service

import (
	"context"
	"net/http"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	provider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
)

// mediaErrorResponseFixture 通过生产媒体入口处理给定响应，仍使用场景原健康实例。
func mediaErrorResponseFixture(executor *gatewayhttp.GrokExecutor, ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, requestID, model string) (*forward.OpenAIResult, error) {
	local := *executor
	local.Transport = &httpUpstreamRecorder{resp: resp}
	local.Credentials = testkit.RequestCredentials(nil, &account.OpenAIExecutionCredentials{Grok: func(context.Context, *account.Record) (string, error) { return "fixture-token", nil }}, nil, nil)
	local.Credentials.HasGrokTokenSource = true
	if target.Record.Credentials == nil {
		target.Record.Credentials = map[string]any{}
	}
	target.Record.Credentials["api_key"] = "fixture-token"
	resp.Header.Set("x-request-id", requestID)
	return local.ForwardGrokMedia(ctx, c, target, grok.GrokMediaEndpointImagesGenerations, "", []byte(`{"model":"`+model+`","prompt":"fixture"}`), "application/json")
}

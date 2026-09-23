package httpapi

import (
	modeltrace "github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"

	"context"
	"strings"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// APIKeyModelRedirectContext 为非 HTTP 请求体入口创建单次 Key 重定向上下文。
func APIKeyModelRedirectContext(ctx context.Context, apiKey *apikey.APIKey, clientModel string) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	clientModel = strings.TrimSpace(clientModel)
	targetModel, matched := apiKey.ResolveModelMapping(clientModel)
	if !matched {
		return ctx, clientModel
	}
	trace := modeltrace.NewAPIKeyModelRedirectTrace(clientModel, clientModel, targetModel)
	ctx = modeltrace.WithContext(ctx, trace)
	ctx = context.WithValue(ctx, telemetry.ClientModel, clientModel)
	return ctx, targetModel
}

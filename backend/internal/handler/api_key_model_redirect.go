package handler

import (
	"context"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/service"
)

// apiKeyModelRedirectContext 为非 HTTP 请求体入口创建单次 Key 重定向上下文。
func apiKeyModelRedirectContext(ctx context.Context, apiKey *service.APIKey, clientModel string) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	clientModel = strings.TrimSpace(clientModel)
	targetModel, matched := apiKey.ResolveModelMapping(clientModel)
	if !matched {
		return ctx, clientModel
	}
	trace := service.NewAPIKeyModelRedirectTrace(clientModel, clientModel, targetModel)
	ctx = service.WithAPIKeyModelRedirectTrace(ctx, trace)
	ctx = context.WithValue(ctx, ctxkey.ClientModel, clientModel)
	return ctx, targetModel
}

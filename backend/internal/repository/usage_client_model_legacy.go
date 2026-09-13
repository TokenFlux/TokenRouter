package repository

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// applyClientModel 仅覆盖展示用请求模型，不改变实际模型与计费模型。
func applyClientModel(ctx context.Context, log *service.UsageLog) {
	if ctx == nil || log == nil {
		return
	}
	if clientModel, ok := ctx.Value(ctxkey.ClientModel).(string); ok && strings.TrimSpace(clientModel) != "" {
		log.RequestedModel = strings.TrimSpace(clientModel)
	}
}

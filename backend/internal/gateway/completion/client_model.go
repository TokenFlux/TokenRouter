package completion

import (
	usage "github.com/TokenFlux/TokenRouter/internal/usage"
)

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// applyClientModel 仅覆盖展示用请求模型，不改变实际模型与计费模型。
func applyClientModel(ctx context.Context, log *usage.UsageLog) {
	if ctx == nil || log == nil {
		return
	}
	if clientModel, ok := ctx.Value(telemetry.ClientModel).(string); ok && strings.TrimSpace(clientModel) != "" {
		log.RequestedModel = strings.TrimSpace(clientModel)
	}
}

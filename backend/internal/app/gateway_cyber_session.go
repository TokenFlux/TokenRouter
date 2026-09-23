package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
)

func provideCyberBlocks(cache session.GatewayCache, settings *moderation.RuntimeSettings) *session.CyberBlocks {
	return session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(cache), settings.GetCyberSessionBlockRuntime, func(format string, args ...any) { logging.LegacyPrintf("service.openai_gateway", format, args...) })
}

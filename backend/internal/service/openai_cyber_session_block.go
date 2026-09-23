package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

func (s *OpenAIGatewayService) BindCyberBlocks(core *session.CyberBlocks) { s.cyberBlocks = core }
func (s *OpenAIGatewayService) CyberBlocks() *session.CyberBlocks {
	if s == nil {
		return nil
	}
	if s.cyberBlocks != nil {
		return s.cyberBlocks
	}
	var read func(context.Context) (bool, time.Duration)
	if s.settingService != nil {
		read = s.settingService.Moderation.GetCyberSessionBlockRuntime
	}
	return session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(s.cache), read, func(format string, args ...any) { logging.LegacyPrintf("service.openai_gateway", format, args...) })
}

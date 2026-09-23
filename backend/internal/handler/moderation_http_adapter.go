package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"
)

func nativeModerationPort(s *moderationcore.ContentModerationService) gatewayhttp.ModerationPort {
	if s == nil {
		return nil
	}
	return s
}
func moderationAccountView(a *gatewayprovider.ExecutionAccount) *moderationflow.Account {
	if a == nil {
		return nil
	}
	return &moderationflow.Account{ID: a.Record.ID, Name: a.Record.Name, Platform: a.Record.Platform}
}

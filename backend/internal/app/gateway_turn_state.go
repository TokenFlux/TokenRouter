package app

import (
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// provideCodexTurnStateHeaders 为所有 HTTP 响应路径绑定同一来源表和原粘性 TTL。
func provideCodexTurnStateHeaders(choices *selection.Compatible) *gatewayhttp.CodexTurnStateHeaders {
	return &gatewayhttp.CodexTurnStateHeaders{Origins: session.NewCodexTurnOrigins(time.Now), TTL: choices.SessionStickyTTL}
}

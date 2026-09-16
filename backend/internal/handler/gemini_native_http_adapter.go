// Gemini 固定端口只提供读取、绑定及一次执行桥接，所有 HTTP 前置组合位于目标 Adapter。
package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type geminiNativeHTTPBackend struct{ messagesHTTPBackend }

// NewGeminiNativeHTTPHandler 只构造固定依赖，可由 app 直接绑定生成路由。
func (h *GatewayHandler) NewGeminiNativeHTTPHandler() *gatewayhttp.GeminiNativeHandler {
	return gatewayhttp.NewGeminiNativeHandler(
		gatewayhttp.GeminiNativeOptions{MaxSwitches: h.maxAccountSwitchesGemini},
		geminiNativeHTTPBackend{messagesHTTPBackend{h}},
		h.gatewayService,
		h.concurrencyHelper,
		func() string { return uuid.New().String() },
		h.NewGeminiNativeExecutor(),
	)
}
func (p geminiNativeHTTPBackend) HasForcedPlatform(c *gin.Context) bool {
	return middleware.HasForcePlatform(c)
}
func (p geminiNativeHTTPBackend) SafeModelSegment(model string) bool {
	return service.IsSafeGeminiModelPathSegment(model)
}
func (p geminiNativeHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, model string, body []byte) *moderation.Decision {
	return p.h.checkContentModeration(c, log, service.APIKeyFromView(key), subject, moderation.ContentModerationProtocolGemini, model, body)
}
func (p geminiNativeHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, userID int64, hash string) error {
	return p.h.ensureGatewaySessionIsolation(ctx, service.APIKeyFromView(key), userID, service.SessionIsolationSourceGemini, hash)
}
func (p geminiNativeHTTPBackend) DigestChain(request *protocolgemini.GeminiRequest) string {
	return service.BuildGeminiDigestChain(request)
}
func (p geminiNativeHTTPBackend) PrefixHash(userID, keyID int64, ip, agent, platform, model string) string {
	return service.GenerateGeminiPrefixHash(userID, keyID, ip, agent, platform, model)
}
func (p geminiNativeHTTPBackend) FindSession(ctx context.Context, groupID int64, prefix, chain string) (string, int64, string, bool) {
	return p.h.gatewayService.FindGeminiSession(ctx, groupID, prefix, chain)
}
func (p geminiNativeHTTPBackend) DigestSessionKey(prefix, id string) string {
	return service.GenerateGeminiDigestSessionKey(prefix, id)
}
func (p geminiNativeHTTPBackend) BindSticky(ctx context.Context, groupID *int64, key string, accountID int64) error {
	return p.h.gatewayService.BindStickySession(ctx, groupID, key, accountID)
}

// NewGeminiNativeExecutor 在 Recorder 完成绑定后构造，不增加共享运行状态。
func (h *GatewayHandler) NewGeminiNativeExecutor() *textflow.MessagesExecutor {
	runtime := &fixedMessagesRuntime{dependencies: newMessageExecutionDependencies(h)}
	return textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, StopOnCanceledContext: false, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, StopOnCanceledContext: false, Observe: telemetry.Failover})
}

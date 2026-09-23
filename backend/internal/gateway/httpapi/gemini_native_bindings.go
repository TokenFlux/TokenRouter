package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"

	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GeminiHTTPBindings 只包含会话和路径资格读取，不持有平台或旧网关服务。
type GeminiHTTPBindings struct {
	SafeModelSegment func(string) bool
	FindSession      func(context.Context, int64, string, string) (string, int64, string, bool)
	BindSticky       func(context.Context, *int64, string, int64) error
}
type geminiNativeHTTPBackend struct {
	messagesHTTPBackend
	gemini GeminiHTTPBindings
}

// NewBoundGeminiNativeHandler 组合共享 HTTP 行为与 Gemini 的明确读取端口。
func NewBoundGeminiNativeHandler(options GeminiNativeOptions, bindings MessagesBindings, gemini GeminiHTTPBindings, prompt MessagesPrompt, concurrency *ConcurrencyHelper, newID func() string, executor execution.Executor) *GeminiNativeHandler {
	return NewGeminiNativeHandler(options, geminiNativeHTTPBackend{messagesHTTPBackend{bindings}, gemini}, prompt, concurrency, newID, executor)
}
func (p geminiNativeHTTPBackend) HasForcedPlatform(c *gin.Context) bool {
	return keyhttp.HasForcePlatform(c)
}
func (p geminiNativeHTTPBackend) SafeModelSegment(model string) bool {
	return p.gemini.SafeModelSegment(model)
}
func (p geminiNativeHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, model string, body []byte) *moderation.Decision {
	return RunContentModeration(GatewayModerationEndpoints{}, c, log, p.bindings.Moderation, apikey.CopyAPIKey(key), subject, moderation.ContentModerationProtocolGemini, model, body)
}
func (p geminiNativeHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, userID int64, hash string) error {
	if p.bindings.IsolateSession == nil {
		return nil
	}
	return p.bindings.IsolateSession(ctx, apikey.CopyAPIKey(key), userID, session.SessionIsolationSourceGemini, hash)
}
func (p geminiNativeHTTPBackend) DigestChain(request *protocolgemini.GeminiRequest) string {
	return session.BuildGeminiDigestChain(request)
}
func (p geminiNativeHTTPBackend) PrefixHash(userID, keyID int64, ip, agent, platform, model string) string {
	return session.GenerateGeminiPrefixHash(userID, keyID, ip, agent, platform, model)
}
func (p geminiNativeHTTPBackend) FindSession(ctx context.Context, groupID int64, prefix, chain string) (string, int64, string, bool) {
	return p.gemini.FindSession(ctx, groupID, prefix, chain)
}
func (p geminiNativeHTTPBackend) DigestSessionKey(prefix, id string) string {
	return session.GenerateGeminiDigestSessionKey(prefix, id)
}
func (p geminiNativeHTTPBackend) BindSticky(ctx context.Context, groupID *int64, key string, accountID int64) error {
	return p.gemini.BindSticky(ctx, groupID, key, accountID)
}

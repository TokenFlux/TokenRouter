// HTTP→WS 旧边界仅投影一次平台执行和会话/指标操作，不拥有重连循环。
package service

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

type openAIHTTPWSForwardAdapter struct {
	s                            *OpenAIGatewayService
	c                            *gin.Context
	account                      *Account
	clientPromptCacheKey, token  string
	decision                     egress.OpenAIWSProtocolDecision
	isCodexCLI, stream           bool
	originalModel, upstreamModel string
	startedAt                    time.Time
	tls                          egress.TLSFingerprintRouterMatchResult
	lineageGroupID               int64
	lineageSessionHash           string
}

func (p *openAIHTTPWSForwardAdapter) Execute(ctx context.Context, body map[string]any, attempt int, lastReason string, agentRecovered *bool) (*gatewayws.ForwardResult, error) {
	result, err := p.s.forwardOpenAIWSV2(ctx, p.c, p.account, body, p.clientPromptCacheKey, p.token, p.decision, p.isCodexCLI, p.stream, p.originalModel, p.upstreamModel, p.startedAt, attempt, lastReason, p.tls, agentRecovered)
	return wsForwardResult(result), err
}
func (p *openAIHTTPWSForwardAdapter) OutputCommitted() bool {
	return p.c != nil && p.c.Writer != nil && p.c.Writer.Written()
}
func (p *openAIHTTPWSForwardAdapter) AgentTaskRecovered(err error) bool {
	var recovered *agentIdentityTaskRecoveredError
	return errors.As(err, &recovered)
}
func (p *openAIHTTPWSForwardAdapter) ClassifyError(err error) (string, bool) {
	return classifyOpenAIWSReconnectReason(err)
}
func (p *openAIHTTPWSForwardAdapter) PayloadString(body map[string]any, key string) string {
	return protocolopenai.WSPayloadString(body, key)
}
func (p *openAIHTTPWSForwardAdapter) EncryptedDigests(body []byte) []string {
	return openai.CollectOpenAIEncryptedContentDigestsRaw(body)
}
func (p *openAIHTTPWSForwardAdapter) MarkEncrypted(entry []byte, digests []string) {
	if p.lineageSessionHash == "" {
		p.lineageSessionHash = p.s.GenerateSessionHash(p.c, entry)
	}
	p.s.markOpenAIWSInvalidEncryptedContentLineage(p.lineageGroupID, p.lineageSessionHash, digests)
}
func (p *openAIHTTPWSForwardAdapter) TruncateID(v string, limit int) string {
	return gatewayprovider.TruncateOpenAIWSLogValue(v, limit)
}
func (p *openAIHTTPWSForwardAdapter) NormalizeLog(v string) string {
	return gatewayprovider.NormalizeOpenAIWSLogValue(v)
}
func (p *openAIHTTPWSForwardAdapter) ClassifyPrevious(v string) string {
	return ClassifyOpenAIPreviousResponseIDKind(v)
}
func (p *openAIHTTPWSForwardAdapter) RetryBudget() time.Duration {
	return p.s.openAIWSRetryTotalBudget()
}
func (p *openAIHTTPWSForwardAdapter) RetryBackoff(attempt int) time.Duration {
	return p.s.openAIWSRetryBackoff(attempt)
}
func (p *openAIHTTPWSForwardAdapter) RecordExhausted() { p.s.recordOpenAIWSRetryExhausted() }
func (p *openAIHTTPWSForwardAdapter) RecordRetry(delay time.Duration) {
	p.s.recordOpenAIWSRetryAttempt(delay)
}
func (p *openAIHTTPWSForwardAdapter) RecordNonRetryable() {
	p.s.recordOpenAIWSNonRetryableFastFallback()
}
func (p *openAIHTTPWSForwardAdapter) FallbackError(reason string, err error) error {
	return gatewayws.WrapFallback(reason, err)
}
func (p *openAIHTTPWSForwardAdapter) WriteFailure(err error) {
	p.s.writeOpenAIWSFallbackErrorResponse(p.c, p.account, err)
}
func (p *openAIHTTPWSForwardAdapter) Debug(msg string) {
	gatewayprovider.LogOpenAIWSModeDebug("%s", msg)
}
func (p *openAIHTTPWSForwardAdapter) Info(msg string) { gatewayprovider.LogOpenAIWSModeInfo("%s", msg) }

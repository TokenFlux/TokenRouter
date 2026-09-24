package service

import (
	"context"
	"net/http"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	grokforward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/grokforward"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	uuid "github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) forwardGrokResponses(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	adapter := &grokForwardAdapter{s: s, c: c, account: account}
	result, err := grokforward.Forward(ctx, adapter, adapter.options(), adapter.input(body, originalModel, reqStream, startTime))
	return legacyGrokForwardResult(result), err
}

func isGrokInvalidEncryptedContentResponse(statusCode int, body []byte) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		IsGrokInvalidEncryptedContentResponse(statusCode, body)
}

func isGrokCompactionReplayDecodeError(statusCode int, body []byte) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		IsGrokCompactionReplayDecodeError(statusCode, body)
}

func sanitizeGrokCompactionReplayBody(body []byte) ([]byte, bool, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		SanitizeGrokCompactionReplayBody(body)
}

func requestHasGrokEncryptedReasoning(body []byte) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		RequestHasGrokEncryptedReasoning(body)
}

func markGrokEncryptedContentStripRetried(ctx context.Context) context.Context {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		MarkGrokEncryptedContentStripRetried(ctx)
}

func grokEncryptedContentStripRetried(ctx context.Context) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		GrokEncryptedContentStripRetried(ctx)
}

func trimGrokInvalidEncryptedContentRetryBody(body []byte) ([]byte, bool, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		TrimGrokInvalidEncryptedContentRetryBody(body)
}

func patchGrokResponsesBody(body []byte, upstreamModel string) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		PatchGrokResponsesBody(body, upstreamModel)
}

func normalizeGrokChatReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		NormalizeGrokChatReasoningEffort(body, upstreamModel)
}

func sanitizeGrokResponsesInput(body []byte) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		SanitizeGrokResponsesInput(body)
}

func (s *OpenAIGatewayService) bridgeGrokComposerImageInputs(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
) ([]byte, openai.ForwardUsage, bool, error) {
	return grok.BridgeComposerImages(body, func(imageURL string, index int) (string, openai.ForwardUsage, error) {
		return s.describeGrokComposerImage(ctx, c, account, token, imageURL, index)
	})
}

// describeGrokComposerImage 复用 Grok Responses 转发配置执行单张图片预检，
// 并沿用账号快照、错误记录和故障转移策略。
func (s *OpenAIGatewayService) describeGrokComposerImage(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	token string,
	imageURL string,
	index int,
) (string, openai.ForwardUsage, error) {
	adapter := &grokForwardAdapter{s: s, c: c, account: account, token: token}
	return grokforward.DescribeImage(ctx, adapter, adapter.options(), adapter.input(nil, "", false, time.Time{}), imageURL, index)
}

func buildGrokResponsesRequest(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, token, cacheIdentity string, cfg *config.Config, settings ...*gatewayprovider.RuntimeReaders) (*http.Request, error) {
	targetURL, err := buildGrokResponsesURL(account, cfg, settings...)
	if err != nil {
		return nil, err
	}
	beta := ""
	if c != nil {
		beta = c.GetHeader("OpenAI-Beta")
	}
	return grok.BuildResponsesRequest(ctx, body, grok.ResponsesRequestOptions{

		URL: targetURL,

		Token: token,

		CacheIdentity: cacheIdentity,

		OAuth: account.View().IsGrokOAuth(),

		OpenAIBeta: beta,

		Profile: func(ctx context.Context) context.Context {
			return upstream.WithHTTPUpstreamProfile(ctx, upstream.HTTPUpstreamProfileGrok)
		},

		ApplyOverrides: bindAccountHeaders(account),
	})
}

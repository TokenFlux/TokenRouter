package httpapi

import (
	"context"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/grokforward"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
)

func (s *GrokExecutor) ForwardResponses(
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
	return result, err
}
func (s *GrokExecutor) BridgeComposerImages(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
) ([]byte, openai.ForwardUsage, bool, error) {
	return grok.BridgeComposerImages(body, func(imageURL string, index int) (string, openai.ForwardUsage, error) {
		return s.DescribeComposerImage(ctx, c, account, token, imageURL, index)
	})
}

// DescribeComposerImage 复用 Grok Responses 转发配置执行单张图片预检，
// 并沿用账号快照、错误记录和故障转移策略。
func (s *GrokExecutor) DescribeComposerImage(
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

package httpapi

import (
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// OpenAITextExecutor 组合协议执行、固定请求构造与独立会话状态，不拥有选号或完成队列。
type OpenAITextExecutor struct {
	Compact        *CompactExecutor
	Requests       *OpenAIRequests
	Output         *OpenAIResponseOutput
	Grok           *GrokExecutor
	Credentials    *provider.RequestCredentials
	FastPolicy     *provider.ExecutionFastPolicy
	Continuation   *session.CompatResponses
	PromptCache    *session.AnthropicPromptCache
	CodexUsage     *accountprovider.CodexUsageObserver
	ForcedTemplate string
	ResponseTTL    func() time.Duration
}

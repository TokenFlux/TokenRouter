package httpapi

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// responsesFixtureOptions 只描述 Responses 断言使用的请求、响应和图片桥接选项。
type responsesFixtureOptions struct {
	Request     OpenAIRequestOptions
	Response    OpenAIResponseOptions
	ImageBridge bool
}

type responsesFixtureInputs struct {
	compactModel string
	transport    httpclient.UpstreamTransport
	options      *responsesFixtureOptions
}

// newResponsesFixture 直接构造原生单次 HTTP 链，不创建账号选择器、WS 池或完成队列。
func newResponsesFixture(v responsesFixtureInputs) *OpenAIResponsesExecutor {
	aux := newAuxiliaryFixture(auxiliaryFixtureInputs{transport: v.transport})
	options := responsesFixtureOptions{}
	if v.options != nil {
		options = *v.options
	}
	aux.Requests.Options = options.Request
	aux.Output.Options = options.Response
	aux.Output.Options.Configured = true
	if aux.Output.Options.ReadLimit == 0 {
		aux.Output.Options.ReadLimit = 128 * 1024 * 1024
	}
	aux.Output.Corrector = openai.NewCodexToolCorrector()
	aux.Output.Turns = aux.Requests.Turns
	aux.Output.Reasoning = &session.ReasoningHistory{}
	store := session.NewOpenAIWSStateStore(nil, provider.LogOpenAIWSModeInfo)
	aux.Output.Responses = store
	aux.Output.ResponseTTL = func() time.Duration { return time.Hour }
	requests := aux.Requests
	credentials := testkit.RequestCredentials(nil, nil, nil, nil)
	routes := provider.GrokRoutes{Validate: (egress.OperatorURLPolicy{}).Validate}
	requests.GrokRoutes = routes
	grok := &GrokExecutor{Credentials: credentials, Transport: v.transport, Output: aux.Output, Health: aux.Output.GrokHealth, Routes: routes, Failure: requests.Failure}
	text := &OpenAITextExecutor{Requests: requests, Output: aux.Output, Grok: grok, Credentials: credentials, FastPolicy: &provider.ExecutionFastPolicy{}, Continuation: &session.CompatResponses{TTL: aux.Output.ResponseTTL}, PromptCache: session.NewAnthropicPromptCache(time.Now), CodexUsage: aux.CodexUsage, ResponseTTL: aux.Output.ResponseTTL, Compact: &CompactExecutor{Models: provider.CompactModels{Default: v.compactModel}}}
	return &OpenAIResponsesExecutor{Requests: requests, Output: aux.Output, Text: text, Grok: grok, Lineage: &OpenAIEncryptedLineage{Store: store, TTL: aux.Output.ResponseTTL}, ImageBridge: &provider.ResponseImagePolicy{DefaultEnabled: options.ImageBridge}, ResolveTransport: func(*provider.ExecutionAccount) egress.OpenAIWSProtocolDecision {
		return egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportHTTPSSE}
	}}
}

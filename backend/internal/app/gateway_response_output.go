package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provideOpenAIResponseHealth 复用账号健康、选号状态和延迟写入的生产实例。
func provideOpenAIResponseHealth(observer *accountprovider.UpstreamHealth, blocks *account.RuntimeBlockState, models *account.ModelTransientState, deferred *account.DeferredService) *accountprovider.OpenAIResponseHealth {
	return &accountprovider.OpenAIResponseHealth{Health: observer, Runtime: blocks, ModelTransient: models, Deferred: deferred}
}

// provideOpenAIResponseOutput 只投影静态参数并绑定输出所需的固定端口。
func provideOpenAIResponseOutput(cfg *config.Config, health *accountprovider.OpenAIResponseHealth, grok *accountprovider.GrokHealth, observer *accountprovider.UpstreamHealth, headers *egress.CompiledHeaderFilter, turns *gatewayhttp.CodexTurnStateHeaders, circuit *egress.ProxyStreamCircuit, readers *provider.RuntimeReaders, responses session.OpenAIWSStateStore, choices *selection.Compatible, history *session.ReasoningHistory) *gatewayhttp.OpenAIResponseOutput {
	output := &gatewayhttp.OpenAIResponseOutput{
		Reasoning: history, Health: health, GrokHealth: grok, Observer: observer, Headers: headers, Turns: turns,
		Corrector: openai.NewCodexToolCorrector(), ProxyCircuit: circuit, Responses: responses,
		ResponseTTL: choices.OpenAIHTTPResponseStickyTTL,
		Options:     gatewayhttp.OpenAIResponseOptions{ReadLimit: config.DefaultUpstreamResponseReadMaxBytes},
	}
	if readers != nil {
		output.TTFT = readers.Gateway.GetOpenAITTFTMode
	}
	if cfg != nil {
		v := cfg.Gateway
		output.Options.Configured = true
		output.Options.MaxLineSize = v.MaxLineSize
		output.Options.StreamDataIntervalTimeout = v.StreamDataIntervalTimeout
		output.Options.StreamKeepaliveInterval = v.StreamKeepaliveInterval
		output.Options.ImageStreamDataIntervalTimeout = v.ImageStreamDataIntervalTimeout
		output.Options.ImageStreamKeepaliveInterval = v.ImageStreamKeepaliveInterval
		output.Options.OpenAIFirstOutputTimeoutSeconds = v.OpenAIFirstOutputTimeoutSeconds
		output.Options.OpenAIHighEffortFirstOutputTimeoutSeconds = v.OpenAIHighEffortFirstOutputTimeoutSeconds
		output.Options.LogUpstreamErrorBody = v.LogUpstreamErrorBody
		output.Options.LogUpstreamErrorBodyMaxBytes = v.LogUpstreamErrorBodyMaxBytes
		output.Options.ResponseHeadersEnabled = cfg.Security.ResponseHeaders.Enabled
		if v.UpstreamResponseReadMaxBytes > 0 {
			output.Options.ReadLimit = v.UpstreamResponseReadMaxBytes
		}
	}
	return output
}

// provideReasoningHistory 只投影已有缓存的可选能力，不新建缓存或连接。
func provideReasoningHistory(cache session.GatewayCache) *session.ReasoningHistory {
	store, _ := cache.(session.ReasoningContentCache)
	return &session.ReasoningHistory{Cache: store, Warn: provider.WarnReasoningCacheFailure}
}

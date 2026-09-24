package httpapi

import (
	"context"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// OpenAIResponseOptions 由装配投影静态配置，动态 TTFT 仍通过读取端口按原时点求值。
type OpenAIResponseOptions struct {
	ImageStreamDataIntervalTimeout, ImageStreamKeepaliveInterval               int
	Configured                                                                 bool
	MaxLineSize                                                                int
	StreamDataIntervalTimeout, StreamKeepaliveInterval                         int
	OpenAIFirstOutputTimeoutSeconds, OpenAIHighEffortFirstOutputTimeoutSeconds int
	LogUpstreamErrorBody                                                       bool
	LogUpstreamErrorBodyMaxBytes                                               int
	ResponseHeadersEnabled                                                     bool
	ReadLimit                                                                  int64
}

// OpenAIResponseOutput 组合原生响应读取器与 HTTP 输出、健康和诊断，不持有选号或结算循环。
type OpenAIResponseOutput struct {
	Reasoning    *session.ReasoningHistory
	Options      OpenAIResponseOptions
	Health       *accountprovider.OpenAIResponseHealth
	GrokHealth   *accountprovider.GrokHealth
	Observer     *accountprovider.UpstreamHealth
	Headers      *egress.CompiledHeaderFilter
	Turns        *CodexTurnStateHeaders
	Corrector    *openai.CodexToolCorrector
	ProxyCircuit *egress.ProxyStreamCircuit
	TTFT         func(context.Context) string
	Redact       func(context.Context, *provider.ExecutionAccount, []byte) []byte
	Responses    session.OpenAIWSStateStore
	ResponseTTL  func() time.Duration
}

func (p *OpenAIResponseOutput) TTFTMode(ctx context.Context) string {
	mode := gateway.OpenAITTFTModeSemantic
	if p != nil && p.TTFT != nil {
		mode = p.TTFT(ctx)
	}
	return gateway.NormalizeOpenAITTFTMode(mode)
}
func (p *OpenAIResponseOutput) redact(ctx context.Context, target *provider.ExecutionAccount, body []byte) []byte {
	if p == nil || p.Redact == nil {
		return body
	}
	return p.Redact(ctx, target, body)
}

const openAIResponseDefaultMaxLineSize = 500 * 1024 * 1024

// ExecutionErrorAccount 仅投影当前尝试的诊断字段，不向输出层传递凭据。
func ExecutionErrorAccount(value *provider.ExecutionAccount) *UpstreamErrorAccount {
	if value == nil {
		return nil
	}
	return &UpstreamErrorAccount{ID: value.Record.ID, Name: value.Record.Name, Platform: value.Record.Platform}
}

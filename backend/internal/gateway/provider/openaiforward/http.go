// Package openaiforward 编排当前账号的 HTTP 恢复与响应消费，账号切换仍由 gateway/text 拥有。
package openaiforward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
)

// HTTPInput 只携带已完成账号和渠道处理的请求快照，不包含业务实体或凭据。
type HTTPInput struct {
	Body, LineageEntryBody                                     []byte
	AccountID                                                  int64
	AccountName, Platform                                      string
	RequestedModel, OriginalModel, BillingModel, UpstreamModel string
	ReasoningEffort                                            *string
	ReasoningEffortValue                                       string
	ImageBillingModel, ImageSizeTier, ImageInputSize           string
	Stream, OAuth, Shadow, Grok                                bool
	StartedAt                                                  time.Time
}

type Result struct {
	UpstreamEndpoint                     string
	RequestedReasoningEffort             *string
	OpenAIWSMode                         bool
	UpstreamTerminalEvent                string
	ResponseHeaders                      http.Header
	ImageOutputSize, ImageSizeSource     string
	ImageSizeBreakdown                   map[string]int
	UpstreamWarning                      *forwardcore.UpstreamWarning
	VideoCount                           int
	VideoResolution                      string
	VideoDurationSeconds, WebSearchCalls int
	AudioUsage                           *protocol.AudioUsage

	ClientDisconnect                                                bool
	SearchCount                                                     int
	RequestID, ResponseID                                           string
	Headers                                                         http.Header
	Usage                                                           wire.ForwardUsage
	Model, BillingModel, UpstreamModel, UpstreamResponseServiceTier string
	ServiceTier, ReasoningEffort                                    *string
	Stream                                                          bool
	Duration                                                        time.Duration
	FirstTokenMs                                                    *int
	ImageCount                                                      int
	ImageSize, ImageInputSize                                       string
	ImageOutputSizes                                                []string
}

// CompactFailure 是流读取器返回的恢复信号投影，不持有原错误的业务对象。
type CompactFailure struct {
	Message string
	Payload []byte
}

// HTTPOptions 的端口仅处理一次外部操作或值转换；恢复次数和重试顺序由 RunHTTP 唯一拥有。
type HTTPOptions struct {
	Exchange              openai.HTTPExchangeOptions
	Sink                  upstream.OutputSink
	StreamOptions         func() openai.StreamOptions
	NonStreamOptions      func() openai.NonStreamOptions
	ReadErrorBody         func(*http.Response) []byte
	IsAgentIdentity       func(context.Context) bool
	InvalidAgentTask      func(int, []byte) bool
	RecoverAgentTask      func(context.Context) error
	RedactErrorBody       func(context.Context, []byte) []byte
	ErrorDetails          func([]byte) (string, string)
	RetryEncrypted        func([]byte) ([]byte, bool, error)
	MarkInvalidLineage    func([]byte)
	CompactRetry          func([]byte, int, string, []byte, bool) ([]byte, string, bool)
	CompactRetryObserved  func(*http.Response, []byte, string)
	CompactSignal         func(error) (CompactFailure, bool)
	CompactErrorResponse  func(*http.Response, CompactFailure) (*http.Response, []byte)
	ShouldFailover        func(int, string, []byte) bool
	HTTPFailover          func(*http.Response, []byte, string, string) error
	CompactFailover       func(*http.Response, []byte, string, string) error
	ErrorResponse         func(*http.Response, []byte, string) error
	ErrorSchedulingModel  func(string, string) string
	WrapResponseBody      func(*http.Response)
	ReleaseDecodedRequest func()
	BindResponseOwner     func(context.Context, string)
	UpdateUsageSnapshot   func(context.Context, http.Header)
	ObserveUpstreamModel  func(string)
	ObservedServiceTier   func() string
	ResolvedServiceTier   func(*string) *string
	ExtractServiceTier    func([]byte) *string
	Log                   func(string, ...any)
}

// RunHTTP 保留 agent task、失效密文、被拒字段和 compact 的独立恢复预算。
func RunHTTP(ctx context.Context, input HTTPInput, o HTTPOptions) (*Result, error) {
	body := input.Body
	upstreamModel := input.UpstreamModel
	encryptedRetried, compactRetried, agentRetried := false, false, false
	rejected := openai.NewOpenAIResponsesRejectedFieldRetryState(body)
	for {
		resp, err := openai.ExchangeHTTP(ctx, body, o.Exchange)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			payload := o.ReadErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			if !agentRetried && o.IsAgentIdentity(ctx) && o.InvalidAgentTask(resp.StatusCode, payload) {
				agentRetried = true
				if err := o.RecoverAgentTask(ctx); err != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
				}
				continue
			}
			payload = o.RedactErrorBody(ctx, payload)
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			message, code := o.ErrorDetails(payload)
			if !encryptedRetried && resp.StatusCode == http.StatusBadRequest && code == "invalid_encrypted_content" {
				retryBody, changed, retryErr := o.RetryEncrypted(body)
				if retryErr != nil {
					return nil, retryErr
				}
				if changed {
					body = retryBody
					o.MarkInvalidLineage(input.LineageEntryBody)
					encryptedRetried = true
					rejected.Remember(body)
					o.Log("[OpenAI] Retrying non-WSv2 request once after invalid_encrypted_content (account: %s)", input.AccountName)
					continue
				}
				o.Log("[OpenAI] Skip non-WSv2 invalid_encrypted_content retry because encrypted state items are missing (account: %s)", input.AccountName)
			}
			if retryBody, reason, changed, retryErr := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, payload); retryErr != nil {
				return nil, fmt.Errorf("normalize rejected Responses field retry body: %w", retryErr)
			} else if changed && rejected.Allow(retryBody) {
				body = retryBody
				o.Log("[OpenAI] Retrying non-WSv2 request after %s (account: %s)", reason, input.AccountName)
				continue
			}
			if retryBody, model, retry := o.CompactRetry(body, resp.StatusCode, message, payload, compactRetried); retry {
				o.CompactRetryObserved(resp, payload, message)
				from := strings.TrimSpace(gjson.GetBytes(body, "model").String())
				body = retryBody
				upstreamModel = model
				compactRetried = true
				o.ObserveUpstreamModel(model)
				o.Log("[OpenAI] Retrying explicit compact request once with fallback model (account: %s, from: %s, to: %s, upstream_code: %s)", input.AccountName, from, model, code)
				continue
			}
			if o.ShouldFailover(resp.StatusCode, message, payload) {
				// 健康动作端口返回 nil 表示原策略要求通用错误，仍在本响应上执行原错误适配。
				if err := o.HTTPFailover(resp, payload, message, upstreamModel); err != nil {
					return nil, err
				}
			}
			return nil, o.ErrorResponse(resp, body, input.BillingModel)
		}
		// 延迟关闭保留原多次 compact 尝试的生命周期；每个闭包绑定本次响应。
		defer func() { _ = resp.Body.Close() }()
		o.WrapResponseBody(resp)
		tier := o.ExtractServiceTier(body)
		o.ReleaseDecodedRequest()
		var usage *wire.ForwardUsage
		var firstToken *int
		responseID := ""
		imageCount := 0
		var imageSizes []string
		if input.Stream {
			result, readErr := openai.ReadStreamingResponse(ctx, resp, upstream.NewOutputContext(o.Sink), o.StreamOptions(), input.StartedAt, input.OriginalModel, upstreamModel, input.ReasoningEffortValue)
			if readErr != nil {
				if signal, ok := o.CompactSignal(readErr); ok {
					if retryBody, model, retry := o.CompactRetry(body, http.StatusBadRequest, signal.Message, signal.Payload, compactRetried); retry {
						if resp.Body != nil {
							_ = resp.Body.Close()
						}
						o.CompactRetryObserved(resp, signal.Payload, signal.Message)
						body = retryBody
						upstreamModel = model
						compactRetried = true
						o.ObserveUpstreamModel(model)
						continue
					}
					if resp.Body != nil {
						_ = resp.Body.Close()
					}
					compactResp, compactBody := o.CompactErrorResponse(resp, signal)
					if o.ShouldFailover(compactResp.StatusCode, signal.Message, compactBody) {
						return nil, o.CompactFailover(compactResp, compactBody, signal.Message, upstreamModel)
					}
					return nil, o.ErrorResponse(compactResp, body, o.ErrorSchedulingModel(input.BillingModel, upstreamModel))
				}
				return nil, readErr
			}
			usage = result.Usage
			firstToken = result.FirstTokenMs
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageSizes = result.ImageOutputSizes
		} else {
			result, readErr := openai.ReadNonStreamingResponse(ctx, resp, upstream.NewOutputContext(o.Sink), o.NonStreamOptions(), input.OriginalModel, upstreamModel)
			if readErr != nil {
				if signal, ok := o.CompactSignal(readErr); ok {
					if retryBody, model, retry := o.CompactRetry(body, http.StatusBadRequest, signal.Message, signal.Payload, compactRetried); retry {
						_ = resp.Body.Close()
						body = retryBody
						upstreamModel = model
						compactRetried = true
						o.ObserveUpstreamModel(model)
						continue
					}
				}
				return nil, readErr
			}
			usage = result.Usage
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageSizes = result.ImageOutputSizes
		}
		o.BindResponseOwner(ctx, responseID)
		if input.OAuth && !input.Shadow {
			o.UpdateUsageSnapshot(ctx, resp.Header)
		}
		if usage == nil {
			usage = &wire.ForwardUsage{}
		}
		result := &Result{RequestID: resp.Header.Get("x-request-id"), ResponseID: responseID, Headers: resp.Header,
			Usage: *usage, Model: input.OriginalModel, BillingModel: input.BillingModel, UpstreamModel: upstreamModel,
			UpstreamResponseServiceTier: o.ObservedServiceTier(), ServiceTier: o.ResolvedServiceTier(tier), ReasoningEffort: input.ReasoningEffort,
			Stream: input.Stream, Duration: time.Since(input.StartedAt), FirstTokenMs: firstToken}
		if imageCount > 0 {
			result.ImageCount = imageCount
			result.ImageSize = input.ImageSizeTier
			result.ImageInputSize = input.ImageInputSize
			result.ImageOutputSizes = imageSizes
			result.BillingModel = input.ImageBillingModel
		}
		return result, nil
	}
}

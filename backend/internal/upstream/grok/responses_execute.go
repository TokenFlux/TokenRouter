// Responses 单次执行拥有原生恢复、流过滤和响应关闭，不决定全局 failover 或资金动作。
package grok

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	bridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type ResponsesTarget struct {
	// 已有桥接读取器直接解释原流，不增加另一层帧过滤。
	PassRawStream  bool
	AccountID      int64
	Model          string
	Enter          func() (func(), error)
	Exchange       ResponsesExchange
	BeforeResponse func(*http.Response, []byte) (bool, error)
	MaxLineSize    int
	ClientTools    bridge.ResponsesClientToolMapping
	ReadResponse   func(*http.Response, upstream.AttemptInput, upstream.OutputSink) (upstream.ResponsesObservation, error)
}

func (t *ResponsesTarget) TargetID() int64  { return t.AccountID }
func (*ResponsesTarget) String() string     { return "grok responses target" }
func (t *ResponsesTarget) GoString() string { return t.String() }

type ResponsesExecutor struct{}

func (ResponsesExecutor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, err error) {
	t, ok := input.Target.(*ResponsesTarget)
	if !ok || t == nil || t.ReadResponse == nil || t.Exchange.Build == nil || t.Exchange.Do == nil || t.Exchange.ReadError == nil {
		return result, errors.New("grok responses target is not configured")
	}
	if input.Protocol != protocol.ProtocolOpenAIResponses {
		return result, errors.New("unsupported grok responses protocol")
	}
	if t.Enter != nil {
		done, e := t.Enter()
		if e != nil {
			return result, e
		}
		defer done()
	}
	started := time.Now()
	resp, body, err := ExchangeResponses(input.Body, t.Exchange)
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	result.UpstreamHeaders = resp.Header
	result.Model = input.ResponseModel
	result.UpstreamModel = t.Model
	result.Stream = input.Stream
	if t.BeforeResponse != nil {
		handled, e := t.BeforeResponse(resp, body)
		if handled || e != nil {
			return result, e
		}
	}
	if input.Stream && !t.PassRawStream {
		resp.Body = NewGrokResponsesBillingPingFilterBody(resp.Body, t.MaxLineSize)
		if len(t.ClientTools.CustomTools) > 0 || t.ClientTools.ToolSearch || len(t.ClientTools.NamespaceTools) > 0 {
			resp.Body = upstream.NewResponsesClientToolStreamBody(resp.Body, t.ClientTools, t.MaxLineSize)
		}
	}
	observed, err := t.ReadResponse(resp, input, sink)
	if observed.Usage != nil {
		u := observed.Usage
		result.Usage = upstream.TokenUsage{
			InputTokens:              u.InputTokens,
			OutputTokens:             u.OutputTokens,
			CacheCreationInputTokens: u.CacheCreationInputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			ImageOutputTokens:        u.ImageOutputTokens,
		}
		result.ImageInputTokens = u.ImageInputTokens
		result.HasUsage = observed.HasUsage
	}
	result.FirstTokenMs = observed.FirstTokenMs
	result.ResponseID = strings.TrimSpace(observed.ResponseID)
	result.SearchCount = observed.SearchCount
	result.ObservedImages = observed.ImageCount
	result.ImageOutputSizes = observed.ImageOutputSizes
	result.Served = observed.Served
	result.HTTPCommitted = observed.HTTPCommitted
	result.RetryCommitted = observed.RetryCommitted
	result.ClientDisconnect = observed.ClientDisconnected
	result.FirstSemanticOutput = observed.FirstSemanticOutput
	result.Duration = time.Since(started)
	return result, err
}

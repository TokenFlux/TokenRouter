// 本文件把一次账号内交换与协议输出组合成统一执行入口；全局选账号仍由调用者拥有。
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	protocolwire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type Target struct {
	AccountID                    int64
	Model                        string
	Exchange                     ExchangeOptions
	Response                     ResponseOptions
	StartedAt                    time.Time
	Passthrough, MimicClaudeCode bool
	Enter                        func() (func(), error)
	// BeforeResponse 衔接 HTTP 错误处理与账号观测；stop 表示外层已处理响应。
	BeforeResponse func(context.Context, *http.Response, []byte) (stop bool, err error)
	BeforeStream   func()
	OnStream       func(*StreamResult, error)
	OnWireBody     func([]byte)
	Accepted       func()
}

func (t *Target) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.AccountID
}
func (t *Target) String() string   { return fmt.Sprintf("anthropic target account=%d", t.TargetID()) }
func (t *Target) GoString() string { return t.String() }

type Executor struct{}

func (Executor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	// 失败路径也返回取消与耗时分类；未发生服务时不生成用量或成功结果。
	enteredAt := time.Now()
	defer func() {
		if failure != nil {
			result.Cancelled = errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded)
			if result.FailureClass == "" {
				result.FailureClass = "upstream"
			}
			if result.Duration == 0 {
				result.Duration = time.Since(enteredAt)
			}
		}
	}()
	target, ok := input.Target.(*Target)
	if !ok || target == nil {
		return upstream.AttemptResult{}, errors.New("anthropic execution target is not configured")
	}
	if input.Protocol != protocol.ProtocolAnthropicMessages {
		return upstream.AttemptResult{}, errors.New("unsupported anthropic client protocol")
	}
	if target.Enter != nil {
		done, err := target.Enter()
		if err != nil {
			return upstream.AttemptResult{}, err
		}
		defer done()
	}
	started := target.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	var resp *http.Response
	var wire []byte
	var err error
	if target.Passthrough {
		resp, wire, err = ExchangePassthrough(ctx, input.Body, target.Exchange)
	} else {
		resp, wire, err = Exchange(ctx, input.Body, target.Exchange)
	}
	if target.OnWireBody != nil {
		target.OnWireBody(wire)
	}
	if err != nil {
		return upstream.AttemptResult{}, err
	}
	if resp == nil || resp.Body == nil {
		return upstream.AttemptResult{}, errors.New("upstream request failed: empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	if target.BeforeResponse != nil {
		stop, err := target.BeforeResponse(ctx, resp, wire)
		if stop || err != nil {
			return upstream.AttemptResult{}, err
		}
	}
	if target.Accepted != nil {
		target.Accepted()
	}
	result = upstream.AttemptResult{RequestID: resp.Header.Get("x-request-id"), UpstreamHeaders: resp.Header, Model: input.ResponseModel, UpstreamModel: target.Model, Stream: input.Stream}

	response := target.Response
	priorObserve := response.Observe
	response.Observe = func(observation protocolwire.Observation) {
		if priorObserve != nil {
			priorObserve(observation)
		}
		result.HasUsage = result.HasUsage || observation.HasUsage
		result.Served = result.Served || observation.Semantic
		if observation.Semantic && result.FirstSemanticOutput == nil {
			elapsed := time.Since(started)
			result.FirstSemanticOutput = &elapsed
		}
	}
	priorState := response.ObserveState
	response.ObserveState = func(usage *upstream.TokenUsage, first *int) {
		if priorState != nil {
			priorState(usage, first)
		}
		if usage != nil {
			result.Usage = *usage
		}
		result.FirstTokenMs = first
	}
	output := upstream.NewOutputContext(sink)
	if input.Stream {
		if target.BeforeStream != nil {
			target.BeforeStream()
		}
		var stream *StreamResult
		if target.Passthrough {
			stream, err = StreamResponsePassthrough(ctx, resp, output, response.StreamOptions, started, target.Model)
		} else {
			stream, err = StreamResponse(ctx, resp, output, response.StreamOptions, started, input.ResponseModel, target.Model, target.MimicClaudeCode)
		}
		if target.OnStream != nil {
			target.OnStream(stream, err)
		}
		if stream != nil {
			if stream.Usage != nil {
				result.Usage = *stream.Usage
			}
			result.FirstTokenMs = stream.FirstTokenMs
			result.ClientDisconnect = stream.ClientDisconnect
		}
	} else {
		var usage *upstream.TokenUsage
		if target.Passthrough {
			usage, err = NonStreamResponsePassthrough(ctx, resp, output, response)
		} else {
			usage, err = NonStreamResponse(ctx, resp, output, response, input.ResponseModel, target.Model)
		}
		if usage != nil {
			result.Usage = *usage
		}
	}
	if strings.TrimSpace(result.Usage.Speed) == "" && strings.EqualFold(strings.TrimSpace(gjson.GetBytes(wire, "speed").String()), "fast") {
		result.Usage.Speed = "fast"
	}
	result.Duration = time.Since(started)
	result.Cancelled = errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if err != nil {
		result.FailureClass = "upstream"
	}
	return result, err
}

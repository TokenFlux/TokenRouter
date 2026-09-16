// 本文件适配统一的单次平台执行契约；用户计费和账号切换始终由调用方拥有。
package bedrock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type Target struct {
	AccountID int64
	Request   RequestOptions
	Retry     RetryPolicy
	Stream    StreamOptions
	StartedAt time.Time
	Enter     func() (func(), error)
	Accepted  func()
	ReadBody  func(io.Reader) ([]byte, error)
	HTTPError func(context.Context, *http.Response) (upstream.AttemptResult, error)
}

func (t *Target) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.AccountID
}
func (t *Target) String() string   { return fmt.Sprintf("bedrock target account=%d", t.TargetID()) }
func (t *Target) GoString() string { return t.String() }

// Executor 不持有账号或运行时缓存；请求体为原调用方按本次 beta 投影准备的字节。
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
	if !ok || target == nil || target.Request.Do == nil {
		return upstream.AttemptResult{}, errors.New("bedrock execution target is not configured")
	}
	if input.Protocol != protocol.ProtocolAnthropicMessages {
		return upstream.AttemptResult{}, errors.New("unsupported bedrock client protocol")
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
	resp, err := ExecuteUpstream(ctx, input.Body, target.Request, target.Retry)
	if err != nil {
		return upstream.AttemptResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if id := resp.Header.Get("x-amzn-requestid"); id != "" && resp.Header.Get("x-request-id") == "" {
		resp.Header.Set("x-request-id", id)
	}
	if resp.StatusCode >= 400 {
		if target.HTTPError != nil {
			return target.HTTPError(ctx, resp)
		}
		return upstream.AttemptResult{RequestID: resp.Header.Get("x-amzn-requestid"), FailureClass: "upstream"}, fmt.Errorf("bedrock upstream status %d", resp.StatusCode)
	}
	if target.Accepted != nil {
		target.Accepted()
	}
	result = upstream.AttemptResult{RequestID: resp.Header.Get("x-amzn-requestid"), UpstreamHeaders: resp.Header, Model: input.ResponseModel, UpstreamModel: target.Request.ModelID, Stream: input.Stream}

	if input.Stream {
		options := target.Stream
		priorObserve := options.Observe
		options.Observe = func(observation anthropic.Observation) {
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
		stream, streamErr := StreamResponse(ctx, resp, upstream.NewOutputContext(sink), options, started, input.ResponseModel)
		if stream != nil {
			if stream.Usage != nil {
				result.Usage = *stream.Usage
			}
			result.FirstTokenMs = stream.FirstTokenMs
			result.ClientDisconnect = stream.ClientDisconnect
		}
		result.Duration = time.Since(started)
		result.Cancelled = errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded)
		if streamErr != nil {
			result.FailureClass = "upstream"
		}
		return result, streamErr
	}
	if target.ReadBody == nil {
		return result, errors.New("bedrock response reader is not configured")
	}
	body, err := target.ReadBody(resp.Body)
	if err != nil {
		return result, err
	}
	body = TransformBedrockInvocationMetrics(body)
	usage := anthropic.ParseClaudeUsageFromResponseBody(body)
	if usage != nil {
		result.Usage = *usage
	}
	observation := anthropic.ObserveMessage(string(body))
	result.HasUsage = observation.HasUsage
	result.Served = observation.Semantic
	if result.Served {
		elapsed := time.Since(started)
		result.FirstSemanticOutput = &elapsed
	}
	head := http.Header{"Content-Type": []string{"application/json"}}
	if id := resp.Header.Get("x-amzn-requestid"); id != "" {
		head.Set("x-request-id", id)
	}
	// 与原 Gin Data 一致，输出失败不撤销已完成的供应商响应。
	if err := sink.Begin(upstream.OutputHead{Status: resp.StatusCode, Header: head}); err != nil {
		result.ClientDisconnect = true
	} else if err := sink.Emit(upstream.OutputEvent{Data: body, Semantic: observation.Semantic, CommitForRetry: true, Terminal: true}); err != nil {
		result.ClientDisconnect = true
	}
	result.Duration = time.Since(started)
	return result, nil
}

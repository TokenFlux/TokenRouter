// 单次执行拥有平台交换、响应输出和关闭；全局 failover、资金及账号状态归调用方。
package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	anthropicwire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type ResponseMode uint8

const (
	ModeClaudeResponse ResponseMode = iota
	ModeGeminiResponse
	ModeChatResponse
	ModeResponsesResponse
	ModeStaticClaudeResponse
)

// Target 只携带单次技术选项及受控端口，不包含账号实体或凭据集合。
type Target struct {
	OutputError    func(error)
	AccountID      int64
	Model          string
	Mode           ResponseMode
	StartedAt      time.Time
	IncludeUsage   bool
	ClientTools    bridge.ResponsesClientToolMapping
	Exchange       func(context.Context) (*http.Response, error)
	BeforeResponse func(context.Context, *http.Response) (bool, error)
	Response       ResponseOptions
	Enter          func() (func(), error)
}

func (t *Target) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.AccountID
}
func (t *Target) String() string   { return fmt.Sprintf("antigravity target account=%d", t.TargetID()) }
func (t *Target) GoString() string { return t.String() }

type Executor struct{}

// @project-doc docs/interfaces/antigravity_upstream.md#antigravity_native_execution
func (Executor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	target, ok := input.Target.(*Target)
	if !ok || target == nil || target.Exchange == nil {
		return result, errors.New("antigravity execution target is not configured")
	}
	switch input.Protocol {
	case protocol.ProtocolAnthropicMessages, protocol.ProtocolGeminiGenerateContent, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolOpenAIResponses:
	default:
		return result, errors.New("unsupported antigravity client protocol")
	}
	if target.Enter != nil {
		done, err := target.Enter()
		if err != nil {
			return result, err
		}
		defer done()
	}
	started := target.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	result = upstream.AttemptResult{Model: input.ResponseModel, UpstreamModel: target.Model, Stream: input.Stream}
	defer func() {
		result.Duration = time.Since(started)
		if failure != nil {
			result.FailureClass = "upstream"
			result.Cancelled = ctx.Err() != nil || errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded)
		}
	}()
	resp, err := target.Exchange(ctx)
	if err != nil {
		return result, err
	}
	if resp == nil || resp.Body == nil {
		return result, errors.New("antigravity upstream returned empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	result.RequestID = resp.Header.Get("x-request-id")
	result.UpstreamHeaders = resp.Header
	if target.BeforeResponse != nil {
		stop, err := target.BeforeResponse(ctx, resp)
		if stop || err != nil {
			return result, err
		}
	}
	options := target.Response
	priorRaw := options.ObserveRaw
	// 仅保存本次已出现的 usage 字段；部分更新不能抹掉之前的观测。
	observedUsage := make(map[string]json.RawMessage)
	options.ObserveRaw = func(raw []byte) {
		if priorRaw != nil {
			priorRaw(raw)
		}
		data := strings.TrimSpace(string(raw))
		if strings.HasPrefix(data, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(data, "data:"))
		}
		body, _ := (&ResponseAdapter{}).UnwrapV1InternalResponse([]byte(data))
		var hasUsage, served bool
		if target.Mode == ModeStaticClaudeResponse {
			observed := anthropicwire.ObserveEvent(string(body))
			hasUsage, served = observed.HasUsage, observed.Semantic
			if hasUsage {
				(&ResponseAdapter{}).ExtractSSEUsage("data: "+string(body), &result.Usage)
			}
		} else {
			observed := geminiwire.ObservePayload(body)
			hasUsage, served = observed.HasUsage, observed.Semantic
			if hasUsage {
				for _, field := range []string{"promptTokenCount", "candidatesTokenCount", "cachedContentTokenCount", "thoughtsTokenCount", "totalTokenCount", "candidatesTokensDetails"} {
					value := gjson.GetBytes(body, "usageMetadata."+field)
					if value.Exists() && value.Type != gjson.Null {
						observedUsage[field] = json.RawMessage(value.Raw)
					}
				}
				// 使用原归一化实现解释观测快照，不估算未出现的 token。
				payload, _ := json.Marshal(map[string]any{"usageMetadata": observedUsage})
				if usage := bridge.NativeUsageProjection(bridge.NativeExtractGeminiUsage(payload)); usage != nil {
					result.Usage = *usage
				}
			}
		}
		result.HasUsage = result.HasUsage || hasUsage
		result.Served = result.Served || served
		if served && result.FirstSemanticOutput == nil {
			elapsed := time.Since(started)
			result.FirstSemanticOutput = &elapsed
		}
	}
	output := upstream.NewOutputContext(&observedSink{OutputSink: sink, nonStream: !input.Stream})
	if result.RequestID != "" && target.Mode != ModeStaticClaudeResponse {
		output.Header("x-request-id", result.RequestID)
	}
	adapter := &ResponseAdapter{Options: options}
	var stream *StreamResult
	switch target.Mode {
	case ModeClaudeResponse:
		if input.Stream {
			stream, err = adapter.HandleClaudeStreamingResponse(output, resp, started, input.ResponseModel)
		} else {
			stream, err = adapter.HandleClaudeStreamToNonStreaming(output, resp, started, input.ResponseModel)
		}
	case ModeGeminiResponse:
		if input.Stream {
			stream, err = adapter.HandleGeminiStreamingResponse(output, resp, started)
		} else {
			stream, err = adapter.HandleGeminiStreamToNonStreaming(output, resp, started)
		}
	case ModeChatResponse:
		if input.Stream {
			stream, err = adapter.HandleChatCompletionsStreamingFromAntigravity(output, resp, started, input.ResponseModel, target.IncludeUsage)
		} else {
			stream, err = adapter.HandleChatCompletionsNonStreamingFromAntigravity(output, resp, started, input.ResponseModel)
		}
	case ModeResponsesResponse:
		if input.Stream {
			stream, err = adapter.HandleResponsesStreamingFromAntigravity(output, resp, started, input.ResponseModel, target.ClientTools)
		} else {
			stream, err = adapter.HandleResponsesNonStreamingFromAntigravity(output, resp, started, input.ResponseModel, target.ClientTools)
		}
	case ModeStaticClaudeResponse:
		if input.Stream {
			output.Header("Content-Type", "text/event-stream")
			output.Header("Cache-Control", "no-cache")
			output.Header("Connection", "keep-alive")
			output.Header("X-Accel-Buffering", "no")
			output.Status(http.StatusOK)
			stream = adapter.StreamUpstreamResponse(output, resp, started)
		} else {
			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return result, fmt.Errorf("read upstream response: %w", readErr)
			}
			observed := anthropicwire.ObserveMessage(string(body))
			result.HasUsage, result.Served = observed.HasUsage, observed.Semantic
			if observed.Semantic {
				elapsed := time.Since(started)
				result.FirstSemanticOutput = &elapsed
			}
			usage := adapter.ExtractClaudeUsage(body)
			if usage != nil {
				result.Usage = *usage
			}
			output.Header("Content-Type", resp.Header.Get("Content-Type"))
			output.Status(http.StatusOK)
			_, _ = output.Writer.Write(body)
		}
	default:
		return result, errors.New("unsupported antigravity response mode")
	}
	if stream != nil {
		if stream.Usage != nil && (err == nil || !result.HasUsage) {
			result.Usage = *stream.Usage
		}
		result.FirstTokenMs = stream.FirstTokenMs
		result.ClientDisconnect = stream.ClientDisconnect
	}
	if err != nil && target.OutputError != nil {
		target.OutputError(err)
	}
	return result, err
}

// observedSink 为已经完整形成的输出段补充事实标记，不缓存或重排帧，不改变 TTFT。
type observedSink struct {
	upstream.OutputSink
	nonStream bool
}

func (s *observedSink) InitialOutput() upstream.OutputHead {
	if source, ok := s.OutputSink.(interface{ InitialOutput() upstream.OutputHead }); ok {
		return source.InitialOutput()
	}
	return upstream.OutputHead{}
}
func (s *observedSink) Emit(event upstream.OutputEvent) error {
	if s.nonStream && len(event.Data) > 0 {
		event.Semantic = event.Semantic || bridge.CompatJSONHasContent(event.Data) || anthropicwire.ObserveMessage(string(event.Data)).Semantic || geminiwire.ObservePayload(event.Data).Semantic
		event.Terminal = event.Terminal || json.Valid(event.Data)
	}

	for _, line := range strings.Split(string(event.Data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		semantic, terminal := bridge.CompatOutputMeaning([]byte("data: " + line + "\n\n"))
		a := anthropicwire.ObserveEvent(line)
		g := geminiwire.ObservePayload([]byte(line))
		event.Semantic = event.Semantic || semantic || a.Semantic || g.Semantic
		event.Terminal = event.Terminal || terminal || a.Terminal || g.Terminal
	}
	// 原 Antigravity 入口以实际写出字节关闭普通重试窗口；语义内容和 TTFT 单独报告。
	event.CommitForRetry = event.CommitForRetry || len(event.Data) > 0
	return s.OutputSink.Emit(event)
}

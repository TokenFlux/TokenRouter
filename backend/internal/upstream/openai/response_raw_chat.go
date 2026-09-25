// Raw Chat 和两种回退输出复用唯一 wire/bridge 算法，各自保留取消与终态规则。
package openai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// RawResponseOptions 只传递当前端点的技术参数与原请求观察端口。
type RawResponseOptions struct {
	Runtime              bridge.Runtime
	AccountID            int64
	Scanner              func(io.Reader) *bufio.Scanner
	CC                   func() CCResponseOptions
	ReadBody             func(io.Reader) ([]byte, error)
	BodyLimitError       error
	Headers              func(http.Header, http.Header)
	WriteError           func(int, string, string)
	Observe              func([]byte, string)
	ObserveSSE           func(string)
	TransformLine        func(string) string
	TransformBody        func([]byte) []byte
	MissingUsage         func(string, wire.ForwardUsage) error
	ServiceTier          func() string
	ResolvedServiceTier  func() *string
	NormalizeServiceTier func(string) string
	TruncatedFailover    func(error) error
	RecordTruncation     func(error)
	SilentRefusal        func() error
	CacheOutput          func([]wire.ResponsesOutput)
	CacheEvents          func([]wire.ResponsesStreamEvent)
}

// ReadRawChatStreaming 保留当前端点独立的终态、取消与用量顺序。
func ReadRawChatStreaming(c *upstream.OutputContext, resp *http.Response, options RawResponseOptions, originalModel, upstreamModel string, reasoningEffort *string, startTime time.Time, requestBodyLen int) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := NewCompatStreamHeaderWriter(c, resp.Header, options.Headers)
	scanner := options.Scanner(resp.Body)

	var usage wire.ForwardUsage
	var firstTokenMs *int
	clientDisconnected := false
	clientOutputStarted := false
	pendingLines := make([]string, 0, 8)
	refusalDetector := NewChatSilentRefusalDetector(requestBodyLen)
	var terminal wire.RawStreamTerminalState

	writeLine := func(line string) {
		if clientDisconnected {
			return
		}
		if !clientOutputStarted && !refusalDetector.ShouldReleaseClientOutput() {
			pendingLines = append(pendingLines, line)
			return
		}
		if !clientOutputStarted {
			writeStreamHeaders()
			for _, pending := range pendingLines {
				if _, werr := io.WriteString(c.Writer, pending+"\n"); werr != nil {
					clientDisconnected = true
					logger.L().Debug("openai chat_completions raw: client disconnected, continuing to drain upstream for billing",
						zap.Error(werr),
						zap.String("request_id", requestID),
					)
					return
				}
			}
			pendingLines = pendingLines[:0]
			clientOutputStarted = true
		}
		if _, werr := io.WriteString(c.Writer, line+"\n"); werr != nil {
			clientDisconnected = true
			logger.L().Debug("openai chat_completions raw: client disconnected, continuing to drain upstream for billing",
				zap.Error(werr),
				zap.String("request_id", requestID),
			)
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		refusalDetector.ObserveSSELine(line)
		if payload, ok := wire.ExtractSSEDataLine(line); ok {
			trimmedPayload := strings.TrimSpace(payload)
			terminal.ObserveDataLine(trimmedPayload)
			if trimmedPayload != "[DONE]" {
				options.Observe([]byte(trimmedPayload), wire.OpenAIChatCompletionServiceTierEventType([]byte(trimmedPayload)))
				usageOnlyChunk := wire.IsOpenAIChatUsageOnlyStreamChunk(payload)
				if u := wire.ExtractCCStreamUsage(payload); u != nil {
					usage = *u
				}
				if firstTokenMs == nil && !usageOnlyChunk {
					elapsed := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &elapsed
				}
			}
		}
		line = options.TransformLine(line)
		line = wire.StripEmptyChatToolCallIdentityFromSSELine(line)

		writeLine(line)
		if line == "" {
			if !clientDisconnected && clientOutputStarted {
				c.Writer.Flush()
			}
			continue
		}
		if !clientDisconnected && clientOutputStarted {
			c.Writer.Flush()
		}
	}

	resultWithUsage := func() *CompatResponseResult {
		return &CompatResponseResult{
			RequestID:       requestID,
			UpstreamHeaders: resp.Header,
			Usage:           usage,
			Model:           originalModel,
			UpstreamModel:   upstreamModel,
			ServiceTier:     options.ServiceTier(),
			ReasoningEffort: reasoningEffort,
			ResolvedTier:    options.ResolvedServiceTier(),
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    firstTokenMs,
		}
	}

	scanErr := scanner.Err()
	if scanErr != nil && !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
		logger.L().Warn("openai chat_completions raw: stream read error",
			zap.Error(scanErr),
			zap.String("request_id", requestID),
		)
	}

	// 客户端取消/断开后上游读失败与上游截断不可区分（取消会连带取消上游请求），
	// 沿用既有语义：按已收到的用量正常收尾计费，不判为上游故障。
	clientAborted := clientDisconnected ||
		errors.Is(scanErr, context.Canceled) ||
		errors.Is(scanErr, context.DeadlineExceeded)

	// 上游在任何终止信号之前结束：连接被 reset（scanErr != nil）或干净 EOF。
	// 两者都不能再记成功——此前统一返回 nil error，把上游截断伪装成
	// `HTTP 200 + usage 0/0`，客户端收到半截回答且 Ops 侧完全无感。
	if !clientAborted && terminal.IsTruncated(clientOutputStarted) {
		cause := scanErr
		if cause == nil {
			cause = ErrOpenAIUpstreamStreamTruncated
		}
		logger.L().Warn("openai chat_completions raw: upstream stream truncated before terminal chunk",
			zap.Error(cause),
			zap.String("request_id", requestID),
			zap.Int64("account_id", options.AccountID),
			zap.String("upstream_model", upstreamModel),
			zap.Bool("saw_sse_data", terminal.SawDataLine()),
			zap.Bool("client_output_started", clientOutputStarted),
		)
		if !clientOutputStarted {
			// 响应头尚未提交：可以透明换号重试，客户端不会看到半截流。
			return nil, options.TruncatedFailover(cause)
		}
		// 已写出语义字节：无法再 failover，改为带类型的上游错误。handler 会据此
		// 补发 SSE error 帧并把本次请求计入 SLA 失败。
		options.RecordTruncation(cause)
		return resultWithUsage(), NewUpstreamStreamReadError(cause)
	}

	if scanErr == nil && !clientDisconnected && !clientOutputStarted {
		if refusalDetector.IsSilentRefusal() {
			return nil, options.SilentRefusal()
		}
		if len(pendingLines) > 0 {
			writeStreamHeaders()
			for _, pending := range pendingLines {
				if _, werr := io.WriteString(c.Writer, pending+"\n"); werr != nil {
					clientDisconnected = true
					logger.L().Debug("openai chat_completions raw: client disconnected during final flush",
						zap.Error(werr),
						zap.String("request_id", requestID),
					)
					break
				}
			}
			if !clientDisconnected {
				c.Writer.Flush()
				clientOutputStarted = true
			}
		}
	}

	return resultWithUsage(), nil
}

// ReadRawChatBuffered 保留当前端点独立的终态、取消与用量顺序。
func ReadRawChatBuffered(c *upstream.OutputContext, resp *http.Response, options RawResponseOptions, originalModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")

	respBody, err := options.ReadBody(resp.Body)
	if err != nil {
		if !errors.Is(err, options.BodyLimitError) {
			options.WriteError(http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, fmt.Errorf("read upstream body: %w", err)
	}

	var usage wire.ForwardUsage
	if parsedUsage, ok := wire.ExtractOpenAIUsageFromJSONBytes(respBody); ok {
		usage = parsedUsage
	}
	if IsEventStreamResponse(resp.Header) || wire.BodyHasSSEFraming(respBody) {
		// 某些兼容上游在 stream=false 时仍返回 SSE；逐帧观察才能拿到
		// response.completed 的实际 service_tier，而不是回退到请求档位。
		options.ObserveSSE(string(respBody))
		wire.ForEachOpenAISSEFrame(string(respBody), func(_ string, payload []byte) {
			if parsed, ok := wire.ExtractOpenAIUsageFromJSONBytes(payload); ok {
				usage = parsed
			}
			if parsed := wire.ExtractCCStreamUsage(string(payload)); parsed != nil {
				usage = *parsed
			}
		})
	} else {
		options.Observe(respBody, "response.completed")
	}
	responseModel := gjson.GetBytes(respBody, "model").String()
	if err := options.MissingUsage(responseModel, usage); err != nil {
		return nil, err
	}

	respBody = options.TransformBody(respBody)

	options.Headers(c.Writer.Header(), resp.Header)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(respBody)

	return &CompatResponseResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		ServiceTier:     options.ServiceTier(),
		ReasoningEffort: reasoningEffort,
		ResolvedTier:    options.ResolvedServiceTier(),
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// ReadCCAsResponsesBuffered 保留当前端点独立的终态、取消与用量顺序。
func ReadCCAsResponsesBuffered(c *upstream.OutputContext, resp *http.Response, options RawResponseOptions, originalModel, upstreamModel string, reasoningEffort *string, startTime time.Time, customTools, functionTools map[string]bool, toolSearch bool, namespaceTools map[string]bridge.NamespacedToolName) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	ccResp, usage, err := ReadCCJSONResponse(resp, options.CC())
	if err != nil {
		return nil, err
	}
	responsesResp := bridge.ChatCompletionsResponseToResponses(options.Runtime, ccResp, originalModel, customTools, functionTools, toolSearch, namespaceTools)
	options.CacheOutput(responsesResp.Output)

	options.Headers(c.Writer.Header(), resp.Header)
	c.JSON(http.StatusOK, responsesResp)

	return &CompatResponseResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		ServiceTier:     options.ServiceTier(),
		ReasoningEffort: reasoningEffort,
		ResolvedTier:    options.ResolvedServiceTier(),
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// ReadCCAsResponsesStreaming 保留当前端点独立的终态、取消与用量顺序。
func ReadCCAsResponsesStreaming(c *upstream.OutputContext, resp *http.Response, options RawResponseOptions, originalModel, upstreamModel string, reasoningEffort *string, startTime time.Time, customTools, functionTools map[string]bool, toolSearch bool, namespaceTools map[string]bridge.NamespacedToolName) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := NewCompatStreamHeaderWriter(c, resp.Header, options.Headers)

	state := bridge.NewChatCompletionsToResponsesStreamState(options.Runtime, originalModel)
	state.CustomTools = customTools
	state.FunctionTools = functionTools
	state.ToolSearchDeclared = toolSearch
	state.NamespaceTools = namespaceTools
	clientDisconnected := false

	writeEvents := func(events []wire.ResponsesStreamEvent) {
		if clientDisconnected || len(events) == 0 {
			return
		}
		writeStreamHeaders()
		for _, event := range events {
			sse, err := bridge.ResponsesEventToSSE(event)
			if err != nil {
				logger.L().Warn("openai responses chat fallback: failed to marshal stream event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientDisconnected = true
				logger.L().Debug("openai responses chat fallback: client disconnected, continuing to drain upstream for billing",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				return
			}
		}
		c.Writer.Flush()
	}

	scan := ScanCCStream(resp, options.CC(), "openai responses chat fallback", requestID, startTime, func(chunk *wire.ChatCompletionsChunk) {
		events := bridge.ChatCompletionsChunkToResponsesEvents(options.Runtime, chunk, state)
		options.CacheEvents(events)
		writeEvents(events)
	})

	if scan.Err != nil {
		return &CompatResponseResult{
			RequestID:       requestID,
			UpstreamHeaders: resp.Header,
			Usage:           scan.Usage,
			Model:           originalModel,
			UpstreamModel:   upstreamModel,
			ServiceTier:     options.NormalizeServiceTier(scan.ServiceTier),
			ReasoningEffort: reasoningEffort,
			ResolvedTier:    options.ResolvedServiceTier(),
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    scan.FirstTokenMs,
		}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
	}
	if err := state.ValidateToolCallArguments(); err != nil {
		return &CompatResponseResult{
			RequestID:       requestID,
			UpstreamHeaders: resp.Header,
			Usage:           scan.Usage,
			Model:           originalModel,
			UpstreamModel:   upstreamModel,
			ServiceTier:     options.NormalizeServiceTier(scan.ServiceTier),
			ReasoningEffort: reasoningEffort,
			ResolvedTier:    options.ResolvedServiceTier(),
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    scan.FirstTokenMs,
		}, fmt.Errorf("invalid tool call arguments from upstream: %w", err)
	}

	finalEvents := bridge.FinalizeChatCompletionsResponsesStream(options.Runtime, state)
	options.CacheEvents(finalEvents)
	writeEvents(finalEvents)
	if !clientDisconnected {
		writeStreamHeaders()
		if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
			clientDisconnected = true
		}
		if !clientDisconnected {
			c.Writer.Flush()
		}
	}
	if !scan.SawDone {
		LogCCStreamMissingDoneSentinel("openai responses chat fallback", requestID)
	}

	return &CompatResponseResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           scan.Usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		ServiceTier:     options.NormalizeServiceTier(scan.ServiceTier),
		ReasoningEffort: reasoningEffort,
		ResolvedTier:    options.ResolvedServiceTier(),
		Stream:          true,
		Duration:        time.Since(startTime),
		FirstTokenMs:    scan.FirstTokenMs,
	}, nil
}

// ReadCCAsMessagesBuffered 保留当前端点独立的终态、取消与用量顺序。
func ReadCCAsMessagesBuffered(c *upstream.OutputContext, resp *http.Response, options RawResponseOptions, originalModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	ccResp, usage, err := ReadCCJSONResponse(resp, options.CC())
	if err != nil {
		return nil, err
	}
	anthropicResp := bridge.ChatCompletionsResponseToAnthropic(options.Runtime, ccResp, originalModel)

	options.Headers(c.Writer.Header(), resp.Header)
	c.JSON(http.StatusOK, anthropicResp)

	return &CompatResponseResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		ServiceTier:     options.ServiceTier(),
		ReasoningEffort: reasoningEffort,
		ResolvedTier:    options.ResolvedServiceTier(),
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// ReadCCAsMessagesStreaming 保留当前端点独立的终态、取消与用量顺序。
func ReadCCAsMessagesStreaming(c *upstream.OutputContext, resp *http.Response, options RawResponseOptions, originalModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := NewCompatStreamHeaderWriter(c, resp.Header, options.Headers)

	anthropicState := bridge.NewChatCompletionsToAnthropicStreamState(options.Runtime, originalModel)
	clientDisconnected := false

	// 与 responses 兄弟不同：客户端断开后仍继续做事件转换（喂 anthropicState），
	// 仅跳过写出，保证 finalize 阶段的 usage 汇总不受断开影响。
	emitChunk := func(chunk *wire.ChatCompletionsChunk) {
		hasToolCallDelta := wire.ChatCompletionsChunkHasToolCallDelta(chunk)
		// 通过单个状态机将 CC chunk 直接转换为 Anthropic events。
		anthropicEvents := bridge.ChatCompletionsChunkToAnthropicEvents(options.Runtime, chunk, anthropicState)
		if hasToolCallDelta && len(anthropicEvents) == 0 {
			// 工具参数聚合期间用标准事件维持下游活动，避免长参数流被误判为空闲。
			anthropicEvents = append(anthropicEvents, anthropic.AnthropicStreamEvent{Type: "ping"})
		}
		if clientDisconnected {
			return
		}
		for _, aEvt := range anthropicEvents {
			sse, err := bridge.ResponsesAnthropicEventToSSE(aEvt)
			if err != nil {
				continue
			}
			writeStreamHeaders()
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientDisconnected = true
				break
			}
		}
		if !clientDisconnected && len(anthropicEvents) > 0 {
			c.Writer.Flush()
		}
	}

	scan := ScanCCStream(resp, options.CC(), "openai messages chat fallback", requestID, startTime, emitChunk)
	usage := scan.Usage

	if scan.Err != nil {
		// 上游读取中断时跳过收尾，避免合成 message_stop 掩盖截断，并返回
		// usage incomplete，与 Responses fallback 保持一致。
		return &CompatResponseResult{
			RequestID:        requestID,
			UpstreamHeaders:  resp.Header,
			Usage:            usage,
			Model:            originalModel,
			UpstreamModel:    upstreamModel,
			ServiceTier:      options.NormalizeServiceTier(scan.ServiceTier),
			ReasoningEffort:  reasoningEffort,
			ResolvedTier:     options.ResolvedServiceTier(),
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     scan.FirstTokenMs,
			ClientDisconnect: clientDisconnected,
		}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
	}

	// 收尾时关闭未结束的内容块，并发出 message_delta/message_stop。
	finalEvents := bridge.FinalizeChatCompletionsAnthropicStream(options.Runtime, anthropicState)
	if !clientDisconnected {
		for _, aEvt := range finalEvents {
			sse, err := bridge.ResponsesAnthropicEventToSSE(aEvt)
			if err != nil {
				continue
			}
			writeStreamHeaders()
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientDisconnected = true
				break
			}
		}
		c.Writer.Flush()
	}
	if !scan.SawDone {
		LogCCStreamMissingDoneSentinel("openai messages chat fallback", requestID)
	}

	return &CompatResponseResult{
		RequestID:        requestID,
		UpstreamHeaders:  resp.Header,
		Usage:            usage,
		Model:            originalModel,
		UpstreamModel:    upstreamModel,
		ServiceTier:      options.NormalizeServiceTier(scan.ServiceTier),
		ReasoningEffort:  reasoningEffort,
		ResolvedTier:     options.ResolvedServiceTier(),
		Stream:           true,
		Duration:         time.Since(startTime),
		FirstTokenMs:     scan.FirstTokenMs,
		ClientDisconnect: clientDisconnected,
	}, nil
}

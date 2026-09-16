// Anthropic 回程使用唯一协议状态机；各入口保留自己的排水、读超时与输出边界。
package openaiforward

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"io"
	"net/http"
	"time"
)

type AnthropicOutputOptions struct {
	Runtime        bridge.Runtime
	MaxLineSize    func() int
	StreamInterval func() time.Duration
	CopyHeaders    func(http.Header, http.Header)
	ReverseTools   func([]byte) []byte
	Error          func(int, string, string)
	Warn           func(string, ...zap.Field)
}

// AnthropicUsageToOpenAI 保留输入与缓存计数口径，不改变资金算法。
func AnthropicUsageToOpenAI(u *upstream.TokenUsage) protocolopenai.ForwardUsage {
	if u == nil {
		return protocolopenai.ForwardUsage{}
	}
	return protocolopenai.ForwardUsage{InputTokens: u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens, OutputTokens: u.OutputTokens, CacheCreationInputTokens: u.CacheCreationInputTokens, CacheReadInputTokens: u.CacheReadInputTokens}
}
func ResponsesFromAnthropicBuffered(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (*Result, error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var finalResp *protocolanthropic.AnthropicResponse
	var usage upstream.TokenUsage

	// 读间隔上限：上游挂住 SSE 时中止组装（缓冲路径尚未提交响应头，可回 502）。
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai responses via native anthropic buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*Result, error) {
		_ = resp.Body.Close()
		o.Warn("openai responses via native anthropic buffered: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream data interval timeout")
		return nil, fmt.Errorf("stream data interval timeout")
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// SSE 规范允许 `event:xxx`（冒号后无空格）：Kimi 等上游返回紧凑格式。
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
			protocolanthropic.MergeAnthropicUsage(&usage, event.Message.Usage)
		}
		if event.Type == "message_delta" {
			if event.Usage != nil {
				protocolanthropic.MergeAnthropicUsage(&usage, *event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = bridge.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			idx := *event.Index
			if idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				case "input_json_delta":
					finalResp.Content[idx].Input = forwardcore.AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if finalResp == nil {
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		finalResp.Usage = protocolanthropic.AnthropicUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		}
	}

	responsesResp := bridge.AnthropicToResponsesResponse(o.Runtime, finalResp)
	responsesResp.Model = originalModel

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	// 非流式响应必须是 application/json（上游被强制流式，透传头会污染）。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = o.ReverseTools(respBytes)
		respBytes, _, err = bridge.RestoreResponsesClientToolPayload(respBytes, clientToolMapping)
		if err != nil {
			return nil, fmt.Errorf("restore responses client tools: %w", err)
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, responsesResp)
	}

	return &Result{
		RequestID:        requestID,
		Headers:          resp.Header,
		Usage:            AnthropicUsageToOpenAI(&usage),
		Model:            originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: "/v1/messages",
		ReasoningEffort:  reasoningEffort,
		Stream:           false,
		Duration:         time.Since(startTime),
	}, nil
}

func ResponsesFromAnthropicStreaming(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (*Result, error) {
	requestID := resp.Header.Get("x-request-id")

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := bridge.NewAnthropicEventToResponsesState(o.Runtime)
	state.Model = originalModel
	clientToolRestorer := bridge.NewResponsesClientToolStreamRestorer(clientToolMapping)

	var usage upstream.TokenUsage
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:        requestID,
			Headers:          resp.Header,
			Usage:            AnthropicUsageToOpenAI(&usage),
			Model:            originalModel,
			BillingModel:     billingModel,
			UpstreamModel:    upstreamModel,
			UpstreamEndpoint: "/v1/messages",
			ReasoningEffort:  reasoningEffort,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     firstTokenMs,
			ClientDisconnect: clientDisconnected,
		}
	}

	// 读间隔上限：上游挂住 SSE（不发数据也不断连）时结束转换循环。上游 ctx 为
	// WithoutCancel 且 http.Client 无整体 Timeout，无此界限则 scanner.Scan()
	// 永久阻塞（见 anthropic native pump 文件注释）。
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai responses via native anthropic stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*Result, error) {
		_ = resp.Body.Close()
		o.Warn("openai responses via native anthropic stream: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		return resultWithUsage(), fmt.Errorf("stream data interval timeout")
	}

	// 客户端断开后不再写出，但继续推进状态机并排水上游；最终 output_tokens
	// 位于末尾 message_delta，提前退出会漏记上游已经产生的用量。
	processAnthropicEvent := func(event *protocolanthropic.AnthropicStreamEvent) {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		if event.Type == "message_delta" && event.Usage != nil {
			protocolanthropic.MergeAnthropicUsage(&usage, *event.Usage)
		}
		if event.Type == "message_start" && event.Message != nil {
			protocolanthropic.MergeAnthropicUsage(&usage, event.Message.Usage)
		}

		events := bridge.AnthropicEventToResponsesEvents(o.Runtime, event, state)
		if clientDisconnected {
			return
		}
		for _, evt := range events {
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			payload = o.ReverseTools(payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					clientDisconnected = true
					return
				}
			}
		}
		if len(events) > 0 {
			c.Writer.Flush()
		}
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		processAnthropicEvent(&event)
	}

	// 终态在断开后仍计算，但只在客户端连接存在时写出；工具名恢复与普通事件一致。
	if finalEvents := bridge.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 && !clientDisconnected {
		wrote := false
		for _, evt := range finalEvents {
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			payload = o.ReverseTools(payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					clientDisconnected = true
					break
				}
				wrote = true
			}
			if clientDisconnected {
				break
			}
		}
		if wrote {
			c.Writer.Flush()
		}
	}

	return resultWithUsage(), nil
}

func ChatFromAnthropicBuffered(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (*Result, error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var finalResp *protocolanthropic.AnthropicResponse
	var usage upstream.TokenUsage

	// 读间隔上限：上游挂住 SSE 时中止组装（缓冲路径尚未提交响应头，可回 502）。
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai cc via native anthropic buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*Result, error) {
		_ = resp.Body.Close()
		o.Warn("openai cc via native anthropic buffered: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream data interval timeout")
		return nil, fmt.Errorf("stream data interval timeout")
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// SSE 规范允许 `event:xxx`（冒号后无空格）：Kimi 等 Anthropic 兼容上游
		// 返回紧凑格式，严格匹配 "event: " 会丢弃全部事件（#4653 同根因）。
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
			protocolanthropic.MergeAnthropicUsage(&usage, event.Message.Usage)
		}
		if event.Type == "message_delta" {
			if event.Usage != nil {
				protocolanthropic.MergeAnthropicUsage(&usage, *event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = bridge.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			idx := *event.Index
			if idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				case "input_json_delta":
					finalResp.Content[idx].Input = forwardcore.AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if finalResp == nil {
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		finalResp.Usage = protocolanthropic.AnthropicUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		}
	}

	responsesResp := bridge.AnthropicToResponsesResponse(o.Runtime, finalResp)
	ccResp := bridge.ResponsesToChatCompletions(o.Runtime, responsesResp, originalModel)

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	// 非流式响应必须是 application/json（上游被强制流式，透传头会污染）。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = o.ReverseTools(respBytes)
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, ccResp)
	}

	return &Result{
		RequestID:        requestID,
		Headers:          resp.Header,
		Usage:            AnthropicUsageToOpenAI(&usage),
		Model:            originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: "/v1/messages",
		ReasoningEffort:  reasoningEffort,
		Stream:           false,
		Duration:         time.Since(startTime),
	}, nil
}

func ChatFromAnthropicStreaming(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, includeUsage bool) (*Result, error) {
	requestID := resp.Header.Get("x-request-id")

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	anthState := bridge.NewAnthropicEventToResponsesState(o.Runtime)
	anthState.Model = originalModel
	ccState := bridge.NewResponsesEventToChatState(o.Runtime)
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage

	var usage upstream.TokenUsage
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:        requestID,
			Headers:          resp.Header,
			Usage:            AnthropicUsageToOpenAI(&usage),
			Model:            originalModel,
			BillingModel:     billingModel,
			UpstreamModel:    upstreamModel,
			UpstreamEndpoint: "/v1/messages",
			ReasoningEffort:  reasoningEffort,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     firstTokenMs,
			ClientDisconnect: clientDisconnected,
		}
	}

	// 读间隔上限：上游挂住 SSE（不发数据也不断连）时结束排水。上游 ctx 为
	// WithoutCancel 且 http.Client 无整体 Timeout，无此界限则客户端断开后
	// scanner.Scan() 永久阻塞（见 anthropic native pump 文件注释）。
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai cc via native anthropic stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	// onIdle 关闭上游连接（解除阻塞的读、归还连接池位），并按已累计 usage
	// 返回——与 messages 主路径 "stream usage incomplete after timeout" 同语义。
	onIdle := func() (*Result, error) {
		_ = resp.Body.Close()
		if !clientDisconnected {
			o.Warn("openai cc via native anthropic stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.Duration("interval", streamInterval),
			)
		}
		return resultWithUsage(), fmt.Errorf("stream data interval timeout")
	}

	writeChunk := func(chunk protocolopenai.ChatCompletionsChunk) bool {
		if clientDisconnected {
			return false // 已断开：不再写客户端，只排水上游累计 usage
		}
		sse, err := bridge.ChatChunkToSSE(chunk)
		if err != nil {
			return false
		}
		out := string(o.ReverseTools([]byte(sse)))
		if _, err := fmt.Fprint(c.Writer, out); err != nil {
			clientDisconnected = true
			return false
		}
		return false
	}

	processAnthropicEvent := func(event *protocolanthropic.AnthropicStreamEvent) bool {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		// usage 恒累计（含客户端断开后的排水阶段，payg 上游照常计费）。
		if event.Type == "message_delta" && event.Usage != nil {
			protocolanthropic.MergeAnthropicUsage(&usage, *event.Usage)
		}
		if event.Type == "message_start" && event.Message != nil {
			protocolanthropic.MergeAnthropicUsage(&usage, event.Message.Usage)
		}

		// 客户端已断开：跳过转换与写出，继续读上游直到流结束（usage 完整、
		// 连接及时归还），不再提前 return。
		if clientDisconnected {
			return false
		}

		responsesEvents := bridge.AnthropicEventToResponsesEvents(o.Runtime, event, anthState)
		for _, resEvt := range responsesEvents {
			ccChunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				writeChunk(chunk)
			}
		}
		if len(responsesEvents) > 0 {
			c.Writer.Flush()
		}
		return false
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			// EOF / 读错误：事件行后流终止，进入 finalize。
			logReadErr(rerr)
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		if processAnthropicEvent(&event) {
			return resultWithUsage(), nil
		}
	}

	// Finalize both state machines（客户端已断开时仍执行，保证 usage 汇总完整）。
	finalResEvents := bridge.FinalizeAnthropicResponsesStream(anthState)
	for _, resEvt := range finalResEvents {
		ccChunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range ccChunks {
			writeChunk(chunk) //nolint:errcheck
		}
	}
	finalCCChunks := bridge.FinalizeResponsesChatStream(ccState)
	for _, chunk := range finalCCChunks {
		writeChunk(chunk) //nolint:errcheck
	}

	if !clientDisconnected {
		fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
		c.Writer.Flush()
	}

	return resultWithUsage(), nil
}

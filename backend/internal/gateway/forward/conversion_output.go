package forward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

// ResponsesBuffered 保留当前转换链的事件推进、用量与退出顺序。
func ResponsesBuffered(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (*Result, error) {
	requestID := in.RequestID

	scanner := in.Lines

	var finalResp *protocolanthropic.AnthropicResponse
	var usage upstream.TokenUsage

	for scanner.Scan() {
		line := scanner.Text()
		eventType, ok := protocolopenai.ExtractSSEEventLine(line)
		if !ok {
			continue
		}

		if !scanner.Scan() {
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(scanner.Text())
		if !ok {
			continue
		}

		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			out.Observe("warn", "forward_as_responses buffered: failed to parse event", err, requestID, eventType)
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
					finalResp.Content[idx].Input = AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_responses buffered: read error", err, requestID, "")
		}
	}

	if finalResp == nil {
		out.Error(502, "server_error", "Upstream stream ended without a response")
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

	responsesResp := bridge.AnthropicToResponsesResponse(in.Runtime, finalResp)
	responsesResp.Model = originalModel // Use original model name

	out.CopyHeaders(in.Headers)
	// 非流式响应必须是 application/json。上游被强制流式后会返回
	// Content-Type: text/event-stream，经 WriteFilteredHeaders 透传后会污染
	// 响应头；而 c.Data/c.JSON 走 Gin 的 writeContentType（仅当头不存在时才设置），
	// 无法覆盖已存在的 SSE 头。这里显式 Set 强制改回 JSON，避免下游中间层
	// （如 new-api）按 Content-Type 误判为流式。
	out.BeginJSON()
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = out.ReverseTools(respBytes)
		respBytes, _, err = bridge.RestoreResponsesClientToolPayload(respBytes, clientToolMapping)
		if err != nil {
			return nil, fmt.Errorf("restore responses client tools: %w", err)
		}
		out.JSONBytes(respBytes)
	} else {
		out.ResponsesJSON(responsesResp)
	}

	return &Result{
		RequestID:       requestID,
		UpstreamHeaders: in.Headers,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// ResponsesStreaming 保留当前转换链的事件推进、用量与退出顺序。
func ResponsesStreaming(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (*Result, error) {
	requestID := in.RequestID

	out.CopyHeaders(in.Headers)
	out.BeginStream()

	state := bridge.NewAnthropicEventToResponsesState(in.Runtime)
	state.Model = originalModel
	clientToolRestorer := bridge.NewResponsesClientToolStreamRestorer(clientToolMapping)
	var usage upstream.TokenUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := in.Lines

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:       requestID,
			UpstreamHeaders: in.Headers,
			Usage:           usage,
			Model:           originalModel,
			UpstreamModel:   mappedModel,
			ReasoningEffort: reasoningEffort,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    firstTokenMs,
		}
	}

	// writeEvent 统一完成名称反转、客户端工具还原与 SSE 写出，尾部事件也复用此路径。
	writeEvent := func(evt protocolopenai.ResponsesStreamEvent) bool {
		payload, err := json.Marshal(evt)
		if err != nil {
			out.Observe("warn", "forward_as_responses stream: failed to marshal event", err, requestID, "")
			return false
		}
		payload = out.ReverseTools(payload)
		payloads, _, err := clientToolRestorer.RestoreEvent(payload)
		if err != nil {
			out.Observe("warn", "forward_as_responses stream: failed to restore client tools", err, requestID, "")
			return false
		}
		for _, restored := range payloads {
			eventType := gjson.GetBytes(restored, "type").String()
			if _, err := out.Event(eventType, restored); err != nil {
				out.Observe("info", "forward_as_responses stream: client disconnected", nil, requestID, "")
				return true
			}
		}
		return false
	}

	processEvent := func(event *protocolanthropic.AnthropicStreamEvent) bool {
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

		events := bridge.AnthropicEventToResponsesEvents(in.Runtime, event, state)
		for _, evt := range events {
			if writeEvent(evt) {
				return true
			}
		}
		if len(events) > 0 {
			out.Flush()
		}
		return false
	}

	finalizeStream := func() (*Result, error) {
		if finalEvents := bridge.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 {
			for _, evt := range finalEvents {
				if writeEvent(evt) {
					return resultWithUsage(), nil
				}
			}
			out.Flush()
		}
		return resultWithUsage(), nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		eventType, ok := protocolopenai.ExtractSSEEventLine(line)
		if !ok {
			continue
		}

		if !scanner.Scan() {
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(scanner.Text())
		if !ok {
			continue
		}

		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			out.Observe("warn", "forward_as_responses stream: failed to parse event", err, requestID, eventType)
			continue
		}

		if processEvent(&event) {
			return resultWithUsage(), nil
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_responses stream: read error", err, requestID, "")
		}
	}

	return finalizeStream()
}

// ChatBuffered 保留当前转换链的事件推进、用量与退出顺序。
func ChatBuffered(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time) (*Result, error) {
	requestID := in.RequestID

	scanner := in.Lines

	var finalResp *protocolanthropic.AnthropicResponse
	var usage upstream.TokenUsage

	for scanner.Scan() {
		line := scanner.Text()
		// SSE 规范允许冒号后不带空格，必须兼容紧凑格式的 Anthropic 上游。
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		if !scanner.Scan() {
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(scanner.Text())
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
					finalResp.Content[idx].Input = AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_cc buffered: read error", err, requestID, "")
		}
	}

	if finalResp == nil {
		out.Error(502, "server_error", "Upstream stream ended without a response")
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

	responsesResp := bridge.AnthropicToResponsesResponse(in.Runtime, finalResp)
	ccResp := bridge.ResponsesToChatCompletions(in.Runtime, responsesResp, originalModel)

	out.CopyHeaders(in.Headers)
	// 非流式响应必须是 application/json。上游被强制流式后会返回
	// Content-Type: text/event-stream，经 WriteFilteredHeaders 透传后会污染
	// 响应头；而 c.Data/c.JSON 走 Gin 的 writeContentType（仅当头不存在时才设置），
	// 无法覆盖已存在的 SSE 头。这里显式 Set 强制改回 JSON，避免下游中间层
	// （如 new-api）按 Content-Type 误判为流式。
	out.BeginJSON()
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = out.ReverseTools(respBytes)
		out.JSONBytes(respBytes)
	} else {
		out.ChatJSON(ccResp)
	}

	return &Result{
		RequestID:       requestID,
		UpstreamHeaders: in.Headers,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// ChatStreaming 保留当前转换链的事件推进、用量与退出顺序。
func ChatStreaming(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time, includeUsage bool) (*Result, error) {
	requestID := in.RequestID

	out.CopyHeaders(in.Headers)
	out.BeginStream()

	anthState := bridge.NewAnthropicEventToResponsesState(in.Runtime)
	anthState.Model = originalModel
	ccState := bridge.NewResponsesEventToChatState(in.Runtime)
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage

	var usage upstream.TokenUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := in.Lines

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:       requestID,
			UpstreamHeaders: in.Headers,
			Usage:           usage,
			Model:           originalModel,
			UpstreamModel:   mappedModel,
			ReasoningEffort: reasoningEffort,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    firstTokenMs,
		}
	}

	writeChunk := func(chunk protocolopenai.ChatCompletionsChunk) bool {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return false
		}
		payload = out.ReverseTools(payload)
		// HTTP Adapter 持有请求侧工具名称映射；无映射时仅做静态前缀还原。
		if _, err := out.Event("", payload); err != nil {
			return true // client disconnected
		}
		return false
	}

	processAnthropicEvent := func(event *protocolanthropic.AnthropicStreamEvent) bool {
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

		responsesEvents := bridge.AnthropicEventToResponsesEvents(in.Runtime, event, anthState)
		for _, resEvt := range responsesEvents {
			ccChunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				if disconnected := writeChunk(chunk); disconnected {
					return true
				}
			}
		}
		out.Flush()
		return false
	}

	for scanner.Scan() {
		line := scanner.Text()
		// 与缓冲路径一致，接受冒号后无空格的紧凑 SSE 格式。
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		if !scanner.Scan() {
			break
		}
		payload, ok := protocolopenai.ExtractSSEDataLine(scanner.Text())
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

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_cc stream: read error", err, requestID, "")
		}
	}

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

	out.Event("", []byte("[DONE]")) //nolint:errcheck
	out.Flush()

	return resultWithUsage(), nil
}
func AppendRawJSON(existing json.RawMessage, fragment string) json.RawMessage {
	var existingObject map[string]json.RawMessage
	isEmptyObject := json.Unmarshal(existing, &existingObject) == nil && existingObject != nil && len(existingObject) == 0
	if len(existing) == 0 || isEmptyObject {
		return json.RawMessage(fragment)
	}
	return json.RawMessage(string(existing) + fragment)
}

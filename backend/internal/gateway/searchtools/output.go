// 合成事件顺序归网关；HTTP 状态、Header、Flush 和实际写出由 Output Adapter 拥有。
package searchtools

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/search/contract"
)

func (s *Emulator) writeStream(
	output Output, query string, resp *contract.SearchResponse, model string, startTime time.Time,
) (*Result, error) {
	msgID := webSearchMsgIDPrefix + s.newID()
	toolUseID := ToolUseIDPrefix + s.newID()[:16]
	textSummary := BuildTextSummary(query, resp.Results)

	output.StartStream()
	w := output
	for _, fn := range []func() error{
		func() error { return writeSSEMessageStart(w, msgID, model) },
		func() error { return writeSSEServerToolUse(w, toolUseID, query, 0) },
		func() error { return writeSSEToolResult(w, toolUseID, resp.Results, 1) },
		func() error { return writeSSETextBlock(w, textSummary, 2) },
		func() error { return writeSSEMessageEnd(w, len(textSummary)/tokenEstimateDivisor) },
	} {
		if err := fn(); err != nil {
			s.observe(Event{Kind: "write_failed", Err: err})
			break
		}
	}
	output.Flush()

	return &Result{Model: model, Duration: s.now().Sub(startTime)}, nil
}

func writeSSEMessageStart(w Output, msgID, model string) error {
	evt := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	}
	return flushSSEJSON(w, "message_start", evt)
}

func writeSSEServerToolUse(w Output, toolUseID, query string, index int) error {
	start := map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{
			"type": "server_tool_use", "id": toolUseID,
			"name": toolNameWebSearch, "input": map[string]string{"query": query},
		},
	}
	if err := flushSSEJSON(w, "content_block_start", start); err != nil {
		return err
	}
	return flushSSEJSON(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func writeSSEToolResult(w Output, toolUseID string, results []contract.SearchResult, index int) error {
	start := map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{
			"type": "web_search_tool_result", "tool_use_id": toolUseID,
			"content": BuildSearchResultBlocks(results),
		},
	}
	if err := flushSSEJSON(w, "content_block_start", start); err != nil {
		return err
	}
	return flushSSEJSON(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func writeSSETextBlock(w Output, text string, index int) error {
	if err := flushSSEJSON(w, "content_block_start", map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		return err
	}
	if err := flushSSEJSON(w, "content_block_delta", map[string]any{
		"type": "content_block_delta", "index": index,
		"delta": map[string]string{"type": "text_delta", "text": text},
	}); err != nil {
		return err
	}
	return flushSSEJSON(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func writeSSEMessageEnd(w Output, outputTokens int) error {
	if err := flushSSEJSON(w, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": outputTokens},
	}); err != nil {
		return err
	}
	return flushSSEJSON(w, "message_stop", map[string]string{"type": "message_stop"})
}

func flushSSEJSON(w Output, event string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return w.WriteEvent(event, body)
}

func (s *Emulator) writeJSON(
	output Output, query string, resp *contract.SearchResponse, model string, startTime time.Time,
) (*Result, error) {
	msgID := webSearchMsgIDPrefix + s.newID()
	toolUseID := ToolUseIDPrefix + s.newID()[:16]
	textSummary := BuildTextSummary(query, resp.Results)

	msg := map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": model,
		"content": []any{
			map[string]any{
				"type": "server_tool_use", "id": toolUseID,
				"name": toolNameWebSearch, "input": map[string]string{"query": query},
			},
			map[string]any{
				"type": "web_search_tool_result", "tool_use_id": toolUseID,
				"content": BuildSearchResultBlocks(resp.Results),
			},
			map[string]any{"type": "text", "text": textSummary},
		},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]int{"input_tokens": 0, "output_tokens": len(textSummary) / tokenEstimateDivisor},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("web search emulation: marshal response: %w", err)
	}
	output.WriteJSON(body)

	return &Result{Model: model, Duration: s.now().Sub(startTime)}, nil
}

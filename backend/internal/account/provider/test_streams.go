package provider

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Gemini 保留 Gemini 流的内容、图片与终态事件顺序。
func (s TestStreamOutput) Gemini(c *TestRun, body io.Reader) error {
	reader := bufio.NewReader(body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			return s.Error(c, fmt.Sprintf("Stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		if jsonStr == "[DONE]" {
			s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
			return nil
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// 同时兼容原生与 CLI 包装的两种响应形状：
		// - AI Studio: {"candidates": [...]}
		// - Gemini CLI: {"response": {"candidates": [...]}}
		if resp, ok := data["response"].(map[string]any); ok && resp != nil {
			data = resp
		}
		if candidates, ok := data["candidates"].([]any); ok && len(candidates) > 0 {
			if candidate, ok := candidates[0].(map[string]any); ok {
				// 先输出内容，再判断本帧是否完成。
				if content, ok := candidate["content"].(map[string]any); ok {
					if parts, ok := content["parts"].([]any); ok {
						for _, part := range parts {
							if partMap, ok := part.(map[string]any); ok {
								if text, ok := partMap["text"].(string); ok && text != "" {
									s.SendEvent(c, accountcore.TestEvent{Type: "content", Text: text})
								}
								if inlineData, ok := partMap["inlineData"].(map[string]any); ok {
									mimeType, _ := inlineData["mimeType"].(string)
									data, _ := inlineData["data"].(string)
									if strings.HasPrefix(strings.ToLower(mimeType), "image/") && data != "" {
										s.SendEvent(c, accountcore.TestEvent{
											Type:     "image",
											ImageURL: fmt.Sprintf("data:%s;base64,%s", mimeType, data),
											MimeType: mimeType,
										})
									}
								}
							}
						}
					}
				}

				// 内容处理完毕后再发送完成事件。
				if finishReason, ok := candidate["finishReason"].(string); ok && finishReason != "" {
					s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
					return nil
				}
			}
		}

		// 错误事件沿用原消息回退。
		if errData, ok := data["error"].(map[string]any); ok {
			errorMsg := "Unknown error"
			if msg, ok := errData["message"].(string); ok {
				errorMsg = msg
			}
			return s.Error(c, errorMsg)
		}
	}
}

// Anthropic 将 Claude 原生流投影为账号测试事件。
func (s TestStreamOutput) Anthropic(c *TestRun, body io.Reader) error {
	reader := bufio.NewReader(body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			return s.Error(c, fmt.Sprintf("Stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}

		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
			return nil
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		switch eventType {
		case "content_block_delta":
			if delta, ok := data["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok {
					s.SendEvent(c, accountcore.TestEvent{Type: "content", Text: text})
				}
			}
		case "message_stop":
			s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
			return nil
		case "error":
			errorMsg := "Unknown error"
			if errData, ok := data["error"].(map[string]any); ok {
				if msg, ok := errData["message"].(string); ok {
					errorMsg = msg
				}
			}
			return s.Error(c, errorMsg)
		}
	}
}

func (s TestStreamOutput) Qoder(c *TestRun, body io.ReadCloser) error {
	if body == nil {
		return s.Error(c, "Qoder response body is nil")
	}
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), upstream.DefaultSSELineLimit)
	seenEvent := false
	for scanner.Scan() {
		events, err := qoder.ParseSSELine(scanner.Text())
		if err != nil {
			return s.Error(c, err.Error())
		}
		for _, event := range events {
			seenEvent = true
			if event.Type == "text_delta" && event.Text != "" {
				s.SendEvent(c, accountcore.TestEvent{Type: "content", Text: event.Text})
			}
			if event.IsDone {
				s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
				return nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return s.Error(c, fmt.Sprintf("Qoder stream read error: %s", err.Error()))
	}
	if seenEvent {
		s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
		return nil
	}
	return s.Error(c, "Qoder stream ended before any response")
}

// ChatCompletions 处理 OpenAI 兼容 Chat Completions API 返回的 SSE 分片。
func (s TestStreamOutput) ChatCompletions(c *TestRun, body io.Reader) error {
	reader := bufio.NewReader(body)
	seenJSON := false
	seenFinish := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if seenFinish {
					s.SendEvent(c, accountcore.TestEvent{Type: "status", Text: "已通过 /v1/chat/completions 验证"})
					s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
					return nil
				}
				if seenJSON {
					return s.Error(c, "Chat Completions stream from /v1/chat/completions ended before [DONE]")
				}
				return s.Error(c, "Invalid Chat Completions response from /v1/chat/completions: expected SSE JSON data")
			}
			return s.Error(c, fmt.Sprintf("Chat Completions stream read error from /v1/chat/completions: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}

		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			s.SendEvent(c, accountcore.TestEvent{Type: "status", Text: "已通过 /v1/chat/completions 验证"})
			s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
			return nil
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			return s.Error(c, "Invalid Chat Completions response from /v1/chat/completions: expected JSON data")
		}
		seenJSON = true

		if errData, ok := data["error"].(map[string]any); ok {
			errorMsg := "Chat Completions API (/v1/chat/completions) returned an error"
			if msg, ok := errData["message"].(string); ok && msg != "" {
				errorMsg = msg
			}
			return s.Error(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) error: %s", errorMsg))
		}

		choices, ok := data["choices"].([]any)
		if !ok {
			continue
		}
		for _, choiceValue := range choices {
			choice, ok := choiceValue.(map[string]any)
			if !ok {
				continue
			}
			if delta, ok := choice["delta"].(map[string]any); ok {
				if text, ok := delta["content"].(string); ok && text != "" {
					s.SendEvent(c, accountcore.TestEvent{Type: "content", Text: text})
				}
			}
			if message, ok := choice["message"].(map[string]any); ok {
				if text, ok := message["content"].(string); ok && text != "" {
					s.SendEvent(c, accountcore.TestEvent{Type: "content", Text: text})
				}
			}
			if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
				seenFinish = true
			}
		}
	}
}

// Responses 要求实际收到终态事件，不能把提前 EOF 当作成功。
func (s TestStreamOutput) Responses(c *TestRun, body io.Reader) error {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return s.Error(c, "Stream ended before response.completed")
			}
			return s.Error(c, fmt.Sprintf("Stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}

		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			return s.Error(c, "Stream ended before response.completed")
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		switch eventType {
		case "response.output_text.delta":
			// Responses 文本增量使用 delta 字段。
			if delta, ok := data["delta"].(string); ok && delta != "" {
				s.SendEvent(c, accountcore.TestEvent{Type: "content", Text: delta})
			}
		case "response.completed", "response.done":
			s.SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
			return nil
		case "response.failed":
			errorMsg := "OpenAI response failed"
			if responseData, ok := data["response"].(map[string]any); ok {
				if errData, ok := responseData["error"].(map[string]any); ok {
					if msg, ok := errData["message"].(string); ok && msg != "" {
						errorMsg = msg
					}
				}
			}
			return s.Error(c, errorMsg)
		case "error":
			errorMsg := "Unknown error"
			if errData, ok := data["error"].(map[string]any); ok {
				if msg, ok := errData["message"].(string); ok {
					errorMsg = msg
				}
			}
			return s.Error(c, errorMsg)
		}
	}
}

func (s TestStreamOutput) SendEvent(c *TestRun, event accountcore.TestEvent) {
	if event.Type == "test_complete" && c.SuppressCompletion() {
		return
	}
	if err := c.Emit(event); err != nil {
		log.Printf("failed to write SSE event: %v", err)
	}
}

// Error 保留原错误日志、事件和返回文本。
func (s TestStreamOutput) Error(c *TestRun, errorMsg string) error {
	log.Printf("Account test error: %s", errorMsg)
	s.SendEvent(c, accountcore.TestEvent{Type: "error", Error: errorMsg})
	return fmt.Errorf("%s", errorMsg)
}

// TestStreamOutput 将供应商测试流同步投影为账号测试事件，不持有请求或共享状态。
type TestStreamOutput struct{}

// 兼容原 data: 前缀后可选空白，保留各平台独立的终态判定。
var sseDataPrefix = regexp.MustCompile(`^data:\s*`)

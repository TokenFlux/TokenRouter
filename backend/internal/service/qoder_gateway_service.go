package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	qoderDefaultMaxTokens = 32768
	qoderStreamTimeout    = 15 * time.Minute
	qoderKeepaliveEvery   = 10 * time.Second
)

// defaultQoderModelAliases maps user-facing model names to Qoder API keys.
// Confirmed keys are based on observed Qoder COSY responses. ultimate exposes
// Claude Opus 4.6 evidence in encrypted reasoning metadata. performance/auto
// report Anthropic but do not expose confirmed thinking metadata, and lite can
// route like Qwen with occasional Claude-style output, so keep those aliases
// explicitly marked as uncertain.
var defaultQoderModelAliases = map[string]qoderModelInfo{
	// Claude/Anthropic tier.
	"claude-opus-4-6": {Key: "ultimate", Source: "system"},
	// UNCERTAIN: performance reports Anthropic but exact Claude variant is unconfirmed.
	"claude-sonnet-4-5": {Key: "performance", Source: "system"},
	// UNCERTAIN: auto is backend-selected and may change routing.
	"claude-haiku-4-5": {Key: "auto", Source: "system"},
	// UNCERTAIN: auto is backend-selected and may change routing.
	"auto":     {Key: "auto", Source: "system"},
	"ultimate": {Key: "ultimate", Source: "system"},
	// UNCERTAIN: performance reports Anthropic but exact Claude variant is unconfirmed.
	"performance": {Key: "performance", Source: "system"},
	// Qwen (Alibaba)
	"qwen3.7-max":  {Key: "qmodel_latest", Source: "system"},
	"qwen3.7-plus": {Key: "qmodel", Source: "system"},
	"efficient":    {Key: "efficient", Source: "system"},
	// UNCERTAIN: OpenAI-compatible default alias routed through the lite tier.
	"gpt-5-codex": {Key: "lite", Source: "system"},
	// UNCERTAIN: usually Qwen, but occasionally observed Claude-style output.
	"lite": {Key: "lite", Source: "system"},
	// DeepSeek
	"deepseek-v4-pro":   {Key: "dmodel", Source: "system"},
	"deepseek-v4-flash": {Key: "dfmodel", Source: "system"},
	// GLM
	"glm-5.1": {Key: "gm51model", Source: "system"},
	// Kimi
	"kimi-k2.7-code": {Key: "kmodel", Source: "system"},
	// MiniMax
	"minimax-m3": {Key: "mmodel", Source: "system"},
}

type qoderModelInfo struct {
	Key         string `json:"key"`
	Source      string `json:"source"`
	Provider    string `json:"provider,omitempty"`
	Notes       string `json:"notes,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type qoderStreamClient interface {
	StreamRequestContext(ctx context.Context, session *qoder.SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error)
}

type qoderStreamClientWithDoer interface {
	StreamRequestContextWithDoer(ctx context.Context, session *qoder.SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string, doer qoder.RequestDoer) (*http.Response, error)
}

// QoderGatewayService forwards OpenAI/Anthropic-compatible requests to Qoder COSY.
type QoderGatewayService struct {
	tokenProvider       *QoderTokenProvider
	client              qoderStreamClient
	accountRepo         AccountRepository
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
}

func NewQoderGatewayService(tokenProvider *QoderTokenProvider, accountRepo AccountRepository, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) *QoderGatewayService {
	if tokenProvider == nil {
		tokenProvider = NewQoderTokenProvider()
	}
	return &QoderGatewayService{
		tokenProvider:       tokenProvider,
		client:              qoder.NewClient(qoder.APIBaseURL),
		accountRepo:         accountRepo,
		httpUpstream:        httpUpstream,
		tlsFPProfileService: tlsFPProfileService,
	}
}

func (s *QoderGatewayService) ForwardChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	streamCtx, cancel := context.WithTimeout(ctx, qoderStreamTimeout)
	defer cancel()

	payload, modelKey, err := BuildQoderPayloadFromChatCompletions(body, qoderUserType(account))
	if err != nil {
		return nil, err
	}
	requestModel := strings.TrimSpace(gjsonString(body, "model"))
	clientStream := gjsonBool(body, "stream")
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal qoder payload: %w", err)
	}

	resp, err := s.openQoderStream(streamCtx, account, payloadBody, modelKey)
	if err != nil {
		s.applyUpstreamErrorPolicy(ctx, account, err)
		return nil, err
	}

	var responseBody []byte
	usage := ClaudeUsage{}
	if clientStream {
		streamResult, err := WriteQoderOpenAIStreamResponse(c, requestModel, resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		usage = streamResult.Usage
	} else {
		events, err := ReadQoderSSEEvents(resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		usage = qoderUsageFromEvents(events)
		responseBody, err = BuildQoderOpenAICompletion(requestModel, events)
		if err != nil {
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}

	return &ForwardResult{
		Model:         requestModel,
		UpstreamModel: modelKey,
		Usage:         usage,
		Stream:        clientStream,
		Duration:      time.Since(start),
		ResponseBody:  responseBody,
	}, nil
}

func (s *QoderGatewayService) ForwardMessages(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	streamCtx, cancel := context.WithTimeout(ctx, qoderStreamTimeout)
	defer cancel()

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, qoderUserType(account))
	if err != nil {
		return nil, err
	}
	requestModel := strings.TrimSpace(gjsonString(body, "model"))
	clientStream := gjsonBool(body, "stream")
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal qoder payload: %w", err)
	}

	resp, err := s.openQoderStream(streamCtx, account, payloadBody, modelKey)
	if err != nil {
		s.applyUpstreamErrorPolicy(ctx, account, err)
		return nil, err
	}

	var responseBody []byte
	usage := ClaudeUsage{}
	if clientStream {
		streamResult, err := WriteQoderAnthropicStreamResponse(c, requestModel, resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		usage = streamResult.Usage
	} else {
		events, err := ReadQoderSSEEvents(resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		usage = qoderUsageFromEvents(events)
		responseBody, err = BuildQoderAnthropicMessage(requestModel, events)
		if err != nil {
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}

	return &ForwardResult{
		Model:         requestModel,
		UpstreamModel: modelKey,
		Usage:         usage,
		Stream:        clientStream,
		Duration:      time.Since(start),
		ResponseBody:  responseBody,
	}, nil
}

func (s *QoderGatewayService) openQoderStream(ctx context.Context, account *Account, payload []byte, modelKey string) (*http.Response, error) {
	if s == nil || s.tokenProvider == nil {
		return nil, errors.New("qoder gateway service is not configured")
	}
	session, err := s.tokenProvider.GetSession(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get qoder session: %w", err)
	}
	client := s.client
	if client == nil {
		client = qoder.NewClient(qoder.APIBaseURL)
	}

	headers := map[string]string{
		"x-model-key":    modelKey,
		"x-model-source": "system",
	}
	if doer := s.qoderRequestDoer(account); doer != nil {
		if doerClient, ok := client.(qoderStreamClientWithDoer); ok {
			return doerClient.StreamRequestContextWithDoer(ctx, session, "", payload, headers, doer)
		}
	}
	return client.StreamRequestContext(ctx, session, "", payload, headers)
}

func (s *QoderGatewayService) qoderRequestDoer(account *Account) qoder.RequestDoer {
	if s == nil {
		return nil
	}
	return newQoderRequestDoer(account, s.httpUpstream, s.tlsFPProfileService)
}

func newQoderRequestDoer(account *Account, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) qoder.RequestDoer {
	if httpUpstream == nil || account == nil {
		return nil
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile *tlsfingerprint.Profile
	if tlsFPProfileService != nil {
		tlsProfile = tlsFPProfileService.ResolveTLSProfile(account)
	}
	return func(req *http.Request) (*http.Response, error) {
		return httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	}
}

func (s *QoderGatewayService) applyUpstreamErrorPolicy(ctx context.Context, account *Account, err error) {
	if s == nil || s.accountRepo == nil || account == nil || err == nil {
		return
	}
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return
	}
	switch {
	case apiErr.IsAgentLimit():
		resetAt, ok := apiErr.AgentLimitResetAt()
		if !ok {
			resetAt = time.Now().Add(30 * time.Second)
		}
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, resetAt)
	case apiErr.StatusCode == http.StatusTooManyRequests:
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, time.Now().Add(30*time.Second))
	case apiErr.StatusCode >= 500:
		_ = s.accountRepo.SetOverloaded(ctx, account.ID, time.Now().Add(30*time.Second))
	}
}

func BuildQoderPayloadFromChatCompletions(body []byte, userType string) (map[string]any, string, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "", fmt.Errorf("parse chat completions request: %w", err)
	}
	model, _ := req["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, "", errors.New("model is required")
	}

	var messages []qoderMessage
	var systemParts []string
	rawMessages, _ := req["messages"].([]any)
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		text := openAIMessageText(msg)
		if role == "system" {
			if text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		messages = append(messages, qoderMessage{Role: role, Text: text})
	}

	tools := qoderAnySlice(req["tools"])
	maxTokens := numberAsInt(req["max_tokens"], qoderDefaultMaxTokens)
	return buildQoderPayload(model, strings.Join(systemParts, "\n"), messages, tools, maxTokens, userType)
}

func BuildQoderPayloadFromAnthropicMessages(body []byte, userType string) (map[string]any, string, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "", fmt.Errorf("parse anthropic messages request: %w", err)
	}
	model, _ := req["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, "", errors.New("model is required")
	}

	system, err := anthropicSystemText(req["system"])
	if err != nil {
		return nil, "", err
	}

	var messages []qoderMessage
	rawMessages, _ := req["messages"].([]any)
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		text, err := anthropicContentText(msg["content"])
		if err != nil {
			return nil, "", err
		}
		messages = append(messages, qoderMessage{Role: role, Text: text})
	}

	tools := qoderAnySlice(req["tools"])
	maxTokens := numberAsInt(req["max_tokens"], qoderDefaultMaxTokens)
	return buildQoderPayload(model, system, messages, tools, maxTokens, userType)
}

type qoderMessage struct {
	Role string
	Text string
}

func buildQoderPayload(model, system string, messages []qoderMessage, tools []any, maxTokens int, userType string) (map[string]any, string, error) {
	modelInfo := resolveQoderModel(model)
	if userType == "" {
		userType = "personal_standard"
	}

	requestID := uuid.NewString()
	prompt := latestQoderUserText(messages)
	payload := qoderBasePayload()
	payload["request_id"] = requestID
	payload["chat_record_id"] = requestID
	payload["request_set_id"] = uuid.NewString()
	payload["session_id"] = uuid.NewString()
	payload["aliyun_user_type"] = userType
	payload["parameters"].(map[string]any)["max_tokens"] = maxTokens
	payload["model_config"].(map[string]any)["key"] = modelInfo.Key
	payload["model_config"].(map[string]any)["source"] = modelInfo.Source
	payload["chat_context"].(map[string]any)["text"].(map[string]any)["text"] = prompt
	extra := payload["chat_context"].(map[string]any)["extra"].(map[string]any)
	extra["modelConfig"].(map[string]any)["key"] = modelInfo.Key
	extra["modelConfig"].(map[string]any)["source"] = modelInfo.Source
	extra["originalContent"].(map[string]any)["text"] = prompt
	payload["business"] = map[string]any{
		"product":  "cli",
		"version":  "1.0.20",
		"type":     "agent",
		"stage":    "init",
		"id":       uuid.NewString(),
		"name":     truncateRunes(prompt, 30),
		"begin_at": time.Now().UnixMilli(),
	}

	outMessages := make([]any, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		outMessages = append(outMessages, qoderPayloadMessage("system", system))
	}
	for _, msg := range messages {
		outMessages = append(outMessages, qoderPayloadMessage(msg.Role, msg.Text))
	}
	payload["messages"] = outMessages
	payload["tools"] = tools
	return payload, modelInfo.Key, nil
}

func qoderBasePayload() map[string]any {
	return map[string]any{
		"request_id":       "",
		"request_set_id":   "",
		"chat_record_id":   "",
		"stream":           true,
		"chat_task":        "FREE_INPUT",
		"image_urls":       nil,
		"is_reply":         true,
		"is_retry":         false,
		"session_id":       "",
		"code_language":    "",
		"source":           1,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       map[string]any{"max_tokens": qoderDefaultMaxTokens},
		"aliyun_user_type": "personal_standard",
		"session_type":     "qodercli",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"chat_context": map[string]any{
			"chatPrompt": "",
			"features":   []any{},
			"imageUrls":  nil,
			"text":       map[string]any{"type": "text", "text": ""},
			"extra": map[string]any{
				"context":         []any{},
				"modelConfig":     map[string]any{"is_reasoning": false, "key": "lite"},
				"originalContent": map[string]any{"type": "text", "text": ""},
			},
		},
		"model_config": map[string]any{
			"key":              "lite",
			"display_name":     "Lite",
			"model":            "",
			"format":           "openai",
			"is_vl":            false,
			"is_reasoning":     false,
			"api_key":          "",
			"url":              "",
			"source":           "system",
			"max_input_tokens": 180000,
		},
		"messages": []any{},
		"tools":    []any{},
	}
}

var qoderBlankResponseMeta = map[string]any{
	"id": "",
	"usage": map[string]any{
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"total_tokens":      0,
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": 0,
		},
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	},
}

func qoderPayloadMessage(role, text string) map[string]any {
	isUser := role == "user"
	content := text
	if isUser {
		content = ""
	}
	msg := map[string]any{
		"role":                        role,
		"content":                     content,
		"contents":                    []any{},
		"reasoning_content_signature": "",
		"response_meta":               qoderBlankResponseMeta,
	}
	if text != "" {
		msg["contents"] = []any{map[string]any{"type": "text", "text": text}}
	}
	return msg
}

func ReadQoderSSEEvents(resp *http.Response) ([]qoder.SSEEvent, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("qoder response body is nil")
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
	events := make([]qoder.SSEEvent, 0)
	for scanner.Scan() {
		parsed, err := qoder.ParseSSELine(scanner.Text())
		if err != nil {
			return nil, err
		}
		for _, event := range parsed {
			events = append(events, event)
			if event.IsDone {
				return events, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func WriteQoderOpenAIStream(c *gin.Context, model string, events []qoder.SSEEvent) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil)); err != nil {
		return err
	}
	usage := ClaudeUsage{}
	totalTokens := 0
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
			totalTokens = event.TotalTokens
			if err := writeSSEData(c.Writer, openAIUsageChunk(completionID, model, usage, totalTokens)); err != nil {
				return err
			}
			continue
		}
		if event.IsDone {
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{}, "stop")); err != nil {
				return err
			}
			_, err := io.WriteString(c.Writer, "data: [DONE]\n\n")
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return err
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"content": event.Text}, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

type qoderStreamResult struct {
	Usage       ClaudeUsage
	TotalTokens int
}

func WriteQoderOpenAIStreamResponse(c *gin.Context, model string, resp *http.Response) (*qoderStreamResult, error) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil)); err != nil {
		closeQoderResponse(resp)
		return nil, err
	}
	result := &qoderStreamResult{}
	if err := streamQoderEvents(resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			result.TotalTokens = event.TotalTokens
			return writeSSEData(c.Writer, openAIUsageChunk(completionID, model, result.Usage, result.TotalTokens))
		}
		if event.IsDone {
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{}, "stop")); err != nil {
				return err
			}
			_, err := io.WriteString(c.Writer, "data: [DONE]\n\n")
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return err
		}
		if event.Type == "text_delta" && event.Text != "" {
			return writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"content": event.Text}, nil))
		}
		return nil
	}, func() error {
		_, err := io.WriteString(c.Writer, ": keep-alive\n\n")
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func WriteQoderAnthropicStream(c *gin.Context, model string, events []qoder.SSEEvent) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := writeAnthropicSSE(c.Writer, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		return err
	}
	if err := writeAnthropicSSE(c.Writer, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		return err
	}
	usage := ClaudeUsage{}
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
			if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{},
				"usage": qoderAnthropicUsage(usage),
			}); err != nil {
				return err
			}
			continue
		}
		if event.IsDone {
			if err := writeAnthropicSSE(c.Writer, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
				return err
			}
			if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
			}); err != nil {
				return err
			}
			return writeAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := writeAnthropicSSE(c.Writer, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": event.Text},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func WriteQoderAnthropicStreamResponse(c *gin.Context, model string, resp *http.Response) (*qoderStreamResult, error) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := writeAnthropicSSE(c.Writer, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		closeQoderResponse(resp)
		return nil, err
	}
	if err := writeAnthropicSSE(c.Writer, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		closeQoderResponse(resp)
		return nil, err
	}
	result := &qoderStreamResult{}
	if err := streamQoderEvents(resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			return writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{},
				"usage": qoderAnthropicUsage(result.Usage),
			})
		}
		if event.IsDone {
			if err := writeAnthropicSSE(c.Writer, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
				return err
			}
			if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
			}); err != nil {
				return err
			}
			return writeAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
		}
		if event.Type == "text_delta" && event.Text != "" {
			return writeAnthropicSSE(c.Writer, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": event.Text},
			})
		}
		return nil
	}, func() error {
		_, err := io.WriteString(c.Writer, ": keep-alive\n\n")
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func streamQoderEvents(resp *http.Response, handle func(qoder.SSEEvent) error, keepalive func() error) error {
	if resp == nil || resp.Body == nil {
		return errors.New("qoder response body is nil")
	}
	defer resp.Body.Close()

	type eventResult struct {
		events []qoder.SSEEvent
		err    error
	}
	results := make(chan eventResult, 1)
	go func() {
		defer close(results)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
		for scanner.Scan() {
			parsed, err := qoder.ParseSSELine(scanner.Text())
			if err != nil {
				results <- eventResult{err: err}
				return
			}
			if len(parsed) > 0 {
				results <- eventResult{events: parsed}
			}
			for _, event := range parsed {
				if event.IsDone {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			results <- eventResult{err: err}
		}
	}()

	ticker := time.NewTicker(qoderKeepaliveEvery)
	defer ticker.Stop()
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			for _, event := range result.events {
				if err := handle(event); err != nil {
					return err
				}
				if event.IsDone {
					return nil
				}
			}
		case <-ticker.C:
			if keepalive != nil {
				if err := keepalive(); err != nil {
					return err
				}
			}
		}
	}
}

func closeQoderResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func BuildQoderOpenAICompletion(model string, events []qoder.SSEEvent) ([]byte, error) {
	content := qoderTextFromEvents(events)
	usage := qoderUsageFromEvents(events)
	totalTokens := qoderTotalTokensFromEvents(events, usage)
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": content},
				"finish_reason": "stop",
			},
		},
		"usage": qoderOpenAIUsage(usage, totalTokens),
	})
}

func BuildQoderAnthropicMessage(model string, events []qoder.SSEEvent) ([]byte, error) {
	content := qoderTextFromEvents(events)
	usage := qoderUsageFromEvents(events)
	return json.Marshal(map[string]any{
		"id":            "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       []any{map[string]any{"type": "text", "text": content}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         qoderAnthropicUsage(usage),
	})
}

func qoderUsageFromEvents(events []qoder.SSEEvent) ClaudeUsage {
	usage := ClaudeUsage{}
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
		}
	}
	return usage
}

func mergeQoderUsageEvent(usage *ClaudeUsage, event qoder.SSEEvent) {
	if usage == nil || !event.HasUsage {
		return
	}
	usage.InputTokens = event.PromptTokens
	usage.OutputTokens = event.CompletionTokens
}

func qoderTotalTokensFromEvents(events []qoder.SSEEvent, usage ClaudeUsage) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].HasUsage && events[i].TotalTokens > 0 {
			return events[i].TotalTokens
		}
	}
	return usage.InputTokens + usage.OutputTokens
}

func qoderOpenAIUsage(usage ClaudeUsage, totalTokens int) map[string]any {
	promptTokens := usage.InputTokens
	completionTokens := usage.OutputTokens
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	return map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
}

func qoderAnthropicUsage(usage ClaudeUsage) map[string]any {
	return map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	}
}

func writeSSEData(w io.Writer, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func writeAnthropicSSE(w io.Writer, event string, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func openAIChunk(id, model string, delta map[string]any, finishReason any) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
}

func openAIUsageChunk(id, model string, usage ClaudeUsage, totalTokens int) map[string]any {
	chunk := openAIChunk(id, model, map[string]any{}, nil)
	chunk["usage"] = qoderOpenAIUsage(usage, totalTokens)
	return chunk
}

func resolveQoderModel(model string) qoderModelInfo {
	if info, ok := lookupQoderModelAlias(strings.TrimSpace(model)); ok {
		return info
	}
	return qoderModelInfo{Key: strings.TrimSpace(model), Source: "system"}
}

func qoderUserType(account *Account) string {
	if account == nil {
		return "personal_standard"
	}
	return firstNonEmptyQoder(account.GetCredential("user_type"), "personal_standard")
}

func latestQoderUserText(messages []qoderMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Text) != "" {
			return messages[i].Text
		}
	}
	return ""
}

func openAIMessageText(msg map[string]any) string {
	if content, ok := msg["content"].(string); ok {
		return content
	}
	if parts, ok := msg["content"].([]any); ok {
		textParts := make([]string, 0, len(parts))
		for _, part := range parts {
			partMap, ok := part.(map[string]any)
			if !ok || partMap["type"] != "text" {
				continue
			}
			if text, ok := partMap["text"].(string); ok {
				textParts = append(textParts, text)
			}
		}
		return strings.Join(textParts, "\n")
	}
	return ""
}

func anthropicSystemText(raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] != "text" {
				return "", fmt.Errorf("unsupported system block type: %v", block["type"])
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", errors.New("unsupported system field")
	}
}

func anthropicContentText(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			case "tool_result":
				parts = append(parts, anthropicToolResultText(block["content"]))
			case "tool_use":
				// Qoder payload.py ignores tool_use when flattening message text.
			default:
				return "", fmt.Errorf("unsupported content block type: %v", block["type"])
			}
		}
		return strings.Join(nonEmptyStrings(parts), "\n"), nil
	default:
		return "", nil
	}
}

func anthropicToolResultText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func qoderAnySlice(raw any) []any {
	if raw == nil {
		return []any{}
	}
	if values, ok := raw.([]any); ok {
		return values
	}
	return []any{}
}

func numberAsInt(raw any, fallback int) int {
	switch v := raw.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return fallback
}

func qoderTextFromEvents(events []qoder.SSEEvent) string {
	var buf bytes.Buffer
	for _, event := range events {
		if event.Type == "text_delta" && event.Text != "" {
			buf.WriteString(event.Text)
		}
	}
	return buf.String()
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func gjsonString(body []byte, path string) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	value, _ := decoded[path].(string)
	return value
}

func gjsonBool(body []byte, path string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	value, _ := decoded[path].(bool)
	return value
}

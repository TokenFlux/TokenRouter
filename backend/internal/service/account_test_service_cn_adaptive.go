package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

const accountTestSuppressCompletionContextKey = "account_test_suppress_completion"

// testCNProviderAdaptiveConnection 验证自适应国产供应商账号实际使用的全部原生端点。
// 智谱验证 Chat Completions 与 Anthropic，DeepSeek 和 Kimi 还验证 Responses。
func (s *AccountTestService) testCNProviderAdaptiveConnection(c *gin.Context, account *Account, modelID string, prompt string) error {
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = openai.DefaultTestModel
	}
	testModelID = account.GetMappedModel(testModelID)

	authToken := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if authToken == "" {
		return s.sendErrorAndEnd(c, "No API key available")
	}

	// Chat 探测负责开启 SSE 生命周期；全部原生端点通过前抑制中间完成事件。
	c.Set(accountTestSuppressCompletionContextKey, true)
	defer c.Set(accountTestSuppressCompletionContextKey, false)
	enabled := account.UpstreamProtocols()
	if len(enabled) == 0 {
		return s.sendErrorAndEnd(c, "No upstream protocols enabled")
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	if slices.Contains(enabled, ProtocolOpenAIChatCompletions) {
		if err := s.testCNProviderChatCompletionsConnection(c, account, modelID, prompt); err != nil {
			return err
		}
	}

	if slices.Contains(enabled, ProtocolAnthropicMessages) {
		if err := s.testCNProviderAdaptiveAnthropicConnection(c, account, testModelID, prompt, authToken); err != nil {
			return err
		}
	}

	if slices.Contains(enabled, ProtocolOpenAIResponses) {
		if err := s.testCNProviderAdaptiveResponsesConnection(c, account, testModelID, prompt, authToken); err != nil {
			return err
		}
	}

	c.Set(accountTestSuppressCompletionContextKey, false)
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *AccountTestService) testCNProviderAdaptiveAnthropicConnection(c *gin.Context, account *Account, testModelID string, prompt string, authToken string) error {
	ctx := c.Request.Context()
	baseURL, err := s.validateUpstreamBaseURL(account.GetCNProtocolBaseURL(APIProtocolAnthropic))
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid adaptive Anthropic base URL: %s", err.Error()))
	}
	apiURL := strings.TrimRight(baseURL, "/") + "/v1/messages"

	payload, err := createTestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create adaptive Anthropic test payload")
	}
	payloadBytes, _ := json.Marshal(payload)

	s.sendEvent(c, TestEvent{Type: "status", Text: "正在通过原生 /v1/messages 测试自适应 Anthropic 端点"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create adaptive Anthropic request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("anthropic-version", "2023-06-01")
	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}
	req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
	setAnthropicAPIKeyAuthHeader(req.Header, account, authToken)
	account.ApplyHeaderOverrides(req.Header)

	resp, err := s.doCNProviderAdaptiveRequest(req, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Adaptive Anthropic endpoint request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("Adaptive Anthropic endpoint returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendErrorAndEnd(c, errMsg)
	}

	if err := s.processCNProviderAdaptiveAnthropicStream(c, resp.Body); err != nil {
		return err
	}
	s.sendEvent(c, TestEvent{Type: "status", Text: "已通过原生 /v1/messages 验证"})
	return nil
}

func (s *AccountTestService) processCNProviderAdaptiveAnthropicStream(c *gin.Context, body io.Reader) error {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return s.sendErrorAndEnd(c, "Adaptive Anthropic stream ended before message_stop")
			}
			return s.sendErrorAndEnd(c, fmt.Sprintf("Adaptive Anthropic stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}
		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			return nil
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		switch eventType, _ := data["type"].(string); eventType {
		case "content_block_delta":
			if delta, ok := data["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok && text != "" {
					s.sendEvent(c, TestEvent{Type: "content", Text: text})
				}
			}
		case "message_stop":
			return nil
		case "error":
			errorMsg := "Unknown error"
			if errData, ok := data["error"].(map[string]any); ok {
				if message, ok := errData["message"].(string); ok && message != "" {
					errorMsg = message
				}
			}
			return s.sendErrorAndEnd(c, fmt.Sprintf("Adaptive Anthropic endpoint error: %s", errorMsg))
		}
	}
}

func (s *AccountTestService) testCNProviderAdaptiveResponsesConnection(c *gin.Context, account *Account, testModelID string, prompt string, authToken string) error {
	ctx := c.Request.Context()
	baseURL, err := s.validateUpstreamBaseURL(account.GetCNProtocolBaseURL(APIProtocolResponses))
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid adaptive Responses base URL: %s", err.Error()))
	}
	apiURL := buildOpenAIResponsesURLForPlatform(account.Platform, baseURL)

	payload := createOpenAITestPayload(testModelID, prompt, false)
	// DeepSeek 原生 Responses 端点无状态，不需要 OpenAI 探测使用的合成 instructions。
	delete(payload, "instructions")
	payloadBytes, _ := json.Marshal(payload)
	payloadBytes = normalizeDeepSeekResponsesRequestBody(account, payloadBytes)

	s.sendEvent(c, TestEvent{Type: "status", Text: "正在通过原生 /responses 测试自适应 Responses 端点"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create adaptive Responses request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+authToken)
	applyOpenAICodexProbeHeaders(req.Header)
	account.ApplyHeaderOverrides(req.Header)

	resp, err := s.doCNProviderAdaptiveRequest(req, account)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Adaptive Responses endpoint request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("Adaptive Responses endpoint returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendErrorAndEnd(c, errMsg)
	}

	if err := s.processOpenAIStream(c, resp.Body); err != nil {
		return err
	}
	s.sendEvent(c, TestEvent{Type: "status", Text: "已通过原生 /responses 验证"})
	return nil
}

func (s *AccountTestService) doCNProviderAdaptiveRequest(req *http.Request, account *Account) (*http.Response, error) {
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveTLSProfile(account))
}

// cnAnthropicBaseURLMisconfigHint 给仍指向 OpenAI 兼容端点的 Anthropic 账号返回可操作提示。
func cnAnthropicBaseURLMisconfigHint(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	path := strings.ToLower(strings.TrimRight(parsed.Path, "/"))
	if path == "" {
		return ""
	}
	openAICompatShaped := strings.Contains(path, "/paas/") ||
		strings.HasSuffix(path, "/chat/completions") ||
		strings.HasSuffix(path, "/responses") ||
		openAIBaseURLHasVersionSuffix(path)
	if !openAICompatShaped {
		return ""
	}
	return fmt.Sprintf(
		"API protocol is anthropic but base_url (%s) looks like an OpenAI-compatible endpoint; set base_url to the provider's Anthropic endpoint (for example https://open.bigmodel.cn/api/anthropic) or switch api_protocol.",
		baseURL,
	)
}

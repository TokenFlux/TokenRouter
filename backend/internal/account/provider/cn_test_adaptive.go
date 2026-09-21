package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ExecuteAdaptive 验证自适应国产供应商账号实际使用的全部原生端点。
// 智谱验证 Chat Completions 与 Anthropic，DeepSeek 和 Kimi 还验证 Responses。
func (s *CNAccountTest) ExecuteAdaptive(c *TestRun, value *accountcore.Record, modelID string, prompt string) error {
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = openai.DefaultTestModel
	}
	testModelID = mappedTestModel(value, testModelID)

	authToken := strings.TrimSpace(value.GetOpenAIProtocolAPIKey())
	if authToken == "" {
		return (TestStreamOutput{}).Error(c, "No API key available")
	}

	// Chat 探测负责开启 SSE 生命周期；全部原生端点通过前抑制中间完成事件。
	c.SetSuppressCompletion(true)
	defer c.SetSuppressCompletion(false)
	enabled := value.UpstreamProtocols()
	if len(enabled) == 0 {
		return (TestStreamOutput{}).Error(c, "No upstream protocols enabled")
	}
	c.Begin(false)
	if slices.Contains(enabled, protocol.ProtocolOpenAIChatCompletions) {
		if err := s.executeChat(c, value, modelID, prompt); err != nil {
			return err
		}
	}

	if slices.Contains(enabled, protocol.ProtocolAnthropicMessages) {
		if err := s.executeAdaptiveAnthropic(c, value, testModelID, prompt, authToken); err != nil {
			return err
		}
	}

	if slices.Contains(enabled, protocol.ProtocolOpenAIResponses) {
		if err := s.executeAdaptiveResponses(c, value, testModelID, prompt, authToken); err != nil {
			return err
		}
	}

	c.SetSuppressCompletion(false)
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *CNAccountTest) executeAdaptiveAnthropic(c *TestRun, value *accountcore.Record, testModelID string, prompt string, authToken string) error {
	ctx := c.Context
	baseURL, err := s.ValidateURL((accountcore.ProtocolTarget{Record: value}).GetCNProtocolBaseURL(accountcore.APIProtocolAnthropic))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid adaptive Anthropic base URL: %s", err.Error()))
	}
	apiURL := strings.TrimRight(baseURL, "/") + "/v1/messages"

	payload, err := claude.TestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create adaptive Anthropic test payload")
	}
	payloadBytes, _ := json.Marshal(payload)

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "status", Text: "正在通过原生 /v1/messages 测试自适应 Anthropic 端点"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create adaptive Anthropic request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("anthropic-version", "2023-06-01")
	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}
	req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
	claude.SetAPIKeyAuthHeader(req.Header, value.GetAnthropicAPIKeyAuthScheme() == accountcore.AnthropicAPIKeyAuthSchemeAuthorizationBearer, authToken)
	applyGrokQuotaHeaders(value, req.Header)

	resp, err := s.doAdaptiveRequest(req, value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Adaptive Anthropic endpoint request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("Adaptive Anthropic endpoint returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusUnauthorized && s.Store != nil {
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, errMsg)
	}

	if err := (TestStreamOutput{}).AdaptiveAnthropic(c, resp.Body); err != nil {
		return err
	}
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "status", Text: "已通过原生 /v1/messages 验证"})
	return nil
}

func (s *CNAccountTest) executeAdaptiveResponses(c *TestRun, value *accountcore.Record, testModelID string, prompt string, authToken string) error {
	ctx := c.Context
	baseURL, err := s.ValidateURL((accountcore.ProtocolTarget{Record: value}).GetCNProtocolBaseURL(accountcore.APIProtocolResponses))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid adaptive Responses base URL: %s", err.Error()))
	}
	apiURL := cnTestResponsesURL(value.Platform, baseURL)

	payload := openai.TestResponsesPayload(testModelID, prompt, false)
	// DeepSeek 原生 Responses 端点无状态，不需要 OpenAI 探测使用的合成 instructions。
	delete(payload, "instructions")
	payloadBytes, _ := json.Marshal(payload)
	if (accountcore.ProtocolTarget{Record: value}).UsesNativeCNResponses() {
		payloadBytes = openaiprotocol.StatelessResponsesRequest(payloadBytes)
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "status", Text: "正在通过原生 /responses 测试自适应 Responses 端点"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create adaptive Responses request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+authToken)
	openai.ApplyOpenAICodexProbeHeaders(req.Header)
	applyGrokQuotaHeaders(value, req.Header)

	resp, err := s.doAdaptiveRequest(req, value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Adaptive Responses endpoint request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("Adaptive Responses endpoint returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusUnauthorized && s.Store != nil {
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, errMsg)
	}

	if err := (TestStreamOutput{}).Responses(c, resp.Body); err != nil {
		return err
	}
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "status", Text: "已通过原生 /responses 验证"})
	return nil
}

func (s *CNAccountTest) doAdaptiveRequest(req *http.Request, value *accountcore.Record) (*http.Response, error) {
	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	return s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.resolveTLSProfile(value))
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
		httpclient.OpenAIBaseURLHasVersionSuffix(path)
	if !openAICompatShaped {
		return ""
	}
	return fmt.Sprintf(
		"API protocol is anthropic but base_url (%s) looks like an OpenAI-compatible endpoint; set base_url to the provider's Anthropic endpoint (for example https://open.bigmodel.cn/api/anthropic) or switch api_protocol.",
		baseURL,
	)
}

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/deepseek"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/zhipu"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func defaultCNProviderTestModel(platform string) string {
	switch platform {
	case capability.PlatformKimi:
		return kimi.DefaultTestModel
	case capability.PlatformZhipu:
		return zhipu.DefaultTestModel
	case capability.PlatformDeepseek:
		return deepseek.DefaultTestModel
	default:
		return ""
	}
}

// Execute 按账号真实上游协议选择测试端点，避免把 Chat 或
// Responses 账号错误地当作 Anthropic API Key 测试。
func (s *CNAccountTest) Execute(
	c *TestRun,
	value *accountcore.Record,
	modelID string,
	prompt string,
) error {
	if value == nil || value.Type != capability.AccountTypeAPIKey {
		return (TestStreamOutput{}).Error(c, "CN provider tests require an API Key account")
	}
	apiKey := strings.TrimSpace(value.GetCNAPIKey())
	if apiKey == "" {
		return (TestStreamOutput{}).Error(c, "No API key available")
	}
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = defaultCNProviderTestModel(value.Platform)
	}
	testModelID = mappedTestModel(value, testModelID)
	if testModelID == "" {
		return (TestStreamOutput{}).Error(c, "No test model available")
	}

	ctx := c.Context
	protocol := (accountcore.ProtocolTarget{Record: value}).GetAPIProtocol()
	var (
		apiURL  string
		payload any
	)
	switch protocol {
	case accountcore.APIProtocolAnthropic:
		baseURL, err := s.ValidateURL((accountcore.ProtocolTarget{Record: value}).GetAnthropicProtocolBaseURL())
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		if hint := cnAnthropicBaseURLMisconfigHint(baseURL); hint != "" {
			return (TestStreamOutput{}).Error(c, hint)
		}
		apiURL = strings.TrimRight(baseURL, "/") + "/v1/messages"
		payload, err = claude.TestPayloadWithPrompt(testModelID, prompt)
		if err != nil {
			return (TestStreamOutput{}).Error(c, "Failed to create test payload")
		}
	case accountcore.APIProtocolResponses:
		baseURL, err := s.ValidateURL((accountcore.ProtocolTarget{Record: value}).GetOpenAIBaseURL())
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = cnTestResponsesURL(value.Platform, baseURL)
		responsesPayload := openai.TestResponsesPayload(testModelID, prompt, false)
		responsesPayload["store"] = false
		payload = responsesPayload
	default:
		baseURL, err := s.ValidateURL((accountcore.ProtocolTarget{Record: value}).GetOpenAIBaseURL())
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = httpclient.BuildOpenAIEndpointURL(baseURL, "/v1/chat/completions")
		payload = openai.TestChatCompletionsPayload(testModelID, prompt)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create test payload")
	}

	c.Begin(true)
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if protocol == accountcore.APIProtocolAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		claude.SetAPIKeyAuthHeader(req.Header, value.GetAnthropicAPIKeyAuthScheme() == accountcore.AnthropicAPIKeyAuthSchemeAuthorizationBearer, apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	applyGrokQuotaHeaders(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	resp, err := s.Transport.DoWithTLS(
		req,
		proxyURL,
		value.ID,
		value.Concurrency,
		s.resolveTLSProfile(value),
	)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))
		if (protocol == accountcore.APIProtocolAnthropic && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden)) && s.Store != nil {
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, errMsg)
	}
	switch protocol {
	case accountcore.APIProtocolAnthropic:
		return (TestStreamOutput{}).Anthropic(c, resp.Body)
	case accountcore.APIProtocolResponses:
		return (TestStreamOutput{}).Responses(c, resp.Body)
	default:
		return (TestStreamOutput{}).ChatCompletions(c, resp.Body)
	}
}

// executeChat 保留上游自适应测试使用的 Chat 探测入口，
// 具体请求仍复用 fork 已有的国产供应商测试实现（含请求头覆写、代理和 TLS 指纹）。
func (s *CNAccountTest) executeChat(
	c *TestRun,
	value *accountcore.Record,
	modelID string,
	prompt string,
) error {
	return s.Execute(c, value, modelID, prompt)
}

// CNAccountTest 保留国产供应商的固定协议和自适应顺序，共享同一技术传输。
type CNAccountTest struct {
	Transport   QoderTransport
	Profiles    *egressprovider.TLSProfiles
	ValidateURL func(string) (string, error)
	Store       interface {
		SetError(context.Context, int64, string) error
	}
	Responses *OpenAIAccountTest
}

func (s *CNAccountTest) resolveTLSProfile(value *accountcore.Record) *tlsfingerprint.Profile {
	if s.Profiles == nil {
		return nil
	}
	return s.Profiles.ResolveRequestTLS(egress.TLSSelection{Enabled: value.IsTLSFingerprintEnabled(), DirectProfileID: value.GetTLSFingerprintProfileID()})
}
func cnTestResponsesURL(platform, base string) string {
	if platform == capability.PlatformDeepseek {
		return httpclient.BuildOpenAIEndpointURL(base, "/responses")
	}
	return httpclient.BuildOpenAIEndpointURL(base, "/v1/responses")
}

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

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// Execute 保留 Anthropic、Vertex 与 Bedrock 各自的测试路径。
func (s *AnthropicAccountTest) Execute(c *TestRun, value *accountcore.Record, modelID string, prompt string) error {
	ctx := c.Context

	// 保留缺省模型及原映射时机。
	testModelID := modelID
	if testModelID == "" {
		testModelID = claude.DefaultTestModel
	}

	// API Key 账号测试连接时也需要应用通配符模型映射。
	if value.Type == "apikey" {
		testModelID = mappedTestModel(value, testModelID)
	}

	// Bedrock 使用独立签名和非流式请求。
	if value.IsBedrock() {
		return s.ExecuteBedrock(c, ctx, value, testModelID, prompt)
	}
	if value.Type == capability.AccountTypeServiceAccount {
		return s.executeVertex(c, ctx, value, testModelID, prompt)
	}

	// 按原凭据类型构造认证与端点。
	var authToken string
	var apiURL string

	if value.IsOAuth() {
		apiURL = "https://api.anthropic.com/v1/messages?beta=true"
		authToken = value.GetCredential("access_token")
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No access token available")
		}
	} else if value.Type == "apikey" {
		authToken = value.GetCredential("api_key")
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}

		baseURL := value.GetBaseURL()
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		normalizedBaseURL, err := s.ValidateURL(baseURL)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = strings.TrimSuffix(normalizedBaseURL, "/") + "/v1/messages?beta=true"
	} else {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported account type: %s", value.Type))
	}

	// 保留事件流开始时机。
	c.Begin(true)

	// 所有类型复用原 Claude Code 测试报文。
	payload, err := claude.TestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create test payload")
	}
	payloadBytes, _ := json.Marshal(payload)

	// 报文构造完成后发布开始事件。
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}

	// 设置共同协议 Header。
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	// 应用原 Claude Code 客户端 Header。
	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}

	// 保留认证 Header 的原顺序。
	if value.IsOAuth() {
		req.Header.Set("anthropic-beta", claude.DefaultBetaHeader)
		req.Header.Set("Authorization", "Bearer "+authToken)
	} else {
		req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
		claude.SetAPIKeyAuthHeader(req.Header, value.GetAnthropicAPIKeyAuthScheme() == accountcore.AnthropicAPIKeyAuthSchemeAuthorizationBearer, authToken)
	}
	s.applyUserAgent(req)

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	applyGrokQuotaHeaders(value, req.Header)

	// 保留账号显式代理绑定。
	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.resolveTLSProfile(value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))

		// 403 表示账号被上游封禁，标记为 error 状态
		if resp.StatusCode == http.StatusForbidden {
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}

		return (TestStreamOutput{}).Error(c, errMsg)
	}

	// 逐事件解析流。
	return (TestStreamOutput{}).Anthropic(c, resp.Body)
}

func (s *AnthropicAccountTest) executeVertex(c *TestRun, ctx context.Context, value *accountcore.Record, testModelID string, prompt string) error {
	if mappedModel, matched := accountcore.ResolveMappedModel(value.Platform, accountcore.ResolveModelMapping(value, ModelDefaults()), testModelID); matched {
		testModelID = mappedModel
	} else {
		testModelID = vertex.NormalizeVertexAnthropicModelID(claude.NormalizeModelID(testModelID))
	}

	c.Begin(true)

	payload, err := claude.TestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create test payload")
	}
	payloadBytes, _ := json.Marshal(payload)
	vertexBody, err := vertex.BuildVertexAnthropicRequestBody(payloadBytes)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to create Vertex request body: %s", err.Error()))
	}

	if s.Tokens == nil {
		return (TestStreamOutput{}).Error(c, "Claude token provider not configured")
	}
	accessToken, err := s.Tokens.GetAccessToken(ctx, value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to get service account access token: %s", err.Error()))
	}

	fullURL, err := vertex.BuildVertexAnthropicURL(value.VertexProjectID(vertex.ServiceAccountProjectID), value.VertexLocation(testModelID), testModelID, true)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to build Vertex URL: %s", err.Error()))
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(vertexBody))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	s.applyUserAgent(req)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.resolveTLSProfile(value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusForbidden {
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, errMsg)
	}

	return (TestStreamOutput{}).Anthropic(c, resp.Body)
}

// ExecuteBedrock 以非流式 invoke 验证 SigV4 或 API Key。
func (s *AnthropicAccountTest) ExecuteBedrock(c *TestRun, ctx context.Context, value *accountcore.Record, testModelID string, prompt string) error {
	route, err := bedrock.ResolveBedrockModelRoute(&bedrock.RouteInput{Region: value.GetCredential("aws_region"), ForceGlobal: value.GetCredential("aws_force_global") == "true", Model: mappedTestModel(value, testModelID)}, testModelID)
	if err != nil {
		return (TestStreamOutput{}).Error(c, bedrock.BedrockRoutingDiagnostic(err))
	}
	region := route.SourceRegion
	testModelID = route.ModelID

	// 保留事件流开始时机。 (test UI expects SSE)
	c.Begin(true)

	// 保留无 stream 与 cache_control 的最小 Bedrock 报文。
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}
	bedrockPayload := map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": testPrompt,
					},
				},
			},
		},
		"max_tokens":  256,
		"temperature": 1,
	}
	bedrockBody, _ := json.Marshal(bedrockPayload)

	// 上游非流式响应仍按 Claude JSON 处理。
	apiURL := bedrock.BuildBedrockURL(region, testModelID, false)

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bedrockBody))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")

	// 按账号类型设置 Bearer 或 SigV4 签名。
	if value.IsBedrockAPIKey() {
		apiKey := value.GetCredential("api_key")
		if apiKey == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else {
		signer, err := NewBedrockSignerFromAccount(value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to create Bedrock signer: %s", err.Error()))
		}
		if err := signer.SignRequest(ctx, req, bedrockBody); err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to sign request: %s", err.Error()))
		}
	}
	s.applyUserAgent(req)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, nil)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// 从原非流式 Claude JSON 提取第一段文字。
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to parse response: %s", err.Error()))
	}

	text := ""
	if len(result.Content) > 0 {
		text = result.Content[0].Text
	}
	if text == "" {
		text = "(empty response)"
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: text})
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

// AnthropicAccountTest 持有技术端口，凭据和健康持久化使用原生账号接口。
type AnthropicAccountTest struct {
	Tokens      *accountcore.ClaudeTokenSource
	Transport   QoderTransport
	Profiles    *egressprovider.TLSProfiles
	ValidateURL func(string) (string, error)
	Store       interface {
		SetError(context.Context, int64, string) error
	}
	UserAgent string
}

func (s *AnthropicAccountTest) applyUserAgent(req *http.Request) {
	if agent := strings.TrimSpace(s.UserAgent); agent != "" {
		req.Header.Set("User-Agent", agent)
	}
}
func (s *AnthropicAccountTest) resolveTLSProfile(value *accountcore.Record) *tlsfingerprint.Profile {
	if s.Profiles == nil {
		return nil
	}
	return s.Profiles.ResolveRequestTLS(egress.TLSSelection{Enabled: value.IsTLSFingerprintEnabled(), DirectProfileID: value.GetTLSFingerprintProfileID()})
}

// mappedTestModel 保留账号测试的单次映射，不持有请求路由缓存。
func mappedTestModel(value *accountcore.Record, model string) string {
	mapped, _ := accountcore.ResolveMappedModel(value.Platform, accountcore.ResolveModelMapping(value, ModelDefaults()), model)
	return mapped
}

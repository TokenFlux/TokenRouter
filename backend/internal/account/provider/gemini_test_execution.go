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
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

// Execute 按原账号类型构造一次 Gemini 测试请求并同步输出事件。
func (s *GeminiAccountTest) Execute(c *TestRun, value *accountcore.Record, modelID string, prompt string, testTypes ...string) error {
	ctx := c.Context

	// 保留缺省测试模型。
	testModelID := modelID
	if testModelID == "" {
		testModelID = geminicli.DefaultTestModel
	}

	// 静态凭据仅应用原精确模型映射。
	if value.Type == capability.AccountTypeAPIKey || value.Type == capability.AccountTypeServiceAccount {
		mapping := accountcore.ResolveModelMapping(value, ModelDefaults())
		if len(mapping) > 0 {
			if mappedModel, exists := mapping[testModelID]; exists {
				testModelID = mappedModel
			}
		}
	}

	// 保留事件流开始时机。
	c.Begin(true)

	// 构造原 Gemini 测试报文。
	payload := geminiTestPayload(testModelID, prompt, testTypes...)

	// 按凭据类型选择原请求构造。
	var req *http.Request
	var err error

	switch value.Type {
	case capability.AccountTypeAPIKey:
		req, err = s.buildGeminiAPIKeyRequest(ctx, value, testModelID, payload)
	case capability.AccountTypeOAuth:
		req, err = s.buildGeminiOAuthRequest(ctx, value, testModelID, payload)
	case capability.AccountTypeServiceAccount:
		req, err = s.buildGeminiServiceAccountRequest(ctx, value, testModelID, payload)
	default:
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported account type: %s", value.Type))
	}

	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to build request: %s", err.Error()))
	}

	// 请求构造成功后发送开始事件。
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})

	// 使用账号原代理及共享传输。
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
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// 逐事件解析，不缓冲完整输出。
	return (TestStreamOutput{}).Gemini(c, resp.Body)
}

// buildGeminiAPIKeyRequest 构造 API Key 请求。
func (s *GeminiAccountTest) buildGeminiAPIKeyRequest(ctx context.Context, value *accountcore.Record, modelID string, payload []byte) (*http.Request, error) {
	apiKey := value.GetCredential("api_key")
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("no API key available")
	}

	baseURL := value.GetCredential("base_url")
	if baseURL == "" {
		baseURL = geminicli.AIStudioBaseURL
	}
	normalizedBaseURL, err := s.ValidateURL(baseURL)
	if err != nil {
		return nil, err
	}

	// 保留 streamGenerateContent 的实时事件反馈。
	fullURL, err := gemini.BuildGeminiAIStudioModelActionURL(normalizedBaseURL, modelID, "streamGenerateContent", true)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	s.applyUserAgent(req)

	return req, nil
}

// buildGeminiOAuthRequest 保留有无 project 的两条 OAuth 路径。
func (s *GeminiAccountTest) buildGeminiOAuthRequest(ctx context.Context, value *accountcore.Record, modelID string, payload []byte) (*http.Request, error) {
	if s.Tokens == nil {
		return nil, fmt.Errorf("gemini token provider not configured")
	}

	// 复用唯一 token source 及其原刷新行为。
	accessToken, err := s.Tokens.GetAccessToken(ctx, value)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	projectID := strings.TrimSpace(value.GetCredential("project_id"))
	if projectID == "" {
		// 无 project_id 时直接使用 AI Studio Bearer 请求。
		baseURL := value.GetCredential("base_url")
		if strings.TrimSpace(baseURL) == "" {
			baseURL = geminicli.AIStudioBaseURL
		}
		normalizedBaseURL, err := s.ValidateURL(baseURL)
		if err != nil {
			return nil, err
		}
		fullURL, err := gemini.BuildGeminiAIStudioModelActionURL(normalizedBaseURL, modelID, "streamGenerateContent", true)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		s.applyUserAgent(req)
		return req, nil
	}

	// 有 project_id 时保留 Code Assist 包装。
	return s.buildCodeAssistRequest(ctx, accessToken, projectID, modelID, payload)
}

func (s *GeminiAccountTest) buildGeminiServiceAccountRequest(ctx context.Context, value *accountcore.Record, modelID string, payload []byte) (*http.Request, error) {
	if s.Tokens == nil {
		return nil, fmt.Errorf("gemini token provider not configured")
	}
	accessToken, err := s.Tokens.GetAccessToken(ctx, value)
	if err != nil {
		return nil, fmt.Errorf("failed to get service account access token: %w", err)
	}
	fullURL, err := vertex.BuildVertexGeminiURL(value.VertexProjectID(vertex.ServiceAccountProjectID), value.VertexLocation(modelID), modelID, "streamGenerateContent", true)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	s.applyUserAgent(req)
	return req, nil
}

// buildCodeAssistRequest 构造原 Code Assist 包装和 Header。
func (s *GeminiAccountTest) buildCodeAssistRequest(ctx context.Context, accessToken, projectID, modelID string, payload []byte) (*http.Request, error) {
	var inner map[string]any
	if err := json.Unmarshal(payload, &inner); err != nil {
		return nil, err
	}

	wrapped := map[string]any{
		"model":   modelID,
		"project": projectID,
		"request": inner,
	}
	wrappedBytes, _ := json.Marshal(wrapped)

	normalizedBaseURL, err := s.ValidateURL(geminicli.GeminiCliBaseURL)
	if err != nil {
		return nil, err
	}
	fullURL := fmt.Sprintf("%s/v1internal:streamGenerateContent?alt=sse", normalizedBaseURL)

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(wrappedBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", geminicli.GeminiCLIUserAgent)
	s.applyUserAgent(req)

	return req, nil
}

// geminiTestPayload 构造测试报文，保留显式类型与旧模型判断。
// 显式图片类型使用图片生成配置；未指定类型时保留旧模型名兼容判断。
func geminiTestPayload(modelID string, prompt string, testTypes ...string) []byte {
	testType, explicitTestType := accountcore.AccountTestTypeFromArgs(testTypes...)
	useImageTest := (explicitTestType && testType == accountcore.AccountTestTypeImage) ||
		(!explicitTestType && antigravity.IsImageGenerationModel(modelID))
	if useImageTest {
		imagePrompt := strings.TrimSpace(prompt)
		if imagePrompt == "" {
			imagePrompt = "Generate a cute orange cat astronaut sticker on a clean pastel background."
		}

		payload := map[string]any{
			"contents": []map[string]any{
				{
					"role": "user",
					"parts": []map[string]any{
						{"text": imagePrompt},
					},
				},
			},
			"generationConfig": map[string]any{
				"responseModalities": []string{"TEXT", "IMAGE"},
				"imageConfig": map[string]any{
					"aspectRatio": "1:1",
				},
			},
		}
		bytes, _ := json.Marshal(payload)
		return bytes
	}

	textPrompt := strings.TrimSpace(prompt)
	if textPrompt == "" {
		textPrompt = "hi"
	}

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": textPrompt},
				},
			},
		},
		"systemInstruction": map[string]any{
			"parts": []map[string]any{
				{"text": "You are a helpful AI assistant."},
			},
		},
	}
	bytes, _ := json.Marshal(payload)
	return bytes
}

// GeminiAccountTest 组合原生账号凭据与平台测试，不持有业务服务或新客户端池。
type GeminiAccountTest struct {
	Tokens      *accountcore.GeminiTokenSource
	Transport   QoderTransport
	Profiles    *egressprovider.TLSProfiles
	ValidateURL func(string) (string, error)
	UserAgent   string
}

func (s *GeminiAccountTest) applyUserAgent(req *http.Request) {
	if userAgent := strings.TrimSpace(s.UserAgent); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
}

func (s *GeminiAccountTest) resolveTLSProfile(value *accountcore.Record) *tlsfingerprint.Profile {
	if s.Profiles == nil {
		return nil
	}
	return s.Profiles.ResolveRequestTLS(egress.TLSSelection{Enabled: value.IsTLSFingerprintEnabled(), DirectProfileID: value.GetTLSFingerprintProfileID()})
}

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Execute 通过 xAI Responses API 测试 Grok OAuth 或 API-key 账号。
func (s *GrokAccountTest) Execute(c *TestRun, value *accountcore.Record, modelID string, testArgs ...string) error {
	ctx := c.Context
	prompt := ""
	testType, explicitTestType := accountcore.AccountTestTypeText, false
	if len(testArgs) > 0 {
		prompt = testArgs[0]
	}
	if len(testArgs) > 1 {
		testType, explicitTestType = accountcore.AccountTestTypeFromArgs(testArgs[1])
	}

	if s.Transport == nil {
		return (TestStreamOutput{}).Error(c, "HTTP upstream not configured")
	}

	billingModel := strings.TrimSpace(modelID)
	if mapped := strings.TrimSpace(mappedTestModel(value, billingModel)); mapped != "" {
		billingModel = mapped
	}
	testModelID := xai.NormalizeModelID(billingModel)

	var authToken string
	switch value.Type {
	case capability.AccountTypeOAuth:
		if s.Tokens == nil {
			return (TestStreamOutput{}).Error(c, "Grok token provider not configured")
		}
		var err error
		// 手动测试不走生产调度资格门：关闭调度、限流/过载/临时冷却中的账号
		// 也应能被管理员探测（#4598），与 Codex/OpenAI 测试行为一致。
		authToken, err = s.Tokens.GetAccessTokenForManualTest(ctx, value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to get Grok access token: %s", err.Error()))
		}
	case capability.AccountTypeAPIKey:
		authToken = strings.TrimSpace(value.GetCredential("api_key"))
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "Grok API key is missing")
		}
	default:
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported Grok account type: %s", value.Type))
	}
	if explicitTestType && testType == accountcore.AccountTestTypeImage {
		imageModel := strings.TrimSpace(modelID)
		if imageModel == "" {
			imageModel = "grok-imagine-image"
		}
		if mapped := strings.TrimSpace(mappedTestModel(value, imageModel)); mapped != "" {
			imageModel = mapped
		}
		imageModel = (xai.MediaCodec{}).NormalizeGrokMediaModelForEndpoint(xai.GrokMediaEndpointImagesGenerations, imageModel, false)
		imagePrompt := strings.TrimSpace(prompt)
		if imagePrompt == "" {
			imagePrompt = "Generate a cute orange cat astronaut sticker on a clean pastel background."
		}
		return s.executeImage(c, ctx, value, authToken, imageModel, imagePrompt)
	}

	apiURL, err := s.responsesURL(value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid Grok base URL: %s", err.Error()))
	}

	c.Begin(true)

	payloadBytes, err := xai.AccountTestBody(testModelID, prompt)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create Grok test payload")
	}

	if !s.SuppressStart {
		(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create Grok request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if value.IsGrokOAuth() && (xai.MediaCodec{}).IsGrokCLIProxyTarget(apiURL) {
		xai.ApplyCLIHeaders(req.Header)
	}
	// 连通性测试与真实转发保持同一套账号级请求头覆写。
	applyGrokQuotaHeaders(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.Do(req, proxyURL, value.ID, value.Concurrency)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Grok Responses API request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	now := time.Now()
	snapshot := xai.ParseQuotaObservation(resp.Header, resp.StatusCode, now)
	accountcore.StampGrokQuotaPlan(value, snapshot, testModelID, xai.ResolveGrokTextResponsesModelID, xai.ApplyGrok45ResponsesPlanSignal)
	if snapshot != nil && s.Store != nil {
		resetAt, limited := accountcore.GrokRateLimitResetAtForAccount(value, snapshot, now)
		if limited {
			accountcore.NormalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
		}
		_ = s.Store.UpdateExtra(ctx, value.ID, map[string]any{
			"grok_usage_snapshot": snapshot,
		})
		if limited {
			accountcore.PersistGrokRateLimit(ctx, s.Store, value, resetAt, slog.Warn)
		} else if accountcore.IsSuccessfulGrokRateLimitRecovery(value, snapshot) {
			accountcore.ClearGrokRateLimitAfterRecovery(ctx, s.Store, value, slog.Warn)
		}
	} else if s.Store != nil && accountcore.IsSuccessfulGrokRateLimitRecovery(value, &xai.QuotaSnapshot{StatusCode: resp.StatusCode}) {
		accountcore.ClearGrokRateLimitAfterRecovery(ctx, s.Store, value, slog.Warn)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if s.Store != nil && !xai.IsGrokContentPolicyRejection(resp.StatusCode, body) {
			decision := xai.ClassifyGrokUpstreamFailure(resp.StatusCode, body, testModelID)
			switch {
			case decision.Class == xai.GrokFailureFreeUsage:
				if resetAt, limited := accountcore.GrokRateLimitResetAtForAccount(value, snapshot, now); limited && resetAt.After(now) {
					accountcore.PersistGrokRateLimit(ctx, s.Store, value, resetAt, slog.Warn)
				} else {
					stateCtx, cancel := grokTestStateContext(ctx)
					_ = s.Store.SetTempUnschedulable(stateCtx, value.ID, now.Add(xai.GrokFreeUsageProbeCooldown), "grok free usage exhausted")
					cancel()
				}
			case decision.Class == xai.GrokFailureBilling && (xai.IsSpendingLimitError(body) || strings.Contains(strings.ToLower(decision.Reason), "credit")):
				accountcore.PersistGrokRateLimit(ctx, s.Store, value, accountcore.GrokSpendingLimitResetAt(value, now), slog.Warn)
			case resp.StatusCode == http.StatusPaymentRequired:
				// 未能从正文识别可恢复消费限额时，保留既有 30 分钟计费冷却。
				stateCtx, cancel := grokTestStateContext(ctx)
				_ = s.Store.SetTempUnschedulable(stateCtx, value.ID, now.Add(30*time.Minute), "grok payment required")
				cancel()
			}
		}
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Grok Responses API returned %d: %s", resp.StatusCode, string(body)))
	}

	return (TestStreamOutput{}).Responses(c, resp.Body)
}

// executeImage 使用账号凭据直接调用 xAI 图片端点并回传预览事件。
func (s *GrokAccountTest) executeImage(c *TestRun, ctx context.Context, value *accountcore.Record, authToken, modelID, prompt string) error {
	apiURL, err := s.imageURL(value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid Grok image base URL: %s", err.Error()))
	}

	c.Begin(true)
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: modelID})

	payloadBytes, err := json.Marshal(map[string]any{
		"model":           modelID,
		"prompt":          prompt,
		"n":               1,
		"response_format": "b64_json",
	})
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create Grok image test payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create Grok image request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if value.IsGrokOAuth() && (xai.MediaCodec{}).IsGrokCLIProxyTarget(apiURL) {
		xai.ApplyCLIHeaders(req.Header)
	}
	applyGrokQuotaHeaders(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	resp, err := s.Transport.Do(req, proxyURL, value.ID, value.Concurrency)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Grok image request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to read Grok image response: %s", err.Error()))
	}
	if resp.StatusCode != http.StatusOK {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Grok image API returned %d: %s", resp.StatusCode, string(body)))
	}

	var result struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
			MimeType      string `json:"mime_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to parse Grok image response: %s", err.Error()))
	}
	if len(result.Data) == 0 {
		return (TestStreamOutput{}).Error(c, "No images returned from Grok API")
	}
	for _, item := range result.Data {
		if item.RevisedPrompt != "" {
			(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		mimeType := strings.TrimSpace(item.MimeType)
		if mimeType == "" {
			mimeType = "image/png"
		}
		switch {
		case strings.TrimSpace(item.B64JSON) != "":
			(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "image", ImageURL: "data:" + mimeType + ";base64," + item.B64JSON, MimeType: mimeType})
		case strings.TrimSpace(item.URL) != "":
			(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "image", ImageURL: item.URL, MimeType: mimeType})
		}
	}
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

// GrokAccountTest 使用原账号 token source 和传输，健康写入交给原生规则。
type GrokAccountTest struct {
	Tokens    *accountcore.GrokTokenSource
	Transport interface {
		Do(*http.Request, string, int64, int) (*http.Response, error)
	}
	Store interface {
		accountcore.GrokRateLimitWriter
		UpdateExtra(context.Context, int64, map[string]any) error
		SetTempUnschedulable(context.Context, int64, time.Time, string) error
	}
	OperatorValidator xai.BaseURLValidator
	DefaultBaseURL    func(context.Context) string
	SuppressStart     bool
}

func (s *GrokAccountTest) responsesURL(value *accountcore.Record) (string, error) {
	validator, err := GrokBaseURLValidator(value, s.OperatorValidator)
	if err != nil {
		return "", err
	}
	// 设置沿用原 Background 读取时机，只有文本端点使用动态默认值。
	transport := GrokQuotaTransport{DefaultBaseURL: s.DefaultBaseURL}
	return xai.BuildResponsesURLWithValidator(transport.baseURL(context.Background(), value, true), validator)
}

func (s *GrokAccountTest) imageURL(value *accountcore.Record) (string, error) {
	validator, err := GrokBaseURLValidator(value, s.OperatorValidator)
	if err != nil {
		return "", err
	}
	base := (&GrokQuotaTransport{}).baseURL(context.Background(), value, false)
	if value.IsGrokOAuth() && (xai.MediaCodec{}).IsGrokCLIProxyTarget(base) {
		base = xai.DefaultBaseURL
	}
	return xai.BuildMediaEndpointURL(base, xai.GrokMediaEndpointImagesGenerations, "", validator)
}

func grokTestStateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, 5*time.Second)
}

package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/deepseek"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/zhipu"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"
	"github.com/google/uuid"
)

// sseDataPrefix matches SSE data lines with optional whitespace after colon.
// Some upstream APIs return non-standard "data:" without space (should be "data: ").
var sseDataPrefix = regexp.MustCompile(`^data:\s*`)

const (
	testClaudeAPIURL      = "https://api.anthropic.com/v1/messages?beta=true"
	chatgptCodexAPIURL    = "https://chatgpt.com/backend-api/codex/responses"
	defaultQoderTestModel = "auto"
)

type accountTestContextKey string

const accountTestBackgroundOptionsContextKey accountTestContextKey = "account_test_background_options"

const openAIAutomaticProbeDecisionKey = "openai_automatic_probe_decision"

type accountTestBackgroundOptions struct {
	userAgent string
}

type openAIAutomaticProbeDecision struct {
	tlsRouterMatch TLSFingerprintRouterMatchResult
}

// withAccountTestUserAgent 标记无人值守测试，并保留显式配置的探针 User-Agent。
func withAccountTestUserAgent(ctx context.Context, userAgent string) context.Context {
	return context.WithValue(ctx, accountTestBackgroundOptionsContextKey, accountTestBackgroundOptions{
		userAgent: strings.TrimSpace(userAgent),
	})
}

func accountTestBackgroundOptionsFromContext(ctx context.Context) (accountTestBackgroundOptions, bool) {
	if ctx == nil {
		return accountTestBackgroundOptions{}, false
	}
	options, ok := ctx.Value(accountTestBackgroundOptionsContextKey).(accountTestBackgroundOptions)
	return options, ok
}

func accountTestUserAgentFromContext(ctx context.Context) string {
	options, _ := accountTestBackgroundOptionsFromContext(ctx)
	return options.userAgent
}

func applyAccountTestUserAgent(req *http.Request) {
	if req == nil {
		return
	}
	if userAgent := accountTestUserAgentFromContext(req.Context()); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
}

type qoderAccountTestSessionProvider interface {
	GetSession(ctx context.Context, account *Account) (*qoder.SessionContext, error)
}

type qoderAccountTestSessionInvalidator interface {
	Invalidate(accountID int64)
}

type qoderAccountTestOAuthClient interface {
	GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error)
}

type TestEvent = accountcore.TestEvent

const (
	defaultGeminiTextTestPrompt  = "hi"
	defaultGeminiImageTestPrompt = "Generate a cute orange cat astronaut sticker on a clean pastel background."
	defaultOpenAIImageTestPrompt = "Generate a cute orange cat astronaut sticker on a clean pastel background."
	defaultGrokImageTestPrompt   = "Generate a cute orange cat astronaut sticker on a clean pastel background."

	// AccountTestTypeText 和 AccountTestTypeImage 是管理端账号测试的显式类型。
	AccountTestTypeText  = accountcore.AccountTestTypeText
	AccountTestTypeImage = accountcore.AccountTestTypeImage
)

func accountTestTypeFromArgs(testTypes ...string) (string, bool) {
	return accountcore.AccountTestTypeFromArgs(testTypes...)
}

func resolveAccountTestModeAndType(mode string, testTypes ...string) (string, string, bool) {
	return accountcore.ResolveAccountTestModeAndType(mode, testTypes...)
}

// isOpenAIImageModel checks if the model is an OpenAI image generation model (e.g. gpt-image-2).
func isOpenAIImageModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "gpt-image-")
}

// AccountTestService handles account testing operations
type AccountTestService struct {
	tester                    *accountcore.TestService
	accountRepo               AccountRepository
	geminiTokenProvider       *GeminiTokenProvider
	claudeTokenProvider       *ClaudeTokenProvider
	grokTokenProvider         *GrokTokenProvider
	antigravityGatewayService *AntigravityGatewayService
	openAIGatewayService      *OpenAIGatewayService
	httpUpstream              HTTPUpstream
	cfg                       *config.Config
	settingService            *SettingService
	tlsFPProfileService       *TLSFingerprintProfileService
	qoderSessionProvider      qoderAccountTestSessionProvider
	qoderClient               qoderStreamClient
	qoderOAuthClient          qoderAccountTestOAuthClient
	agentIdentityTaskMu       sync.Mutex
	agentIdentityWS           agentIdentityWSConnectionInvalidator
}

// SetSettingService 注入 Grok 系统级默认上游策略。
func (s *AccountTestService) SetSettingService(settingService *SettingService) {
	if s != nil {
		s.settingService = settingService
	}
}

// NewAccountTestService creates a new AccountTestService
func NewAccountTestService(
	accountRepo AccountRepository,
	geminiTokenProvider *GeminiTokenProvider,
	claudeTokenProvider *ClaudeTokenProvider,
	grokTokenProvider *GrokTokenProvider,
	antigravityGatewayService *AntigravityGatewayService,
	openAIGatewayService *OpenAIGatewayService,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
	tlsFPProfileService *TLSFingerprintProfileService,
) *AccountTestService {
	qoderSessionProvider := NewQoderTokenProvider()
	qoderSessionProvider.SetHTTPUpstream(httpUpstream, tlsFPProfileService)
	return &AccountTestService{
		accountRepo:               accountRepo,
		geminiTokenProvider:       geminiTokenProvider,
		claudeTokenProvider:       claudeTokenProvider,
		grokTokenProvider:         grokTokenProvider,
		antigravityGatewayService: antigravityGatewayService,
		openAIGatewayService:      openAIGatewayService,
		httpUpstream:              httpUpstream,
		cfg:                       cfg,
		tlsFPProfileService:       tlsFPProfileService,
		qoderSessionProvider:      qoderSessionProvider,
		qoderClient:               qoder.NewClient(qoder.APIBaseURL),
		agentIdentityWS:           openAIGatewayService,
	}
}

func (s *AccountTestService) resolveTLSProfile(account *Account) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	// ResolveTLSProfile 会校验账号能力，OpenAI API Key 即使手写 extra 也不会启用。
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

// prepareOpenAIAutomaticProbeRun 在读取上游令牌前完成探针身份、TLS 路由和客户端策略决策。
func (s *AccountTestService) prepareOpenAIAutomaticProbeRun(c *accountTestRun, account *Account) error {
	options, automatic := accountTestBackgroundOptionsFromContext(c.ctx)
	if !automatic {
		return nil
	}
	if s.openAIGatewayService == nil {
		return errors.New("OpenAI automatic probe routing is not configured")
	}

	userAgent := options.userAgent
	if userAgent == "" && account.IsOpenAIOAuth() {
		credentialAccount := account
		if account.IsCredentialShadow() {
			resolved, err := resolveCredentialAccount(c.ctx, s.accountRepo, account)
			if err != nil {
				return err
			}
			credentialAccount = resolved
		}
		userAgent = strings.TrimSpace(credentialAccount.GetOpenAIUserAgent())
		if userAgent == "" {
			userAgent = CodexCanonicalUserAgent()
		}
	}

	if c.headers == nil {
		c.headers = make(http.Header)
	}
	c.headers.Set("User-Agent", userAgent)
	c.headers.Del("originator")
	if originator, _, ok := openai.PairCodexClientIdentity(userAgent); ok {
		c.headers.Set("originator", originator)
	}

	routerMatch := s.openAIGatewayService.matchTLSFingerprintRouterForClient(func() string { return c.GetHeader("User-Agent") }, account)
	policyResult := s.openAIGatewayService.detectCodexClientRestrictionForClient(c.ctx, func() (string, string) { return c.GetHeader("User-Agent"), c.GetHeader("originator") }, account, routerMatch)
	if policyResult.Enabled && !policyResult.Matched {
		return fmt.Errorf("OpenAI automatic probe rejected: policy=%s reason=%s", policyResult.Policy, policyResult.Reason)
	}
	c.Set(openAIAutomaticProbeDecisionKey, openAIAutomaticProbeDecision{tlsRouterMatch: routerMatch})
	return nil
}

func openAIAutomaticProbeDecisionFromRun(c *accountTestRun) (openAIAutomaticProbeDecision, bool) {
	value, ok := c.Get(openAIAutomaticProbeDecisionKey)
	if !ok {
		return openAIAutomaticProbeDecision{}, false
	}
	decision, ok := value.(openAIAutomaticProbeDecision)
	return decision, ok
}

// applyOpenAIAccountTestRoutingRun 让自动探针复用正常网关的上游身份优先级，手动测试保持原样。
func (s *AccountTestService) applyOpenAIAccountTestRoutingRun(c *accountTestRun, account *Account, req *http.Request, isOAuth bool) {
	decision, automatic := openAIAutomaticProbeDecisionFromRun(c)
	if !automatic {
		applyAccountTestUserAgent(req)
		return
	}

	if userAgent := strings.TrimSpace(c.GetHeader("User-Agent")); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if isOAuth {
		isOfficialClient := openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
		req.Header.Set("originator", resolveOpenAIUpstreamOriginatorForClient(func() string { return c.GetHeader("originator") }, isOfficialClient, decision.tlsRouterMatch))
	}
	s.openAIGatewayService.applyOpenAIUpstreamUserAgent(req.Context(), nil, account, req, false, decision.tlsRouterMatch)
}

func (s *AccountTestService) resolveOpenAIAccountTestTLSProfileRun(c *accountTestRun, account *Account) *tlsfingerprint.Profile {
	if decision, automatic := openAIAutomaticProbeDecisionFromRun(c); automatic {
		return s.openAIGatewayService.resolveOpenAITLSProfile(account, decision.tlsRouterMatch)
	}
	return s.resolveTLSProfile(account)
}

func (s *AccountTestService) validateUpstreamBaseURL(raw string) (string, error) {
	if s.cfg == nil {
		return "", errors.New("config is not available")
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		return "", err
	}
	return normalized, nil
}

// generateSessionString generates a Claude Code style session string.
// The output format is determined by the UA version in claude.DefaultHeaders,
// ensuring consistency between the user_id format and the UA sent to upstream.
func generateSessionString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	hex64 := hex.EncodeToString(b)
	sessionUUID := uuid.New().String()
	uaVersion := ExtractCLIVersion(claude.DefaultHeaders["User-Agent"])
	return FormatMetadataUserID(hex64, "", sessionUUID, uaVersion), nil
}

// createTestPayloadWithPrompt 创建 Claude Code 风格测试请求，并允许主动探测传入轻量提示词。
func createTestPayloadWithPrompt(modelID string, prompt string) (map[string]any, error) {
	sessionID, err := generateSessionString()
	if err != nil {
		return nil, err
	}
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}

	return map[string]any{
		"model": modelID,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": testPrompt,
						"cache_control": map[string]string{
							"type": "ephemeral",
						},
					},
				},
			},
		},
		"system": []map[string]any{
			{
				"type": "text",
				"text": claudeCodeSystemPrompt,
				"cache_control": map[string]string{
					"type": "ephemeral",
				},
			},
		},
		"metadata": map[string]string{
			"user_id": sessionID,
		},
		"max_tokens":  1024,
		"temperature": 1,
		"stream":      true,
	}, nil
}

func defaultCNProviderTestModel(platform string) string {
	switch platform {
	case PlatformKimi:
		return kimi.DefaultTestModel
	case PlatformZhipu:
		return zhipu.DefaultTestModel
	case PlatformDeepseek:
		return deepseek.DefaultTestModel
	default:
		return ""
	}
}

// testCNProviderAccountConnectionRun 按账号真实上游协议选择测试端点，避免把 Chat 或
// Responses 账号错误地当作 Anthropic API Key 测试。
func (s *AccountTestService) testCNProviderAccountConnectionRun(
	c *accountTestRun,
	account *Account,
	modelID string,
	prompt string,
) error {
	if account == nil || account.Type != AccountTypeAPIKey {
		return s.sendTestErrorAndEnd(c, "CN provider tests require an API Key account")
	}
	apiKey := strings.TrimSpace(account.GetCNAPIKey())
	if apiKey == "" {
		return s.sendTestErrorAndEnd(c, "No API key available")
	}
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = defaultCNProviderTestModel(account.Platform)
	}
	testModelID = account.GetMappedModel(testModelID)
	if testModelID == "" {
		return s.sendTestErrorAndEnd(c, "No test model available")
	}

	ctx := c.ctx
	protocol := account.GetAPIProtocol()
	var (
		apiURL  string
		payload any
	)
	switch protocol {
	case APIProtocolAnthropic:
		baseURL, err := s.validateUpstreamBaseURL(account.GetAnthropicProtocolBaseURL())
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		if hint := cnAnthropicBaseURLMisconfigHint(baseURL); hint != "" {
			return s.sendTestErrorAndEnd(c, hint)
		}
		apiURL = strings.TrimRight(baseURL, "/") + "/v1/messages"
		payload, err = createTestPayloadWithPrompt(testModelID, prompt)
		if err != nil {
			return s.sendTestErrorAndEnd(c, "Failed to create test payload")
		}
	case APIProtocolResponses:
		baseURL, err := s.validateUpstreamBaseURL(account.GetOpenAIBaseURL())
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = buildOpenAIResponsesURLForPlatform(account.Platform, baseURL)
		responsesPayload := createOpenAITestPayload(testModelID, prompt, false)
		responsesPayload["store"] = false
		payload = responsesPayload
	default:
		baseURL, err := s.validateUpstreamBaseURL(account.GetOpenAIBaseURL())
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = buildOpenAIChatCompletionsURL(baseURL)
		payload = createOpenAIChatCompletionsTestPayload(testModelID, prompt)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create test payload")
	}

	c.begin(true)
	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if protocol == APIProtocolAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		setAnthropicAPIKeyAuthHeader(req.Header, account, apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(
		req,
		proxyURL,
		account.ID,
		account.Concurrency,
		s.resolveTLSProfile(account),
	)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))
		if (protocol == APIProtocolAnthropic && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden)) && s.accountRepo != nil {
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendTestErrorAndEnd(c, errMsg)
	}
	switch protocol {
	case APIProtocolAnthropic:
		return s.processClaudeStreamRun(c, resp.Body)
	case APIProtocolResponses:
		return s.processOpenAIStreamRun(c, resp.Body)
	default:
		return s.processOpenAIChatCompletionsStreamRun(c, resp.Body)
	}
}

// testCNProviderChatCompletionsConnectionRun 保留上游自适应测试使用的 Chat 探测入口，
// 具体请求仍复用 fork 已有的国产供应商测试实现（含请求头覆写、代理和 TLS 指纹）。
func (s *AccountTestService) testCNProviderChatCompletionsConnectionRun(
	c *accountTestRun,
	account *Account,
	modelID string,
	prompt string,
) error {
	return s.testCNProviderAccountConnectionRun(c, account, modelID, prompt)
}

// 测试协议仅在当前请求生效，不改变账号持久化配置。
type accountTestProtocolContextKey struct{}

// testClaudeAccountConnectionRun tests an Anthropic Claude account's connection
func (s *AccountTestService) testClaudeAccountConnectionRun(c *accountTestRun, account *Account, modelID string, prompt string) error {
	ctx := c.ctx

	// Determine the model to use
	testModelID := modelID
	if testModelID == "" {
		testModelID = claude.DefaultTestModel
	}

	// API Key 账号测试连接时也需要应用通配符模型映射。
	if account.Type == "apikey" {
		testModelID = account.GetMappedModel(testModelID)
	}

	// Bedrock accounts use a separate test path
	if account.IsBedrock() {
		return s.testBedrockAccountConnectionRun(c, ctx, account, testModelID, prompt)
	}
	if account.Type == AccountTypeServiceAccount {
		return s.testClaudeVertexServiceAccountConnectionRun(c, ctx, account, testModelID, prompt)
	}

	// Determine authentication method and API URL
	var authToken string
	var apiURL string

	if account.IsOAuth() {
		apiURL = testClaudeAPIURL
		authToken = account.GetCredential("access_token")
		if authToken == "" {
			return s.sendTestErrorAndEnd(c, "No access token available")
		}
	} else if account.Type == "apikey" {
		authToken = account.GetCredential("api_key")
		if authToken == "" {
			return s.sendTestErrorAndEnd(c, "No API key available")
		}

		baseURL := account.GetBaseURL()
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = strings.TrimSuffix(normalizedBaseURL, "/") + "/v1/messages?beta=true"
	} else {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}

	// Set SSE headers
	c.begin(true)

	// Create Claude Code style payload (same for all account types)
	payload, err := createTestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create test payload")
	}
	payloadBytes, _ := json.Marshal(payload)

	// Send test_start event
	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}

	// Set common headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	// Apply Claude Code client headers
	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}

	// Set authentication header
	if account.IsOAuth() {
		req.Header.Set("anthropic-beta", claude.DefaultBetaHeader)
		req.Header.Set("Authorization", "Bearer "+authToken)
	} else {
		req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
		setAnthropicAPIKeyAuthHeader(req.Header, account, authToken)
	}
	applyAccountTestUserAgent(req)

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	account.ApplyHeaderOverrides(req.Header)

	// Get proxy URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveTLSProfile(account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))

		// 403 表示账号被上游封禁，标记为 error 状态
		if resp.StatusCode == http.StatusForbidden {
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}

		return s.sendTestErrorAndEnd(c, errMsg)
	}

	// Process SSE stream
	return s.processClaudeStreamRun(c, resp.Body)
}

func (s *AccountTestService) testClaudeVertexServiceAccountConnectionRun(c *accountTestRun, ctx context.Context, account *Account, testModelID string, prompt string) error {
	if mappedModel, matched := account.ResolveMappedModel(testModelID); matched {
		testModelID = mappedModel
	} else {
		testModelID = normalizeVertexAnthropicModelID(claude.NormalizeModelID(testModelID))
	}

	c.begin(true)

	payload, err := createTestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create test payload")
	}
	payloadBytes, _ := json.Marshal(payload)
	vertexBody, err := buildVertexAnthropicRequestBody(payloadBytes)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to create Vertex request body: %s", err.Error()))
	}

	if s.claudeTokenProvider == nil {
		return s.sendTestErrorAndEnd(c, "Claude token provider not configured")
	}
	accessToken, err := s.claudeTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to get service account access token: %s", err.Error()))
	}

	fullURL, err := buildVertexAnthropicURL(account.VertexProjectID(), account.VertexLocation(testModelID), testModelID, true)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to build Vertex URL: %s", err.Error()))
	}

	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(vertexBody))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	applyAccountTestUserAgent(req)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveTLSProfile(account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusForbidden {
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendTestErrorAndEnd(c, errMsg)
	}

	return s.processClaudeStreamRun(c, resp.Body)
}

// testBedrockAccountConnectionRun tests a Bedrock (SigV4 or API Key) account using non-streaming invoke
func (s *AccountTestService) testBedrockAccountConnectionRun(c *accountTestRun, ctx context.Context, account *Account, testModelID string, prompt string) error {
	route, err := resolveBedrockModelRoute(account, testModelID)
	if err != nil {
		return s.sendTestErrorAndEnd(c, bedrockRoutingDiagnostic(err))
	}
	region := route.SourceRegion
	testModelID = route.ModelID

	// Set SSE headers (test UI expects SSE)
	c.begin(true)

	// Create a minimal Bedrock-compatible payload (no stream, no cache_control)
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

	// Use non-streaming endpoint (response is standard Claude JSON)
	apiURL := BuildBedrockURL(region, testModelID, false)

	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bedrockBody))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")

	// Sign or set auth based on account type
	if account.IsBedrockAPIKey() {
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return s.sendTestErrorAndEnd(c, "No API key available")
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else {
		signer, err := NewBedrockSignerFromAccount(account)
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to create Bedrock signer: %s", err.Error()))
		}
		if err := signer.SignRequest(ctx, req, bedrockBody); err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to sign request: %s", err.Error()))
		}
	}
	applyAccountTestUserAgent(req)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, nil)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// Bedrock non-streaming response is standard Claude JSON, extract the text
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to parse response: %s", err.Error()))
	}

	text := ""
	if len(result.Content) > 0 {
		text = result.Content[0].Text
	}
	if text == "" {
		text = "(empty response)"
	}

	s.sendTestEvent(c, TestEvent{Type: "content", Text: text})
	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// testOpenAIAccountConnectionRun tests an OpenAI account's connection
func (s *AccountTestService) testOpenAIAccountConnectionRun(c *accountTestRun, account *Account, modelID string, prompt string, mode string, testTypes ...string) error {
	ctx := c.ctx
	mode, testType, explicitTestType := resolveAccountTestModeAndType(mode, testTypes...)

	// Default to openai.DefaultTestModel for OpenAI testing
	testModelID := modelID
	if testModelID == "" {
		testModelID = openai.DefaultTestModel
	}

	// 原生 V2 与普通 Responses 一样只使用常规模型映射；旧端点兼容性测试才
	// 在其基础上追加 compact_model_mapping。
	testModelID = account.GetMappedModel(testModelID)
	if mode == AccountTestModeCompact {
		return s.testOpenAINativeCompactionV2ConnectionRun(c, account, testModelID)
	}
	if mode == AccountTestModeLegacyCompact {
		testModelID = resolveOpenAICompactForwardModel(account, testModelID)
		return s.testOpenAILegacyCompactConnectionRun(c, account, testModelID)
	}

	// 显式类型优先于模型名；未指定类型时才保留旧版图片模型兼容判断。
	if (explicitTestType && testType == AccountTestTypeImage) ||
		(!explicitTestType && isOpenAIImageModel(testModelID)) {
		imagePrompt := strings.TrimSpace(prompt)
		if imagePrompt == "" {
			imagePrompt = defaultOpenAIImageTestPrompt
		}
		if account.Type == "apikey" {
			return s.testOpenAIImageAPIKeyRun(c, ctx, account, testModelID, imagePrompt)
		}
		return s.testOpenAIImageOAuthRun(c, ctx, account, testModelID, imagePrompt)
	}

	credentialAccount := account
	if account.IsCredentialShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return s.sendTestErrorAndEnd(c, err.Error())
		}
		credentialAccount = resolved
	}

	// Determine authentication method and API URL
	var authToken string
	var apiURL string
	var isOAuth bool

	if credentialAccount.IsOAuth() {
		isOAuth = true
		// Agent Identity 对每次请求单独签名，不保存 OAuth token。
		if !credentialAccount.IsOpenAIAgentIdentity() {
			authToken = credentialAccount.GetOpenAIAccessToken()
		}
		if authToken == "" && !credentialAccount.IsOpenAIAgentIdentity() {
			return s.sendTestErrorAndEnd(c, "No access token available")
		}

		// OAuth uses ChatGPT internal API
		apiURL = chatgptCodexAPIURL
	} else if credentialAccount.Type == "apikey" {
		// API Key - use Platform API
		// 国产 OpenAI 兼容供应商通过协议族密钥读取器复用此探针。
		authToken = credentialAccount.GetOpenAIProtocolAPIKey()
		if authToken == "" {
			return s.sendTestErrorAndEnd(c, "No API key available")
		}

		baseURL := credentialAccount.GetOpenAIBaseURL()
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		protocol := openai_compat.ResolveUpstreamTextProtocol(account.Extra, openai_compat.TextProtocolResponses)
		if requested, ok := ctx.Value(accountTestProtocolContextKey{}).(openai_compat.TextProtocol); ok && account.IsOpenAI() {
			protocol = requested
		}
		if protocol == openai_compat.TextProtocolChatCompletions {
			return s.testOpenAIChatCompletionsConnectionRun(c, account, testModelID, prompt, normalizedBaseURL, authToken)
		}
		apiURL = buildOpenAIResponsesURL(normalizedBaseURL)
	} else {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}

	// Set SSE headers
	c.begin(true)

	// OAuth 账号使用 ChatGPT Codex 上游，测试请求必须与真实转发使用同一模型归一化规则。
	upstreamTestModelID := testModelID
	if isOAuth {
		upstreamTestModelID = normalizeOpenAIModelForUpstream(credentialAccount, testModelID)
	}
	payload := createOpenAITestPayload(upstreamTestModelID, prompt, isOAuth)
	payloadBytes, _ := json.Marshal(payload)

	// task 失效时会注册新 task 并重试探针，因此开始事件只发送一次。
	if !agentIdentityTaskRecoveryWasTried(ctx) {
		s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	// Set common headers
	req.Header.Set("Content-Type", "application/json")
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount)
		if authErr != nil {
			return s.sendTestErrorAndEnd(c, "Failed to build Agent Identity authentication")
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// Set OAuth-specific headers for ChatGPT internal API
	if isOAuth {
		req.Host = "chatgpt.com"
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("Originator", resolveCodexOutboundIdentity("").originator)
		if customUA := strings.TrimSpace(credentialAccount.GetOpenAIUserAgent()); customUA != "" {
			req.Header.Set("User-Agent", customUA)
		} else {
			req.Header.Set("User-Agent", CodexCanonicalUserAgent())
		}
		setOpenAIChatGPTAccountHeaders(req.Header, credentialAccount)
	}
	s.applyOpenAIAccountTestRoutingRun(c, account, req, isOAuth)
	if account.Type == AccountTypeOAuth {
		// 必须在测试专用 UA 覆写之后配对身份，否则测试请求仍可能因头部错配返回 404。
		enforceCodexIdentityHeaders(req.Header)
	}

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	credentialAccount.ApplyHeaderOverrides(req.Header)

	// Get proxy URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAIAccountTestTLSProfileRun(c, account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if isOAuth && s.accountRepo != nil {
		if updates, err := extractOpenAICodexProbeUpdates(resp); err == nil && len(updates) > 0 {
			_ = s.accountRepo.UpdateExtra(ctx, account.ID, updates)
			mergeAccountExtra(account, updates)
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		body = redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, credentialAccount, body)
		if !agentIdentityTaskRecoveryWasTried(ctx) && credentialAccount.IsOpenAIAgentIdentity() && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, body) {
			expectedTaskID := credentialAccount.GetCredential("task_id")
			if err := ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount, expectedTaskID); err != nil {
				return s.sendTestErrorAndEnd(c, fmt.Sprintf("Agent Identity task recovery failed: %s", err.Error()))
			}
			c.ctx = markAgentIdentityTaskRecoveryTried(ctx)
			return s.testOpenAIAccountConnectionRun(c, account, modelID, prompt, mode, testType)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, account, resp.Header, body)
		}
		// 401 Unauthorized: 标记账号为永久错误
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			errMsg := fmt.Sprintf("Authentication failed (401): %s", string(body))
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// Process SSE stream
	return s.processOpenAIStreamRun(c, resp.Body)
}

// testGrokAccountConnectionRun 通过 xAI Responses API 测试 Grok OAuth 或 API-key 账号。
func (s *AccountTestService) testGrokAccountConnectionRun(c *accountTestRun, account *Account, modelID string, testArgs ...string) error {
	ctx := c.ctx
	prompt := ""
	testType, explicitTestType := AccountTestTypeText, false
	if len(testArgs) > 0 {
		prompt = testArgs[0]
	}
	if len(testArgs) > 1 {
		testType, explicitTestType = accountTestTypeFromArgs(testArgs[1])
	}

	if s.httpUpstream == nil {
		return s.sendTestErrorAndEnd(c, "HTTP upstream not configured")
	}

	billingModel := strings.TrimSpace(modelID)
	if mapped := strings.TrimSpace(account.GetMappedModel(billingModel)); mapped != "" {
		billingModel = mapped
	}
	testModelID := normalizeOpenAIModelForUpstream(account, billingModel)

	var authToken string
	switch account.Type {
	case AccountTypeOAuth:
		if s.grokTokenProvider == nil {
			return s.sendTestErrorAndEnd(c, "Grok token provider not configured")
		}
		var err error
		// 手动测试不走生产调度资格门：关闭调度、限流/过载/临时冷却中的账号
		// 也应能被管理员探测（#4598），与 Codex/OpenAI 测试行为一致。
		authToken, err = s.grokTokenProvider.GetAccessTokenForManualTest(ctx, account)
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to get Grok access token: %s", err.Error()))
		}
	case AccountTypeAPIKey:
		authToken = strings.TrimSpace(account.GetCredential("api_key"))
		if authToken == "" {
			return s.sendTestErrorAndEnd(c, "Grok API key is missing")
		}
	default:
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported Grok account type: %s", account.Type))
	}
	if explicitTestType && testType == AccountTestTypeImage {
		imageModel := strings.TrimSpace(modelID)
		if imageModel == "" {
			imageModel = "grok-imagine-image"
		}
		if mapped := strings.TrimSpace(account.GetMappedModel(imageModel)); mapped != "" {
			imageModel = mapped
		}
		imageModel = NormalizeGrokMediaModelForEndpoint(GrokMediaEndpointImagesGenerations, imageModel, false)
		imagePrompt := strings.TrimSpace(prompt)
		if imagePrompt == "" {
			imagePrompt = defaultGrokImageTestPrompt
		}
		return s.testGrokImageGenerationRun(c, ctx, account, authToken, imageModel, imagePrompt)
	}

	apiURL, err := buildGrokResponsesURL(account, s.cfg, s.settingService)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid Grok base URL: %s", err.Error()))
	}

	c.begin(true)

	payloadBytes, err := buildGrokAccountTestBody(testModelID, prompt)
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create Grok test payload")
	}

	if !agentIdentityTaskRecoveryWasTried(ctx) {
		s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create Grok request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(apiURL) {
		applyGrokCLIHeaders(req.Header)
	}
	// 连通性测试与真实转发保持同一套账号级请求头覆写。
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Grok Responses API request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	now := time.Now()
	snapshot := parseGrokQuotaSnapshot(resp.Header, resp.StatusCode, now)
	stampGrokQuotaSnapshotForPlan(account, snapshot, testModelID)
	if snapshot != nil && s.accountRepo != nil {
		resetAt, limited := grokRateLimitResetAtForAccount(account, snapshot, now)
		if limited {
			normalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
		}
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
			grokQuotaSnapshotExtraKey: snapshot,
		})
		if limited {
			persistGrokRateLimit(ctx, s.accountRepo, account, resetAt)
		} else if isSuccessfulGrokRateLimitRecovery(account, snapshot) {
			clearGrokRateLimitAfterRecovery(ctx, s.accountRepo, account)
		}
	} else if s.accountRepo != nil && isSuccessfulGrokRateLimitRecovery(account, &xai.QuotaSnapshot{StatusCode: resp.StatusCode}) {
		clearGrokRateLimitAfterRecovery(ctx, s.accountRepo, account)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if s.accountRepo != nil && !isGrokContentPolicyRejection(resp.StatusCode, body) {
			decision := classifyGrokUpstreamFailure(resp.StatusCode, body, testModelID)
			switch {
			case decision.Class == GrokFailureFreeUsage:
				if resetAt, limited := grokRateLimitResetAtForAccount(account, snapshot, now); limited && resetAt.After(now) {
					persistGrokRateLimit(ctx, s.accountRepo, account, resetAt)
				} else {
					stateCtx, cancel := openAIAccountStateContext(ctx)
					_ = s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, now.Add(grokFreeUsageProbeCooldown), "grok free usage exhausted")
					cancel()
				}
			case decision.Class == GrokFailureBilling && (isGrokSpendingLimitError(body) || strings.Contains(strings.ToLower(decision.Reason), "credit")):
				persistGrokRateLimit(ctx, s.accountRepo, account, grokSpendingLimitResetAt(account, now))
			case resp.StatusCode == http.StatusPaymentRequired:
				// 未能从正文识别可恢复消费限额时，保留既有 30 分钟计费冷却。
				stateCtx, cancel := openAIAccountStateContext(ctx)
				_ = s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, now.Add(30*time.Minute), "grok payment required")
				cancel()
			}
		}
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Grok Responses API returned %d: %s", resp.StatusCode, string(body)))
	}

	return s.processOpenAIStreamRun(c, resp.Body)
}

// testGrokImageGenerationRun 使用账号凭据直接调用 xAI 图片端点并回传预览事件。
func (s *AccountTestService) testGrokImageGenerationRun(c *accountTestRun, ctx context.Context, account *Account, authToken, modelID, prompt string) error {
	apiURL, err := buildGrokMediaURL(account, s.cfg, GrokMediaEndpointImagesGenerations, "")
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid Grok image base URL: %s", err.Error()))
	}

	c.begin(true)
	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: modelID})

	payloadBytes, err := json.Marshal(map[string]any{
		"model":           modelID,
		"prompt":          prompt,
		"n":               1,
		"response_format": "b64_json",
	})
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create Grok image test payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create Grok image request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(apiURL) {
		applyGrokCLIHeaders(req.Header)
	}
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Grok image request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to read Grok image response: %s", err.Error()))
	}
	if resp.StatusCode != http.StatusOK {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Grok image API returned %d: %s", resp.StatusCode, string(body)))
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
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to parse Grok image response: %s", err.Error()))
	}
	if len(result.Data) == 0 {
		return s.sendTestErrorAndEnd(c, "No images returned from Grok API")
	}
	for _, item := range result.Data {
		if item.RevisedPrompt != "" {
			s.sendTestEvent(c, TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		mimeType := strings.TrimSpace(item.MimeType)
		if mimeType == "" {
			mimeType = "image/png"
		}
		switch {
		case strings.TrimSpace(item.B64JSON) != "":
			s.sendTestEvent(c, TestEvent{Type: "image", ImageURL: "data:" + mimeType + ";base64," + item.B64JSON, MimeType: mimeType})
		case strings.TrimSpace(item.URL) != "":
			s.sendTestEvent(c, TestEvent{Type: "image", ImageURL: item.URL, MimeType: mimeType})
		}
	}
	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// buildGrokAccountTestBody 保留 Responses 探测所需字段，同时使用管理端自定义提示词。
func buildGrokAccountTestBody(model, prompt string) ([]byte, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = grokDefaultResponsesModel
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = grokQuotaProbeInput
	}
	return json.Marshal(map[string]any{
		"model":  model,
		"input":  prompt,
		"stream": true,
	})
}

// testOpenAIChatCompletionsConnectionRun 通过原始 /v1/chat/completions 端点测试 OpenAI 兼容 API Key 账号。
func (s *AccountTestService) testOpenAIChatCompletionsConnectionRun(
	c *accountTestRun,
	account *Account,
	testModelID string,
	prompt string,
	normalizedBaseURL string,
	authToken string,
) error {
	ctx := c.ctx
	apiURL := buildOpenAIChatCompletionsURL(normalizedBaseURL)

	c.begin(true)

	payload := createOpenAIChatCompletionsTestPayload(testModelID, prompt)
	payloadBytes, _ := json.Marshal(payload)

	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	s.sendTestEvent(c, TestEvent{Type: "status", Text: "正在通过 /v1/chat/completions 测试连接"})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create Chat Completions request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+authToken)
	s.applyOpenAIAccountTestRoutingRun(c, account, req, false)

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAIAccountTestTLSProfileRun(c, account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, account, resp.Header, body)
		}
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			errMsg := fmt.Sprintf("Chat Completions authentication failed (401): %s", string(body))
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) returned %d: %s", resp.StatusCode, string(body)))
	}

	return s.processOpenAIChatCompletionsStreamRun(c, resp.Body)
}

// testOpenAINativeCompactionV2ConnectionRun 测试原生 V2（流式 /responses +
// compaction_trigger）。它使用普通模型映射，测试结果不改变管理员开关。
func (s *AccountTestService) testOpenAINativeCompactionV2ConnectionRun(c *accountTestRun, account *Account, testModelID string) error {
	ctx := c.ctx
	credentialAccount := account
	if account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return s.sendTestErrorAndEnd(c, "Failed to resolve account credentials")
		}
		credentialAccount = resolved
	}

	authToken := ""
	apiURL := ""
	isOAuth := false
	switch {
	case credentialAccount.IsOAuth():
		isOAuth = true
		if !credentialAccount.IsOpenAIAgentIdentity() {
			authToken = credentialAccount.GetOpenAIAccessToken()
		}
		if authToken == "" && !credentialAccount.IsOpenAIAgentIdentity() {
			return s.sendTestErrorAndEnd(c, "No access token available")
		}
		apiURL = chatgptCodexAPIURL
	case account.Type == AccountTypeAPIKey:
		authToken = account.GetOpenAIApiKey()
		if authToken == "" {
			return s.sendTestErrorAndEnd(c, "No API key available")
		}
		baseURL := account.GetOpenAIBaseURL()
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = buildOpenAIResponsesURL(normalizedBaseURL)
	default:
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}

	c.begin(true)

	if isOAuth {
		testModelID = normalizeOpenAIModelForUpstream(credentialAccount, testModelID)
	}
	payloadBytes, _ := json.Marshal(createOpenAICompactionTestPayload(testModelID, isOAuth))
	if !agentIdentityTaskRecoveryWasTried(ctx) {
		s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// 与真实 V2 请求相同，即使账号覆盖尝试移除该头，后面也会重新补齐。
	ensureOpenAIRemoteCompactionV2BetaFeature(req.Header)
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount)
		if authErr != nil {
			return s.sendTestErrorAndEnd(c, "Failed to build Agent Identity authentication")
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	canonical := resolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.originator)
	req.Header.Set("User-Agent", canonical.userAgent)
	req.Header.Set("Version", canonical.version)
	testSessionID := compactionTestSessionID(account.ID)
	req.Header.Set("Session_ID", testSessionID)
	req.Header.Set("Conversation_ID", testSessionID)
	s.applyOpenAIAccountTestRoutingRun(c, account, req, isOAuth)

	if isOAuth {
		req.Host = "chatgpt.com"
		setOpenAIChatGPTAccountHeaders(req.Header, credentialAccount)
		if fingerprintIDs := resolveCodexFingerprintIDsFromRequest(account, req.Header); fingerprintIDs != nil {
			applyCodexFingerprintHeaders(req.Header, fingerprintIDs)
		}
		enforceCodexIdentityHeaders(req.Header)
	}

	// 账号覆盖先执行，再补 V2 协商头，保证手动测试和真实转发有相同的协议契约。
	account.ApplyHeaderOverrides(req.Header)
	ensureOpenAIRemoteCompactionV2BetaFeature(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAIAccountTestTLSProfileRun(c, account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	body = redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, credentialAccount, body)
	if !agentIdentityTaskRecoveryWasTried(ctx) && credentialAccount.IsOpenAIAgentIdentity() && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, body) {
		expectedTaskID := credentialAccount.GetCredential("task_id")
		if err := ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount, expectedTaskID); err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Agent Identity task recovery failed: %s", err.Error()))
		}
		c.ctx = markAgentIdentityTaskRecoveryTried(ctx)
		return s.testOpenAINativeCompactionV2ConnectionRun(c, account, testModelID)
	}

	compactionFound := openAICompactionTestHasOutput(body)
	if s.accountRepo != nil {
		// 手动测试只保存额度观测，不修改管理员开关。
		var updates map[string]any
		if codexUpdates, err := extractOpenAICodexProbeUpdates(resp); err == nil && len(codexUpdates) > 0 {
			updates = mergeExtraUpdates(updates, codexUpdates)
		}
		if len(updates) > 0 {
			_ = s.accountRepo.UpdateExtra(ctx, account.ID, updates)
			mergeAccountExtra(account, updates)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, account, resp.Header, body)
		}
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			errMsg := fmt.Sprintf("Authentication failed (401): %s", string(body))
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}
	if !compactionFound {
		return s.sendTestErrorAndEnd(c, "Upstream returned 2xx without a compaction output item (native remote compaction v2 unsupported on this chain)")
	}

	s.sendTestEvent(c, TestEvent{Type: "content", Text: "Native remote compaction v2 test succeeded"})
	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// testOpenAILegacyCompactConnectionRun 仅测试旧版 /responses/compact 连接。
// 本次结果不写入能力状态或管理员开关，认证错误和限流仍按账号测试流程处理。
func (s *AccountTestService) testOpenAILegacyCompactConnectionRun(c *accountTestRun, account *Account, testModelID string) error {
	ctx := c.ctx
	credentialAccount := account
	if account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return s.sendTestErrorAndEnd(c, "Failed to resolve account credentials")
		}
		credentialAccount = resolved
	}

	authToken := ""
	apiURL := ""
	isOAuth := false

	switch {
	case credentialAccount.IsOAuth():
		isOAuth = true
		if !credentialAccount.IsOpenAIAgentIdentity() {
			authToken = credentialAccount.GetOpenAIAccessToken()
		}
		if authToken == "" && !credentialAccount.IsOpenAIAgentIdentity() {
			return s.sendTestErrorAndEnd(c, "No access token available")
		}
		apiURL = chatgptCodexAPIURL + "/compact"
	case account.Type == AccountTypeAPIKey:
		authToken = account.GetOpenAIApiKey()
		if authToken == "" {
			return s.sendTestErrorAndEnd(c, "No API key available")
		}
		baseURL := account.GetOpenAIBaseURL()
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = appendOpenAIResponsesRequestPathSuffix(buildOpenAIResponsesURL(normalizedBaseURL), "/compact")
	default:
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}

	c.begin(true)

	payloadBytes, _ := json.Marshal(createOpenAILegacyCompactionTestPayload(testModelID))
	if !agentIdentityTaskRecoveryWasTried(ctx) {
		s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount)
		if authErr != nil {
			return s.sendTestErrorAndEnd(c, "Failed to build Agent Identity authentication")
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	canonical := resolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.originator)
	req.Header.Set("User-Agent", canonical.userAgent)
	req.Header.Set("Version", canonical.version)
	testSessionID := legacyCompactionTestSessionID(account.ID)
	req.Header.Set("Session_ID", testSessionID)
	req.Header.Set("Conversation_ID", testSessionID)
	s.applyOpenAIAccountTestRoutingRun(c, account, req, isOAuth)

	if isOAuth {
		req.Host = "chatgpt.com"
		setOpenAIChatGPTAccountHeaders(req.Header, credentialAccount)
		// Compact 连接测试同样访问 Codex 上游，测试 UA 覆写完成后必须重新配对身份头。
		enforceCodexIdentityHeaders(req.Header)
	}

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAIAccountTestTLSProfileRun(c, account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	body = redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, credentialAccount, body)
	if !agentIdentityTaskRecoveryWasTried(ctx) && credentialAccount.IsOpenAIAgentIdentity() && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, body) {
		expectedTaskID := credentialAccount.GetCredential("task_id")
		if err := ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount, expectedTaskID); err != nil {
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Agent Identity task recovery failed: %s", err.Error()))
		}
		c.ctx = markAgentIdentityTaskRecoveryTried(ctx)
		return s.testOpenAILegacyCompactConnectionRun(c, account, testModelID)
	}

	if s.accountRepo != nil {
		// 手动测试只保存额度观测，不修改管理员开关。
		var updates map[string]any
		if codexUpdates, err := extractOpenAICodexProbeUpdates(resp); err == nil && len(codexUpdates) > 0 {
			updates = mergeExtraUpdates(updates, codexUpdates)
		}
		if len(updates) > 0 {
			_ = s.accountRepo.UpdateExtra(ctx, account.ID, updates)
			mergeAccountExtra(account, updates)
		}
		// 手动测试如返回 429，主动同步限流状态,避免后续短时间内继续选中。
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, account, resp.Header, body)
		}
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized && s.accountRepo != nil {
			errMsg := fmt.Sprintf("Authentication failed (401): %s", string(body))
			_ = s.accountRepo.SetError(ctx, account.ID, errMsg)
		}
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	s.sendTestEvent(c, TestEvent{Type: "content", Text: "Compact test succeeded"})
	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *AccountTestService) reconcileOpenAI429State(ctx context.Context, account *Account, headers http.Header, body []byte) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}

	persistOpenAI429PlanType(ctx, s.accountRepo, account, body)

	var resetAt *time.Time
	if calculated := calculateOpenAI429ResetTime(headers); calculated != nil {
		resetAt = calculated
	} else if unixTs := parseOpenAIRateLimitResetTime(body); unixTs != nil {
		t := time.Unix(*unixTs, 0)
		resetAt = &t
	}
	if resetAt == nil {
		return
	}

	if err := s.accountRepo.SetRateLimited(ctx, account.ID, *resetAt); err != nil {
		return
	}

	now := time.Now()
	account.RateLimitedAt = &now
	account.RateLimitResetAt = resetAt

	if account.Status == StatusError {
		if err := s.accountRepo.ClearError(ctx, account.ID); err != nil {
			return
		}
		account.Status = StatusActive
		account.ErrorMessage = ""
	}
}

// testGeminiAccountConnectionRun tests a Gemini account's connection
func (s *AccountTestService) testGeminiAccountConnectionRun(c *accountTestRun, account *Account, modelID string, prompt string, testTypes ...string) error {
	ctx := c.ctx

	// Determine the model to use
	testModelID := modelID
	if testModelID == "" {
		testModelID = geminicli.DefaultTestModel
	}

	// For static upstream credentials with model mapping, map the model
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mapping := account.GetModelMapping()
		if len(mapping) > 0 {
			if mappedModel, exists := mapping[testModelID]; exists {
				testModelID = mappedModel
			}
		}
	}

	// Set SSE headers
	c.begin(true)

	// Create test payload (Gemini format)
	payload := createGeminiTestPayload(testModelID, prompt, testTypes...)

	// Build request based on account type
	var req *http.Request
	var err error

	switch account.Type {
	case AccountTypeAPIKey:
		req, err = s.buildGeminiAPIKeyRequest(ctx, account, testModelID, payload)
	case AccountTypeOAuth:
		req, err = s.buildGeminiOAuthRequest(ctx, account, testModelID, payload)
	case AccountTypeServiceAccount:
		req, err = s.buildGeminiServiceAccountRequest(ctx, account, testModelID, payload)
	default:
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}

	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to build request: %s", err.Error()))
	}

	// Send test_start event
	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	// Get proxy and execute request
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveTLSProfile(account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// Process SSE stream
	return s.processGeminiStreamRun(c, resp.Body)
}

// routeAntigravityTestRun 路由 Antigravity 账号的测试请求。
// APIKey 类型走原生协议（与 gateway_handler 路由一致），OAuth/Upstream 走 CRS 中转。
func (s *AccountTestService) routeAntigravityTestRun(c *accountTestRun, account *Account, modelID string, prompt string, testTypes ...string) error {
	testType, explicitTestType := accountTestTypeFromArgs(testTypes...)
	if account.Type == AccountTypeAPIKey {
		if (explicitTestType && testType == AccountTestTypeImage) || strings.HasPrefix(strings.ToLower(modelID), "gemini-") {
			return s.testGeminiAccountConnectionRun(c, account, modelID, prompt, testTypes...)
		}
		return s.testClaudeAccountConnectionRun(c, account, modelID, prompt)
	}
	if explicitTestType && testType == AccountTestTypeImage {
		return s.sendTestErrorAndEnd(c, "Image tests are not supported for this Antigravity account type")
	}
	return s.testAntigravityAccountConnectionRun(c, account, modelID, prompt)
}

func (s *AccountTestService) testQoderAccountConnectionRun(c *accountTestRun, account *Account, modelID string, prompt string) error {
	if account.Type != AccountTypeCosy {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Unsupported account type: %s", account.Type))
	}

	ctx := c.ctx
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = defaultQoderTestModel
	}
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}

	sessionProvider := s.qoderSessionProvider
	if sessionProvider == nil {
		sessionProvider = NewQoderTokenProvider()
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		if invalidator, ok := sessionProvider.(qoderAccountTestSessionInvalidator); ok {
			invalidator.Invalidate(account.ID)
		}
	}
	session, err := sessionProvider.GetSession(ctx, account)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Qoder session failed: %s", err.Error()))
	}
	site, err := qoderSiteForAccount(account)
	if err != nil {
		return s.sendTestErrorAndEnd(c, err.Error())
	}

	requestBody, err := json.Marshal(map[string]any{
		"model":      testModelID,
		"messages":   []map[string]string{{"role": "user", "content": testPrompt}},
		"max_tokens": 16,
		"stream":     true,
	})
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to encode Qoder test payload")
	}
	requestBody = applyQoderAccountModelMapping(account, requestBody)
	payload, modelKey, err := BuildQoderPayloadFromChatCompletionsForSite(requestBody, qoderUserType(account), site)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to build Qoder test payload: %s", err.Error()))
	}
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to encode Qoder test payload")
	}

	c.begin(true)

	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	if site == qoder.SiteGlobal {
		if err := s.probeQoderUserInfo(ctx, account, session); err != nil {
			return s.sendTestErrorAndEnd(c, err.Error())
		}
	}
	s.sendTestEvent(c, TestEvent{Type: "status", Text: "正在通过 Qoder COSY 测试连接"})

	client, err := qoderStreamClientForAccount(s.qoderClient, account)
	if err != nil {
		return s.sendTestErrorAndEnd(c, err.Error())
	}
	headers := map[string]string{
		"x-model-key":    modelKey,
		"x-model-source": "system",
	}
	if doer := newQoderRequestDoer(account, s.httpUpstream, s.tlsFPProfileService); doer != nil {
		if doerClient, ok := client.(qoderStreamClientWithDoer); ok {
			resp, err := doerClient.StreamRequestContextWithDoer(ctx, session, "", payloadBody, headers, doer)
			if err != nil {
				return s.sendTestErrorAndEnd(c, err.Error())
			}
			return s.processQoderStreamRun(c, resp.Body)
		}
	}
	resp, err := client.StreamRequestContext(ctx, session, "", payloadBody, headers)
	if err != nil {
		return s.sendTestErrorAndEnd(c, err.Error())
	}

	return s.processQoderStreamRun(c, resp.Body)
}

func (s *AccountTestService) probeQoderUserInfo(ctx context.Context, account *Account, session *qoder.SessionContext) error {
	if session == nil || session.Identity == nil {
		return errors.New("qoder session identity is empty")
	}
	token := strings.TrimSpace(session.Identity.SecurityOauthToken)
	if token == "" {
		token = strings.TrimSpace(account.GetCredential("security_oauth_token"))
	}
	if token == "" {
		return errors.New("qoder security_oauth_token is empty")
	}
	client := s.qoderOAuthClient
	var userInfo *qoder.UserInfo
	var err error
	if client != nil {
		userInfo, err = client.GetUserInfo(ctx, token)
	} else {
		userInfo, err = s.getQoderUserInfoForAccount(ctx, account, token)
	}
	if err != nil {
		return fmt.Errorf("qoder userinfo probe failed: %w", err)
	}
	if userInfo != nil {
		if session.Identity.UID == "" && strings.TrimSpace(userInfo.ID) != "" {
			session.Identity.UID = strings.TrimSpace(userInfo.ID)
		}
		if session.Identity.Name == "" && strings.TrimSpace(userInfo.Name) != "" {
			session.Identity.Name = strings.TrimSpace(userInfo.Name)
		}
	}
	return nil
}

func (s *AccountTestService) getQoderUserInfoForAccount(ctx context.Context, account *Account, token string) (*qoder.UserInfo, error) {
	profile, err := qoderProfileForAccount(account)
	if err != nil {
		return nil, err
	}
	if doer := newQoderRequestDoer(account, s.httpUpstream, s.tlsFPProfileService); doer != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.OpenAPIBaseURL+qoder.UserInfoPath, nil)
		if err != nil {
			return nil, fmt.Errorf("qoder: create userinfo request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("User-Agent", profile.OpenAPIUserAgent())

		resp, err := doer(req)
		if err != nil {
			return nil, fmt.Errorf("qoder: userinfo request: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, fmt.Errorf("qoder: userinfo failed with status %d: %s", resp.StatusCode, qoder.RedactSensitiveText(string(body)))
		}

		var info qoder.UserInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, fmt.Errorf("qoder: parse userinfo response: %w", err)
		}
		return &info, nil
	}
	return qoder.NewOAuthClientForProfile(profile, nil).GetUserInfo(ctx, token)
}

// testAntigravityAccountConnectionRun tests an Antigravity account's connection
// 支持 Claude 和 Gemini 两种协议，使用非流式请求
func (s *AccountTestService) testAntigravityAccountConnectionRun(c *accountTestRun, account *Account, modelID string, prompt ...string) error {
	ctx := c.ctx

	// 默认模型：Claude 使用 claude-sonnet-4-5，Gemini 使用 gemini-3-pro-preview
	testModelID := modelID
	if testModelID == "" {
		testModelID = "claude-sonnet-4-5"
	}

	if s.antigravityGatewayService == nil {
		return s.sendTestErrorAndEnd(c, "Antigravity gateway service not configured")
	}

	// Set SSE headers
	c.begin(true)

	// Send test_start event
	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	// 调用 AntigravityGatewayService.TestConnection（复用协议转换逻辑）
	result, err := s.antigravityGatewayService.TestConnection(ctx, account, testModelID, prompt...)
	if err != nil {
		return s.sendTestErrorAndEnd(c, err.Error())
	}

	// 发送响应内容
	if result.Text != "" {
		s.sendTestEvent(c, TestEvent{Type: "content", Text: result.Text})
	}

	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// buildGeminiAPIKeyRequest builds request for Gemini API Key accounts
func (s *AccountTestService) buildGeminiAPIKeyRequest(ctx context.Context, account *Account, modelID string, payload []byte) (*http.Request, error) {
	apiKey := account.GetCredential("api_key")
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("no API key available")
	}

	baseURL := account.GetCredential("base_url")
	if baseURL == "" {
		baseURL = geminicli.AIStudioBaseURL
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	// Use streamGenerateContent for real-time feedback
	fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, modelID, "streamGenerateContent", true)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	applyAccountTestUserAgent(req)

	return req, nil
}

// buildGeminiOAuthRequest builds request for Gemini OAuth accounts
func (s *AccountTestService) buildGeminiOAuthRequest(ctx context.Context, account *Account, modelID string, payload []byte) (*http.Request, error) {
	if s.geminiTokenProvider == nil {
		return nil, fmt.Errorf("gemini token provider not configured")
	}

	// Get access token (auto-refreshes if needed)
	accessToken, err := s.geminiTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	if projectID == "" {
		// AI Studio OAuth mode (no project_id): call generativelanguage API directly with Bearer token.
		baseURL := account.GetCredential("base_url")
		if strings.TrimSpace(baseURL) == "" {
			baseURL = geminicli.AIStudioBaseURL
		}
		normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, modelID, "streamGenerateContent", true)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		applyAccountTestUserAgent(req)
		return req, nil
	}

	// Code Assist mode (with project_id)
	return s.buildCodeAssistRequest(ctx, accessToken, projectID, modelID, payload)
}

func (s *AccountTestService) buildGeminiServiceAccountRequest(ctx context.Context, account *Account, modelID string, payload []byte) (*http.Request, error) {
	if s.geminiTokenProvider == nil {
		return nil, fmt.Errorf("gemini token provider not configured")
	}
	accessToken, err := s.geminiTokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get service account access token: %w", err)
	}
	fullURL, err := buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(modelID), modelID, "streamGenerateContent", true)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	applyAccountTestUserAgent(req)
	return req, nil
}

// buildCodeAssistRequest builds request for Google Code Assist API (used by Gemini CLI and Antigravity)
func (s *AccountTestService) buildCodeAssistRequest(ctx context.Context, accessToken, projectID, modelID string, payload []byte) (*http.Request, error) {
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

	normalizedBaseURL, err := s.validateUpstreamBaseURL(geminicli.GeminiCliBaseURL)
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
	applyAccountTestUserAgent(req)

	return req, nil
}

// createGeminiTestPayload creates a minimal test payload for Gemini API.
// 显式图片类型使用图片生成配置；未指定类型时保留旧模型名兼容判断。
func createGeminiTestPayload(modelID string, prompt string, testTypes ...string) []byte {
	testType, explicitTestType := accountTestTypeFromArgs(testTypes...)
	useImageTest := (explicitTestType && testType == AccountTestTypeImage) ||
		(!explicitTestType && isImageGenerationModel(modelID))
	if useImageTest {
		imagePrompt := strings.TrimSpace(prompt)
		if imagePrompt == "" {
			imagePrompt = defaultGeminiImageTestPrompt
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
		textPrompt = defaultGeminiTextTestPrompt
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

// processGeminiStreamRun processes SSE stream from Gemini API
func (s *AccountTestService) processGeminiStreamRun(c *accountTestRun, body io.Reader) error {
	reader := bufio.NewReader(body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		if jsonStr == "[DONE]" {
			s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
			return nil
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// Support two Gemini response formats:
		// - AI Studio: {"candidates": [...]}
		// - Gemini CLI: {"response": {"candidates": [...]}}
		if resp, ok := data["response"].(map[string]any); ok && resp != nil {
			data = resp
		}
		if candidates, ok := data["candidates"].([]any); ok && len(candidates) > 0 {
			if candidate, ok := candidates[0].(map[string]any); ok {
				// Extract content first (before checking completion)
				if content, ok := candidate["content"].(map[string]any); ok {
					if parts, ok := content["parts"].([]any); ok {
						for _, part := range parts {
							if partMap, ok := part.(map[string]any); ok {
								if text, ok := partMap["text"].(string); ok && text != "" {
									s.sendTestEvent(c, TestEvent{Type: "content", Text: text})
								}
								if inlineData, ok := partMap["inlineData"].(map[string]any); ok {
									mimeType, _ := inlineData["mimeType"].(string)
									data, _ := inlineData["data"].(string)
									if strings.HasPrefix(strings.ToLower(mimeType), "image/") && data != "" {
										s.sendTestEvent(c, TestEvent{
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

				// Check for completion after extracting content
				if finishReason, ok := candidate["finishReason"].(string); ok && finishReason != "" {
					s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
					return nil
				}
			}
		}

		// Handle errors
		if errData, ok := data["error"].(map[string]any); ok {
			errorMsg := "Unknown error"
			if msg, ok := errData["message"].(string); ok {
				errorMsg = msg
			}
			return s.sendTestErrorAndEnd(c, errorMsg)
		}
	}
}

// createOpenAITestPayload creates a test payload for OpenAI Responses API
func createOpenAITestPayload(modelID string, prompt string, isOAuth bool) map[string]any {
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}
	payload := map[string]any{
		"model": modelID,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": testPrompt,
					},
				},
			},
		},
		"stream": true,
	}

	// OAuth accounts using ChatGPT internal API require store: false
	if isOAuth {
		payload["store"] = false
	}

	// All accounts require instructions for Responses API
	payload["instructions"] = openai.DefaultInstructions

	return payload
}

func createOpenAIChatCompletionsTestPayload(modelID string, prompt string) map[string]any {
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}

	return map[string]any{
		"model": modelID,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": testPrompt,
			},
		},
		"stream": true,
	}
}

// processClaudeStreamRun processes the SSE stream from Claude API
func (s *AccountTestService) processClaudeStreamRun(c *accountTestRun, body io.Reader) error {
	reader := bufio.NewReader(body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}

		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
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
					s.sendTestEvent(c, TestEvent{Type: "content", Text: text})
				}
			}
		case "message_stop":
			s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
			return nil
		case "error":
			errorMsg := "Unknown error"
			if errData, ok := data["error"].(map[string]any); ok {
				if msg, ok := errData["message"].(string); ok {
					errorMsg = msg
				}
			}
			return s.sendTestErrorAndEnd(c, errorMsg)
		}
	}
}

func (s *AccountTestService) processQoderStreamRun(c *accountTestRun, body io.ReadCloser) error {
	if body == nil {
		return s.sendTestErrorAndEnd(c, "Qoder response body is nil")
	}
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
	seenEvent := false
	for scanner.Scan() {
		events, err := qoder.ParseSSELine(scanner.Text())
		if err != nil {
			return s.sendTestErrorAndEnd(c, err.Error())
		}
		for _, event := range events {
			seenEvent = true
			if event.Type == "text_delta" && event.Text != "" {
				s.sendTestEvent(c, TestEvent{Type: "content", Text: event.Text})
			}
			if event.IsDone {
				s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
				return nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Qoder stream read error: %s", err.Error()))
	}
	if seenEvent {
		s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
		return nil
	}
	return s.sendTestErrorAndEnd(c, "Qoder stream ended before any response")
}

// processOpenAIChatCompletionsStreamRun 处理 OpenAI 兼容 Chat Completions API 返回的 SSE 分片。
func (s *AccountTestService) processOpenAIChatCompletionsStreamRun(c *accountTestRun, body io.Reader) error {
	reader := bufio.NewReader(body)
	seenJSON := false
	seenFinish := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if seenFinish {
					s.sendTestEvent(c, TestEvent{Type: "status", Text: "已通过 /v1/chat/completions 验证"})
					s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
					return nil
				}
				if seenJSON {
					return s.sendTestErrorAndEnd(c, "Chat Completions stream from /v1/chat/completions ended before [DONE]")
				}
				return s.sendTestErrorAndEnd(c, "Invalid Chat Completions response from /v1/chat/completions: expected SSE JSON data")
			}
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Chat Completions stream read error from /v1/chat/completions: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}

		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			s.sendTestEvent(c, TestEvent{Type: "status", Text: "已通过 /v1/chat/completions 验证"})
			s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
			return nil
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			return s.sendTestErrorAndEnd(c, "Invalid Chat Completions response from /v1/chat/completions: expected JSON data")
		}
		seenJSON = true

		if errData, ok := data["error"].(map[string]any); ok {
			errorMsg := "Chat Completions API (/v1/chat/completions) returned an error"
			if msg, ok := errData["message"].(string); ok && msg != "" {
				errorMsg = msg
			}
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) error: %s", errorMsg))
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
					s.sendTestEvent(c, TestEvent{Type: "content", Text: text})
				}
			}
			if message, ok := choice["message"].(map[string]any); ok {
				if text, ok := message["content"].(string); ok && text != "" {
					s.sendTestEvent(c, TestEvent{Type: "content", Text: text})
				}
			}
			if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
				seenFinish = true
			}
		}
	}
}

// processOpenAIStreamRun processes the SSE stream from OpenAI Responses API
func (s *AccountTestService) processOpenAIStreamRun(c *accountTestRun, body io.Reader) error {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return s.sendTestErrorAndEnd(c, "Stream ended before response.completed")
			}
			return s.sendTestErrorAndEnd(c, fmt.Sprintf("Stream read error: %s", err.Error()))
		}

		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}

		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			return s.sendTestErrorAndEnd(c, "Stream ended before response.completed")
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		switch eventType {
		case "response.output_text.delta":
			// OpenAI Responses API uses "delta" field for text content
			if delta, ok := data["delta"].(string); ok && delta != "" {
				s.sendTestEvent(c, TestEvent{Type: "content", Text: delta})
			}
		case "response.completed", "response.done":
			s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
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
			return s.sendTestErrorAndEnd(c, errorMsg)
		case "error":
			errorMsg := "Unknown error"
			if errData, ok := data["error"].(map[string]any); ok {
				if msg, ok := errData["message"].(string); ok {
					errorMsg = msg
				}
			}
			return s.sendTestErrorAndEnd(c, errorMsg)
		}
	}
}

// testOpenAIImageAPIKeyRun tests OpenAI image generation using an API Key account.
func (s *AccountTestService) testOpenAIImageAPIKeyRun(c *accountTestRun, ctx context.Context, account *Account, modelID, prompt string) error {
	authToken := account.GetOpenAIApiKey()
	if authToken == "" {
		return s.sendTestErrorAndEnd(c, "No API key available")
	}

	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
	}
	apiURL := buildOpenAIImagesURL(normalizedBaseURL, openAIImagesGenerationsEndpoint)

	// Set SSE headers
	c.begin(true)

	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: modelID})

	payload := map[string]any{
		"model":           modelID,
		"prompt":          prompt,
		"n":               1,
		"response_format": "b64_json",
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)
	s.applyOpenAIAccountTestRoutingRun(c, account, req, false)

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAIAccountTestTLSProfileRun(c, account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to read response: %s", err.Error()))
	}

	if resp.StatusCode != http.StatusOK {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// Parse {"data": [{"b64_json": "...", "revised_prompt": "..."}]}
	var result struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to parse response: %s", err.Error()))
	}

	if len(result.Data) == 0 {
		return s.sendTestErrorAndEnd(c, "No images returned from API")
	}

	for _, item := range result.Data {
		if item.RevisedPrompt != "" {
			s.sendTestEvent(c, TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		if item.B64JSON != "" {
			s.sendTestEvent(c, TestEvent{
				Type:     "image",
				ImageURL: "data:image/png;base64," + item.B64JSON,
				MimeType: "image/png",
			})
		}
	}

	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// testOpenAIImageOAuthRun tests OpenAI image generation using an OAuth account via Codex /responses API.
func (s *AccountTestService) testOpenAIImageOAuthRun(c *accountTestRun, ctx context.Context, account *Account, modelID, prompt string) error {
	credentialAccount := account
	if account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return s.sendTestErrorAndEnd(c, "Failed to resolve account credentials")
		}
		credentialAccount = resolved
	}
	authToken := ""
	if !credentialAccount.IsOpenAIAgentIdentity() {
		authToken = credentialAccount.GetOpenAIAccessToken()
	}
	if authToken == "" && !credentialAccount.IsOpenAIAgentIdentity() {
		return s.sendTestErrorAndEnd(c, "No access token available")
	}

	// Set SSE headers
	c.begin(true)

	s.sendTestEvent(c, TestEvent{Type: "test_start", Model: modelID})
	s.sendTestEvent(c, TestEvent{Type: "content", Text: "Calling Codex /responses image tool...\n"})

	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Model:    strings.TrimSpace(modelID),
		Prompt:   prompt,
	}
	applyOpenAIImagesDefaults(parsed)

	responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, parsed.Model)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to build image request: %s", err.Error()))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexAPIURL, bytes.NewReader(responsesBody))
	if err != nil {
		return s.sendTestErrorAndEnd(c, "Failed to create request")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credentialAccount)
		if authErr != nil {
			return s.sendTestErrorAndEnd(c, "Failed to build Agent Identity authentication")
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", resolveCodexOutboundIdentity("").originator)
	if customUA := strings.TrimSpace(credentialAccount.GetOpenAIUserAgent()); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	} else {
		req.Header.Set("User-Agent", CodexCanonicalUserAgent())
	}
	s.applyOpenAIAccountTestRoutingRun(c, account, req, true)
	setOpenAIChatGPTAccountHeaders(req.Header, credentialAccount)
	// 与真实转发一致：originator 与最终 User-Agent 首段配套（原 opencode 与 Codex UA 错配会 404，issue #3901）。
	enforceCodexIdentityHeaders(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAIAccountTestTLSProfileRun(c, account))
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Responses API request failed: %s", err.Error()))
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		body = redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, credentialAccount, body)
		message := strings.TrimSpace(extractUpstreamErrorMessage(body))
		if message == "" {
			message = fmt.Sprintf("Responses API returned %d", resp.StatusCode)
		}
		return s.sendTestErrorAndEnd(c, message)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to read image response: %s", err.Error()))
	}
	body = redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, credentialAccount, body)

	results, _, _, _, _, err := collectOpenAIImagesFromResponsesBody(body)
	if err != nil {
		return s.sendTestErrorAndEnd(c, fmt.Sprintf("Failed to parse image response: %s", err.Error()))
	}
	if len(results) == 0 {
		return s.sendTestErrorAndEnd(c, "No images returned from responses API")
	}

	for _, item := range results {
		if item.RevisedPrompt != "" {
			s.sendTestEvent(c, TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		mimeType := openAIImageOutputMIMEType(item.OutputFormat)
		s.sendTestEvent(c, TestEvent{
			Type:     "image",
			ImageURL: "data:" + mimeType + ";base64," + item.Result,
			MimeType: mimeType,
		})
	}

	s.sendTestEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *AccountTestService) sendTestEvent(c *accountTestRun, event TestEvent) {
	if event.Type == "test_complete" {
		if suppress, ok := c.Get(accountTestSuppressCompletionContextKey); ok {
			if suppressCompletion, _ := suppress.(bool); suppressCompletion {
				return
			}
		}
	}
	if err := c.emit(event); err != nil {
		log.Printf("failed to write SSE event: %v", err)
	}
}

// sendTestErrorAndEnd sends an error event and ends the stream
func (s *AccountTestService) sendTestErrorAndEnd(c *accountTestRun, errorMsg string) error {
	log.Printf("Account test error: %s", errorMsg)
	s.sendTestEvent(c, TestEvent{Type: "error", Error: errorMsg})
	return fmt.Errorf("%s", errorMsg)
}

// 后台调用同一事件用例，保留原方法名称供维护入口过渡。
func (s *AccountTestService) RunTestBackground(ctx context.Context, id int64, model string) (*ScheduledTestResult, error) {
	return s.Tester().RunTestBackground(ctx, id, model)
}
func (s *AccountTestService) RunTestBackgroundWithPrompt(ctx context.Context, id int64, model, prompt string) (*ScheduledTestResult, error) {
	return s.Tester().RunTestBackgroundWithPrompt(ctx, id, model, prompt)
}
func (s *AccountTestService) RunTestBackgroundWithPromptAndUserAgent(ctx context.Context, id int64, model, prompt, userAgent string) (*ScheduledTestResult, error) {
	return s.Tester().RunTestBackgroundWithPromptAndUserAgent(ctx, id, model, prompt, userAgent)
}

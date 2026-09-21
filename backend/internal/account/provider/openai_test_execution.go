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

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// Execute 按原凭据、模式和显式测试类型执行 OpenAI 测试。
func (s *OpenAIAccountTest) Execute(c *TestRun, value *accountcore.Record, modelID string, prompt string, mode string, testTypes ...string) error {
	ctx := c.Context
	mode, testType, explicitTestType := accountcore.ResolveAccountTestModeAndType(mode, testTypes...)

	// 保留 OpenAI 缺省测试模型。
	testModelID := modelID
	if testModelID == "" {
		testModelID = openai.DefaultTestModel
	}

	// 原生 V2 与普通 Responses 一样只使用常规模型映射；旧端点兼容性测试才
	// 在其基础上追加 compact_model_mapping。
	testModelID = mappedTestModel(value, testModelID)
	if mode == accountcore.AccountTestModeCompact {
		return s.executeNativeCompaction(c, value, testModelID)
	}
	if mode == accountcore.AccountTestModeLegacyCompact {
		testModelID = accountcore.ResolveCompactForwardModel(value, testModelID)
		return s.executeLegacyCompact(c, value, testModelID)
	}

	// 显式类型优先于模型名；未指定类型时才保留旧版图片模型兼容判断。
	if (explicitTestType && testType == accountcore.AccountTestTypeImage) ||
		(!explicitTestType && strings.HasPrefix(strings.ToLower(testModelID), "gpt-image-")) {
		imagePrompt := strings.TrimSpace(prompt)
		if imagePrompt == "" {
			imagePrompt = "Generate a cute orange cat astronaut sticker on a clean pastel background."
		}
		if value.Type == "apikey" {
			return s.ExecuteImageAPIKey(c, ctx, value, testModelID, imagePrompt)
		}
		return s.ExecuteImageOAuth(c, ctx, value, testModelID, imagePrompt)
	}

	credentialAccount := value
	if value.IsCredentialShadow() {
		resolved, err := accountcore.ResolveCredentialRecord(ctx, s.read, value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, err.Error())
		}
		credentialAccount = resolved
	}

	// 按原顺序解析认证与端点。
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
			return (TestStreamOutput{}).Error(c, "No access token available")
		}

		// OAuth 继续使用 ChatGPT 内部端点。
		apiURL = "https://chatgpt.com/backend-api/codex/responses"
	} else if credentialAccount.Type == "apikey" {
		// API Key 使用平台端点。
		// 国产 OpenAI 兼容供应商通过协议族密钥读取器复用此探针。
		authToken = credentialAccount.GetOpenAIProtocolAPIKey()
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}

		baseURL := credentialAccount.OpenAIBaseURL((accountcore.ProtocolTarget{Record: credentialAccount}).IsAdaptiveAPIProtocol())
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalizedBaseURL, err := s.ValidateURL(baseURL)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		protocol := accountcore.ResolveUpstreamTextProtocol(value.Extra, accountcore.TextProtocolResponses)
		if requested := c.RequestedProtocol; requested != "" && value.IsOpenAI() {
			protocol = requested
		}
		if protocol == accountcore.TextProtocolChatCompletions {
			return s.executeChat(c, value, testModelID, prompt, normalizedBaseURL, authToken)
		}
		apiURL = httpclient.BuildOpenAIEndpointURL(normalizedBaseURL, "/v1/responses")
	} else {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported account type: %s", value.Type))
	}

	// 保留事件流提交时机。
	c.Begin(true)

	// OAuth 账号使用 ChatGPT Codex 上游，测试请求必须与真实转发使用同一模型归一化规则。
	upstreamTestModelID := testModelID
	if isOAuth {
		upstreamTestModelID = s.normalizeModel(credentialAccount, testModelID)
	}
	payload := openai.TestResponsesPayload(upstreamTestModelID, prompt, isOAuth)
	payloadBytes, _ := json.Marshal(payload)

	// task 失效时会注册新 task 并重试探针，因此开始事件只发送一次。
	if !c.TaskRecoveryTried {
		(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))

	// 设置共同协议 Header。
	req.Header.Set("Content-Type", "application/json")
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := s.agentHeaders(ctx, credentialAccount)
		if authErr != nil {
			return (TestStreamOutput{}).Error(c, "Failed to build Agent Identity authentication")
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// 按原顺序设置 ChatGPT OAuth 专用 Header。
	if isOAuth {
		req.Host = "chatgpt.com"
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("Originator", openai.ResolveCodexOutboundIdentity("").Originator)
		if customUA := strings.TrimSpace(credentialAccount.GetOpenAIUserAgent()); customUA != "" {
			req.Header.Set("User-Agent", customUA)
		} else {
			req.Header.Set("User-Agent", openai.CodexCanonicalUserAgent())
		}
		setTestChatGPTHeaders(req.Header, credentialAccount)
	}
	s.ApplyRouting(c, value, req, isOAuth)
	if value.Type == capability.AccountTypeOAuth {
		// 必须在测试专用 UA 覆写之后配对身份，否则测试请求仍可能因头部错配返回 404。
		openai.EnforceCodexIdentityHeaders(req.Header)
	}

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	applyGrokQuotaHeaders(credentialAccount, req.Header)

	// 保留账号关联代理。
	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.ResolveTLS(c, value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if isOAuth && s.Store != nil {
		if updates, err := ExtractOpenAIUsageUpdates(resp, time.Now()); err == nil && len(updates) > 0 {
			_ = s.Store.UpdateExtra(ctx, value.ID, updates)
			value.Extra = accountcore.MergeUsageExtra(value.Extra, updates)
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		body = RedactAgentIdentityBody(ctx, s.read, credentialAccount, body)
		if !c.TaskRecoveryTried && credentialAccount.IsOpenAIAgentIdentity() && openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, body) {
			expectedTaskID := credentialAccount.GetCredential("task_id")
			if err := s.EnsureTask(ctx, credentialAccount, expectedTaskID); err != nil {
				return (TestStreamOutput{}).Error(c, fmt.Sprintf("Agent Identity task recovery failed: %s", err.Error()))
			}
			c.TaskRecoveryTried = true
			return s.Execute(c, value, modelID, prompt, mode, testType)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, value, resp.Header, body)
		}
		// 401 Unauthorized: 标记账号为永久错误
		if resp.StatusCode == http.StatusUnauthorized && s.Store != nil {
			errMsg := fmt.Sprintf("Authentication failed (401): %s", string(body))
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// 逐事件解析响应流。
	return (TestStreamOutput{}).Responses(c, resp.Body)
}

// executeChat 通过原始 /v1/chat/completions 端点测试 OpenAI 兼容 API Key 账号。
func (s *OpenAIAccountTest) executeChat(
	c *TestRun,
	value *accountcore.Record,
	testModelID string,
	prompt string,
	normalizedBaseURL string,
	authToken string,
) error {
	ctx := c.Context
	apiURL := httpclient.BuildOpenAIEndpointURL(normalizedBaseURL, "/v1/chat/completions")

	c.Begin(true)

	payload := openai.TestChatCompletionsPayload(testModelID, prompt)
	payloadBytes, _ := json.Marshal(payload)

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "status", Text: "正在通过 /v1/chat/completions 测试连接"})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create Chat Completions request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+authToken)
	s.ApplyRouting(c, value, req, false)

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	applyGrokQuotaHeaders(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.ResolveTLS(c, value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, value, resp.Header, body)
		}
		if resp.StatusCode == http.StatusUnauthorized && s.Store != nil {
			errMsg := fmt.Sprintf("Chat Completions authentication failed (401): %s", string(body))
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) returned %d: %s", resp.StatusCode, string(body)))
	}

	return (TestStreamOutput{}).ChatCompletions(c, resp.Body)
}

// executeNativeCompaction 测试原生 V2（流式 /responses +
// compaction_trigger）。它使用普通模型映射，测试结果不改变管理员开关。
func (s *OpenAIAccountTest) executeNativeCompaction(c *TestRun, value *accountcore.Record, testModelID string) error {
	ctx := c.Context
	credentialAccount := value
	if value.IsShadow() {
		resolved, err := accountcore.ResolveCredentialRecord(ctx, s.read, value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, "Failed to resolve account credentials")
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
			return (TestStreamOutput{}).Error(c, "No access token available")
		}
		apiURL = "https://chatgpt.com/backend-api/codex/responses"
	case value.Type == capability.AccountTypeAPIKey:
		authToken = value.GetOpenAIApiKey()
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}
		baseURL := value.OpenAIBaseURL((accountcore.ProtocolTarget{Record: value}).IsAdaptiveAPIProtocol())
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalizedBaseURL, err := s.ValidateURL(baseURL)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = httpclient.BuildOpenAIEndpointURL(normalizedBaseURL, "/v1/responses")
	default:
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported account type: %s", value.Type))
	}

	c.Begin(true)

	if isOAuth {
		testModelID = s.normalizeModel(credentialAccount, testModelID)
	}
	payloadBytes, _ := json.Marshal(openai.CompactionTestPayload(testModelID, isOAuth))
	if !c.TaskRecoveryTried {
		(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// 与真实 V2 请求相同，即使账号覆盖尝试移除该头，后面也会重新补齐。
	openai.EnsureRemoteCompactionV2Header(req.Header)
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := s.agentHeaders(ctx, credentialAccount)
		if authErr != nil {
			return (TestStreamOutput{}).Error(c, "Failed to build Agent Identity authentication")
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
	canonical := openai.ResolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.Originator)
	req.Header.Set("User-Agent", canonical.UserAgent)
	req.Header.Set("Version", canonical.Version)
	testSessionID := openai.CompactionTestSessionID(value.ID)
	req.Header.Set("Session_ID", testSessionID)
	req.Header.Set("Conversation_ID", testSessionID)
	s.ApplyRouting(c, value, req, isOAuth)

	if isOAuth {
		req.Host = "chatgpt.com"
		setTestChatGPTHeaders(req.Header, credentialAccount)
		if fingerprintIDs := testCodexFingerprint(value, req.Header); fingerprintIDs != nil {
			openai.ApplyCodexFingerprintHeaders(req.Header, fingerprintIDs)
		}
		openai.EnforceCodexIdentityHeaders(req.Header)
	}

	// 账号覆盖先执行，再补 V2 协商头，保证手动测试和真实转发有相同的协议契约。
	applyGrokQuotaHeaders(value, req.Header)
	openai.EnsureRemoteCompactionV2Header(req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.ResolveTLS(c, value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	body = RedactAgentIdentityBody(ctx, s.read, credentialAccount, body)
	if !c.TaskRecoveryTried && credentialAccount.IsOpenAIAgentIdentity() && openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, body) {
		expectedTaskID := credentialAccount.GetCredential("task_id")
		if err := s.EnsureTask(ctx, credentialAccount, expectedTaskID); err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Agent Identity task recovery failed: %s", err.Error()))
		}
		c.TaskRecoveryTried = true
		return s.executeNativeCompaction(c, value, testModelID)
	}

	compactionFound := openai.CompactionTestHasOutput(body)
	if s.Store != nil {
		// 手动测试只保存额度观测，不修改管理员开关。
		var updates map[string]any
		if codexUpdates, err := ExtractOpenAIUsageUpdates(resp, time.Now()); err == nil && len(codexUpdates) > 0 {
			updates = mergeTestExtraUpdates(updates, codexUpdates)
		}
		if len(updates) > 0 {
			_ = s.Store.UpdateExtra(ctx, value.ID, updates)
			value.Extra = accountcore.MergeUsageExtra(value.Extra, updates)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, value, resp.Header, body)
		}
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized && s.Store != nil {
			errMsg := fmt.Sprintf("Authentication failed (401): %s", string(body))
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}
	if !compactionFound {
		return (TestStreamOutput{}).Error(c, "Upstream returned 2xx without a compaction output item (native remote compaction v2 unsupported on this chain)")
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: "Native remote compaction v2 test succeeded"})
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

// executeLegacyCompact 仅测试旧版 /responses/compact 连接。
// 本次结果不写入能力状态或管理员开关，认证错误和限流仍按账号测试流程处理。
func (s *OpenAIAccountTest) executeLegacyCompact(c *TestRun, value *accountcore.Record, testModelID string) error {
	ctx := c.Context
	credentialAccount := value
	if value.IsShadow() {
		resolved, err := accountcore.ResolveCredentialRecord(ctx, s.read, value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, "Failed to resolve account credentials")
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
			return (TestStreamOutput{}).Error(c, "No access token available")
		}
		apiURL = "https://chatgpt.com/backend-api/codex/responses" + "/compact"
	case value.Type == capability.AccountTypeAPIKey:
		authToken = value.GetOpenAIApiKey()
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}
		baseURL := value.OpenAIBaseURL((accountcore.ProtocolTarget{Record: value}).IsAdaptiveAPIProtocol())
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		normalizedBaseURL, err := s.ValidateURL(baseURL)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = openai.AppendResponsesPathSuffix(httpclient.BuildOpenAIEndpointURL(normalizedBaseURL, "/v1/responses"), "/compact")
	default:
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported account type: %s", value.Type))
	}

	c.Begin(true)

	payloadBytes, _ := json.Marshal(openai.LegacyCompactionTestPayload(testModelID))
	if !c.TaskRecoveryTried {
		(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: testModelID})
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := s.agentHeaders(ctx, credentialAccount)
		if authErr != nil {
			return (TestStreamOutput{}).Error(c, "Failed to build Agent Identity authentication")
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
	canonical := openai.ResolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.Originator)
	req.Header.Set("User-Agent", canonical.UserAgent)
	req.Header.Set("Version", canonical.Version)
	testSessionID := openai.LegacyCompactionTestSessionID(value.ID)
	req.Header.Set("Session_ID", testSessionID)
	req.Header.Set("Conversation_ID", testSessionID)
	s.ApplyRouting(c, value, req, isOAuth)

	if isOAuth {
		req.Host = "chatgpt.com"
		setTestChatGPTHeaders(req.Header, credentialAccount)
		// Compact 连接测试同样访问 Codex 上游，测试 UA 覆写完成后必须重新配对身份头。
		openai.EnforceCodexIdentityHeaders(req.Header)
	}

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	applyGrokQuotaHeaders(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.ResolveTLS(c, value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	body = RedactAgentIdentityBody(ctx, s.read, credentialAccount, body)
	if !c.TaskRecoveryTried && credentialAccount.IsOpenAIAgentIdentity() && openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, body) {
		expectedTaskID := credentialAccount.GetCredential("task_id")
		if err := s.EnsureTask(ctx, credentialAccount, expectedTaskID); err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Agent Identity task recovery failed: %s", err.Error()))
		}
		c.TaskRecoveryTried = true
		return s.executeLegacyCompact(c, value, testModelID)
	}

	if s.Store != nil {
		// 手动测试只保存额度观测，不修改管理员开关。
		var updates map[string]any
		if codexUpdates, err := ExtractOpenAIUsageUpdates(resp, time.Now()); err == nil && len(codexUpdates) > 0 {
			updates = mergeTestExtraUpdates(updates, codexUpdates)
		}
		if len(updates) > 0 {
			_ = s.Store.UpdateExtra(ctx, value.ID, updates)
			value.Extra = accountcore.MergeUsageExtra(value.Extra, updates)
		}
		// 手动测试如返回 429，主动同步限流状态,避免后续短时间内继续选中。
		if resp.StatusCode == http.StatusTooManyRequests {
			s.reconcileOpenAI429State(ctx, value, resp.Header, body)
		}
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized && s.Store != nil {
			errMsg := fmt.Sprintf("Authentication failed (401): %s", string(body))
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: "Compact test succeeded"})
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *OpenAIAccountTest) reconcileOpenAI429State(ctx context.Context, value *accountcore.Record, headers http.Header, body []byte) {
	if s == nil || s.Store == nil || value == nil {
		return
	}

	accountcore.PersistOpenAIObservedPlan(ctx, s.Store, value, openai.ParseUsageLimitPlanType(body), slog.Info, slog.Warn)

	var resetAt *time.Time
	if calculated := accountcore.OpenAI429ResetTime(openai.ParseCodexRateLimitHeaders(headers), time.Now, slog.Info); calculated != nil {
		resetAt = calculated
	} else if unixTs := openai.ParseUsageLimitResetTime(body, time.Now); unixTs != nil {
		t := time.Unix(*unixTs, 0)
		resetAt = &t
	}
	if resetAt == nil {
		return
	}

	if err := s.Store.SetRateLimited(ctx, value.ID, *resetAt); err != nil {
		return
	}

	now := time.Now()
	value.RateLimitedAt = &now
	value.RateLimitResetAt = resetAt

	if value.Status == accountcore.StatusError {
		if err := s.Store.ClearError(ctx, value.ID); err != nil {
			return
		}
		value.Status = accountcore.StatusActive
		value.ErrorMessage = ""
	}
}

// ExecuteImageAPIKey 使用 API Key 请求图片端点并输出原预览事件。
func (s *OpenAIAccountTest) ExecuteImageAPIKey(c *TestRun, ctx context.Context, value *accountcore.Record, modelID, prompt string) error {
	authToken := value.GetOpenAIApiKey()
	if authToken == "" {
		return (TestStreamOutput{}).Error(c, "No API key available")
	}

	baseURL := value.OpenAIBaseURL((accountcore.ProtocolTarget{Record: value}).IsAdaptiveAPIProtocol())
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := s.ValidateURL(baseURL)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
	}
	apiURL := httpclient.BuildOpenAIEndpointURL(normalizedBaseURL, upstream.OpenAIImagesGenerationsEndpoint)

	// 保留事件流提交时机。
	c.Begin(true)

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: modelID})

	payload := map[string]any{
		"model":           modelID,
		"prompt":          prompt,
		"n":               1,
		"response_format": "b64_json",
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)
	s.ApplyRouting(c, value, req, false)

	// 账号级请求头覆写：测试请求与真实转发保持一致的最终头
	applyGrokQuotaHeaders(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.ResolveTLS(c, value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to read response: %s", err.Error()))
	}

	if resp.StatusCode != http.StatusOK {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// 保留图片结果及修订提示词的原解析形状。
	var result struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to parse response: %s", err.Error()))
	}

	if len(result.Data) == 0 {
		return (TestStreamOutput{}).Error(c, "No images returned from API")
	}

	for _, item := range result.Data {
		if item.RevisedPrompt != "" {
			(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		if item.B64JSON != "" {
			(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{
				Type:     "image",
				ImageURL: "data:image/png;base64," + item.B64JSON,
				MimeType: "image/png",
			})
		}
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

// ExecuteImageOAuth 使用 OAuth 的 Codex Responses 图片工具。
func (s *OpenAIAccountTest) ExecuteImageOAuth(c *TestRun, ctx context.Context, value *accountcore.Record, modelID, prompt string) error {
	credentialAccount := value
	if value.IsShadow() {
		resolved, err := accountcore.ResolveCredentialRecord(ctx, s.read, value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, "Failed to resolve account credentials")
		}
		credentialAccount = resolved
	}
	authToken := ""
	if !credentialAccount.IsOpenAIAgentIdentity() {
		authToken = credentialAccount.GetOpenAIAccessToken()
	}
	if authToken == "" && !credentialAccount.IsOpenAIAgentIdentity() {
		return (TestStreamOutput{}).Error(c, "No access token available")
	}

	// 保留事件流提交时机。
	c.Begin(true)

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_start", Model: modelID})
	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: "Calling Codex /responses image tool...\n"})

	parsed := &upstream.ImageRequest{
		Endpoint: upstream.OpenAIImagesGenerationsEndpoint,
		Model:    strings.TrimSpace(modelID),
		Prompt:   prompt,
	}
	upstream.ApplyOpenAIImagesDefaults(parsed)

	responsesBody, err := openai.BuildOpenAIImagesResponsesRequest(parsed, parsed.Model)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to build image request: %s", err.Error()))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(responsesBody))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := s.agentHeaders(ctx, credentialAccount)
		if authErr != nil {
			return (TestStreamOutput{}).Error(c, "Failed to build Agent Identity authentication")
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
	req.Header.Set("originator", openai.ResolveCodexOutboundIdentity("").Originator)
	if customUA := strings.TrimSpace(credentialAccount.GetOpenAIUserAgent()); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	} else {
		req.Header.Set("User-Agent", openai.CodexCanonicalUserAgent())
	}
	s.ApplyRouting(c, value, req, true)
	setTestChatGPTHeaders(req.Header, credentialAccount)
	// 与真实转发一致：originator 与最终 User-Agent 首段配套（原 opencode 与 Codex UA 错配会 404，issue #3901）。
	openai.EnforceCodexIdentityHeaders(req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.ResolveTLS(c, value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Responses API request failed: %s", err.Error()))
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		body = RedactAgentIdentityBody(ctx, s.read, credentialAccount, body)
		message := strings.TrimSpace(upstream.ExtractErrorMessage(body))
		if message == "" {
			message = fmt.Sprintf("Responses API returned %d", resp.StatusCode)
		}
		return (TestStreamOutput{}).Error(c, message)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to read image response: %s", err.Error()))
	}
	body = RedactAgentIdentityBody(ctx, s.read, credentialAccount, body)

	results, _, _, _, _, err := openai.CollectOpenAIImagesFromResponsesBody(body, time.Now)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to parse image response: %s", err.Error()))
	}
	if len(results) == 0 {
		return (TestStreamOutput{}).Error(c, "No images returned from responses API")
	}

	for _, item := range results {
		if item.RevisedPrompt != "" {
			(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "content", Text: item.RevisedPrompt})
		}
		mimeType := openai.OpenAIImageOutputMIMEType(item.OutputFormat)
		(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{
			Type:     "image",
			ImageURL: "data:" + mimeType + ";base64," + item.Result,
			MimeType: mimeType,
		})
	}

	(TestStreamOutput{}).SendEvent(c, accountcore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

// OpenAIAccountTest 固定账号存储、共享传输和策略端口，单次状态只留在 TestRun。
type OpenAIAccountTest struct {
	Store        OpenAIAccountTestStore
	Transport    QoderTransport
	ValidateURL  func(string) (string, error)
	ApplyRouting func(*TestRun, *accountcore.Record, *http.Request, bool)
	ResolveTLS   func(*TestRun, *accountcore.Record) *tlsfingerprint.Profile
	EnsureTask   func(context.Context, *accountcore.Record, string) error
	ModelRules   openai.CodexModelRules
	Prepare      func(*TestRun, *accountcore.Record) error
}

// OpenAIAccountTestStore 只暴露测试路径原本使用的字段操作。
type OpenAIAccountTestStore interface {
	GetByID(context.Context, int64) (*accountcore.Record, error)
	UpdateExtra(context.Context, int64, map[string]any) error
	SetError(context.Context, int64, string) error
	ClearError(context.Context, int64) error
	SetRateLimited(context.Context, int64, time.Time) error
	accountcore.OpenAIPlanWriter
}

func (s *OpenAIAccountTest) read(ctx context.Context, id int64) (*accountcore.Record, error) {
	return s.Store.GetByID(ctx, id)
}
func (s *OpenAIAccountTest) agentHeaders(ctx context.Context, value *accountcore.Record) (http.Header, error) {
	headers, _, err := AgentIdentityHeaders(ctx, value, s.EnsureTask)
	return headers, err
}
func (s *OpenAIAccountTest) normalizeModel(value *accountcore.Record, model string) string {
	if value.UsesOpenAICodexProtocol() {
		return openai.NormalizeCodexModel(model, s.ModelRules)
	}
	return strings.TrimSpace(model)
}
func setTestChatGPTHeaders(headers http.Header, value *accountcore.Record) {
	if headers == nil || value == nil || !value.IsOpenAIOAuthLike() {
		return
	}
	openai.SetChatGPTAccountHeaders(headers, value.GetChatGPTAccountID(), value.IsChatGPTAccountFedRAMP())
}

func mergeTestExtraUpdates(base, more map[string]any) map[string]any {
	if len(base) == 0 && len(more) == 0 {
		return nil
	}
	result := make(map[string]any, len(base)+len(more))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range more {
		result[key] = value
	}
	return result
}
func testCodexFingerprint(value *accountcore.Record, headers http.Header) *openai.FingerprintIDs {
	if value == nil || !value.IsOpenAIOAuthLike() {
		return nil
	}
	mode := accountcore.CodexFingerprintModeFromExtra(value.Extra)
	if mode == accountcore.CodexFingerprintOff {
		return nil
	}
	clientSession := ""
	if headers != nil {
		clientSession = openai.ExtractClientSessionID(headers)
	}
	return CodexFingerprintIDs(value, clientSession, mode)
}

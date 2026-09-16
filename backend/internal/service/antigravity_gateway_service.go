package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

const (
	antigravityForwardBaseURLEnv  = "GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL"
	antigravityFallbackSecondsEnv = "GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS"
)

const antigravityProjectIDFallbackCredentialKey = "antigravity_project_id"

// AntigravityGatewayService 处理 Antigravity 平台的 API 转发
type AntigravityGatewayService struct {
	nativeAttemptActivity func() (func(), error)
	accountRepo           AccountRepository
	tokenProvider         *AntigravityTokenProvider
	rateLimitService      *RateLimitService
	httpUpstream          HTTPUpstream
	settingService        *SettingService
	cache                 GatewayCache // 用于模型级限流时清除粘性会话绑定
	schedulerSnapshot     *SchedulerSnapshotService
	internal500Cache      Internal500CounterCache // INTERNAL 500 渐进惩罚计数器
}

func (s *AntigravityGatewayService) upstreamErrorBodyReadLimit() int64 {
	limit := gatewayUpstreamErrorBodyReadLimit
	if s != nil && s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Gateway.LogUpstreamErrorBody && s.settingService.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.settingService.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

func (s *AntigravityGatewayService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, s.upstreamErrorBodyReadLimit()))
	return body
}

func NewAntigravityGatewayService(
	accountRepo AccountRepository,
	cache GatewayCache,
	schedulerSnapshot *SchedulerSnapshotService,
	tokenProvider *AntigravityTokenProvider,
	rateLimitService *RateLimitService,
	httpUpstream HTTPUpstream,
	settingService *SettingService,
	internal500Cache Internal500CounterCache,
) *AntigravityGatewayService {
	return &AntigravityGatewayService{
		accountRepo:       accountRepo,
		tokenProvider:     tokenProvider,
		rateLimitService:  rateLimitService,
		httpUpstream:      httpUpstream,
		settingService:    settingService,
		cache:             cache,
		schedulerSnapshot: schedulerSnapshot,
		internal500Cache:  internal500Cache,
	}
}

// GetTokenProvider 返回 token provider
func (s *AntigravityGatewayService) GetTokenProvider() *AntigravityTokenProvider {
	return s.tokenProvider
}

// getLogConfig 获取上游错误日志配置
// 返回是否记录日志体和最大字节数
func (s *AntigravityGatewayService) getLogConfig() (logBody bool, maxBytes int) {
	maxBytes = 2048 // 默认值
	if s.settingService == nil || s.settingService.cfg == nil {
		return false, maxBytes
	}
	cfg := s.settingService.cfg.Gateway
	if cfg.LogUpstreamErrorBodyMaxBytes > 0 {
		maxBytes = cfg.LogUpstreamErrorBodyMaxBytes
	}
	return cfg.LogUpstreamErrorBody, maxBytes
}

// getUpstreamErrorDetail 获取上游错误详情（用于日志记录）
func (s *AntigravityGatewayService) getUpstreamErrorDetail(body []byte) string {
	logBody, maxBytes := s.getLogConfig()
	if !logBody {
		return ""
	}
	return truncateString(string(body), maxBytes)
}

// checkErrorPolicy nil 安全的包装
func (s *AntigravityGatewayService) checkErrorPolicy(ctx context.Context, account *Account, statusCode int, body []byte, requestedModel ...string) ErrorPolicyResult {
	if s.rateLimitService == nil {
		return ErrorPolicyNone
	}
	return s.rateLimitService.CheckErrorPolicy(ctx, account, statusCode, body, firstRequestedModel(requestedModel))
}

// applyErrorPolicy 应用错误策略结果，返回是否应终止当前循环及应返回的状态码。
// ErrorPolicyCustomSkipped 时 outStatus 为 500（前端约定：未命中的错误返回 500）。
func (s *AntigravityGatewayService) applyErrorPolicy(p antigravityRetryLoopParams, statusCode int, headers http.Header, respBody []byte) (handled bool, outStatus int, retErr error) {
	modelKey := resolveFinalAntigravityModelKey(p.ctx, p.account, p.requestedModel)
	switch s.checkErrorPolicy(p.ctx, p.account, statusCode, respBody, modelKey) {
	case ErrorPolicyCustomSkipped:
		if s.handleAntigravityModelRateLimitBeforePolicy(p, statusCode, headers, respBody) {
			return true, statusCode, nil
		}
		return true, http.StatusInternalServerError, nil
	case ErrorPolicyCustomMatched:
		if s.handleAntigravityModelRateLimitBeforePolicy(p, statusCode, headers, respBody) {
			return true, statusCode, nil
		}
		_ = p.handleError(p.ctx, p.prefix, p.account, statusCode, headers, respBody,
			p.requestedModel, p.groupID, p.sessionHash, p.isStickySession)
		return true, statusCode, nil
	case ErrorPolicyTempUnscheduled:
		slog.Info("temp_unschedulable_matched",
			"prefix", p.prefix, "status_code", statusCode, "account_id", p.account.ID)
		return true, statusCode, &AntigravityAccountSwitchError{OriginalAccountID: p.account.ID, RateLimitedModel: p.requestedModel, IsStickySession: p.isStickySession}
	case ErrorPolicyPoolBypassed:
		// Antigravity 正常账号不开放池模式；导入的异常组合也不应被当作自定义未命中返回 500。
		return false, statusCode, nil
	}
	return false, statusCode, nil
}

func (s *AntigravityGatewayService) handleAntigravityModelRateLimitBeforePolicy(p antigravityRetryLoopParams, statusCode int, headers http.Header, respBody []byte) bool {
	if statusCode != http.StatusTooManyRequests && statusCode != http.StatusServiceUnavailable {
		return false
	}
	if p.account == nil || p.account.Platform != PlatformAntigravity {
		return false
	}
	_, shouldRateLimitModel, waitDuration, modelName, isModelCapacityExhausted := shouldTriggerAntigravitySmartRetry(p.account, respBody)
	if isModelCapacityExhausted || !shouldRateLimitModel || strings.TrimSpace(modelName) == "" {
		return false
	}
	rateLimitDuration := waitDuration
	if rateLimitDuration <= 0 {
		rateLimitDuration = antigravityDefaultRateLimitDuration
	}
	resetAt := time.Now().Add(rateLimitDuration)
	if !s.setAntigravityModelRateLimits(p.ctx, p.accountRepo, p.account, modelName, p.prefix, statusCode, resetAt, false) {
		return false
	}
	s.clearStickySession(p.ctx, p.groupID, p.sessionHash)
	logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d model_rate_limited_before_error_policy model=%s account=%d reset_in=%v",
		p.prefix, statusCode, modelName, p.account.ID, rateLimitDuration)
	return true
}

// mapAntigravityModel 获取映射后的模型名
// 完全依赖映射配置：账户映射（通配符）→ 默认映射兜底（DefaultAntigravityModelMapping）
// 注意：返回空字符串表示模型不被支持，调度时会过滤掉该账号
func mapAntigravityModel(account *Account, requestedModel string) string {
	if account == nil {
		return ""
	}
	requestedModel = strings.TrimPrefix(requestedModel, "models/")

	// 获取映射表（未配置时自动使用 DefaultAntigravityModelMapping）
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return "" // 无映射配置（非 Antigravity 平台）
	}

	// 通过映射表查询（支持精确匹配 + 通配符）
	mapped := account.GetMappedModel(requestedModel)

	// 判断是否映射成功（mapped != requestedModel 说明找到了映射规则）
	if mapped != requestedModel {
		return mapped
	}

	// 如果 mapped == requestedModel，检查是否在映射表中配置（精确或通配符）
	// 这区分两种情况：
	// 1. 映射表中有 "model-a": "model-a"（显式透传）→ 返回 model-a
	// 2. 通配符匹配 "claude-*": "claude-sonnet-4-5" 恰好目标等于请求名 → 返回 model-a
	// 3. 映射表中没有 model-a 的配置 → 返回空（不支持）
	if account.IsModelSupported(requestedModel) {
		return requestedModel
	}

	// 未在映射表中配置的模型，返回空字符串（不支持）
	return ""
}

// getMappedModel 获取映射后的模型名
// 完全依赖映射配置：账户映射（通配符）→ 默认映射兜底
func (s *AntigravityGatewayService) getMappedModel(account *Account, requestedModel string) string {
	return mapAntigravityModel(account, requestedModel)
}

func resolveAntigravityProjectID(account *Account) (string, error) {
	return accountcore.ResolveAntigravityProjectID(AccountRecordView(account), errAntigravityProjectIDRequired)
}

// IsModelSupported 检查模型是否被支持
// 所有 claude- 和 gemini- 前缀的模型都能通过映射或透传支持
func (s *AntigravityGatewayService) IsModelSupported(requestedModel string) bool {
	return strings.HasPrefix(requestedModel, "claude-") ||
		strings.HasPrefix(requestedModel, "gemini-")
}

type TestConnectionResult = antigravity.TestConnectionResult

// TestConnection 测试 Antigravity 账号连接；prompt 为空时使用最小默认提示词。
// 复用 antigravityRetryLoop 的完整重试 / credits overages / 智能重试逻辑，
// 与真实调度行为一致。差异：不做账号切换（测试指定账号）、不记录 ops 错误。
func (s *AntigravityGatewayService) TestConnection(ctx context.Context, account *Account, modelID string, prompts ...string) (*TestConnectionResult, error) {

	// 获取 token
	if s.tokenProvider == nil {
		return nil, errors.New("antigravity token provider not configured")
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("获取 access_token 失败: %w", err)
	}

	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		return nil, err
	}

	// 模型映射
	mappedModel := s.getMappedModel(account, modelID)
	if mappedModel == "" {
		return nil, fmt.Errorf("model %s not in whitelist", modelID)
	}

	// 构建请求体
	var requestBody []byte
	testPrompt := "."
	if len(prompts) > 0 && strings.TrimSpace(prompts[0]) != "" {
		testPrompt = strings.TrimSpace(prompts[0])
	}
	if strings.HasPrefix(modelID, "gemini-") {
		requestBody, err = s.buildGeminiTestRequest(projectID, mappedModel, testPrompt)
	} else {
		requestBody, err = s.buildClaudeTestRequest(projectID, mappedModel, testPrompt)
	}
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}

	// 代理 URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 复用 antigravityRetryLoop：完整的重试 / credits overages / 智能重试
	prefix := fmt.Sprintf("[antigravity-Test] account=%d(%s)", account.ID, account.Name)
	p := antigravityRetryLoopParams{
		ctx:            ctx,
		prefix:         prefix,
		account:        account,
		proxyURL:       proxyURL,
		accessToken:    accessToken,
		action:         "streamGenerateContent",
		body:           requestBody,
		c:              nil, // 无 gin.Context → 跳过 ops 追踪
		httpUpstream:   s.httpUpstream,
		settingService: s.settingService,
		accountRepo:    s.accountRepo,
		requestedModel: modelID,
		handleError:    testConnectionHandleError,
	}

	adapter, input := s.antigravityRetryAdapter(p)
	return antigravity.Probe(ctx, input, adapter.Options, mappedModel, s.upstreamErrorBodyReadLimit, s.nativeAttemptActivity)
}

// testConnectionHandleError 是 TestConnection 使用的轻量 handleError 回调。
// 仅记录日志，不做 ops 错误追踪或粘性会话清除。
func testConnectionHandleError(
	_ context.Context, prefix string, account *Account,
	statusCode int, _ http.Header, body []byte,
	requestedModel string, _ int64, _ string, _ bool,
) *handleModelRateLimitResult {
	logger.LegacyPrintf("service.antigravity_gateway",
		"%s test_handle_error status=%d model=%s account=%d body=%s",
		prefix, statusCode, requestedModel, account.ID, truncateForLog(body, 200))
	return nil
}

func (s *AntigravityGatewayService) buildGeminiTestRequest(projectID, model string, prompts ...string) ([]byte, error) {
	return antigravity.BuildGeminiTestRequest(projectID, model, prompts...)
}

func (s *AntigravityGatewayService) buildClaudeTestRequest(projectID, mappedModel string, prompts ...string) ([]byte, error) {
	return antigravity.BuildClaudeTestRequest(projectID, mappedModel, prompts...)
}

func (s *AntigravityGatewayService) getClaudeTransformOptions(ctx context.Context) antigravity.TransformOptions {
	opts := antigravity.DefaultTransformOptions()
	if s.settingService == nil {
		return opts
	}
	opts.EnableIdentityPatch = s.settingService.IsIdentityPatchEnabled(ctx)
	opts.IdentityPatch = s.settingService.GetIdentityPatchPrompt(ctx)
	return opts
}

// BindNativeAttemptActivity 仅在构造阶段绑定同一 app 屏障，停止等待已进入尝试。
func (s *AntigravityGatewayService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

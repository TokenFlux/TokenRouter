package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	grokforward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/grokforward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	uuid "github.com/google/uuid"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) forwardGrokResponses(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	adapter := &grokForwardAdapter{s: s, c: c, account: account}
	result, err := grokforward.Forward(ctx, adapter, adapter.options(), adapter.input(body, originalModel, reqStream, startTime))
	return legacyGrokForwardResult(result), err
}

func isGrokInvalidEncryptedContentResponse(statusCode int, body []byte) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		IsGrokInvalidEncryptedContentResponse(statusCode, body)
}

func isGrokCompactionReplayDecodeError(statusCode int, body []byte) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		IsGrokCompactionReplayDecodeError(statusCode, body)
}

func sanitizeGrokCompactionReplayBody(body []byte) ([]byte, bool, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		SanitizeGrokCompactionReplayBody(body)
}

func requestHasGrokEncryptedReasoning(body []byte) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		RequestHasGrokEncryptedReasoning(body)
}

func markGrokEncryptedContentStripRetried(ctx context.Context) context.Context {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		MarkGrokEncryptedContentStripRetried(ctx)
}

func grokEncryptedContentStripRetried(ctx context.Context) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		GrokEncryptedContentStripRetried(ctx)
}

func trimGrokInvalidEncryptedContentRetryBody(body []byte) ([]byte, bool, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		TrimGrokInvalidEncryptedContentRetryBody(body)
}

func patchGrokResponsesBody(body []byte, upstreamModel string) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		PatchGrokResponsesBody(body, upstreamModel)
}

func normalizeGrokChatReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		NormalizeGrokChatReasoningEffort(body, upstreamModel)
}

func sanitizeGrokResponsesInput(body []byte) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		SanitizeGrokResponsesInput(body)
}

func (s *OpenAIGatewayService) bridgeGrokComposerImageInputs(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
) ([]byte, openai.ForwardUsage, bool, error) {
	return grok.BridgeComposerImages(body, func(imageURL string, index int) (string, openai.ForwardUsage, error) {
		return s.describeGrokComposerImage(ctx, c, account, token, imageURL, index)
	})
}

// describeGrokComposerImage 复用 Grok Responses 转发配置执行单张图片预检，
// 并沿用账号快照、错误记录和故障转移策略。
func (s *OpenAIGatewayService) describeGrokComposerImage(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	token string,
	imageURL string,
	index int,
) (string, openai.ForwardUsage, error) {
	adapter := &grokForwardAdapter{s: s, c: c, account: account, token: token}
	return grokforward.DescribeImage(ctx, adapter, adapter.options(), adapter.input(nil, "", false, time.Time{}), imageURL, index)
}

func buildGrokResponsesRequest(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, token, cacheIdentity string, cfg *config.Config, settings ...*gatewayprovider.RuntimeReaders) (*http.Request, error) {
	targetURL, err := buildGrokResponsesURL(account, cfg, settings...)
	if err != nil {
		return nil, err
	}
	beta := ""
	if c != nil {
		beta = c.GetHeader("OpenAI-Beta")
	}
	return grok.BuildResponsesRequest(ctx, body, grok.ResponsesRequestOptions{

		URL: targetURL,

		Token: token,

		CacheIdentity: cacheIdentity,

		OAuth: account.View().IsGrokOAuth(),

		OpenAIBeta: beta,

		Profile: func(ctx context.Context) context.Context {
			return upstream.WithHTTPUpstreamProfile(ctx, upstream.HTTPUpstreamProfileGrok)
		},

		ApplyOverrides: bindAccountHeaders(account),
	})
}

func (s *OpenAIGatewayService) updateGrokUsageSnapshot(ctx context.Context, account *gatewayprovider.ExecutionAccount, snapshot *grok.QuotaSnapshot) {
	s.updateGrokUsageSnapshotWithRateLimit(ctx, account, snapshot, true)
}

func (s *OpenAIGatewayService) updateGrokUsageSnapshotWithRateLimit(ctx context.Context, account *gatewayprovider.ExecutionAccount, snapshot *grok.QuotaSnapshot, installRateLimit bool) {
	if s == nil || account == nil || account.Record.ID <= 0 || snapshot == nil {
		return
	}
	accountID := account.Record.ID
	now := time.Now()
	resetAt, hasActiveLimit := grokRateLimitResetAtForAccount(account, snapshot, now)
	if hasActiveLimit {
		accountcore.NormalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
	}
	recovery := isSuccessfulGrokRateLimitRecovery(account, snapshot)
	critical := snapshot.StatusCode == http.StatusTooManyRequests || hasActiveLimit || recovery
	if s.codexSnapshotThrottle != nil {
		allowed := s.codexSnapshotThrottle.Allow(accountID, now)
		if !critical && !allowed {
			return
		}
	}

	updates := map[string]any{
		grokQuotaSnapshotExtraKey: snapshot,
	}
	// 同时派生 grokThresholdCandidates 评估器读取的调度阈值扩展字段 grok_sched_*。
	// 缺少此写入逻辑时，管理员配置的 Grok 自动暂停阈值无法触发。
	for k, v := range accountcore.BuildGrokSchedulerExtraUpdates(snapshot) {
		updates[k] = v
	}
	stateCtx := ctx
	if hasActiveLimit {
		var cancel context.CancelFunc
		stateCtx, cancel = openAIAccountStateContext(ctx)
		defer cancel()
	}
	// 请求路径中的 Account 指针来自每次 Redis/DB 解码，不是进程内共享缓存；
	// 这里与 token 刷新和限流写入保持一致，调用方不得跨 goroutine 复用同一指针。
	if account.Record.Extra == nil {
		account.Record.Extra = map[string]any{}
	}
	account.Record.Extra[grokQuotaSnapshotExtraKey] = snapshot
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(stateCtx, accountID, updates)
	}
	// 池模式上游本身负责在真实账号池中切换，额度头只作为观测数据保留，不能反向
	// 冷却本地这个聚合账号。非池模式仍将错误响应或成功后耗尽的窗口写成真实限流。
	if installRateLimit && hasActiveLimit && !account.View().IsPoolMode() {
		s.rateLimitGrok(stateCtx, account, resetAt)
	} else if recovery {
		clearGrokRateLimitAfterRecovery(stateCtx, s.accountRepo, account)
	}
}

// updateGrokUsageFromResponse 委托原生观测编排，账号领域写入继续使用唯一旧能力。
func (s *OpenAIGatewayService) updateGrokUsageFromResponse(ctx context.Context, account *gatewayprovider.ExecutionAccount, headers http.Header, statusCode int) {
	grokforward.ObserveResponse(ctx, grokObservationAdapter{s: s, account: account}, headers, statusCode, grokRequestedModelFromCtx(ctx))
}

func grokRateLimitResetAtForAccount(account *gatewayprovider.ExecutionAccount, snapshot *grok.QuotaSnapshot, now time.Time) (time.Time, bool) {
	return accountcore.GrokRateLimitResetAtForAccount(gatewayprovider.ExecutionRecord(account), snapshot, now)
}

func normalizeGrokRateLimitResetAt(account *gatewayprovider.ExecutionAccount, resetAt, now time.Time) time.Time {
	return accountcore.NormalizeGrokRateLimitResetAt(gatewayprovider.ExecutionRecord(account), resetAt, now)
}

func isSuccessfulGrokRateLimitRecovery(account *gatewayprovider.ExecutionAccount, snapshot *grok.QuotaSnapshot) bool {
	return accountcore.IsSuccessfulGrokRateLimitRecovery(gatewayprovider.ExecutionRecord(account), snapshot)
}

func (s *OpenAIGatewayService) rateLimitGrok(ctx context.Context, account *gatewayprovider.ExecutionAccount, resetAt time.Time) {
	if s == nil || account == nil {
		return
	}
	now := time.Now()
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, now)

	runtimeUntil := resetAt
	if account.Record.TempUnschedulableUntil != nil && account.Record.TempUnschedulableUntil.After(runtimeUntil) {
		runtimeUntil = *account.Record.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, runtimeUntil, "429")
	persistGrokRateLimit(ctx, s.accountRepo, account, resetAt)

	// 扩散短期团队与模型冷却，使同一 xAI 团队的关联 OAuth 账号跳过热点模型，
	// 无需等待每个账号分别收到 429。模型优先取最新请求上下文；为空时
	// markGrokTeamModelRateLimit 不执行操作。
	if model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string); model != "" {
		markGrokTeamModelRateLimit(account, model, accountcore.ResolveGrokTeamRateLimitUntil(resetAt, now))
	}
}

// grokTeamRateLimitModelContextKey 在上下文中携带团队冷却所需的上游模型。
type grokTeamRateLimitModelContextKey struct{}

// withGrokTeamRateLimitModel 附加上游模型名，供团队与模型冷却等限流副作用使用。
// 模型为空时可安全调用。
func withGrokTeamRateLimitModel(ctx context.Context, model string) context.Context {
	model = strings.TrimSpace(model)
	if model == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, grokTeamRateLimitModelContextKey{}, model)
}

// grokRequestedModelFromCtx 读取当前请求实际使用的 Grok 上游模型。
func grokRequestedModelFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string)
	return strings.TrimSpace(model)
}

func persistGrokTransientModelCooldown(account *gatewayprovider.ExecutionAccount, decision grok.GrokUpstreamFailureDecision) bool {
	if account == nil {
		return false
	}
	model := strings.TrimSpace(decision.Model)
	if model == "" {
		return false
	}
	cooldown := decision.Cooldown
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	accountcore.MarkGrokModelTransientBlock(account.Record.ID, model, time.Now().Add(cooldown))
	return true
}

// applyGrokAccountUpstreamError 先处理精确模型状态和显式策略，再处理非池模式的 Grok 默认状态。
// 调用方应传入账号映射后的模型；这里仅补做幂等的平台规范化，绝不再次执行账号映射。
func (s *OpenAIGatewayService) applyGrokAccountUpstreamError(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	canonicalModel ...string,
) accountcore.UpstreamErrorDecision {
	if s == nil || account == nil {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if isOpenAIAccountPolicyRequestScopedError(account, statusCode, responseBody) {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if model := requeststate.FirstHealthModel(canonicalModel); model != "" {
		canonicalModel = []string{gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(model)}
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	stateCtx = requeststate.WithHealthModel(stateCtx, canonicalModel)

	now := time.Now()
	quotaSnapshot := grok.ParseQuotaObservation(headers, statusCode, now)
	quotaModel := requeststate.FirstHealthModel(canonicalModel)
	if quotaModel == "" {
		quotaModel = grokRequestedModelFromCtx(ctx)
	}
	stampGrokQuotaSnapshotForPlan(account, quotaSnapshot, quotaModel)
	// 模型容量 429 属于请求压力，只保存额度观测，不安装账号级限流状态。
	snapshotFailure := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, quotaModel)
	s.updateGrokUsageSnapshotWithRateLimit(stateCtx, account, quotaSnapshot, snapshotFailure.Class != grok.GrokFailureModelCapacity)

	decision := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(account), statusCode)
	if s.rateLimitService != nil {
		if account.View().IsPoolMode() || account.View().IsCustomErrorCodesEnabled() {
			decision.Policy = s.rateLimitService.UpstreamHealth().ApplyExplicitErrorPolicy(stateCtx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(stateCtx, statusCode, nil, responseBody, canonicalModel))
			decision.StopScheduling = decision.Policy == accountcore.ErrorPolicyCustomMatched || decision.Policy == accountcore.ErrorPolicyTempUnscheduled
		} else {
			decision = accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case accountcore.ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		return decision
	case accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}

	if s.rateLimitService != nil && len(canonicalModel) > 0 &&
		s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, canonicalModel[0], statusCode, responseBody) {
		decision.StopScheduling = true
		return decision
	}
	// 普通账号先保留模型不存在等精确处理，再应用管理员临时规则。
	if s.rateLimitService != nil && statusCode != http.StatusUnauthorized &&
		!account.View().IsPoolMode() && !account.View().IsCustomErrorCodesEnabled() &&
		s.rateLimitService.HandleTempUnschedulable(stateCtx, account, statusCode, responseBody, canonicalModel...) {
		decision.Policy = accountcore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if requeststate.FirstHealthModel(canonicalModel) == "" {
			s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		}
		return decision
	}

	// Grok API Key 的 5xx 与 OpenAI API Key 共用账号+最终模型的瞬态冷却；
	// OAuth 和模型未知的请求继续沿用账号级退避，避免扩大既有行为变化。
	model := requeststate.FirstHealthModel(canonicalModel)
	if model == "" {
		model = quotaModel
	}
	if account.Record.Type == capability.AccountTypeAPIKey && model != "" && statusCode >= 500 &&
		shouldCooldownOpenAITransientUpstreamError(statusCode, responseBody) {
		s.recordOpenAICompatibleModelTransientFailure(account, model)
		return decision
	}

	// 响应体中的免费额度、账单、空输出和容量语义优先于通用状态码处理。
	failure := grok.ClassifyGrokUpstreamFailure(statusCode, responseBody, model)
	if failure.ShouldCooldown && failure.Class != grok.GrokFailureNone && failure.Class != grok.GrokFailureRateLimit {
		if failure.Class == grok.GrokFailureFreeUsage {
			if resetAt, limited := grokRateLimitResetAtForAccount(account, quotaSnapshot, now); limited && resetAt.After(now) {
				if failure.Model != "" && accountcore.IsGrokModelSpecificFreeUsage(strings.ToLower(failure.Reason), failure.Model) {
					accountcore.MarkGrokModelQuotaBlock(account.Record.ID, failure.Model, resetAt)
					decision.StopScheduling = true
					return decision
				}
				s.rateLimitGrok(stateCtx, account, resetAt)
				decision.StopScheduling = true
				return decision
			}
		}
		if s.applyGrokUpstreamFailureDecision(stateCtx, account, failure) {
			decision.StopScheduling = true
			return decision
		}
	}

	switch statusCode {
	case http.StatusUnauthorized:
		s.tempUnscheduleGrok(stateCtx, account, 10*time.Minute, "grok credentials unauthorized")
		decision.StopScheduling = true
		return decision
	case http.StatusPaymentRequired:
		// 402 表示当前账号计费不可用，短期排除以避免后续请求反复命中。
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok payment required")
		decision.StopScheduling = true
		return decision
	case http.StatusForbidden:
		if s.applyGrokForbiddenPolicy(stateCtx, account, responseBody) {
			decision.StopScheduling = true
			return decision
		}
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok access or entitlement denied")
		decision.StopScheduling = true
		return decision
	case http.StatusMethodNotAllowed:
		// 当前账号不支持所选 Grok 端点，临时排除可避免粘性会话反复命中同一账号。
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok endpoint not supported (405)")
		decision.StopScheduling = true
		return decision
	case http.StatusTooManyRequests:
		// updateGrokUsageSnapshot 已同时写入运行时和持久化限流状态。
		decision.StopScheduling = true
		return decision
	default:
		if statusCode >= 500 {
			s.tempUnscheduleGrok(stateCtx, account, 2*time.Minute, "grok upstream temporary error")
			decision.StopScheduling = true
			return decision
		}
	}
	return decision
}

func (s *OpenAIGatewayService) tempUnscheduleGrok(ctx context.Context, account *gatewayprovider.ExecutionAccount, cooldown time.Duration, reason string) {
	if s == nil || account == nil {
		return
	}
	until := time.Now().Add(cooldown)
	if account.Record.TempUnschedulableUntil != nil && account.Record.TempUnschedulableUntil.After(until) {
		until = *account.Record.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, until, reason)
	if s.accountRepo != nil {
		stateCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		_ = s.accountRepo.SetTempUnschedulable(stateCtx, account.Record.ID, until, reason)
	}
}

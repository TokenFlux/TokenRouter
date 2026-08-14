package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	"github.com/BrandonVee/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

const maxAPIKeyAuthorizationHeaderBytes = service.MaxAPIKeyCredentialBytes + 128

// NewAPIKeyAuthMiddleware 创建 API Key 认证中间件
func NewAPIKeyAuthMiddleware(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) APIKeyAuthMiddleware {
	return APIKeyAuthMiddleware(apiKeyAuthWithSubscription(apiKeyService, subscriptionService, cfg))
}

// apiKeyAuthWithSubscription API Key认证中间件（支持订阅验证）
//
// 中间件职责分为两层：
//   - 鉴权（Authentication）：验证 Key 有效性、用户状态、IP 限制 —— 始终执行
//   - 计费执行（Billing Enforcement）：过期/配额/订阅/余额检查 —— skipBilling 时整块跳过
//
// /v1/usage 与既有批任务管理只需鉴权，不需要计费执行。
// usage 允许过期/配额耗尽的 Key 查询自身用量；
// 批任务管理允许已耗尽额度的 Key 取回或清理自己的既有任务。
func apiKeyAuthWithSubscription(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ── 1. 提取 API Key ──────────────────────────────────────────
		if rejectInvalidAuthAbuse(c, apiKeyService) {
			AbortWithError(c, http.StatusTooManyRequests, "INVALID_AUTH_RATE_LIMITED", "Too many invalid authentication attempts; retry later")
			return
		}

		if apiKeyHeadersTooLarge(c) {
			recordInvalidAuthFailure(c, apiKeyService)
			MarkIngressRejected(c, IngressRejectInvalidAPIKey)
			AbortWithError(c, http.StatusUnauthorized, "INVALID_API_KEY", "Invalid API key")
			return
		}

		queryKey := strings.TrimSpace(c.Query("key"))
		queryApiKey := strings.TrimSpace(c.Query("api_key"))
		if queryKey != "" || queryApiKey != "" {
			recordInvalidAuthFailure(c, apiKeyService)
			MarkIngressRejected(c, IngressRejectQueryAPIKeyDeprecated)
			AbortWithError(c, 400, "api_key_in_query_deprecated", "API key in query parameter is deprecated. Please use Authorization header instead.")
			return
		}

		// 尝试从Authorization header中提取API key (Bearer scheme)
		authHeader := c.GetHeader("Authorization")
		var apiKeyString string

		if authHeader != "" {
			// 验证Bearer scheme
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				apiKeyString = strings.TrimSpace(parts[1])
			}
		}

		// 如果Authorization header中没有，尝试从x-api-key header中提取
		if apiKeyString == "" {
			apiKeyString = c.GetHeader("x-api-key")
		}
		if len(apiKeyString) > service.MaxAPIKeyCredentialBytes {
			recordInvalidAuthFailure(c, apiKeyService)
			MarkIngressRejected(c, IngressRejectInvalidAPIKey)
			AbortWithError(c, http.StatusUnauthorized, "INVALID_API_KEY", "Invalid API key")
			return
		}

		// 如果x-api-key header中没有，尝试从x-goog-api-key header中提取（Gemini CLI兼容）
		if apiKeyString == "" {
			apiKeyString = c.GetHeader("x-goog-api-key")
		}

		// 如果所有header都没有API key
		if apiKeyString == "" {
			recordInvalidAuthFailure(c, apiKeyService)
			if hasAPIKeyCredentialInput(c) {
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
			} else {
				MarkIngressRejected(c, IngressRejectAPIKeyRequired)
			}
			AbortWithError(c, 401, "API_KEY_REQUIRED", "API key is required in Authorization header (Bearer scheme), x-api-key header, or x-goog-api-key header")
			return
		}

		// ── 2. 验证 Key 存在 ─────────────────────────────────────────

		apiKey, err := apiKeyService.GetByKey(c.Request.Context(), apiKeyString)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				recordInvalidAuthFailure(c, apiKeyService)
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
				AbortWithError(c, 401, "INVALID_API_KEY", "Invalid API key")
				return
			}
			if errors.Is(err, service.ErrGroupDisabledForUser) {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
				AbortWithError(c, 403, "GROUP_DISABLED_FOR_USER", "API Key 所属公开分组已被禁用")
				return
			}
			if errors.Is(err, service.ErrAPIKeyAuthOverloaded) {
				MarkIngressRejected(c, IngressRejectAPIKeyAuthOverloaded)
				AbortWithError(c, http.StatusServiceUnavailable, "API_KEY_AUTH_OVERLOADED", "API key authentication is temporarily unavailable")
				return
			}
			if abortTeamAPIKeyError(c, err) {
				return
			}
			AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate API key")
			return
		}

		// apiKey 已加载（含 User/Group）。即便后续因分组停用/Key 停用/用户停用/
		// IP 限制等早退中断，也让 Ops 错误日志能回退取到 user/group/platform。
		SetOpsFallbackAPIKey(c, apiKey)

		// ── 3. 基础鉴权（始终执行） ─────────────────────────────────

		// disabled / 未知状态 → 无条件拦截（expired 和 quota_exhausted 留给计费阶段）
		if !apiKey.IsActive() &&
			apiKey.Status != service.StatusAPIKeyExpired &&
			apiKey.Status != service.StatusAPIKeyQuotaExhausted {
			MarkIngressRejected(c, IngressRejectAPIKeyDisabled)
			AbortWithError(c, 401, "API_KEY_DISABLED", "API key is disabled")
			return
		}
		if err := apiKeyService.ValidateTeamKeyLifecycle(apiKey); err != nil {
			if abortTeamAPIKeyError(c, err) {
				return
			}
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to validate team API key")
			return
		}
		if !isAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path) {
			if err := apiKeyService.CheckTeamMemberLimits(apiKey); err != nil {
				if abortTeamAPIKeyError(c, err) {
					return
				}
				AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to validate team member limits")
				return
			}
		}

		// 检查 IP 限制（白名单/黑名单）
		// 注意：错误信息故意模糊，避免暴露具体的 IP 限制机制
		if len(apiKey.IPWhitelist) > 0 || len(apiKey.IPBlacklist) > 0 {
			clientIP := ip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
			allowed, _ := ip.CheckIPRestrictionWithCompiledRules(clientIP, apiKey.CompiledIPWhitelist, apiKey.CompiledIPBlacklist)
			if !allowed {
				if clientIP == "" {
					clientIP = "unknown"
				}
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonIPRestriction)
				MarkIngressRejected(c, IngressRejectIPRestricted)
				AbortWithError(c, 403, "ACCESS_DENIED", fmt.Sprintf("Access denied. Your IP is %s", clientIP))
				return
			}
		}

		// 检查关联的用户
		if apiKey.User == nil {
			AbortWithError(c, 401, "USER_NOT_FOUND", "User associated with API key not found")
			return
		}

		// 检查用户状态
		if !apiKey.User.IsActive() {
			MarkIngressRejected(c, IngressRejectUserInactive)
			AbortWithError(c, 401, "USER_INACTIVE", "User account is not active")
			return
		}
		apiKey, err = resolveCompositeAPIKeyRequest(c, apiKeyService, apiKey)
		if err != nil {
			abortCompositeKeyError(c, err)
			return
		}
		SetOpsFallbackAPIKey(c, apiKey)
		if abortIfAPIKeyGroupUnavailable(c, apiKey) {
			return
		}
		if abortIfAPIKeyGroupNotAllowed(c, apiKey) {
			return
		}
		ctx := context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID)
		ctx = context.WithValue(ctx, ctxkey.APIKeyFastModePolicy, apiKey.FastModePolicy)
		c.Request = c.Request.WithContext(ctx)
		applyAPIKeyModelRedirect(c, apiKey)
		// 批任务管理只读取已有数据或释放冻结；即使任务耗尽额度，结果仍应可取回或取消。
		skipBilling := isAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) ||
			isBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) ||
			(apiKey.IsComposite && isGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path))

		// ── 4. SimpleMode → early return ─────────────────────────────

		if cfg.RunMode == config.RunModeSimple {
			// 简易模式不执行计费拦截，但用量查询和复合 Key 模型列表仍需读取指定套餐范围。
			if shouldResolveAPIKeyBillingInSimpleMode(apiKey, c.Request.Method, c.Request.URL.Path) {
				if billingContext, billingErr := resolveAPIKeyBillingContext(c.Request.Context(), apiKey, subscriptionService, false); billingErr == nil && billingContext != nil {
					c.Set(string(ContextKeyAPIKeyBilling), billingContext)
					if billingContext.Subscription != nil {
						c.Set(string(ContextKeySubscription), billingContext.Subscription)
					}
				}
			}
			c.Set(string(ContextKeyAPIKey), apiKey)
			c.Set(string(ContextKeyUser), AuthSubject{
				UserID:      apiKey.User.ID,
				Concurrency: apiKey.User.Concurrency,
			})
			c.Set(string(ContextKeyUserRole), apiKey.User.Role)
			setGroupContext(c, apiKey.Group)
			_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
			c.Next()
			return
		}

		// ── 5. 解析 Key 的结算来源 ───────────────────────────────────

		billingContext, billingErr := resolveAPIKeyBillingContext(c.Request.Context(), apiKey, subscriptionService, !skipBilling)
		if billingErr != nil {
			switch {
			case errors.Is(billingErr, service.ErrPreferredSubscriptionGroup):
				AbortWithError(c, 403, "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED", billingErr.Error())
			case errors.Is(billingErr, service.ErrPreferredSubscriptionInvalid):
				AbortWithError(c, 403, "PREFERRED_SUBSCRIPTION_INVALID", billingErr.Error())
			default:
				AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate subscription")
			}
			return
		}
		var subscription *service.UserSubscription
		if billingContext != nil {
			subscription = billingContext.Subscription
		}

		// ── 6. 计费执行（skipBilling 时整块跳过） ────────────────────

		if !skipBilling {
			// Key 状态检查
			switch apiKey.Status {
			case service.StatusAPIKeyQuotaExhausted:
				abortWithAPIKeyQuotaError(c)
				return
			case service.StatusAPIKeyExpired:
				AbortWithError(c, 403, "API_KEY_EXPIRED", "API key 已过期")
				return
			}

			// 运行时过期/配额检查（即使状态是 active，也要检查时间和用量）
			if apiKey.IsExpired() {
				AbortWithError(c, 403, "API_KEY_EXPIRED", "API key 已过期")
				return
			}
			if apiKey.IsQuotaExhausted() {
				abortWithAPIKeyQuotaError(c)
				return
			}

			if subscription != nil {
				needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
				if needsMaintenance {
					refreshed, maintenanceErr := subscriptionService.EnsureWindowMaintenance(c.Request.Context(), subscription)
					if maintenanceErr != nil {
						AbortWithError(c, 500, "SUBSCRIPTION_MAINTENANCE_FAILED", "Failed to maintain subscription usage windows")
						return
					}
					subscription = refreshed
					_, validateErr = subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
				}
				if validateErr != nil {
					code := "SUBSCRIPTION_INVALID"
					status := 403
					if errors.Is(validateErr, service.ErrDailyLimitExceeded) ||
						errors.Is(validateErr, service.ErrWeeklyLimitExceeded) ||
						errors.Is(validateErr, service.ErrMonthlyLimitExceeded) {
						code = "USAGE_LIMIT_EXCEEDED"
						status = 429
					}
					AbortWithError(c, status, code, validateErr.Error())
					return
				}
				if billingContext != nil {
					billingContext.Subscription = subscription
					billingContext.Available = true
				}
			} else {
				// auto 与 balance 可使用余额；指定订阅在上方已严格解析，绝不回退。
				if apiKeyBalanceBelowAuthThreshold(apiKey.User.Balance, cfg) {
					AbortWithError(c, 403, "INSUFFICIENT_BALANCE", "Insufficient account balance")
					return
				}
			}
		}

		// ── 7. 设置上下文 → Next ─────────────────────────────────────

		if billingContext != nil {
			c.Set(string(ContextKeyAPIKeyBilling), billingContext)
		}
		if subscription != nil {
			c.Set(string(ContextKeySubscription), subscription)
		}
		c.Set(string(ContextKeyAPIKey), apiKey)
		c.Set(string(ContextKeyUser), AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
		c.Set(string(ContextKeyUserRole), apiKey.User.Role)
		setGroupContext(c, apiKey.Group)
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)

		c.Next()
	}
}

func apiKeyHeadersTooLarge(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return len(c.GetHeader("Authorization")) > maxAPIKeyAuthorizationHeaderBytes ||
		len(c.GetHeader("x-api-key")) > service.MaxAPIKeyCredentialBytes ||
		len(c.GetHeader("x-goog-api-key")) > service.MaxAPIKeyCredentialBytes
}

func hasAPIKeyCredentialInput(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return c.GetHeader("Authorization") != "" ||
		c.GetHeader("x-api-key") != "" ||
		c.GetHeader("x-goog-api-key") != ""
}

func abortWithAPIKeyQuotaError(c *gin.Context) {
	const message = "API key 额度已用完"
	if isOpenAICompatibleAPIKeyRequest(c) {
		abortWithOpenAIQuotaError(c, http.StatusTooManyRequests, message)
		return
	}
	AbortWithError(c, http.StatusTooManyRequests, "API_KEY_QUOTA_EXHAUSTED", message)
}

func isOpenAICompatibleAPIKeyRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}

	path := strings.TrimRight(c.Request.URL.Path, "/")
	for _, root := range []string{
		"/v1/responses",
		"/openai/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

// isBatchImageManagementRequest 标识不会创建新生成任务的批任务管理请求。
func isBatchImageManagementRequest(method, path string) bool {
	path = strings.TrimRight(path, "/")
	const root = "/v1/images/batches"
	if method == http.MethodGet && (path == root || strings.HasPrefix(path, root+"/")) {
		return true
	}
	if method == http.MethodDelete && strings.HasPrefix(path, root+"/") {
		return true
	}
	return method == http.MethodPost && strings.HasPrefix(path, root+"/") && strings.HasSuffix(path, "/cancel")
}

// isBatchImageBillingBypassRequest 排除模型列表，仅让既有批任务的查询和管理绕过计费。
func isBatchImageBillingBypassRequest(method, path string) bool {
	path = strings.TrimRight(path, "/")
	return path != "/v1/images/batches/models" && isBatchImageManagementRequest(method, path)
}

// isAPIKeyNonConsumingRequest 标识只读取已有状态或释放资源的网关请求。
// 未知路径默认视为会产生消费，避免新增路由自动绕过团队限额。
func isAPIKeyNonConsumingRequest(method, path string) bool {
	path = strings.TrimRight(path, "/")
	if isAPIKeyUsageRequest(method, path) {
		return true
	}
	if method == http.MethodGet {
		if strings.HasSuffix(path, "/models") || isBatchImageManagementRequest(method, path) || isGrokVideoTaskRead(method, path) {
			return true
		}
	}
	if isBatchImageManagementRequest(method, path) {
		return true
	}
	if method == http.MethodPost && strings.HasSuffix(path, "/messages/count_tokens") {
		return true
	}
	return false
}

// isAPIKeyUsageRequest 统一识别两套 Claude 风格的 Key 用量查询入口。
// 这类接口只读取 Key 自身状态，不能因为订阅或 Key 配额已耗尽而被消费准入拦截。
func isAPIKeyUsageRequest(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch strings.TrimRight(path, "/") {
	case "/v1/usage", "/antigravity/v1/usage":
		return true
	default:
		return false
	}
}

// shouldResolveAPIKeyBillingInSimpleMode 为不执行资金预检的简易模式保留必要的权益上下文。
// 严格指定订阅的复合 Key 必须据此过滤模型列表，避免在简易模式泄露套餐外映射。
func shouldResolveAPIKeyBillingInSimpleMode(apiKey *service.APIKey, method, path string) bool {
	if isAPIKeyUsageRequest(method, path) {
		return true
	}
	return apiKey != nil &&
		apiKey.IsComposite &&
		service.APIKeyEffectiveBillingMode(apiKey) == service.APIKeyBillingModeSubscription &&
		isCompositeKeyModelListEndpoint(method, path)
}

// abortTeamAPIKeyError 将团队生命周期与成员限额错误映射为稳定的网关响应。
func abortTeamAPIKeyError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrTeamMemberDailyExceeded):
		AbortWithError(c, http.StatusTooManyRequests, "TEAM_MEMBER_DAILY_LIMIT_EXCEEDED", "团队成员日限额已用完")
	case errors.Is(err, service.ErrTeamMemberWeeklyExceeded):
		AbortWithError(c, http.StatusTooManyRequests, "TEAM_MEMBER_WEEKLY_LIMIT_EXCEEDED", "团队成员周限额已用完")
	case errors.Is(err, service.ErrTeamMemberMonthlyExceeded):
		AbortWithError(c, http.StatusTooManyRequests, "TEAM_MEMBER_MONTHLY_LIMIT_EXCEEDED", "团队成员月限额已用完")
	case errors.Is(err, service.ErrTeamFeatureDisabled):
		AbortWithError(c, http.StatusForbidden, "TEAM_FEATURE_DISABLED", "团队功能未启用")
	case errors.Is(err, service.ErrTeamSuspended):
		AbortWithError(c, http.StatusForbidden, "TEAM_SUSPENDED", "团队已暂停")
	case errors.Is(err, service.ErrTeamMembershipRequired):
		AbortWithError(c, http.StatusForbidden, "TEAM_MEMBERSHIP_REQUIRED", "团队成员关系已失效")
	case errors.Is(err, service.ErrTeamActorInactive):
		AbortWithError(c, http.StatusForbidden, "TEAM_ACTOR_INACTIVE", "团队密钥所属成员已停用")
	case errors.Is(err, service.ErrTeamBillingOwnerInactive):
		AbortWithError(c, http.StatusForbidden, "TEAM_BILLING_OWNER_INACTIVE", "团队付款所有者已停用")
	default:
		return false
	}
	return true
}

// GetAPIKeyFromContext 从上下文中获取API key
func GetAPIKeyFromContext(c *gin.Context) (*service.APIKey, bool) {
	value, exists := c.Get(string(ContextKeyAPIKey))
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*service.APIKey)
	return apiKey, ok
}

// SetOpsFallbackAPIKey 记录已加载的 API Key，供 Ops 错误日志在鉴权早退时回退使用。
// 与 ContextKeyAPIKey 区分：写入它不代表请求已通过鉴权，因此不影响 handler、
// 审计日志等对“已鉴权”的判断。
func SetOpsFallbackAPIKey(c *gin.Context, apiKey *service.APIKey) {
	if c == nil || apiKey == nil {
		return
	}
	c.Set(string(ContextKeyOpsFallbackAPIKey), apiKey)
}

// GetOpsFallbackAPIKey 读取 Ops 错误日志专用的回退 API Key。
func GetOpsFallbackAPIKey(c *gin.Context) (*service.APIKey, bool) {
	value, exists := c.Get(string(ContextKeyOpsFallbackAPIKey))
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*service.APIKey)
	return apiKey, ok
}

// GetSubscriptionFromContext 从上下文中获取订阅信息
func GetSubscriptionFromContext(c *gin.Context) (*service.UserSubscription, bool) {
	value, exists := c.Get(string(ContextKeySubscription))
	if !exists {
		return nil, false
	}
	subscription, ok := value.(*service.UserSubscription)
	return subscription, ok
}

func setGroupContext(c *gin.Context, group *service.Group) {
	if !service.IsGroupContextValid(group) {
		return
	}
	if existing, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group); ok && existing != nil && existing.ID == group.ID && service.IsGroupContextValid(existing) {
		return
	}
	ctx := context.WithValue(c.Request.Context(), ctxkey.Group, group)
	c.Request = c.Request.WithContext(ctx)
}

// apiKeyBalanceBelowAuthThreshold 保持鉴权层的历史语义：仅在余额耗尽（<=0）时拒绝。
// MinimumBalanceReserve 只作为 billing-cache 预检的保守下限，不得复用为鉴权硬门槛，
// 否则已配置该值的存量部署升级后，0 < balance < reserve 的用户会在所有端点被静默 403。
func apiKeyBalanceBelowAuthThreshold(balance float64, _ *config.Config) bool {
	return balance <= 0
}

func abortIfAPIKeyGroupUnavailable(c *gin.Context, apiKey *service.APIKey) bool {
	code, message, ok := validateAPIKeyGroupAvailable(apiKey)
	if ok {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
	if code == "GROUP_DELETED" {
		MarkIngressRejected(c, IngressRejectGroupDeleted)
	} else {
		MarkIngressRejected(c, IngressRejectGroupDisabled)
	}
	AbortWithError(c, 403, code, message)
	return true
}

func abortIfAPIKeyGroupNotAllowed(c *gin.Context, apiKey *service.APIKey) bool {
	if validateAPIKeyGroupAllowed(apiKey) {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
	MarkIngressRejected(c, IngressRejectGroupNotAllowed)
	AbortWithError(c, 403, "GROUP_NOT_ALLOWED", "API Key 所属专属分组不再允许当前用户使用")
	return true
}

func validateAPIKeyGroupAllowed(apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.GroupID == nil || apiKey.User == nil || apiKey.Group == nil {
		return true
	}
	group := apiKey.Group
	return apiKey.User.CanBindGroup(group.ID, group.IsExclusive)
}

func validateAPIKeyGroupAvailable(apiKey *service.APIKey) (string, string, bool) {
	if apiKey == nil || apiKey.GroupID == nil {
		return "", "", true
	}
	group := apiKey.Group
	if group == nil || strings.EqualFold(group.Status, "deleted") {
		return "GROUP_DELETED", "API Key 所属分组已删除", false
	}
	if !group.IsActive() {
		return "GROUP_DISABLED", "API Key 所属分组已停用", false
	}
	return "", "", true
}

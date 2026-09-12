package middleware

import (
	"context"
	"errors"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

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
		access, ok := authenticateAPIKeyRequest(c, apiKeyService, cfg, false)
		if !ok {
			return
		}
		apiKey := service.APIKeyFromView(access.KeyView())
		var err error

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
			keyhttp.SetAccessPrincipal(c, access)
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
				needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(subscription)
				if needsMaintenance {
					refreshed, maintenanceErr := subscriptionService.EnsureWindowMaintenance(c.Request.Context(), subscription)
					if maintenanceErr != nil {
						AbortWithError(c, 500, "SUBSCRIPTION_MAINTENANCE_FAILED", "Failed to maintain subscription usage windows")
						return
					}
					subscription = refreshed
					_, validateErr = subscriptionService.ValidateAndCheckLimits(subscription)
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

		keyhttp.SetAccessPrincipal(c, access)
		c.Next()
	}
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

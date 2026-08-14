package middleware

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/googleapi"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	"github.com/BrandonVee/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// APIKeyAuthGoogle is a Google-style error wrapper for API key auth.
func APIKeyAuthGoogle(apiKeyService *service.APIKeyService, cfg *config.Config) gin.HandlerFunc {
	return APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg)
}

// APIKeyAuthWithSubscriptionGoogle behaves like ApiKeyAuthWithSubscription but returns Google-style errors:
// {"error":{"code":401,"message":"...","status":"UNAUTHENTICATED"}}
//
// It is intended for Gemini native endpoints (/v1beta) to match Gemini SDK expectations.
func APIKeyAuthWithSubscriptionGoogle(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rejectInvalidAuthAbuse(c, apiKeyService) {
			abortWithGoogleError(c, 429, "Too many invalid authentication attempts; retry later")
			return
		}
		if apiKeyHeadersTooLarge(c) {
			recordInvalidAuthFailure(c, apiKeyService)
			MarkIngressRejected(c, IngressRejectInvalidAPIKey)
			abortWithGoogleError(c, 401, "Invalid API key")
			return
		}
		if v := strings.TrimSpace(c.Query("api_key")); v != "" {
			recordInvalidAuthFailure(c, apiKeyService)
			MarkIngressRejected(c, IngressRejectQueryAPIKeyDeprecated)
			abortWithGoogleError(c, 400, "Query parameter api_key is deprecated. Use Authorization header or key instead.")
			return
		}
		apiKeyString := extractAPIKeyForGoogle(c)
		if apiKeyString == "" {
			recordInvalidAuthFailure(c, apiKeyService)
			if hasAPIKeyCredentialInput(c) {
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
			} else {
				MarkIngressRejected(c, IngressRejectAPIKeyRequired)
			}
			abortWithGoogleError(c, 401, "API key is required")
			return
		}
		if len(apiKeyString) > service.MaxAPIKeyCredentialBytes {
			recordInvalidAuthFailure(c, apiKeyService)
			MarkIngressRejected(c, IngressRejectInvalidAPIKey)
			abortWithGoogleError(c, 401, "Invalid API key")
			return
		}

		apiKey, err := apiKeyService.GetByKey(c.Request.Context(), apiKeyString)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				recordInvalidAuthFailure(c, apiKeyService)
				MarkIngressRejected(c, IngressRejectInvalidAPIKey)
				abortWithGoogleError(c, 401, "Invalid API key")
				return
			}
			if errors.Is(err, service.ErrGroupDisabledForUser) {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
				abortWithGoogleError(c, 403, "API Key 所属公开分组已被禁用")
				return
			}
			if errors.Is(err, service.ErrAPIKeyAuthOverloaded) {
				MarkIngressRejected(c, IngressRejectAPIKeyAuthOverloaded)
				abortWithGoogleError(c, 503, "API key authentication is temporarily unavailable")
				return
			}
			if status, message, ok := googleTeamAPIKeyError(err); ok {
				abortWithGoogleError(c, status, message)
				return
			}
			abortWithGoogleError(c, 500, "Failed to validate API key")
			return
		}

		// 同 api_key_auth.go：早退中断前也写入 Ops 回退 key，便于错误日志展示
		// user/group/platform。
		SetOpsFallbackAPIKey(c, apiKey)

		// disabled / 未知状态 → 无条件拦截（expired 和 quota_exhausted 留给计费阶段，
		// 与主中间件 api_key_auth.go 保持一致）。
		if !apiKey.IsActive() &&
			apiKey.Status != service.StatusAPIKeyExpired &&
			apiKey.Status != service.StatusAPIKeyQuotaExhausted {
			MarkIngressRejected(c, IngressRejectAPIKeyDisabled)
			abortWithGoogleError(c, 401, "API key is disabled")
			return
		}
		if err := apiKeyService.ValidateTeamKeyLifecycle(apiKey); err != nil {
			if status, message, ok := googleTeamAPIKeyError(err); ok {
				abortWithGoogleError(c, status, message)
				return
			}
			abortWithGoogleError(c, 500, "Failed to validate team API key")
			return
		}
		if !isAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path) {
			if err := apiKeyService.CheckTeamMemberLimits(apiKey); err != nil {
				if status, message, ok := googleTeamAPIKeyError(err); ok {
					abortWithGoogleError(c, status, message)
					return
				}
				abortWithGoogleError(c, 500, "Failed to validate team member limits")
				return
			}
		}

		// 检查 IP 限制（白名单/黑名单）。与主中间件保持一致，避免 Gemini 端点绕过 Key 的 IP ACL。
		if len(apiKey.IPWhitelist) > 0 || len(apiKey.IPBlacklist) > 0 {
			clientIP := ip.GetSecurityClientIP(c, cfg.TrustForwardedIPForAPIKeyACL())
			allowed, _ := ip.CheckIPRestrictionWithCompiledRules(clientIP, apiKey.CompiledIPWhitelist, apiKey.CompiledIPBlacklist)
			if !allowed {
				if clientIP == "" {
					clientIP = "unknown"
				}
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonIPRestriction)
				MarkIngressRejected(c, IngressRejectIPRestricted)
				abortWithGoogleError(c, 403, fmt.Sprintf("Access denied. Your IP is %s", clientIP))
				return
			}
		}

		if apiKey.User == nil {
			abortWithGoogleError(c, 401, "User associated with API key not found")
			return
		}
		if !apiKey.User.IsActive() {
			MarkIngressRejected(c, IngressRejectUserInactive)
			abortWithGoogleError(c, 401, "User account is not active")
			return
		}
		apiKey, err = resolveCompositeAPIKeyRequest(c, apiKeyService, apiKey)
		if err != nil {
			abortCompositeKeyGoogleError(c, err)
			return
		}
		SetOpsFallbackAPIKey(c, apiKey)
		if code, message, ok := validateAPIKeyGroupAvailable(apiKey); !ok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
			if code == "GROUP_DELETED" {
				MarkIngressRejected(c, IngressRejectGroupDeleted)
			} else {
				MarkIngressRejected(c, IngressRejectGroupDisabled)
			}
			abortWithGoogleError(c, 403, message)
			return
		}
		// 专属分组授权校验：用户对该专属分组的授权被撤销后应拒绝（与主中间件一致，防止越权）。
		if !validateAPIKeyGroupAllowed(apiKey) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
			MarkIngressRejected(c, IngressRejectGroupNotAllowed)
			abortWithGoogleError(c, 403, "API Key 所属专属分组不再允许当前用户使用")
			return
		}
		applyAPIKeyModelRedirect(c, apiKey)
		skipBilling := isAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) ||
			isBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) ||
			(apiKey.IsComposite && isGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path))

		// 简易模式不执行计费拦截，但用量查询和复合 Key 模型列表仍需读取指定套餐范围。
		if cfg.RunMode == config.RunModeSimple {
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

		// 非消费请求（包括 /v1/usage 和批任务管理）只读取配置快照，不因资金来源失效而拒绝。
		billingContext, billingErr := resolveAPIKeyBillingContext(c.Request.Context(), apiKey, subscriptionService, !skipBilling)
		if billingErr != nil {
			switch {
			case errors.Is(billingErr, service.ErrPreferredSubscriptionGroup):
				abortWithGoogleError(c, 403, "Preferred subscription does not allow this group")
			case errors.Is(billingErr, service.ErrPreferredSubscriptionInvalid):
				abortWithGoogleError(c, 403, "Preferred subscription is unavailable")
			default:
				abortWithGoogleError(c, 500, "Failed to validate subscription")
			}
			return
		}
		var subscription *service.UserSubscription
		if billingContext != nil {
			subscription = billingContext.Subscription
		}
		if billingContext != nil {
			c.Set(string(ContextKeyAPIKeyBilling), billingContext)
		}
		if !skipBilling {
			// 状态字段可能因后台异步刷新而滞后，因此在消费请求中做运行时二次检查。
			switch apiKey.Status {
			case service.StatusAPIKeyQuotaExhausted:
				abortWithGoogleError(c, 429, "API key 额度已用完")
				return
			case service.StatusAPIKeyExpired:
				abortWithGoogleError(c, 403, "API key 已过期")
				return
			}
			if apiKey.IsExpired() {
				abortWithGoogleError(c, 403, "API key 已过期")
				return
			}
			if apiKey.IsQuotaExhausted() {
				abortWithGoogleError(c, 429, "API key 额度已用完")
				return
			}

			if subscription != nil {
				needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
				if needsMaintenance {
					refreshed, maintenanceErr := subscriptionService.EnsureWindowMaintenance(c.Request.Context(), subscription)
					if maintenanceErr != nil {
						abortWithGoogleError(c, 500, "Failed to maintain subscription usage windows")
						return
					}
					subscription = refreshed
					_, validateErr = subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
				}
				if validateErr != nil {
					status := 403
					if errors.Is(validateErr, service.ErrDailyLimitExceeded) ||
						errors.Is(validateErr, service.ErrWeeklyLimitExceeded) ||
						errors.Is(validateErr, service.ErrMonthlyLimitExceeded) {
						status = 429
					}
					abortWithGoogleError(c, status, validateErr.Error())
					return
				}
				billingContext.Subscription = subscription
				billingContext.Available = true
			} else if apiKeyBalanceBelowAuthThreshold(apiKey.User.Balance, cfg) {
				abortWithGoogleError(c, 403, "Insufficient account balance")
				return
			}
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

// extractAPIKeyForGoogle extracts API key for Google/Gemini endpoints.
// Priority: x-goog-api-key > Authorization: Bearer > x-api-key > query key
// This allows OpenClaw and other clients using Bearer auth to work with Gemini endpoints.
func extractAPIKeyForGoogle(c *gin.Context) string {
	// 1) preferred: Gemini native header
	if k := strings.TrimSpace(c.GetHeader("x-goog-api-key")); k != "" {
		return k
	}

	// 2) fallback: Authorization: Bearer <key>
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			if k := strings.TrimSpace(parts[1]); k != "" {
				return k
			}
		}
	}

	// 3) x-api-key header (backward compatibility)
	if k := strings.TrimSpace(c.GetHeader("x-api-key")); k != "" {
		return k
	}

	// 4) query parameter key (for specific paths)
	if allowGoogleQueryKey(c.Request.URL.Path) {
		if v := strings.TrimSpace(c.Query("key")); v != "" {
			return v
		}
	}

	return ""
}

func allowGoogleQueryKey(path string) bool {
	return strings.HasPrefix(path, "/v1beta") || strings.HasPrefix(path, "/antigravity/v1beta")
}

func abortWithGoogleError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  googleapi.HTTPStatusToGoogleStatus(status),
		},
	})
	c.Abort()
}

// googleTeamAPIKeyError 返回 Google 风格团队鉴权错误需要的状态码和消息。
func googleTeamAPIKeyError(err error) (int, string, bool) {
	switch {
	case errors.Is(err, service.ErrTeamMemberDailyExceeded), errors.Is(err, service.ErrTeamMemberWeeklyExceeded), errors.Is(err, service.ErrTeamMemberMonthlyExceeded):
		return 429, "Team member usage limit exceeded", true
	case errors.Is(err, service.ErrTeamFeatureDisabled):
		return 403, "Team feature is disabled", true
	case errors.Is(err, service.ErrTeamSuspended):
		return 403, "Team is suspended", true
	case errors.Is(err, service.ErrTeamMembershipRequired):
		return 403, "Team membership is no longer valid", true
	case errors.Is(err, service.ErrTeamActorInactive):
		return 403, "Team key member is inactive", true
	case errors.Is(err, service.ErrTeamBillingOwnerInactive):
		return 403, "Team billing owner is inactive", true
	default:
		return 0, "", false
	}
}

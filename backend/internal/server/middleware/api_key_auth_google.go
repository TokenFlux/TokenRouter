package middleware

import (
	"errors"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
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
		access, ok := authenticateAPIKeyRequest(c, apiKeyService, cfg, true)
		if !ok {
			return
		}
		apiKey := service.APIKeyFromView(access.KeyView())
		var err error

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
			keyhttp.SetAccessPrincipal(c, access)
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
				needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(subscription)
				if needsMaintenance {
					refreshed, maintenanceErr := subscriptionService.EnsureWindowMaintenance(c.Request.Context(), subscription)
					if maintenanceErr != nil {
						abortWithGoogleError(c, 500, "Failed to maintain subscription usage windows")
						return
					}
					subscription = refreshed
					_, validateErr = subscriptionService.ValidateAndCheckLimits(subscription)
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
		keyhttp.SetAccessPrincipal(c, access)
		c.Next()
	}
}

func abortWithGoogleError(c *gin.Context, status int, message string) {
	keyhttp.AbortGoogleError(c, status, message)
}

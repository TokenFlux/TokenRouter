// 认证入口只拥有 HTTP 时序与错误形状；Key、路由与资金规则分别委托所属模块。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// AuthorizationSubscriptions 保留同一权益实例的读取、校验与窗口维护。
type AuthorizationSubscriptions interface {
	admission.SubscriptionReader
	admission.SubscriptionValidator
}

// APIKeyAuthorizationOptions 只携带入口选项、观测与旧 context 投影，不接收完整配置。
type APIKeyAuthorizationOptions struct {
	Simple         bool
	Authentication keyhttp.AuthenticationOptions
	BindLegacyKey  func(*gin.Context, *apikey.APIKey)
}

func (o APIKeyAuthorizationOptions) loaded(c *gin.Context, k *apikey.APIKey) {
	if o.Authentication.Loaded != nil {
		o.Authentication.Loaded(c, k)
	}
}
func (o APIKeyAuthorizationOptions) business(c *gin.Context) {
	if o.Authentication.BusinessLimited != nil {
		o.Authentication.BusinessLimited(c, "api_key_group_unavailable")
	}
}
func (o APIKeyAuthorizationOptions) reject(c *gin.Context, reason string) {
	if o.Authentication.Rejected != nil {
		o.Authentication.Rejected(c, reason)
	}
}
func (o APIKeyAuthorizationOptions) bind(c *gin.Context, k *apikey.APIKey) {
	c.Set("gateway_effective_key", k)
	if o.BindLegacyKey != nil {
		o.BindLegacyKey(c, k)
	}
}

// EffectiveAPIKey 只供新网关读取已经完成准入的最终 Key 视图。
func EffectiveAPIKey(c *gin.Context) (*apikey.APIKey, bool) {
	value, ok := c.Get("gateway_effective_key")
	if !ok {
		return nil, false
	}
	key, ok := value.(*apikey.APIKey)
	return key, ok
}
func NewAPIKeyAuthorization(apiKeyService *apikey.APIKeyService, subscriptionService AuthorizationSubscriptions, options APIKeyAuthorizationOptions) gin.HandlerFunc {
	options.Authentication.Google = false
	return func(c *gin.Context) {
		access, ok := keyhttp.Authenticate(c, apiKeyService, options.Authentication)
		if !ok {
			return
		}
		apiKey := access.KeyView()
		var err error

		apiKey, err = ResolveCompositeAPIKeyRequest(c, apiKeyService, apiKey)
		if err != nil {
			AbortCompositeKeyError(c, err)
			return
		}
		options.loaded(c, apiKey)
		if abortAuthorizationGroupUnavailable(c, apiKey, options) {
			return
		}
		if abortAuthorizationGroupNotAllowed(c, apiKey, options) {
			return
		}
		// 沿用通用入口的绑定时点；Google 入口仍不增加这一步策略投影。
		requestAccess := *access
		requestAccess.PayerUserID = apiKey.User.ID
		ctx := apikey.WithAccessSnapshot(c.Request.Context(), requestAccess)
		c.Request = c.Request.WithContext(apikey.WithFastModePolicy(ctx, apiKey.FastModePolicy))
		ApplyAPIKeyModelRedirect(c, apiKey)
		// 批任务管理只读取已有数据或释放冻结；即使任务耗尽额度，结果仍应可取回或取消。
		skipBilling := IsAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) ||
			IsBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) ||
			(apiKey.IsComposite && IsGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path))

		// ── 4. SimpleMode → early return ─────────────────────────────

		if options.Simple {
			// 简易模式不执行计费拦截，但用量查询和复合 Key 模型列表仍需读取指定套餐范围。
			if ShouldResolveAPIKeyBillingInSimpleMode(apiKey, c.Request.Method, c.Request.URL.Path) {
				if billingContext, billingErr := admission.ResolveFundingFromKey(c.Request.Context(), apiKey, subscriptionService, false); billingErr == nil && billingContext != nil {
					c.Set("api_key_billing", billingContext)
					if billingContext.Subscription != nil {
						c.Set("subscription", billingContext.Subscription)
					}
				}
			}
			c.Set("api_key", apiKey)
			c.Set("user", authctx.AuthSubject{
				UserID:      apiKey.User.ID,
				Concurrency: apiKey.User.Concurrency,
			})
			c.Set("user_role", apiKey.User.Role)
			options.bind(c, apiKey)
			_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
			keyhttp.SetAccessPrincipal(c, access)
			c.Next()
			return
		}

		// ── 5. 解析 Key 的结算来源 ───────────────────────────────────

		billingContext, billingErr := admission.ResolveFundingFromKey(c.Request.Context(), apiKey, subscriptionService, !skipBilling)
		if billingErr != nil {
			switch {
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionGroup):
				httpx.AbortWithError(c, 403, "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED", billingErr.Error())
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionInvalid):
				httpx.AbortWithError(c, 403, "PREFERRED_SUBSCRIPTION_INVALID", billingErr.Error())
			default:
				httpx.AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate subscription")
			}
			return
		}
		var subscription *billing.UserSubscription
		if billingContext != nil {
			subscription = billingContext.Subscription
		}

		// ── 6. 计费执行（skipBilling 时整块跳过） ────────────────────

		if !skipBilling {
			checked, failure := admission.CheckConsumption(c.Request.Context(), admission.ConsumptionInput{Status: apiKey.Status, Limits: apiKey, Balance: apiKey.User.Balance, Subscription: subscription}, subscriptionService)
			if failure != nil {
				switch failure.Kind {
				case admission.KeyQuotaExceeded:
					AbortAPIKeyQuotaError(c)
				case admission.KeyExpired:
					httpx.AbortWithError(c, 403, "API_KEY_EXPIRED", "API key 已过期")
				case admission.MaintenanceFailed:
					httpx.AbortWithError(c, 500, "SUBSCRIPTION_MAINTENANCE_FAILED", "Failed to maintain subscription usage windows")
				case admission.SubscriptionLimitExceeded:
					httpx.AbortWithError(c, 429, "USAGE_LIMIT_EXCEEDED", failure.Error())
				case admission.SubscriptionInvalid:
					httpx.AbortWithError(c, 403, "SUBSCRIPTION_INVALID", failure.Error())
				case admission.InsufficientBalance:
					httpx.AbortWithError(c, 403, "INSUFFICIENT_BALANCE", "Insufficient account balance")
				}
				return
			}
			subscription = checked
			if billingContext != nil && subscription != nil {
				billingContext.Subscription = subscription
				billingContext.Available = true
			}
		}

		// ── 7. 设置上下文 → Next ─────────────────────────────────────

		if billingContext != nil {
			c.Set("api_key_billing", billingContext)
		}
		if subscription != nil {
			c.Set("subscription", subscription)
		}
		c.Set("api_key", apiKey)
		c.Set("user", authctx.AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
		c.Set("user_role", apiKey.User.Role)
		options.bind(c, apiKey)
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)

		keyhttp.SetAccessPrincipal(c, access)
		c.Next()
	}
}

// NewGoogleAPIKeyAuthorization 保留 Google 入口的认证与错误时序。
func NewGoogleAPIKeyAuthorization(apiKeyService *apikey.APIKeyService, subscriptionService AuthorizationSubscriptions, options APIKeyAuthorizationOptions) gin.HandlerFunc {
	options.Authentication.Google = true
	return func(c *gin.Context) {
		access, ok := keyhttp.Authenticate(c, apiKeyService, options.Authentication)
		if !ok {
			return
		}
		apiKey := access.KeyView()
		var err error

		apiKey, err = ResolveCompositeAPIKeyRequest(c, apiKeyService, apiKey)
		if err != nil {
			AbortCompositeKeyGoogleError(c, err)
			return
		}
		options.loaded(c, apiKey)
		if code, message, ok := admission.GroupAvailable(apiKey); !ok {
			options.business(c)
			if code == "GROUP_DELETED" {
				options.reject(c, "group_deleted")
			} else {
				options.reject(c, "group_disabled")
			}
			keyhttp.AbortGoogleError(c, 403, message)
			return
		}
		// 专属分组授权校验：用户对该专属分组的授权被撤销后应拒绝（与主中间件一致，防止越权）。
		if !admission.GroupAllowed(apiKey) {
			options.business(c)
			options.reject(c, "group_not_allowed")
			keyhttp.AbortGoogleError(c, 403, "API Key 所属专属分组不再允许当前用户使用")
			return
		}
		ApplyAPIKeyModelRedirect(c, apiKey)
		skipBilling := IsAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) ||
			IsBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) ||
			(apiKey.IsComposite && IsGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path))

		// 简易模式不执行计费拦截，但用量查询和复合 Key 模型列表仍需读取指定套餐范围。
		if options.Simple {
			if ShouldResolveAPIKeyBillingInSimpleMode(apiKey, c.Request.Method, c.Request.URL.Path) {
				if billingContext, billingErr := admission.ResolveFundingFromKey(c.Request.Context(), apiKey, subscriptionService, false); billingErr == nil && billingContext != nil {
					c.Set("api_key_billing", billingContext)
					if billingContext.Subscription != nil {
						c.Set("subscription", billingContext.Subscription)
					}
				}
			}
			c.Set("api_key", apiKey)
			c.Set("user", authctx.AuthSubject{
				UserID:      apiKey.User.ID,
				Concurrency: apiKey.User.Concurrency,
			})
			c.Set("user_role", apiKey.User.Role)
			options.bind(c, apiKey)
			_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
			keyhttp.SetAccessPrincipal(c, access)
			c.Next()
			return
		}

		// 非消费请求（包括 /v1/usage 和批任务管理）只读取配置快照，不因资金来源失效而拒绝。
		billingContext, billingErr := admission.ResolveFundingFromKey(c.Request.Context(), apiKey, subscriptionService, !skipBilling)
		if billingErr != nil {
			switch {
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionGroup):
				keyhttp.AbortGoogleError(c, 403, "Preferred subscription does not allow this group")
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionInvalid):
				keyhttp.AbortGoogleError(c, 403, "Preferred subscription is unavailable")
			default:
				keyhttp.AbortGoogleError(c, 500, "Failed to validate subscription")
			}
			return
		}
		var subscription *billing.UserSubscription
		if billingContext != nil {
			subscription = billingContext.Subscription
		}
		if billingContext != nil {
			c.Set("api_key_billing", billingContext)
		}
		if !skipBilling {
			checked, failure := admission.CheckConsumption(c.Request.Context(), admission.ConsumptionInput{Status: apiKey.Status, Limits: apiKey, Balance: apiKey.User.Balance, Subscription: subscription}, subscriptionService)
			if failure != nil {
				status := 403
				message := ""
				switch failure.Kind {
				case admission.KeyQuotaExceeded:
					status = 429
					message = "API key 额度已用完"
				case admission.KeyExpired:
					message = "API key 已过期"
				case admission.MaintenanceFailed:
					status = 500
					message = "Failed to maintain subscription usage windows"
				case admission.SubscriptionLimitExceeded:
					status = 429
					message = failure.Error()
				case admission.SubscriptionInvalid:
					message = failure.Error()
				case admission.InsufficientBalance:
					message = "Insufficient account balance"
				}
				keyhttp.AbortGoogleError(c, status, message)
				return
			}
			subscription = checked
			if subscription != nil {
				billingContext.Subscription = subscription
			}
		}
		if subscription != nil {
			c.Set("subscription", subscription)
		}

		c.Set("api_key", apiKey)
		c.Set("user", authctx.AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
		c.Set("user_role", apiKey.User.Role)
		options.bind(c, apiKey)
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
		keyhttp.SetAccessPrincipal(c, access)
		c.Next()
	}
}
func AbortAPIKeyQuotaError(c *gin.Context) {
	const message = "API key 额度已用完"
	if IsOpenAICompatibleAPIKeyRequest(c) {
		AbortOpenAIQuotaError(c, http.StatusTooManyRequests, message)
		return
	}
	httpx.AbortWithError(c, http.StatusTooManyRequests, "API_KEY_QUOTA_EXHAUSTED", message)
}
func IsOpenAICompatibleAPIKeyRequest(c *gin.Context) bool {
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

// AbortOpenAIQuotaError 输出与 OpenAI 兼容的配额不足响应。
func AbortOpenAIQuotaError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "insufficient_quota",
			"param":   nil,
			"code":    "insufficient_quota",
		},
	})
	c.Abort()
}
func abortAuthorizationGroupUnavailable(c *gin.Context, key *apikey.APIKey, o APIKeyAuthorizationOptions) bool {
	code, message, ok := admission.GroupAvailable(key)
	if ok {
		return false
	}
	o.business(c)
	if code == "GROUP_DELETED" {
		o.reject(c, "group_deleted")
	} else {
		o.reject(c, "group_disabled")
	}
	httpx.AbortWithError(c, 403, code, message)
	return true
}
func abortAuthorizationGroupNotAllowed(c *gin.Context, key *apikey.APIKey, o APIKeyAuthorizationOptions) bool {
	if admission.GroupAllowed(key) {
		return false
	}
	o.business(c)
	o.reject(c, "group_not_allowed")
	httpx.AbortWithError(c, 403, "GROUP_NOT_ALLOWED", "API Key 所属专属分组不再允许当前用户使用")
	return true
}

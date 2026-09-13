// PublicUsageHandler 保留 CC Switch 公开用量与权益展示协议。
package httpapi

import (
	"context"
	"net/http"
	"time"

	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/gin-gonic/gin"
)

type PublicUsageContext struct {
	Key          func(*gin.Context) (*keycore.APIKey, bool)
	Billing      func(*gin.Context) (*billingcore.APIKeyBillingContext, bool)
	Subscription func(*gin.Context) (*billingcore.UserSubscription, bool)
}
type PublicUserBalance struct{ Balance float64 }
type PublicBalanceReader interface {
	GetByID(context.Context, int64) (*PublicUserBalance, error)
}
type PublicBalanceQuery func(context.Context, int64) (*PublicUserBalance, error)

func (f PublicBalanceQuery) GetByID(ctx context.Context, id int64) (*PublicUserBalance, error) {
	return f(ctx, id)
}

type PublicWindowReader interface {
	GetRateLimitData(context.Context, int64) (*billingcore.APIKeyRateLimitData, error)
}
type BalanceUnitReader interface{ GetBalanceUnitName(context.Context) string }
type PublicUsageHandler struct {
	usageService   *usage.UsageService
	apiKeyService  PublicWindowReader
	userService    PublicBalanceReader
	settingService BalanceUnitReader
	request        PublicUsageContext
}

func NewPublicUsageHandler(u *usage.UsageService, keys PublicWindowReader, users PublicBalanceReader, settings BalanceUnitReader, request PublicUsageContext) *PublicUsageHandler {
	return &PublicUsageHandler{u, keys, users, settings, request}
}
func (h *PublicUsageHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": errType, "message": message}})
}
func (h *PublicUsageHandler) calculateSubscriptionRemaining(sub *billingcore.UserSubscription) float64 {
	return billingcore.SubscriptionRemainingForDisplay(sub)
}

// apiKeyUsageSubscriptionPayload 返回订阅资金来源的额度快照。
func apiKeyUsageSubscriptionPayload(subscription *billingcore.UserSubscription) gin.H {
	if subscription == nil {
		return nil
	}
	return gin.H{
		"id":                  subscription.ID,
		"plan_id":             subscription.PlanID,
		"daily_usage_usd":     subscription.DailyUsageUSD,
		"weekly_usage_usd":    subscription.WeeklyUsageUSD,
		"monthly_usage_usd":   subscription.MonthlyUsageUSD,
		"daily_limit_usd":     subscription.DailyLimitUSD,
		"weekly_limit_usd":    subscription.WeeklyLimitUSD,
		"monthly_limit_usd":   subscription.MonthlyLimitUSD,
		"weekly_window_start": subscription.WeeklyWindowStart,
		"expires_at":          subscription.ExpiresAt,
		"status":              subscription.Status,
	}
}

// buildAPIKeyUsageBilling 生成 /v1/usage 的稳定资金来源视图。
// 返回内容严格遵循 Key 的结算模式，不能因为查询时套餐失效而泄露或回退到余额。
func (h *PublicUsageHandler) buildAPIKeyUsageBilling(c *gin.Context, ctx context.Context, apiKey *keycore.APIKey, subject identityhttp.AuthSubject, balanceUnitName string) (gin.H, *billingcore.UserSubscription, *float64, error) {
	mode := keycore.APIKeyEffectiveBillingMode(apiKey)
	billing := gin.H{
		"mode":                      mode,
		"source":                    "balance",
		"preferred_subscription_id": nil,
		"available":                 true,
		"unit":                      balanceUnitName,
	}
	if apiKey != nil && apiKey.PreferredSubscriptionID != nil {
		billing["preferred_subscription_id"] = *apiKey.PreferredSubscriptionID
	}

	var subscription *billingcore.UserSubscription
	if contextBilling, ok := h.request.Billing(c); ok && contextBilling != nil {
		billing["mode"] = contextBilling.Mode
		billing["source"] = contextBilling.Source
		billing["available"] = contextBilling.Available
		subscription = contextBilling.Subscription
	} else if contextSubscription, ok := h.request.Subscription(c); ok {
		billing["source"] = "subscription"
		subscription = contextSubscription
	}

	if billing["source"] == "subscription" {
		if subscription != nil {
			billing["subscription_id"] = subscription.ID
			billing["plan_id"] = subscription.PlanID
			billing["remaining"] = h.calculateSubscriptionRemaining(subscription)
			if subscription.Plan != nil {
				billing["plan_name"] = subscription.Plan.Name
			}
		}
		return billing, subscription, nil, nil
	}

	var balance float64
	if h.userService != nil {
		latestUser, err := h.userService.GetByID(ctx, subject.UserID)
		if err != nil {
			return nil, nil, nil, err
		}
		balance = latestUser.Balance
	} else if apiKey != nil && apiKey.User != nil {
		balance = apiKey.User.Balance
	}
	billing["balance"] = balance
	billing["remaining"] = balance
	billing["available"] = balance > 0
	return billing, nil, &balance, nil
}

// usageUnrestricted 处理 unrestricted 模式的响应（向后兼容）
func (h *PublicUsageHandler) UsageUnrestricted(c *gin.Context, ctx context.Context, apiKey *keycore.APIKey, subject identityhttp.AuthSubject, usageData gin.H, dailyUsage any, modelStats any, balanceUnitName string) {
	billing, subscription, balance, billingErr := h.buildAPIKeyUsageBilling(c, ctx, apiKey, subject, balanceUnitName)
	if billingErr != nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to get billing source")
		return
	}
	if billing["source"] == "subscription" {
		planName := ""
		if subscription != nil && subscription.Plan != nil {
			planName = subscription.Plan.Name
		} else if apiKey.Group != nil {
			planName = apiKey.Group.Name
		}
		resp := gin.H{
			"mode":     "unrestricted",
			"isValid":  billing["available"],
			"planName": planName,
			"unit":     balanceUnitName,
			"billing":  billing,
		}

		remaining := 0.0
		if subscription != nil {
			remaining = h.calculateSubscriptionRemaining(subscription)
		}
		resp["remaining"] = remaining
		if subscription != nil {
			resp["subscription"] = apiKeyUsageSubscriptionPayload(subscription)
		} else {
			resp["subscription"] = gin.H{
				"id":        billing["preferred_subscription_id"],
				"available": false,
			}
		}

		if usageData != nil {
			resp["usage"] = usageData
		}
		if dailyUsage != nil {
			resp["daily_usage"] = dailyUsage
		}
		if modelStats != nil {
			resp["model_stats"] = modelStats
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	// 余额模式只返回付款主体余额，指定订阅失效时不会到达这个分支。
	currentBalance := 0.0
	if balance != nil {
		currentBalance = *balance
	}

	resp := gin.H{
		"mode":      "unrestricted",
		"isValid":   billing["available"],
		"planName":  "钱包余额",
		"remaining": currentBalance,
		"unit":      balanceUnitName,
		"balance":   currentBalance,
		"billing":   billing,
	}
	if usageData != nil {
		resp["usage"] = usageData
	}
	if dailyUsage != nil {
		resp["daily_usage"] = dailyUsage
	}
	if modelStats != nil {
		resp["model_stats"] = modelStats
	}
	c.JSON(http.StatusOK, resp)
}

// usageQuotaLimited 处理 quota_limited 模式的响应
func (h *PublicUsageHandler) usageQuotaLimited(c *gin.Context, ctx context.Context, apiKey *keycore.APIKey, subject identityhttp.AuthSubject, usageData gin.H, dailyUsage any, modelStats any, balanceUnitName string) {
	resp := gin.H{
		"mode":    "quota_limited",
		"isValid": apiKey.Status == keycore.StatusAPIKeyActive || apiKey.Status == keycore.StatusAPIKeyQuotaExhausted || apiKey.Status == keycore.StatusAPIKeyExpired,
		"status":  apiKey.Status,
	}
	billing, subscription, balance, billingErr := h.buildAPIKeyUsageBilling(c, ctx, apiKey, subject, balanceUnitName)
	if billingErr != nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to get billing source")
		return
	}
	resp["billing"] = billing
	if subscription != nil {
		resp["subscription"] = apiKeyUsageSubscriptionPayload(subscription)
	} else if billing["source"] == "subscription" {
		resp["subscription"] = gin.H{
			"id":        billing["preferred_subscription_id"],
			"available": false,
		}
	}
	if balance != nil {
		resp["balance"] = *balance
	}

	// 总额度信息
	if apiKey.Quota > 0 {
		remaining := apiKey.GetQuotaRemaining()
		resp["quota"] = gin.H{
			"limit":     apiKey.Quota,
			"used":      apiKey.QuotaUsed,
			"remaining": remaining,
			"unit":      balanceUnitName,
		}
		resp["remaining"] = remaining
		resp["unit"] = balanceUnitName
	}

	// 速率限制信息（从 DB 获取实时用量）
	if apiKey.HasRateLimits() && h.apiKeyService != nil {
		rateLimitData, err := h.apiKeyService.GetRateLimitData(ctx, apiKey.ID)
		if err == nil && rateLimitData != nil {
			var rateLimits []gin.H
			if apiKey.RateLimit5h > 0 {
				used := rateLimitData.EffectiveUsage5h()
				entry := gin.H{
					"window":       "5h",
					"limit":        apiKey.RateLimit5h,
					"used":         used,
					"remaining":    max(0, apiKey.RateLimit5h-used),
					"window_start": rateLimitData.Window5hStart,
				}
				if rateLimitData.Window5hStart != nil && !billingcore.IsWindowExpired(rateLimitData.Window5hStart, keycore.RateLimitWindow5h) {
					entry["reset_at"] = rateLimitData.Window5hStart.Add(keycore.RateLimitWindow5h)
				}
				rateLimits = append(rateLimits, entry)
			}
			if apiKey.RateLimit1d > 0 {
				used := rateLimitData.EffectiveUsage1d()
				entry := gin.H{
					"window":       "1d",
					"limit":        apiKey.RateLimit1d,
					"used":         used,
					"remaining":    max(0, apiKey.RateLimit1d-used),
					"window_start": rateLimitData.Window1dStart,
				}
				if rateLimitData.Window1dStart != nil && !billingcore.IsWindowExpired(rateLimitData.Window1dStart, keycore.RateLimitWindow1d) {
					entry["reset_at"] = rateLimitData.Window1dStart.Add(keycore.RateLimitWindow1d)
				}
				rateLimits = append(rateLimits, entry)
			}
			if apiKey.RateLimit7d > 0 {
				used := rateLimitData.EffectiveUsage7d()
				entry := gin.H{
					"window":       "7d",
					"limit":        apiKey.RateLimit7d,
					"used":         used,
					"remaining":    max(0, apiKey.RateLimit7d-used),
					"window_start": rateLimitData.Window7dStart,
				}
				if rateLimitData.Window7dStart != nil && !billingcore.IsWindowExpired(rateLimitData.Window7dStart, keycore.RateLimitWindow7d) {
					entry["reset_at"] = rateLimitData.Window7dStart.Add(keycore.RateLimitWindow7d)
				}
				rateLimits = append(rateLimits, entry)
			}
			if len(rateLimits) > 0 {
				resp["rate_limits"] = rateLimits
			}
		}
	}

	// 过期时间
	if apiKey.ExpiresAt != nil {
		resp["expires_at"] = apiKey.ExpiresAt
		resp["days_until_expiry"] = apiKey.GetDaysUntilExpiry()
	}

	if usageData != nil {
		resp["usage"] = usageData
	}
	if dailyUsage != nil {
		resp["daily_usage"] = dailyUsage
	}
	if modelStats != nil {
		resp["model_stats"] = modelStats
	}

	c.JSON(http.StatusOK, resp)
}
func (h *PublicUsageHandler) buildAPIKeyDailyUsage(c *gin.Context, apiKeyID int64, days int) any {
	if h.usageService == nil {
		return nil
	}
	startTime, endTime := APIKeyDailyUsageRange(days, c.Query("timezone"))
	stats, err := h.usageService.GetAPIKeyDailyUsageByKey(c.Request.Context(), apiKeyID, startTime, endTime)
	if err != nil {
		return nil
	}
	return stats
}

// buildUsageData 构建 today/total 用量摘要
func (h *PublicUsageHandler) buildUsageData(ctx context.Context, apiKeyID int64) gin.H {
	if h.usageService == nil {
		return nil
	}
	dashStats, err := h.usageService.GetAPIKeyDashboardStats(ctx, apiKeyID)
	if err != nil || dashStats == nil {
		return nil
	}
	return gin.H{
		"today": gin.H{
			"requests":              dashStats.TodayRequests,
			"input_tokens":          dashStats.TodayInputTokens,
			"output_tokens":         dashStats.TodayOutputTokens,
			"cache_creation_tokens": dashStats.TodayCacheCreationTokens,
			"cache_read_tokens":     dashStats.TodayCacheReadTokens,
			"total_tokens":          dashStats.TodayTokens,
			"cost":                  dashStats.TodayCost,
			"actual_cost":           dashStats.TodayActualCost,
		},
		"total": gin.H{
			"requests":              dashStats.TotalRequests,
			"input_tokens":          dashStats.TotalInputTokens,
			"output_tokens":         dashStats.TotalOutputTokens,
			"cache_creation_tokens": dashStats.TotalCacheCreationTokens,
			"cache_read_tokens":     dashStats.TotalCacheReadTokens,
			"total_tokens":          dashStats.TotalTokens,
			"cost":                  dashStats.TotalCost,
			"actual_cost":           dashStats.TotalActualCost,
		},
		"average_duration_ms": dashStats.AverageDurationMs,
		"rpm":                 dashStats.Rpm,
		"tpm":                 dashStats.Tpm,
	}
}

// parseUsageDateRange 解析 start_date / end_date query params，默认返回近 30 天范围。
func (h *PublicUsageHandler) parseUsageDateRange(c *gin.Context) (time.Time, time.Time) {
	now := timezone.Now()
	endTime := now
	startTime := now.AddDate(0, 0, -30)
	userTZ := c.Query("timezone")

	if s := c.Query("start_date"); s != "" {
		if t, _, err := timezone.ParseDateTimeInUserLocation(s, userTZ); err == nil {
			startTime = t
		}
	}
	if s := c.Query("end_date"); s != "" {
		if t, dateOnly, err := timezone.ParseDateTimeInUserLocation(s, userTZ); err == nil {
			if dateOnly {
				t = t.AddDate(0, 0, 1)
			}
			endTime = t
		}
	}
	return startTime, endTime
}

// Usage handles getting account balance and usage statistics for CC Switch integration
// GET /v1/usage
//
// Two modes:
//   - quota_limited: API Key has quota or rate limits configured. Returns key-level limits/usage.
//   - unrestricted:  No key-level limits. Returns subscription or wallet balance info.
//
// @project-doc docs/operations/observability_and_data_lifecycle.md#usage_query_contracts
func (h *PublicUsageHandler) Usage(c *gin.Context) {
	apiKey, ok := h.request.Key(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := identityhttp.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	ctx := c.Request.Context()
	balanceUnitName := "USD"
	if h.settingService != nil {
		balanceUnitName = h.settingService.GetBalanceUnitName(ctx)
	}

	// 解析可选的日期范围参数（用于 model_stats 查询）
	startTime, endTime := h.parseUsageDateRange(c)
	days, ok := ParseAPIKeyDailyUsageDays(c.DefaultQuery("days", ""))
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid days, allowed range is 1-90")
		return
	}

	// Best-effort: 获取用量统计（按当前 API Key 过滤），失败不影响基础响应
	usageData := h.buildUsageData(ctx, apiKey.ID)
	dailyUsage := h.buildAPIKeyDailyUsage(c, apiKey.ID, days)

	// Best-effort: 获取模型统计
	var modelStats any
	if h.usageService != nil {
		if stats, err := h.usageService.GetAPIKeyModelStats(ctx, apiKey.ID, startTime, endTime); err == nil && len(stats) > 0 {
			modelStats = stats
		}
	}

	// 判断模式: key 有总额度或速率限制 → quota_limited，否则 → unrestricted
	isQuotaLimited := apiKey.Quota > 0 || apiKey.HasRateLimits()

	if isQuotaLimited {
		h.usageQuotaLimited(c, ctx, apiKey, subject, usageData, dailyUsage, modelStats, balanceUnitName)
		return
	}

	h.UsageUnrestricted(c, ctx, apiKey, subject, usageData, dailyUsage, modelStats, balanceUnitName)
}

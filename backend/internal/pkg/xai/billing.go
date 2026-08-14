package xai

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// cli-chat-proxy 的 billing 端点要求携带 CLI 客户端身份。
	CLITokenAuthHeader     = "x-xai-token-auth"
	CLITokenAuthValue      = "xai-grok-cli"
	CLIClientVersionHeader = "x-grok-client-version"
	// CLIClientVersion 是 Grok CLI 固定版本的唯一来源，repository 与 service
	// 均基于它构造客户端身份；版本需与 https://x.ai/cli/stable 保持同步。
	CLIClientVersion = "0.2.114"
	// billingCLIUserAgent 是账单探测沿用的 pager/shell UA，
	// 与 cli_identity.go 中 workspace 风格的 CLIUserAgent 不同。
	billingCLIUserAgent = "grok-pager/" + CLIClientVersion + " grok-shell/" + CLIClientVersion + " (macos; aarch64)"

	BillingWeeklyPath  = "/billing?format=credits"
	BillingMonthlyPath = "/billing"

	SuperGrokLimitCents      = 15_000  // $150.00
	SuperGrokHeavyLimitCents = 150_000 // $1,500.00
)

// BillingPeriod 描述当前周度或月度窗口。
type BillingPeriod struct {
	Type  string `json:"type,omitempty"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// BillingProductUsage 表示周度 credits 窗口内单个产品的用量。
type BillingProductUsage struct {
	Product      string   `json:"product,omitempty"`
	UsagePercent *float64 `json:"usagePercent,omitempty"`
}

// BillingConfig 对应 /v1/billing 响应中的嵌套 config 对象。
type BillingConfig struct {
	CurrentPeriod        *BillingPeriod        `json:"currentPeriod,omitempty"`
	CreditUsagePercent   *float64              `json:"creditUsagePercent,omitempty"`
	ProductUsage         []BillingProductUsage `json:"productUsage,omitempty"`
	MonthlyLimit         json.RawMessage       `json:"monthlyLimit,omitempty"`
	Used                 json.RawMessage       `json:"used,omitempty"`
	OnDemandCap          json.RawMessage       `json:"onDemandCap,omitempty"`
	OnDemandUsed         json.RawMessage       `json:"onDemandUsed,omitempty"`
	PrepaidBalance       json.RawMessage       `json:"prepaidBalance,omitempty"`
	IsUnifiedBillingUser bool                  `json:"isUnifiedBillingUser,omitempty"`
	TopUpMethod          string                `json:"topUpMethod,omitempty"`
	BillingPeriodStart   string                `json:"billingPeriodStart,omitempty"`
	BillingPeriodEnd     string                `json:"billingPeriodEnd,omitempty"`
}

// BillingPayload 对应 /v1/billing 的顶层响应体。
type BillingPayload struct {
	Config *BillingConfig `json:"config,omitempty"`
}

// BillingProductSummary 是供前端使用的规范化产品用量记录。
type BillingProductSummary struct {
	Product      string   `json:"product"`
	UsagePercent *float64 `json:"usage_percent,omitempty"`
}

// BillingSummary 是合并周度和月度数据后的 billing 视图。
type BillingSummary struct {
	PeriodType           string                  `json:"period_type,omitempty"` // weekly、monthly 或 unknown
	UsagePercent         *float64                `json:"usage_percent,omitempty"`
	PeriodStart          string                  `json:"period_start,omitempty"`
	PeriodEnd            string                  `json:"period_end,omitempty"`
	ProductUsage         []BillingProductSummary `json:"product_usage,omitempty"`
	MonthlyLimitCents    *float64                `json:"monthly_limit_cents,omitempty"`
	UsedCents            *float64                `json:"used_cents,omitempty"`
	IncludedUsedCents    *float64                `json:"included_used_cents,omitempty"`
	BillingPeriodStart   string                  `json:"billing_period_start,omitempty"`
	BillingPeriodEnd     string                  `json:"billing_period_end,omitempty"`
	UsedPercent          *float64                `json:"used_percent,omitempty"`
	Plan                 string                  `json:"plan,omitempty"` // SuperGrok、SuperGrok Heavy 或空字符串
	StatusCode           int                     `json:"status_code,omitempty"`
	WeeklyStatusCode     int                     `json:"weekly_status_code,omitempty"`
	MonthlyStatusCode    int                     `json:"monthly_status_code,omitempty"`
	Source               string                  `json:"source,omitempty"`
	FetchedAt            string                  `json:"fetched_at,omitempty"`
	UpdatedAt            string                  `json:"updated_at,omitempty"`
	WeeklyUpdatedAt      string                  `json:"weekly_updated_at,omitempty"`
	MonthlyUpdatedAt     string                  `json:"monthly_updated_at,omitempty"`
	Partial              bool                    `json:"partial,omitempty"`
	FailedWindows        []string                `json:"failed_windows,omitempty"`
	PrepaidBalance       *float64                `json:"prepaid_balance,omitempty"`
	OnDemandCap          *float64                `json:"on_demand_cap,omitempty"`
	OnDemandUsed         *float64                `json:"on_demand_used,omitempty"`
	MonthlyLimit         *float64                `json:"monthly_limit,omitempty"`
	MonthlyUsed          *float64                `json:"monthly_used,omitempty"`
	TopUpMethod          string                  `json:"top_up_method,omitempty"`
	IsUnifiedBillingUser bool                    `json:"is_unified_billing_user,omitempty"`
}

// BuildBillingURL 构造 CLI chat proxy 的周度或月度 billing URL。
func BuildBillingURL(formatCredits bool) string {
	base := strings.TrimRight(DefaultCLIBaseURL, "/")
	if formatCredits {
		return base + BillingWeeklyPath
	}
	return base + BillingMonthlyPath
}

// BuildBillingURLWithValidator 按调用方解析的基础地址构造周度或月度 billing URL，
// 并先应用调用方的出站 URL 信任策略。使用自定义上游转发的账号会在同一上游执行额度探测。
func BuildBillingURLWithValidator(baseURL string, formatCredits bool, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	if formatCredits {
		return validatedBaseURL + BillingWeeklyPath, nil
	}
	return validatedBaseURL + BillingMonthlyPath, nil
}

// ApplyCLIBillingHeaders 为 billing GET 请求设置 Authorization 和 CLI 身份请求头。
func ApplyCLIBillingHeaders(req *http.Request, accessToken string) {
	if req == nil {
		return
	}
	token := strings.TrimSpace(accessToken)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CLITokenAuthHeader, CLITokenAuthValue)
	req.Header.Set(CLIClientVersionHeader, CLIClientVersion)
	req.Header.Set("User-Agent", billingCLIUserAgent)
}

// ParseBillingPayload 解析 billing API 响应体。
func ParseBillingPayload(body []byte) (*BillingPayload, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty billing body")
	}
	var payload BillingPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// BuildBillingSummary 将 billing 配置规范化为适合前端展示的摘要。
func BuildBillingSummary(config *BillingConfig) *BillingSummary {
	if config == nil {
		return nil
	}
	summary := &BillingSummary{}
	period := config.CurrentPeriod
	periodType := resolvePeriodType(period)
	creditUsage := cloneFloat(config.CreditUsagePercent)

	// 周度周期不能回退使用月度账单边界，否则周用量较高时会按月度截止时间停调账号。
	periodStart := ""
	periodEnd := ""
	if period != nil {
		periodStart = strings.TrimSpace(period.Start)
		periodEnd = strings.TrimSpace(period.End)
	}

	products := make([]BillingProductSummary, 0, len(config.ProductUsage))
	for _, item := range config.ProductUsage {
		product := strings.TrimSpace(item.Product)
		if product == "" {
			continue
		}
		products = append(products, BillingProductSummary{
			Product:      product,
			UsagePercent: cloneFloat(item.UsagePercent),
		})
	}

	monthlyLimit := parseCentValue(config.MonthlyLimit)
	used := parseCentValue(config.Used)
	// credits 响应中的绝对金额以美元计，月度 limit/used 则以美分计。
	prepaid := parseCentValue(config.PrepaidBalance)
	onDemandCap := parseCentValue(config.OnDemandCap)
	onDemandUsed := parseCentValue(config.OnDemandUsed)
	billingStart := strings.TrimSpace(config.BillingPeriodStart)
	billingEnd := strings.TrimSpace(config.BillingPeriodEnd)

	var includedUsed *float64
	if used != nil {
		if monthlyLimit != nil && *monthlyLimit > 0 {
			v := math.Min(*used, *monthlyLimit)
			includedUsed = &v
		} else {
			includedUsed = cloneFloat(used)
		}
	}

	var usedPercent *float64
	if monthlyLimit != nil && *monthlyLimit > 0 && includedUsed != nil {
		v := (*includedUsed / *monthlyLimit) * 100
		usedPercent = &v
	}

	hasWeekly := creditUsage != nil || periodType == "weekly" || len(products) > 0 || prepaid != nil || onDemandCap != nil || onDemandUsed != nil
	hasMonthly := monthlyLimit != nil || used != nil || (!hasWeekly && billingEnd != "")
	if !hasWeekly && !hasMonthly {
		return nil
	}

	if hasWeekly {
		if periodType == "unknown" {
			periodType = "weekly"
		}
		summary.PeriodType = periodType
		summary.UsagePercent = creditUsage
		summary.PeriodStart = periodStart
		summary.PeriodEnd = periodEnd
	} else {
		// 仅有月度数据时不要把月度百分比写入周度进度条使用的 UsagePercent。
		// 前端只在 PeriodType 为 weekly 时渲染周度进度条。
		summary.PeriodType = "monthly"
		summary.PeriodStart = billingStart
		summary.PeriodEnd = billingEnd
	}
	summary.ProductUsage = products
	summary.MonthlyLimitCents = monthlyLimit
	summary.UsedCents = used
	summary.IncludedUsedCents = includedUsed
	if hasMonthly {
		summary.BillingPeriodStart = billingStart
		summary.BillingPeriodEnd = billingEnd
	}
	summary.UsedPercent = usedPercent
	summary.PrepaidBalance = prepaid
	if onDemandCap != nil {
		summary.OnDemandCap = onDemandCap
	}
	if onDemandUsed != nil {
		summary.OnDemandUsed = onDemandUsed
	}
	// 管理界面的绝对金额行以美元展示月度美分值。
	if monthlyLimit != nil {
		v := *monthlyLimit / 100
		summary.MonthlyLimit = &v
	}
	if used != nil {
		v := *used / 100
		summary.MonthlyUsed = &v
	}
	summary.TopUpMethod = strings.TrimSpace(config.TopUpMethod)
	summary.IsUnifiedBillingUser = config.IsUnifiedBillingUser
	summary.Plan = resolvePlan(monthlyLimit)
	return summary
}

// MergeBillingProbeResult 更新探测成功的 billing 域，并为刷新失败的域保留旧值。
func MergeBillingProbeResult(previous, weekly, monthly *BillingSummary, weeklyOK, monthlyOK bool) *BillingSummary {
	var out BillingSummary
	if previous != nil {
		out = *previous
		previousUpdatedAt := previous.UpdatedAt
		if previousUpdatedAt == "" {
			previousUpdatedAt = previous.FetchedAt
		}
		if out.WeeklyUpdatedAt == "" && (out.UsagePercent != nil || len(out.ProductUsage) > 0) {
			out.WeeklyUpdatedAt = previousUpdatedAt
		}
		if out.MonthlyUpdatedAt == "" && (out.MonthlyLimitCents != nil || out.UsedPercent != nil) {
			out.MonthlyUpdatedAt = previousUpdatedAt
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if weeklyOK && weekly != nil {
		out.PeriodType = weekly.PeriodType
		out.UsagePercent = weekly.UsagePercent
		out.PeriodStart = weekly.PeriodStart
		out.PeriodEnd = weekly.PeriodEnd
		out.ProductUsage = weekly.ProductUsage
		// 预付和按需绝对金额通常来自 credits 周度响应。
		if weekly.PrepaidBalance != nil {
			out.PrepaidBalance = weekly.PrepaidBalance
		}
		if weekly.OnDemandCap != nil {
			out.OnDemandCap = weekly.OnDemandCap
		}
		if weekly.OnDemandUsed != nil {
			out.OnDemandUsed = weekly.OnDemandUsed
		}
		if weekly.TopUpMethod != "" {
			out.TopUpMethod = weekly.TopUpMethod
		}
		if weekly.IsUnifiedBillingUser {
			out.IsUnifiedBillingUser = true
		}
		out.WeeklyUpdatedAt = now
	}
	if monthlyOK && monthly != nil {
		if out.PeriodType == "" {
			out.PeriodType = "monthly"
		}
		out.MonthlyLimitCents = monthly.MonthlyLimitCents
		out.UsedCents = monthly.UsedCents
		out.IncludedUsedCents = monthly.IncludedUsedCents
		out.BillingPeriodStart = monthly.BillingPeriodStart
		out.BillingPeriodEnd = monthly.BillingPeriodEnd
		out.UsedPercent = monthly.UsedPercent
		out.MonthlyLimit = monthly.MonthlyLimit
		out.MonthlyUsed = monthly.MonthlyUsed
		// credits 缺少按需上限时，月度探测也可能提供该值。
		if monthly.OnDemandCap != nil && out.OnDemandCap == nil {
			out.OnDemandCap = monthly.OnDemandCap
		}
		if monthly.OnDemandUsed != nil && out.OnDemandUsed == nil {
			out.OnDemandUsed = monthly.OnDemandUsed
		}
		out.Plan = monthly.Plan
		out.MonthlyUpdatedAt = now
	}

	out.Partial = !weeklyOK || !monthlyOK
	out.FailedWindows = nil
	if !weeklyOK {
		out.FailedWindows = append(out.FailedWindows, "weekly")
	}
	if !monthlyOK {
		out.FailedWindows = append(out.FailedWindows, "monthly")
	}
	if !weeklyOK && !monthlyOK && previous == nil {
		return nil
	}
	return &out
}

// StampBillingSummary 设置拉取元数据。
func StampBillingSummary(summary *BillingSummary, statusCode int, source string) *BillingSummary {
	if summary == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	summary.StatusCode = statusCode
	summary.Source = source
	summary.FetchedAt = now
	summary.UpdatedAt = now
	return summary
}

func resolvePeriodType(period *BillingPeriod) string {
	if period == nil {
		return "unknown"
	}
	raw := strings.ToLower(strings.TrimSpace(period.Type))
	if strings.Contains(raw, "weekly") {
		return "weekly"
	}
	if strings.Contains(raw, "monthly") {
		return "monthly"
	}
	return "unknown"
}

func resolvePlan(monthlyLimitCents *float64) string {
	if monthlyLimitCents == nil {
		return ""
	}
	// 容忍浮点数计算产生的轻微误差。
	limit := math.Round(*monthlyLimitCents)
	switch limit {
	case SuperGrokLimitCents:
		return "SuperGrok"
	case SuperGrokHeavyLimitCents:
		return "SuperGrok Heavy"
	default:
		return ""
	}
}

func parseCentValue(raw json.RawMessage) *float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	// 对象形式：{"val": 123}
	var obj struct {
		Val any `json:"val"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Val != nil {
		return anyToFloat(obj.Val)
	}
	// 裸数字或字符串形式。
	var n any
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil
	}
	return anyToFloat(n)
}

func anyToFloat(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case float32:
		f := float64(n)
		return &f
	case int:
		f := float64(n)
		return &f
	case int64:
		f := float64(n)
		return &f
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil
		}
		return &f
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		return &f
	default:
		return nil
	}
}

func cloneFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	f := *v
	return &f
}

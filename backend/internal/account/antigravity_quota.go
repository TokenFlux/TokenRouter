// 账号拥有额度查询编排和展示，供应商 wire/错误由窄端口提供。
package account

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

type AntigravityQuotaClient interface {
	FetchAvailableModels(context.Context, string, string, int64) (*google.AntigravityFetchAvailableModelsResponse, map[string]any, error)
	LoadCodeAssist(context.Context, string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error)
}
type AntigravityQuotaOptions struct {
	NewClient                        func(string) (AntigravityQuotaClient, error)
	ReadLimit                        func() int64
	ResolveProxy                     func(context.Context, int64) (string, bool)
	ForbiddenBody                    func(error) (string, bool)
	ClassifyForbidden, ValidationURL func(string) string
	Warn                             func(string, ...any)
}
type AntigravityQuota struct{ Options AntigravityQuotaOptions }

const (
	ForbiddenTypeValidation = "validation"
	ForbiddenTypeViolation  = "violation"
	ForbiddenTypeForbidden  = "forbidden"

	// 机器可读的错误码
	ErrorCodeForbidden       = "forbidden"
	ErrorCodeUnauthenticated = "unauthenticated"
	ErrorCodeRateLimited     = "rate_limited"
	ErrorCodeNetworkError    = "network_error"
)

// CanFetch 检查是否可以获取此账户的额度
func (f *AntigravityQuota) CanFetch(account *Record) bool {
	if account.Platform != PlatformAntigravity {
		return false
	}
	accessToken := account.GetCredential("access_token")
	return accessToken != ""
}

// FetchQuota 获取 Antigravity 账户额度信息
func (f *AntigravityQuota) FetchQuota(ctx context.Context, account *Record, proxyURL string) (*QuotaResult, error) {
	accessToken := account.GetCredential("access_token")
	projectID := account.GetCredential("project_id")

	client, err := f.Options.NewClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create antigravity client failed: %w", err)
	}

	// 调用 API 获取配额
	modelsResp, modelsRaw, err := client.FetchAvailableModels(ctx, accessToken, projectID, f.Options.ReadLimit())
	if err != nil {
		// 403 Forbidden: 不报错，返回 is_forbidden 标记
		if forbiddenBody, ok := f.Options.ForbiddenBody(err); ok {
			now := time.Now()
			fbType := f.Options.ClassifyForbidden(forbiddenBody)
			return &QuotaResult{
				UsageInfo: &UsageInfo{
					UpdatedAt:       &now,
					IsForbidden:     true,
					ForbiddenReason: forbiddenBody,
					ForbiddenType:   fbType,
					ValidationURL:   f.Options.ValidationURL(forbiddenBody),
					NeedsVerify:     fbType == ForbiddenTypeValidation,
					IsBanned:        fbType == ForbiddenTypeViolation,
					ErrorCode:       ErrorCodeForbidden,
				},
			}, nil
		}
		return nil, err
	}

	// 调用 LoadCodeAssist 获取订阅等级和 AI Credits 余额（非关键路径，失败不影响主流程）
	tierRaw, tierNormalized, loadResp := f.fetchSubscriptionTier(ctx, client, accessToken)

	// 转换为 UsageInfo
	usageInfo := f.BuildUsageInfo(modelsResp, tierRaw, tierNormalized, loadResp)

	return &QuotaResult{
		UsageInfo: usageInfo,
		Raw:       modelsRaw,
	}, nil
}

// fetchSubscriptionTier 获取账号订阅等级，失败返回空字符串。
// 同时返回 LoadCodeAssistResponse，以便提取 AI Credits 余额。
func (f *AntigravityQuota) fetchSubscriptionTier(ctx context.Context, client AntigravityQuotaClient, accessToken string) (raw, normalized string, loadResp *google.AntigravityLoadCodeAssistResponse) {
	loadResp, _, err := client.LoadCodeAssist(ctx, accessToken)
	if err != nil {
		f.Options.Warn("failed to fetch subscription tier", "error", err)
		return "", "", nil
	}
	if loadResp == nil {
		return "", "", nil
	}

	raw = loadResp.GetTier() // 已有方法：paidTier > currentTier
	normalized = NormalizeAntigravityTier(raw)
	return raw, normalized, loadResp
}

// normalizeTier 将原始 tier 字符串归一化为 FREE/PRO/ULTRA/UNKNOWN
func NormalizeAntigravityTier(raw string) string {
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "ultra"):
		return "ULTRA"
	case strings.Contains(lower, "pro"):
		return "PRO"
	case strings.Contains(lower, "free"):
		return "FREE"
	default:
		return "UNKNOWN"
	}
}

// buildUsageInfo 将 API 响应转换为 UsageInfo。
func (f *AntigravityQuota) BuildUsageInfo(modelsResp *google.AntigravityFetchAvailableModelsResponse, tierRaw, tierNormalized string, loadResp *google.AntigravityLoadCodeAssistResponse) *UsageInfo {
	now := time.Now()
	info := &UsageInfo{
		UpdatedAt:               &now,
		AntigravityQuota:        make(map[string]*AntigravityModelQuota),
		AntigravityQuotaDetails: make(map[string]*AntigravityModelDetail),
		SubscriptionTier:        tierNormalized,
		SubscriptionTierRaw:     tierRaw,
	}

	// 遍历所有模型，填充 AntigravityQuota 和 AntigravityQuotaDetails
	for modelName, modelInfo := range modelsResp.Models {
		if modelInfo.QuotaInfo == nil {
			continue
		}

		// remainingFraction 是剩余比例 (0.0-1.0)，转换为使用率百分比
		utilization := int((1.0 - modelInfo.QuotaInfo.RemainingFraction) * 100)

		info.AntigravityQuota[modelName] = &AntigravityModelQuota{
			Utilization: utilization,
			ResetTime:   modelInfo.QuotaInfo.ResetTime,
		}

		// 填充模型详细能力信息
		detail := &AntigravityModelDetail{
			DisplayName:        modelInfo.DisplayName,
			SupportsImages:     modelInfo.SupportsImages,
			SupportsThinking:   modelInfo.SupportsThinking,
			ThinkingBudget:     modelInfo.ThinkingBudget,
			Recommended:        modelInfo.Recommended,
			MaxTokens:          modelInfo.MaxTokens,
			MaxOutputTokens:    modelInfo.MaxOutputTokens,
			SupportedMimeTypes: modelInfo.SupportedMimeTypes,
		}
		info.AntigravityQuotaDetails[modelName] = detail
	}

	// 废弃模型转发规则
	if len(modelsResp.DeprecatedModelIDs) > 0 {
		info.ModelForwardingRules = make(map[string]string, len(modelsResp.DeprecatedModelIDs))
		for oldID, deprecated := range modelsResp.DeprecatedModelIDs {
			info.ModelForwardingRules[oldID] = deprecated.NewModelID
		}
	}

	// 同时设置 FiveHour 用于兼容展示（取主要模型）
	priorityModels := []string{"claude-sonnet-4-20250514", "claude-sonnet-4", "gemini-2.5-pro"}
	for _, modelName := range priorityModels {
		if modelInfo, ok := modelsResp.Models[modelName]; ok && modelInfo.QuotaInfo != nil {
			utilization := (1.0 - modelInfo.QuotaInfo.RemainingFraction) * 100
			progress := &UsageProgress{
				Utilization: utilization,
			}
			if modelInfo.QuotaInfo.ResetTime != "" {
				if resetTime, err := time.Parse(time.RFC3339, modelInfo.QuotaInfo.ResetTime); err == nil {
					progress.ResetsAt = &resetTime
					progress.RemainingSeconds = int(time.Until(resetTime).Seconds())
				}
			}
			info.FiveHour = progress
			break
		}
	}

	if loadResp != nil {
		for _, credit := range loadResp.GetAvailableCredits() {
			info.AICredits = append(info.AICredits, AICredit{
				CreditType:     credit.CreditType,
				Amount:         credit.GetAmount(),
				MinimumBalance: credit.GetMinimumAmount(),
			})
		}
	}

	return info
}

// GetProxyURL 获取账户的代理 URL
func (f *AntigravityQuota) GetProxyURL(ctx context.Context, account *Record) string {
	if account.ProxyID == nil || f.Options.ResolveProxy == nil {
		return ""
	}
	value, ok := f.Options.ResolveProxy(ctx, *account.ProxyID)
	if !ok {
		return ""
	}
	return value
}

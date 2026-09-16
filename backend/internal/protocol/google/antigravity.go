// Antigravity 的 Google 内部 wire 变体保留字段与自定义编解码，不与公开 API 形状合并。
package google

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// AntigravityTokenResponse Google OAuth token 响应
type AntigravityTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// AntigravityUserInfo Google 用户信息
type AntigravityUserInfo struct {
	Email      string `json:"email"`
	Name       string `json:"name,omitempty"`
	GivenName  string `json:"given_name,omitempty"`
	FamilyName string `json:"family_name,omitempty"`
	Picture    string `json:"picture,omitempty"`
}

// AntigravityLoadCodeAssistRequest loadCodeAssist 请求
type AntigravityLoadCodeAssistRequest struct {
	Metadata struct {
		IDEType    string `json:"ideType"`
		IDEVersion string `json:"ideVersion"`
		IDEName    string `json:"ideName"`
	} `json:"metadata"`
}

// AntigravityTierInfo 账户类型信息
type AntigravityTierInfo struct {
	ID          string `json:"id"`          // free-tier, g1-pro-tier, g1-ultra-tier
	Name        string `json:"name"`        // 显示名称
	Description string `json:"description"` // 描述
}

// UnmarshalJSON supports both legacy string tiers and object tiers.
func (t *AntigravityTierInfo) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var id string
		if err := json.Unmarshal(data, &id); err != nil {
			return err
		}
		t.ID = id
		return nil
	}
	type alias AntigravityTierInfo
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = AntigravityTierInfo(decoded)
	return nil
}

// AntigravityIneligibleTier 不符合条件的层级信息
type AntigravityIneligibleTier struct {
	Tier *AntigravityTierInfo `json:"tier,omitempty"`
	// ReasonCode 不符合条件的原因代码，如 INELIGIBLE_ACCOUNT
	ReasonCode    string `json:"reasonCode,omitempty"`
	ReasonMessage string `json:"reasonMessage,omitempty"`
}

// AntigravityLoadCodeAssistResponse loadCodeAssist 响应
type AntigravityLoadCodeAssistResponse struct {
	CloudAICompanionProject string                       `json:"cloudaicompanionProject"`
	CurrentTier             *AntigravityTierInfo         `json:"currentTier,omitempty"`
	PaidTier                *AntigravityPaidTierInfo     `json:"paidTier,omitempty"`
	IneligibleTiers         []*AntigravityIneligibleTier `json:"ineligibleTiers,omitempty"`
}

// AntigravityPaidTierInfo 付费等级信息，包含 AI Credits 余额。
type AntigravityPaidTierInfo struct {
	ID               string                       `json:"id"`
	Name             string                       `json:"name"`
	Description      string                       `json:"description"`
	AvailableCredits []AntigravityAvailableCredit `json:"availableCredits,omitempty"`
}

// UnmarshalJSON 兼容 paidTier 既可能是字符串也可能是对象的情况。
func (p *AntigravityPaidTierInfo) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var id string
		if err := json.Unmarshal(data, &id); err != nil {
			return err
		}
		p.ID = id
		return nil
	}
	type alias AntigravityPaidTierInfo
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = AntigravityPaidTierInfo(raw)
	return nil
}

// AntigravityAvailableCredit 表示一条 AI Credits 余额记录。
type AntigravityAvailableCredit struct {
	CreditType                  string `json:"creditType,omitempty"`
	CreditAmount                string `json:"creditAmount,omitempty"`
	MinimumCreditAmountForUsage string `json:"minimumCreditAmountForUsage,omitempty"`
}

// GetAmount 将 creditAmount 解析为浮点数。
func (c *AntigravityAvailableCredit) GetAmount() float64 {
	if c.CreditAmount == "" {
		return 0
	}
	var value float64
	_, _ = fmt.Sscanf(c.CreditAmount, "%f", &value)
	return value
}

// GetMinimumAmount 将 minimumCreditAmountForUsage 解析为浮点数。
func (c *AntigravityAvailableCredit) GetMinimumAmount() float64 {
	if c.MinimumCreditAmountForUsage == "" {
		return 0
	}
	var value float64
	_, _ = fmt.Sscanf(c.MinimumCreditAmountForUsage, "%f", &value)
	return value
}

// AntigravityOnboardUserRequest onboardUser 请求
type AntigravityOnboardUserRequest struct {
	TierID   string `json:"tierId"`
	Metadata struct {
		IDEType    string `json:"ideType"`
		Platform   string `json:"platform,omitempty"`
		PluginType string `json:"pluginType,omitempty"`
	} `json:"metadata"`
}

// AntigravityOnboardUserResponse onboardUser 响应
type AntigravityOnboardUserResponse struct {
	Name     string         `json:"name,omitempty"`
	Done     bool           `json:"done"`
	Response map[string]any `json:"response,omitempty"`
}

// GetTier 获取账户类型
// 优先返回 paidTier（付费订阅级别），否则返回 currentTier
func (r *AntigravityLoadCodeAssistResponse) GetTier() string {
	if r.PaidTier != nil && r.PaidTier.ID != "" {
		return r.PaidTier.ID
	}
	if r.CurrentTier != nil {
		return r.CurrentTier.ID
	}
	return ""
}

// GetAvailableCredits 返回 paid tier 中的 AI Credits 余额列表。
func (r *AntigravityLoadCodeAssistResponse) GetAvailableCredits() []AntigravityAvailableCredit {
	if r.PaidTier == nil {
		return nil
	}
	return r.PaidTier.AvailableCredits
}

// AntigravityTierIDToPlanType 将 tier ID 映射为用户可见的套餐名。
func AntigravityTierIDToPlanType(tierID string) string {
	switch strings.ToLower(strings.TrimSpace(tierID)) {
	case "free-tier":
		return "Free"
	case "g1-pro-tier":
		return "Pro"
	case "g1-ultra-tier":
		return "Ultra"
	default:
		if tierID == "" {
			return "Free"
		}
		return tierID
	}
}

// AntigravityModelQuotaInfo 模型配额信息
type AntigravityModelQuotaInfo struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
}

// AntigravityModelInfo 模型信息
type AntigravityModelInfo struct {
	QuotaInfo          *AntigravityModelQuotaInfo `json:"quotaInfo,omitempty"`
	DisplayName        string                     `json:"displayName,omitempty"`
	SupportsImages     *bool                      `json:"supportsImages,omitempty"`
	SupportsThinking   *bool                      `json:"supportsThinking,omitempty"`
	ThinkingBudget     *int                       `json:"thinkingBudget,omitempty"`
	Recommended        *bool                      `json:"recommended,omitempty"`
	MaxTokens          *int                       `json:"maxTokens,omitempty"`
	MaxOutputTokens    *int                       `json:"maxOutputTokens,omitempty"`
	SupportedMimeTypes map[string]bool            `json:"supportedMimeTypes,omitempty"`
}

// AntigravityDeprecatedModelInfo 废弃模型转发信息
type AntigravityDeprecatedModelInfo struct {
	NewModelID string `json:"newModelId"`
}

// AntigravityFetchAvailableModelsRequest fetchAvailableModels 请求
type AntigravityFetchAvailableModelsRequest struct {
	Project string `json:"project"`
}

// AntigravityFetchAvailableModelsResponse fetchAvailableModels 响应
type AntigravityFetchAvailableModelsResponse struct {
	Models             map[string]AntigravityModelInfo           `json:"models"`
	DeprecatedModelIDs map[string]AntigravityDeprecatedModelInfo `json:"deprecatedModelIds,omitempty"`
}

// AntigravitySetUserSettingsRequest setUserSettings 请求体
type AntigravitySetUserSettingsRequest struct {
	UserSettings map[string]any `json:"user_settings"`
}

// AntigravityFetchUserInfoRequest fetchUserInfo 请求体
type AntigravityFetchUserInfoRequest struct {
	Project string `json:"project"`
}

// AntigravityFetchUserInfoResponse fetchUserInfo 响应体
type AntigravityFetchUserInfoResponse struct {
	UserSettings map[string]any `json:"userSettings,omitempty"`
	RegionCode   string         `json:"regionCode,omitempty"`
}

// IsPrivate 判断隐私是否已设置：userSettings 为空或不含 telemetryEnabled 表示已设置
func (r *AntigravityFetchUserInfoResponse) IsPrivate() bool {
	if r == nil || r.UserSettings == nil {
		return true
	}
	_, hasTelemetry := r.UserSettings["telemetryEnabled"]
	return !hasTelemetry
}

// AntigravitySetUserSettingsResponse setUserSettings 响应体
type AntigravitySetUserSettingsResponse struct {
	UserSettings map[string]any `json:"userSettings,omitempty"`
}

// IsSuccess 判断 setUserSettings 是否成功：返回 {"userSettings":{}} 且无 telemetryEnabled
func (r *AntigravitySetUserSettingsResponse) IsSuccess() bool {
	if r == nil {
		return false
	}
	// userSettings 为 nil 或空 map 均视为成功
	if len(r.UserSettings) == 0 {
		return true
	}
	// 如果包含 telemetryEnabled 字段，说明未成功清除
	_, hasTelemetry := r.UserSettings["telemetryEnabled"]
	return !hasTelemetry
}

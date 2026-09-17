package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
)

// 网关策略设置沿用原持久键，不将规则推入通用设置存储。
const (
	SettingKeyBetaPolicySettings       = "beta_policy_settings"
	SettingKeyOpenAIFastPolicySettings = "openai_fast_policy_settings"
	SettingKeyRectifierSettings        = "rectifier_settings"
)

// Fast 策略复用唯一的纯策略实现。
type OpenAIFastPolicySettings = tierpolicy.OpenAIFastPolicySettings

// RuntimeSettingsStore 仅提供运行规则所需存取。
type RuntimeSettingsStore interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
	GetValue(context.Context, string) (string, error)
	Set(context.Context, string, string) error
}

// RuntimeSettings 的平台缺省规则通过值投影提供，核心不导入具体上游。
type RuntimeSettings struct {
	clientOptions               ClientSettingsOptions
	antigravityUAVersionCache   atomic.Value
	antigravityUAVersionSF      singleflight.Group
	openAICodexUACache          atomic.Value
	openAICodexUASF             singleflight.Group
	openAIAllowCodexPluginCache atomic.Value
	openAIAllowCodexPluginSF    singleflight.Group
	versionBoundsCache          atomic.Value
	versionBoundsSF             singleflight.Group
	gatewayForwardingCache      atomic.Value
	gatewayForwardingSF         singleflight.Group
	settingRepo                 RuntimeSettingsStore
	notFound                    error
	defaultBeta                 func() *BetaPolicySettings
}

// NewRuntimeSettings 构造不回源；默认规则工厂由静态装配提供。
func NewRuntimeSettings(repo RuntimeSettingsStore, notFound error, betaDefaults func() *BetaPolicySettings, clientOptions ...ClientSettingsOptions) *RuntimeSettings {
	value := &RuntimeSettings{settingRepo: repo, notFound: notFound, defaultBeta: betaDefaults}
	if len(clientOptions) > 0 {
		value.clientOptions = clientOptions[0]
	}
	return value
}

// RectifierSettings 由网关拥有请求策略值。
type RectifierSettings struct {
	Enabled                  bool     `json:"enabled"`                    // 总开关
	ThinkingSignatureEnabled bool     `json:"thinking_signature_enabled"` // Thinking 签名整流
	ThinkingBudgetEnabled    bool     `json:"thinking_budget_enabled"`    // Thinking Budget 整流
	APIKeySignatureEnabled   bool     `json:"apikey_signature_enabled"`   // API Key 签名整流开关
	APIKeySignaturePatterns  []string `json:"apikey_signature_patterns"`  // API Key 自定义匹配关键词
}

// DefaultRectifierSettings 保留原默认开启的整流策略。
func DefaultRectifierSettings() *RectifierSettings {
	return &RectifierSettings{
		Enabled:                  true,
		ThinkingSignatureEnabled: true,
		ThinkingBudgetEnabled:    true,
	}
}

// BetaPolicyRule 是网关管理策略的独立投影，不依赖具体供应商实现。
type BetaPolicyRule struct {
	BetaToken            string   `json:"beta_token"`                       // beta token 值
	Action               string   `json:"action"`                           // "pass" | "filter" | "block"
	Scope                string   `json:"scope"`                            // "all" | "oauth" | "apikey" | "bedrock"
	ErrorMessage         string   `json:"error_message,omitempty"`          // 自定义错误消息 (action=block 时生效)
	ModelWhitelist       []string `json:"model_whitelist,omitempty"`        // 模型匹配模式列表（为空=对所有模型生效）
	FallbackAction       string   `json:"fallback_action,omitempty"`        // 未匹配白名单的模型的处理方式
	FallbackErrorMessage string   `json:"fallback_error_message,omitempty"` // 未匹配白名单时的自定义错误消息 (fallback_action=block 时生效)
}

// BetaPolicySettings 是网关管理策略的独立投影，不依赖具体供应商实现。
type BetaPolicySettings struct {
	Rules []BetaPolicyRule `json:"rules"`
}

// Beta 操作和值域与已有管理协议保持一致。
const (
	BetaPolicyActionPass   = "pass"   // 透传，不做任何处理
	BetaPolicyActionFilter = "filter" // 过滤，从 beta header 中移除该 token
	BetaPolicyActionBlock  = "block"  // 拦截，直接返回错误

	BetaPolicyScopeAll     = "all"     // 所有账号类型
	BetaPolicyScopeOAuth   = "oauth"   // 仅 OAuth 账号
	BetaPolicyScopeAPIKey  = "apikey"  // 仅 API Key 账号
	BetaPolicyScopeBedrock = "bedrock" // 仅 AWS Bedrock 账号
)

// GetRectifierSettings 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) GetRectifierSettings(ctx context.Context) (*RectifierSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRectifierSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultRectifierSettings(), nil
		}
		return nil, fmt.Errorf("get rectifier settings: %w", err)
	}
	if value == "" {
		return DefaultRectifierSettings(), nil
	}

	var settings RectifierSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultRectifierSettings(), nil
	}

	return &settings, nil
}

// SetRectifierSettings 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) SetRectifierSettings(ctx context.Context, settings *RectifierSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal rectifier settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyRectifierSettings, string(data))
}

// IsSignatureRectifierEnabled 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) IsSignatureRectifierEnabled(ctx context.Context) bool {
	settings, err := s.GetRectifierSettings(ctx)
	if err != nil {
		return true // fail-open: 查询失败时默认启用
	}
	return settings.Enabled && settings.ThinkingSignatureEnabled
}

// IsBudgetRectifierEnabled 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) IsBudgetRectifierEnabled(ctx context.Context) bool {
	settings, err := s.GetRectifierSettings(ctx)
	if err != nil {
		return true // fail-open: 查询失败时默认启用
	}
	return settings.Enabled && settings.ThinkingBudgetEnabled
}

// GetBetaPolicySettings 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) GetBetaPolicySettings(ctx context.Context) (*BetaPolicySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyBetaPolicySettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return s.defaultBeta(), nil
		}
		return nil, fmt.Errorf("get beta policy settings: %w", err)
	}
	if value == "" {
		return s.defaultBeta(), nil
	}

	var settings BetaPolicySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return s.defaultBeta(), nil
	}

	return &settings, nil
}

// SetBetaPolicySettings 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) SetBetaPolicySettings(ctx context.Context, settings *BetaPolicySettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	validActions := map[string]bool{
		BetaPolicyActionPass: true, BetaPolicyActionFilter: true, BetaPolicyActionBlock: true,
	}
	validScopes := map[string]bool{
		BetaPolicyScopeAll: true, BetaPolicyScopeOAuth: true, BetaPolicyScopeAPIKey: true, BetaPolicyScopeBedrock: true,
	}

	for i, rule := range settings.Rules {
		if rule.BetaToken == "" {
			return fmt.Errorf("rule[%d]: beta_token cannot be empty", i)
		}
		if !validActions[rule.Action] {
			return fmt.Errorf("rule[%d]: invalid action %q", i, rule.Action)
		}
		if !validScopes[rule.Scope] {
			return fmt.Errorf("rule[%d]: invalid scope %q", i, rule.Scope)
		}
		// Validate model_whitelist patterns
		for j, pattern := range rule.ModelWhitelist {
			trimmed := strings.TrimSpace(pattern)
			if trimmed == "" {
				return fmt.Errorf("rule[%d]: model_whitelist[%d] cannot be empty", i, j)
			}
			settings.Rules[i].ModelWhitelist[j] = trimmed
		}
		// Validate fallback_action
		if rule.FallbackAction != "" && !validActions[rule.FallbackAction] {
			return fmt.Errorf("rule[%d]: invalid fallback_action %q", i, rule.FallbackAction)
		}
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal beta policy settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyBetaPolicySettings, string(data))
}

// GetOpenAIFastPolicySettings 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) GetOpenAIFastPolicySettings(ctx context.Context) (*OpenAIFastPolicySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIFastPolicySettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return tierpolicy.Default(), nil
		}
		return nil, fmt.Errorf("get openai fast policy settings: %w", err)
	}
	if value == "" {
		return tierpolicy.Default(), nil
	}

	var settings OpenAIFastPolicySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		// JSON 损坏时静默 fallback 到默认配置会让策略意外失效（管理员配
		// 置的 block/filter 规则被忽略）。记录 Warn 让运维能在出现异常
		// 行为时定位到 settings 表里的脏数据。
		slog.Warn("failed to unmarshal openai fast policy settings, falling back to defaults",
			"error", err,
			"key", SettingKeyOpenAIFastPolicySettings)
		return tierpolicy.Default(), nil
	}

	return &settings, nil
}

// SetOpenAIFastPolicySettings 保留原网关策略设置的缺省、校验及持久化行为。
func (s *RuntimeSettings) SetOpenAIFastPolicySettings(ctx context.Context, settings *OpenAIFastPolicySettings) error {
	value, err := tierpolicy.Prepare(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyOpenAIFastPolicySettings, value)
}

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/TokenFlux/TokenRouter/internal/site"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	accounttransfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"

	"strings"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// SystemSettings 仅保留 S16 旧调用的值别名；所有读取和准备规则由所属模块执行。
type SystemSettings = composite.Snapshot

type DefaultSubscriptionSetting = identity.DefaultSubscriptionSetting

type PublicSettings = site.PublicSettings

type LoginAgreementDocument = site.LoginAgreementDocument

// WeChatConnectOAuthConfig 保留旧调用类型，生效配置由 identity 唯一拥有。
type WeChatConnectOAuthConfig = identity.WeChatConnectOAuthConfig

type StreamTimeoutSettings = accountcore.StreamTimeoutSettings

const StreamTimeoutActionTempUnsched = accountcore.StreamTimeoutActionTempUnsched
const StreamTimeoutActionError = accountcore.StreamTimeoutActionError
const StreamTimeoutActionNone = accountcore.StreamTimeoutActionNone

// DefaultStreamTimeoutSettings 返回默认的流超时配置
func DefaultStreamTimeoutSettings() *StreamTimeoutSettings {
	return accountcore.DefaultStreamTimeoutSettings()
}

// RectifierSettings 请求整流器配置
type RectifierSettings = gateway.RectifierSettings

// DefaultRectifierSettings 返回默认的整流器配置（全部启用）
func DefaultRectifierSettings() *RectifierSettings { return gateway.DefaultRectifierSettings() }

const BetaPolicyActionPass = claude.BetaPolicyActionPass
const BetaPolicyActionFilter = claude.BetaPolicyActionFilter
const BetaPolicyActionBlock = claude.BetaPolicyActionBlock
const BetaPolicyScopeAll = claude.BetaPolicyScopeAll
const BetaPolicyScopeOAuth = claude.BetaPolicyScopeOAuth
const BetaPolicyScopeAPIKey = claude.BetaPolicyScopeAPIKey
const BetaPolicyScopeBedrock = claude.BetaPolicyScopeBedrock

type BetaPolicyRule = claude.BetaPolicyRule

type BetaPolicySettings = claude.BetaPolicySettings

type OverloadCooldownSettings = accountcore.OverloadCooldownSettings

type RateLimit429CooldownSettings = accountcore.RateLimit429CooldownSettings

// OpenAIImagesOAuthUnavailableCooldownSettings controls how long an OAuth account's image capability is paused when unavailable.
type OpenAIImagesOAuthUnavailableCooldownSettings = accountcore.OpenAIImagesOAuthUnavailableCooldownSettings

const ()

// OpenAIAPIKeyHealthBreakerSettings controls cross-instance failure counting for OpenAI pool API keys.
type OpenAIAPIKeyHealthBreakerSettings = accountcore.OpenAIAPIKeyHealthBreakerSettings

func DefaultOpenAIAPIKeyHealthBreakerSettings() *OpenAIAPIKeyHealthBreakerSettings {
	return accountcore.DefaultOpenAIAPIKeyHealthBreakerSettings()
}

// DefaultOverloadCooldownSettings 返回默认的过载冷却配置（启用，10分钟）
func DefaultOverloadCooldownSettings() *OverloadCooldownSettings {
	return accountcore.DefaultOverloadCooldownSettings()
}

type OpenAI403CooldownSettings = accountcore.OpenAI403CooldownSettings

func DefaultOpenAI403CooldownSettings() *OpenAI403CooldownSettings {
	return accountcore.DefaultOpenAI403CooldownSettings()
}

func DefaultRateLimit429CooldownSettings() *RateLimit429CooldownSettings {
	return accountcore.DefaultRateLimit429CooldownSettings()
}

func DefaultOpenAIImagesOAuthUnavailableCooldownSettings() *OpenAIImagesOAuthUnavailableCooldownSettings {
	return accountcore.DefaultOpenAIImagesOAuthUnavailableCooldownSettings()
}

func DefaultBetaPolicySettings() *BetaPolicySettings { return claude.DefaultBetaPolicySettings() }

// Fast 策略值类型归网关，旧消费者使用别名。
const OpenAIFastTierAny = tierpolicy.OpenAIFastTierAny
const OpenAIFastTierPriority = tierpolicy.OpenAIFastTierPriority
const OpenAIFastTierUltrafast = tierpolicy.OpenAIFastTierUltrafast
const OpenAIFastTierFlex = tierpolicy.OpenAIFastTierFlex
const OpenAIFastPolicyActionForcePriority = tierpolicy.OpenAIFastPolicyActionForcePriority
const OpenAIFastPolicyActionForceUltrafast = tierpolicy.OpenAIFastPolicyActionForceUltrafast

type OpenAIFastPolicyRule = tierpolicy.OpenAIFastPolicyRule
type OpenAIFastPolicySettings = tierpolicy.OpenAIFastPolicySettings

type OpenAIOAuthImportAccountDefaults = accounttransfer.OpenAIOAuthImportAccountDefaults

type OpenAIOAuthImportDefaults = accounttransfer.OpenAIOAuthImportDefaults

// DefaultOpenAIOAuthImportDefaults 返回 OpenAI OAuth 导入模板的内置默认值。
func DefaultOpenAIOAuthImportDefaults() *OpenAIOAuthImportDefaults {
	return accountcore.DefaultOpenAIOAuthImportDefaults()
}

// DefaultOpenAIFastPolicySettings 返回默认的 OpenAI fast 策略配置。
// 默认不配置任何规则，保留 OpenAI 上游 service_tier 语义；管理员如需
// 限制 priority/flex，可以在 admin UI 中显式配置 filter 或 block 规则。
func DefaultOpenAIFastPolicySettings() *OpenAIFastPolicySettings {
	return tierpolicy.Default()
}

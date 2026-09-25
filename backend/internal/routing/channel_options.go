// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const (
	StatusActive                 = "active"
	StatusDisabled               = "disabled"
	PlatformOpenAI               = capability.PlatformOpenAI
	PlatformAnthropic            = capability.PlatformAnthropic
	PlatformGemini               = capability.PlatformGemini
	PlatformAntigravity          = capability.PlatformAntigravity
	PlatformQoder                = capability.PlatformQoder
	PlatformGrok                 = capability.PlatformGrok
	PlatformKimi                 = capability.PlatformKimi
	PlatformZhipu                = capability.PlatformZhipu
	PlatformDeepseek             = capability.PlatformDeepseek
	featureKeyBedrockCCCompat    = "bedrock_cc_compat"
	featureKeyWebSearchEmulation = "web_search_emulation"
)

type ModelPricing = pricing.ModelPricing

// GroupAuthInvalidator 只发布原有提交后认证失效。
type GroupAuthInvalidator interface{ InvalidateAuthCacheByGroupID(context.Context, int64) }
type ChannelOptions struct {
	// Warn 接收读取失败诊断，日志后端由外层装配。
	Warn         func(string, ...any)
	Now          func() time.Time
	LoadLocation func(string) (*time.Location, error)
}

// ChannelValidation 隐藏完整的价卡、冲突和时间规则检查顺序。
type ChannelValidation struct {
	LoadLocation func(string) (*time.Location, error)
}

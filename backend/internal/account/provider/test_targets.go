package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// TestTargets 在一次原生账号读取后选择固定的平台适配器，不建立缓存或第二套测试用例。
type TestTargets struct {
	Read        func(context.Context, int64) (*account.Record, error)
	Qoder       *QoderAccountTest
	Gemini      *GeminiAccountTest
	Grok        *GrokAccountTest
	Anthropic   *AnthropicAccountTest
	OpenAI      *OpenAIAccountTest
	CN          *CNAccountTest
	Antigravity *AntigravityAccountTest
}

func (t *TestTargets) LoadTestTarget(ctx context.Context, request account.TestRequest) (account.TestTarget, error) {
	value, err := t.Read(ctx, request.AccountID)
	if err != nil {
		return nil, err
	}
	switch value.Platform {
	case account.PlatformQoder:
		return t.Qoder.Target(value), nil
	case account.PlatformGemini:
		return t.Gemini.Target(value), nil
	case account.PlatformGrok:
		return t.Grok.Target(value), nil
	case account.PlatformOpenAI:
		return t.OpenAI.Target(value), nil
	case account.PlatformKimi, account.PlatformZhipu, account.PlatformDeepseek:
		return t.CN.Target(value), nil
	case account.PlatformAntigravity:
		return t.Antigravity.Target(value), nil
	default:
		return t.Anthropic.Target(value), nil
	}
}

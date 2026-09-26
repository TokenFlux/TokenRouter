package account

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// oauthForeignModelPrefixes 列出明确属于其他厂商家族的模型名前缀。
// Codex 上游不可能服务这些模型：转发阶段 normalizeOpenAIModelForUpstream
// 对未知模型原样透传，上游必然返回不可重试的 400。
//
// 已知的其他厂商模型在此排除；未知或自定义别名继续交给后续模型规则和上游校验。
// 本函数只按传入的模型名筛选，不在此执行分组或账号映射。
var oauthForeignModelPrefixes = []string{
	"deepseek-",
	"glm-",
	"kimi-",
	"moonshot-",
	"qwen-",
	"qwen2-",
	"qwen3-",
	"qwen4-",
	"qwq-",
	"minimax-",
	"gemini-",
	"gemma-",
	"grok-",
	"doubao-",
	"hunyuan-",
	"llama-",
	"llama2-",
	"llama3-",
	"meta-llama",
	"mistral-",
	"mixtral-",
	"baichuan-",
	"ernie-",
	"step-",
	"seed-",
	"yi-",
}

// IsOpenAIOAuthServableModel 判断 OpenAI OAuth 账号能否服务请求模型。
// 默认仍为“允许”，仅排除明确属于其他厂商的模型家族；这类模型
// 原样透传必然被 Codex 上游以不可重试的 400 拒绝，应在调度阶段跳过该账号。
func IsOpenAIOAuthServableModel(requestedModel string) bool {
	model := strings.ToLower(capability.LastOpenAIModelSegment(requestedModel))
	if model == "" {
		return true // 空模型交由上层必填校验处理。
	}
	// Kimi Code 官方 bare model ID 没有厂商前缀，前缀黑名单无法识别。
	if model == "k3" || model == "k3-256k" {
		return false
	}
	for _, prefix := range oauthForeignModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return false
		}
	}
	return true
}

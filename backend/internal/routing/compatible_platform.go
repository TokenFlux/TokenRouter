// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

// NormalizeOpenAICompatiblePlatform 保留 grok 与国产 OpenAI 兼容供应商（kimi/zhipu/
// deepseek）的原值，其他值一律归一为 openai。调度器据此对账号与请求做精确平台匹配：
// kimi 分组请求只命中 kimi 账号，语义与 openai/grok 一致。
// （upstream 曾将本函数改为未导出 normalizeOpenAICompatiblePlatform，本分支的
// handler 调度入口仍需导出，保持导出名。）
func NormalizeOpenAICompatiblePlatform(platform string) string {
	switch platform {
	case PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return platform
	default:
		return PlatformOpenAI
	}
}

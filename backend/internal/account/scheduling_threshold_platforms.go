// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

// AllowedSchedulingThresholdPlatforms 是允许设置账号自动停调阈值的平台列表。
// openai/anthropic/grok 有原生用量窗口；kimi/zhipu 的 Coding Plan 同样暴露 5h/weekly
// 滚动窗口，纳入阈值评估。deepseek 为余额型，走余额检测而非阈值。
var AllowedSchedulingThresholdPlatforms = []string{
	PlatformOpenAI,
	PlatformAnthropic,
	PlatformGrok,
	PlatformKimi,
	PlatformZhipu,
}

const AnthropicFableRateLimitKey = "claude-fable-5"

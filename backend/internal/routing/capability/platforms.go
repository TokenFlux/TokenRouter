package capability

// PlatformAnthropic 等常量定义能力目录中的平台标识。
const (
	PlatformAnthropic   = "anthropic"
	PlatformOpenAI      = "openai"
	PlatformGemini      = "gemini"
	PlatformAntigravity = "antigravity"
	PlatformGrok        = "grok"
	PlatformQoder       = "qoder"
	PlatformKimi        = "kimi"
	PlatformZhipu       = "zhipu"
	PlatformDeepseek    = "deepseek"
	PlatformJev         = "jev"
)

// ProviderPlatforms 返回提供商平台目录的独立副本，供跨平台分组构造候选池。
func ProviderPlatforms() []string {
	return []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformJev}
}

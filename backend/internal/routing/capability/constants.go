package capability

// 平台与账号类型属于能力目录，不依赖旧业务实体。
// Platform constants
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
)

// Account type constants
const (
	AccountTypeOAuth          = "oauth"           // OAuth类型账号（full scope: profile + inference）
	AccountTypeSetupToken     = "setup-token"     // Setup Token类型账号（inference only scope）
	AccountTypeAPIKey         = "apikey"          // API Key类型账号
	AccountTypeUpstream       = "upstream"        // 上游透传类型账号（通过 Base URL + API Key 连接上游）
	AccountTypeBedrock        = "bedrock"         // AWS Bedrock 类型账号（通过 SigV4 签名或 API Key 连接 Bedrock，由 credentials.auth_mode 区分）
	AccountTypeServiceAccount = "service_account" // Google Service Account 类型账号（用于 Vertex AI）
	AccountTypeCosy           = "cosy"            // Qoder COSY 协议账号
)

const OpenAIAuthModePersonalAccessToken = "personalAccessToken"
const OpenAIAuthModeAgentIdentity = "agentIdentity"

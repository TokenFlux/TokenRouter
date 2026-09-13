// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

type OpenAIEndpointCapability string

const (
	OpenAIEndpointCapabilityTextGeneration OpenAIEndpointCapability = "text_generation"
	OpenAIEndpointCapabilityEmbeddings     OpenAIEndpointCapability = "embeddings"
	OpenAIEndpointCapabilityAlphaSearch    OpenAIEndpointCapability = "alpha_search"
	// OpenAIEndpointCapabilityLive 表示仅 ChatGPT OAuth 账号支持的 Frameless Live 能力。
	OpenAIEndpointCapabilityLive OpenAIEndpointCapability = "live"
	// OpenAIEndpointCapabilityGrokMediaGeneration 用于排除被显式禁用或计费资格
	// 探测遭拒的 Grok 账号；视频状态查询不要求该能力，以便继续查询已提交的任务。
	OpenAIEndpointCapabilityGrokMediaGeneration OpenAIEndpointCapability = "grok_media_generation"
	// OpenAIEndpointCapabilityResponses 表示上游确实提供 /v1/responses 端点。
	// 由启用的原生协议与路由规则判断，不读取已废弃的 Responses 探测字段。
	OpenAIEndpointCapabilityResponses OpenAIEndpointCapability = "responses"
	// OpenAIEndpointCapabilityRemoteCompactionV2 表示账号可承接原生 remote_compaction_v2。
	// 它仍要求普通 Responses 能力，额外受账号级 V2 开关控制。
	OpenAIEndpointCapabilityRemoteCompactionV2 OpenAIEndpointCapability = "remote_compaction_v2"
)

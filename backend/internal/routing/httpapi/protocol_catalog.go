package httpapi

import (
	"maps"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/gin-gonic/gin"
)

// ProtocolAccountProfile 描述可展示的原生选项，不包含令牌或账号标识。
type ProtocolAccountProfile struct {
	Platform  string                `json:"platform"`
	Type      string                `json:"type"`
	AuthMode  string                `json:"auth_mode"`
	Protocols []protocol.ProtocolID `json:"protocols"`
}

type ProtocolGroupProfile struct {
	Platform         string                                        `json:"platform"`
	Protocols        []protocol.ProtocolID                         `json:"protocols"`
	Defaults         []protocol.ProtocolID                         `json:"defaults"`
	FallbackTargets  map[protocol.ProtocolID][]protocol.ProtocolID `json:"fallback_targets"`
	DefaultFallbacks map[protocol.ProtocolID]protocol.ProtocolID   `json:"default_fallbacks"`
}

// ProtocolCatalogResponse 显式描述目录响应，便于调用方和契约测试检查完整结构。
type ProtocolCatalogResponse struct {
	Protocols           []Protocol               `json:"protocols"`
	Accounts            []ProtocolAccountProfile `json:"accounts"`
	Groups              []ProtocolGroupProfile   `json:"groups"`
	AuxiliaryOperations []AuxiliaryOperation     `json:"auxiliary_operations"`
}

// AdminProtocolCatalog 是前后端共用的唯一目录投影。
func AdminProtocolCatalog(endpoints map[protocol.ProtocolID]string) ProtocolCatalogResponse {
	accounts := []ProtocolAccountProfile{}
	groups := []ProtocolGroupProfile{}
	for _, platform := range []string{capability.PlatformAnthropic, capability.PlatformOpenAI, capability.PlatformGemini, capability.PlatformAntigravity, capability.PlatformGrok, capability.PlatformQoder, capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken, capability.AccountTypeAPIKey, capability.AccountTypeUpstream, capability.AccountTypeBedrock, capability.AccountTypeServiceAccount, capability.AccountTypeCosy} {
			modes := []string{""}
			if platform == capability.PlatformOpenAI && accountType == capability.AccountTypeOAuth {
				modes = append(modes, capability.OpenAIAuthModePersonalAccessToken, capability.OpenAIAuthModeAgentIdentity)
			}
			for _, mode := range modes {
				accounts = append(accounts, ProtocolAccountProfile{platform, accountType, mode, capability.NativeProtocolOptions(platform, accountType, mode)})
			}
		}
		supported := capability.SupportedGroupClientProtocols(platform)
		targets := map[protocol.ProtocolID][]protocol.ProtocolID{}
		for _, source := range supported {
			targets[source] = capability.ProtocolFallbackTargets(platform, source)
		}
		groups = append(groups, ProtocolGroupProfile{platform, supported, capability.DefaultGroupClientProtocols(platform), targets, capability.DefaultProtocolFallbacks(platform)})
	}
	return ProtocolCatalogResponse{
		Protocols: publicProtocols(endpoints), Accounts: accounts, Groups: groups,
		AuxiliaryOperations: AuxiliaryOperations(),
	}
}

// AuxiliaryOperation 登记主协议的辅助操作；资源生命周期不会生成新的协议复选框。
type AuxiliaryOperation struct {
	Operation     string              `json:"operation"`
	Protocol      protocol.ProtocolID `json:"protocol,omitempty"`
	Authorization string              `json:"authorization"`
}

func AuxiliaryOperations() []AuxiliaryOperation {
	return []AuxiliaryOperation{
		{"/messages/count_tokens", protocol.ProtocolAnthropicMessages, "protocol"},
		{"/responses/input_tokens", protocol.ProtocolOpenAIResponses, "protocol"},
		{"Gemini :countTokens", protocol.ProtocolGeminiGenerateContent, "protocol"},
		{"native_compaction_v2", protocol.ProtocolOpenAIResponses, "capability"},
		{"http_continuation", protocol.ProtocolOpenAIResponses, "capability"},
		{"responses_image_tools", protocol.ProtocolOpenAIResponses, "image_policy"},
		{"/models, /usage", "", "local"},
		{"video status/content", protocol.ProtocolVideosGenerations, "resource"},
		{"batch status/download/cancel/delete", protocol.ProtocolImageBatches, "resource"},
		{"Live sideband", protocol.ProtocolLive, "session"},
		{"custom voice read/update/delete/audio", protocol.ProtocolCustomVoices, "protocol_and_resource"},
	}
}

// Protocol 保留管理员目录的 JSON 顺序和字段。
type Protocol struct {
	ID           protocol.ProtocolID `json:"id"`
	Name         string              `json:"name"`
	Endpoint     string              `json:"endpoint"`
	UpstreamOnly bool                `json:"upstream_only"`
	Platforms    []string            `json:"platforms"`
}

func publicProtocols(endpoints map[protocol.ProtocolID]string) []Protocol {
	catalog := capability.ProtocolCatalog()
	out := make([]Protocol, len(catalog))
	for i, p := range catalog {
		out[i] = Protocol{ID: p.ID, Name: p.Name, Endpoint: endpoints[p.ID], UpstreamOnly: p.UpstreamOnly, Platforms: p.Platforms}
	}
	return out
}

// NewProtocolCatalogHandler 冻结 app 注入的展示投影，不反向依赖网关路由。
func NewProtocolCatalogHandler(endpoints map[protocol.ProtocolID]string) gin.HandlerFunc {
	snapshot := maps.Clone(endpoints)
	return func(c *gin.Context) { httpx.Success(c, AdminProtocolCatalog(snapshot)) }
}

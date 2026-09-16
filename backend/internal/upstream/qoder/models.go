package qoder

import "strings"

// Model 表示暴露给管理端和模型选择 API 的 Qoder 模型。
type Model struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// globalModels 是国际站当前客户端展示的稳定模型快照。
var globalModels = []Model{
	{ID: "claude-opus-4-6", Type: "model", DisplayName: "Claude Opus 4.6", CreatedAt: ""},
	{ID: "auto", Type: "model", DisplayName: "Qoder Auto", CreatedAt: ""},
	{ID: "performance", Type: "model", DisplayName: "Qoder Performance", CreatedAt: ""},
	{ID: "efficient", Type: "model", DisplayName: "Qoder Efficient", CreatedAt: ""},
	{ID: "lite", Type: "model", DisplayName: "Qoder Lite", CreatedAt: ""},
	{ID: "qwen3.8-max", Type: "model", DisplayName: "Qwen3.8-Max", CreatedAt: ""},
	{ID: "qwen3.7-max", Type: "model", DisplayName: "Qwen3.7-Max", CreatedAt: ""},
	{ID: "qwen3.7-plus", Type: "model", DisplayName: "Qwen3.7-Plus", CreatedAt: ""},
	// Kimi-K3 与 Kimi-K2.7-Code 是 Qoder 当前同时提供的两个独立模型。
	{ID: "kimi-k3", Type: "model", DisplayName: "Kimi-K3", CreatedAt: ""},
	{ID: "kimi-k2.7-code", Type: "model", DisplayName: "Kimi-K2.7-Code", CreatedAt: ""},
	{ID: "glm-5.3", Type: "model", DisplayName: "GLM-5.3", CreatedAt: ""},
	{ID: "glm-5.2", Type: "model", DisplayName: "GLM-5.2", CreatedAt: ""},
	{ID: "deepseek-v4-pro", Type: "model", DisplayName: "DeepSeek-V4-Pro", CreatedAt: ""},
	{ID: "deepseek-v4-flash", Type: "model", DisplayName: "DeepSeek-V4-Flash", CreatedAt: ""},
	{ID: "minimax-m3", Type: "model", DisplayName: "MiniMax-M3", CreatedAt: ""},
}

// cnModels 是国内站当前客户端展示的稳定模型快照。
var cnModels = []Model{
	{ID: "auto", Type: "model", DisplayName: "Qoder Auto", CreatedAt: ""},
	{ID: "qwen3.8-max", Type: "model", DisplayName: "Qwen3.8-Max", CreatedAt: ""},
	{ID: "qwen3.7-max", Type: "model", DisplayName: "Qwen3.7-Max", CreatedAt: ""},
	{ID: "qwen3.7-plus", Type: "model", DisplayName: "Qwen3.7-Plus", CreatedAt: ""},
	{ID: "qwen3.6-flash", Type: "model", DisplayName: "Qwen3.6-Flash", CreatedAt: ""},
	{ID: "deepseek-v4-pro", Type: "model", DisplayName: "DeepSeek-V4-Pro", CreatedAt: ""},
	{ID: "deepseek-v4-flash", Type: "model", DisplayName: "DeepSeek-V4-Flash", CreatedAt: ""},
	{ID: "glm-5.3", Type: "model", DisplayName: "GLM-5.3", CreatedAt: ""},
	{ID: "glm-5.2", Type: "model", DisplayName: "GLM-5.2", CreatedAt: ""},
	{ID: "kimi-k2.7-code", Type: "model", DisplayName: "Kimi-K2.7-Code", CreatedAt: ""},
	{ID: "minimax-m2.7", Type: "model", DisplayName: "MiniMax-M2.7", CreatedAt: ""},
}

// globalAliases 与 cnAliases 固化公开模型 ID 到内部 route key 的站点映射。
var globalAliases = map[string]string{
	"claude-opus-4-6":   "ultimate",
	"auto":              "auto",
	"performance":       "performance",
	"efficient":         "efficient",
	"lite":              "lite",
	"qwen3.8-max":       "qmodel_38max",
	"qwen3.7-max":       "qmodel_latest",
	"qwen3.7-plus":      "qmodel",
	"kimi-k3":           "kmodel_latest",
	"kimi-k2.7-code":    "kmodel",
	"glm-5.3":           "gmodel",
	"glm-5.2":           "gm51model",
	"deepseek-v4-pro":   "dmodel",
	"deepseek-v4-flash": "dfmodel",
	"minimax-m3":        "mmodel",
}

var cnAliases = map[string]string{
	"auto":              "auto",
	"qwen3.8-max":       "qmodel_38max",
	"qwen3.7-max":       "qmodel_latest",
	"qwen3.7-plus":      "qmodel",
	"qwen3.6-flash":     "q36fmodel",
	"deepseek-v4-pro":   "dmodel",
	"deepseek-v4-flash": "dfmodel",
	"glm-5.3":           "gmodel",
	"glm-5.2":           "gm51model",
	"kimi-k2.7-code":    "kmodel",
	"minimax-m2.7":      "mmodel",
}

// ThinkingCapability 描述 Qoder 模型可由客户端调整的思考能力。
// 能力快照来自 Qoder 国际版和国内版 1.24.2；更新模型列表时必须同步核对对应站点能力表。
type ThinkingCapability uint8

const (
	ThinkingUnsupported ThinkingCapability = iota
	ThinkingToggleOnly
	ThinkingHighMax
	ThinkingLowHighMax
)

// globalThinkingCapabilities 显式记录每个国际站 route key 的可调思考能力。
// 两站共有 route key 与国内站保持一致；国际站独有模型仍只采用已验证的能力。
var globalThinkingCapabilities = map[string]ThinkingCapability{
	"ultimate":      ThinkingUnsupported,
	"auto":          ThinkingUnsupported,
	"performance":   ThinkingUnsupported,
	"efficient":     ThinkingUnsupported,
	"lite":          ThinkingUnsupported,
	"qmodel_38max":  ThinkingToggleOnly,
	"qmodel_latest": ThinkingToggleOnly,
	"qmodel":        ThinkingToggleOnly,
	"kmodel_latest": ThinkingUnsupported,
	"kmodel":        ThinkingUnsupported,
	"gmodel":        ThinkingLowHighMax,
	"gm51model":     ThinkingHighMax,
	"dmodel":        ThinkingHighMax,
	"dfmodel":       ThinkingHighMax,
	"mmodel":        ThinkingUnsupported,
}

// cnThinkingCapabilities 显式记录每个国内站 route key 的可调思考能力。
var cnThinkingCapabilities = map[string]ThinkingCapability{
	"auto":          ThinkingUnsupported,
	"qmodel_38max":  ThinkingToggleOnly,
	"qmodel_latest": ThinkingToggleOnly,
	"qmodel":        ThinkingToggleOnly,
	"q36fmodel":     ThinkingUnsupported,
	"dmodel":        ThinkingHighMax,
	"dfmodel":       ThinkingHighMax,
	"gmodel":        ThinkingLowHighMax,
	"gm51model":     ThinkingHighMax,
	"kmodel":        ThinkingUnsupported,
	"mmodel":        ThinkingUnsupported,
}

// FallbackMaxInputTokens 是未知或尚未纳入能力快照的 Qoder route 使用的保守输入上限。
const FallbackMaxInputTokens = 200000

// ContextCapability 描述 Qoder route 的最高输入上限以及是否支持运行时上下文档位。
type ContextCapability struct {
	MaxInputTokens    int
	RuntimeSelectable bool
}

// globalContextCapabilities 来自 Qoder 国际版 1.24.2 的 Assistant 运行时模型配置。
var globalContextCapabilities = map[string]ContextCapability{
	"ultimate":      {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"auto":          {MaxInputTokens: 180000},
	"performance":   {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"efficient":     {MaxInputTokens: 180000},
	"lite":          {MaxInputTokens: 180000},
	"qmodel_38max":  {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"qmodel_latest": {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"qmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"kmodel_latest": {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"kmodel":        {MaxInputTokens: 256000, RuntimeSelectable: true},
	"gmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"gm51model":     {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"dmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"dfmodel":       {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"mmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
}

// cnContextCapabilities 来自 Qoder CN 1.24.2 的 Assistant 运行时模型配置。
var cnContextCapabilities = map[string]ContextCapability{
	"auto":          {MaxInputTokens: 180000},
	"qmodel_38max":  {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"qmodel_latest": {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"qmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"q36fmodel":     {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"dmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"dfmodel":       {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"gmodel":        {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"gm51model":     {MaxInputTokens: 1000000, RuntimeSelectable: true},
	"kmodel":        {MaxInputTokens: 256000, RuntimeSelectable: true},
	"mmodel":        {MaxInputTokens: 200000, RuntimeSelectable: true},
}

// DefaultModels 是无账号上下文使用的两站稳定并集，国际站模型排在前面。
var DefaultModels = unionModels(globalModels, cnModels)

// DefaultModelsForSite 返回指定站点的模型快照副本。
func DefaultModelsForSite(site Site) []Model {
	models := globalModels
	if site == SiteCN {
		models = cnModels
	}
	return append([]Model(nil), models...)
}

// AliasesForSite 返回指定站点公开 alias 到内部 route key 的副本。
func AliasesForSite(site Site) map[string]string {
	aliases := globalAliases
	if site == SiteCN {
		aliases = cnAliases
	}
	out := make(map[string]string, len(aliases))
	for alias, route := range aliases {
		out[alias] = route
	}
	return out
}

// AliasForSite 解析指定站点的公开 alias。
func AliasForSite(site Site, model string) (string, bool) {
	aliases := globalAliases
	if site == SiteCN {
		aliases = cnAliases
	}
	route, ok := aliases[model]
	return route, ok
}

// ThinkingCapabilityForSite 按站点和最终路由查询可调思考能力。
// 公开 alias 会先解析为 route key；未知路由以及不可调模型均不主动透传。
// 空站点沿用旧账号兼容语义，按国际站能力查询。
func ThinkingCapabilityForSite(site Site, model string) ThinkingCapability {
	var capabilities map[string]ThinkingCapability
	switch site {
	case "", SiteGlobal:
		capabilities = globalThinkingCapabilities
	case SiteCN:
		capabilities = cnThinkingCapabilities
	default:
		return ThinkingUnsupported
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if route, ok := AliasForSite(site, model); ok {
		model = route
	}
	return capabilities[model]
}

// ContextCapabilityForSite 按站点和最终路由返回最高上下文能力。
// 公开 alias 会先解析为 route key；空站点按国际站处理，未知 route 回退 200K 且不选择运行时档位。
func ContextCapabilityForSite(site Site, model string) ContextCapability {
	var capabilities map[string]ContextCapability
	switch site {
	case "", SiteGlobal:
		capabilities = globalContextCapabilities
	case SiteCN:
		capabilities = cnContextCapabilities
	default:
		return ContextCapability{MaxInputTokens: FallbackMaxInputTokens}
	}

	model = strings.ToLower(strings.TrimSpace(model))
	if route, ok := AliasForSite(site, model); ok {
		model = route
	}
	if capability, ok := capabilities[model]; ok {
		return capability
	}
	return ContextCapability{MaxInputTokens: FallbackMaxInputTokens}
}

// ModelCompatibleWithSite 判断已知 alias 或 route key 是否能由指定站点处理。
// 未知 raw key 保持透传，因此返回 true。
func ModelCompatibleWithSite(site Site, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if _, ok := AliasForSite(site, model); ok {
		return true
	}
	if isKnownPublicAlias(model) {
		return false
	}
	if !isKnownRouteKey(model) {
		return true
	}
	for _, route := range AliasesForSite(site) {
		if route == model {
			return true
		}
	}
	return false
}

func isKnownPublicAlias(model string) bool {
	_, global := globalAliases[model]
	_, cn := cnAliases[model]
	return global || cn
}

func isKnownRouteKey(model string) bool {
	for _, aliases := range []map[string]string{globalAliases, cnAliases} {
		for _, route := range aliases {
			if route == model {
				return true
			}
		}
	}
	return false
}

func unionModels(groups ...[]Model) []Model {
	seen := make(map[string]struct{})
	var models []Model
	for _, group := range groups {
		for _, model := range group {
			if _, ok := seen[model.ID]; ok {
				continue
			}
			seen[model.ID] = struct{}{}
			models = append(models, model)
		}
	}
	return models
}

// DefaultRequestModelIDsForSite 返回指定站点的公开模型 ID。
func DefaultRequestModelIDsForSite(site Site) []string {
	models := DefaultModelsForSite(site)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// DefaultRequestModelIDs 返回无账号上下文使用的两站模型并集。
func DefaultRequestModelIDs() []string {
	ids := make([]string, 0, len(DefaultModels))
	for _, model := range DefaultModels {
		ids = append(ids, model.ID)
	}
	return ids
}

// AuthInfo 保存从本地 Qoder 认证存储解密出的用户信息。
type AuthInfo struct {
	UID                    string `json:"uid"`
	Name                   string `json:"name"`
	AccessToken            string `json:"access_token"`
	SecurityOauthToken     string `json:"security_oauth_token"`
	RefreshToken           string `json:"refresh_token"`
	ExpireTime             int64  `json:"expire_time"`
	RefreshTokenExpireTime int64  `json:"refresh_token_expire_time"`
	LoginMethod            string `json:"login_method"`
	LoginTimestamp         int64  `json:"login_timestamp"`
	EncryptUserInfo        string `json:"encrypt_user_info"`
	Key                    string `json:"key"`
	Email                  string `json:"email"`
	UserType               string `json:"userType"`
	MachineID              string `json:"_machine_id"`
	OrganizationID         string `json:"organization_id"`
	OrganizationName       string `json:"organization_name"`
}

// ToAuthIdentity 将本地认证信息转换为用于构建 session 的 AuthIdentity。
func (info *AuthInfo) ToAuthIdentity() *AuthIdentity {
	token := info.SecurityOauthToken
	if token == "" {
		token = info.AccessToken
	}
	userType := info.UserType
	if userType == "" {
		userType = "personal_standard"
	}
	return &AuthIdentity{
		Name:               info.Name,
		AID:                info.UID,
		UID:                info.UID,
		OrganizationID:     info.OrganizationID,
		OrganizationName:   info.OrganizationName,
		UserType:           userType,
		SecurityOauthToken: token,
		RefreshToken:       info.RefreshToken,
	}
}

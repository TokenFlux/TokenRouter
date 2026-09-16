// Package service provides business logic and domain services for the application.
package service

import (
	slog "log/slog"
	url "net/url"
	strings "strings"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type Account struct {
	// resolvedCandidate 只属于本次尝试，不能进入共享缓存或持久化。
	resolvedCandidate *routing.CandidatePlan
	// resolvedProtocol 仅属于本次转发副本，不能写入共享快照。
	resolvedProtocol domain.ProtocolID

	ID                      int64
	Name                    string
	Notes                   *string
	Platform                string
	Type                    string
	Credentials             map[string]any
	Extra                   map[string]any
	ProxyID                 *int64
	ProxyFallbackOriginID   *int64
	ProxyFallbackOriginName *string // 仅展示用
	Concurrency             int
	Priority                int
	// RateMultiplier 账号计费倍率（>=0，允许 0 表示该账号计费为 0）。
	// 使用指针用于兼容旧版本调度缓存（Redis）中缺字段的情况：nil 表示按 1.0 处理。
	RateMultiplier     *float64
	LoadFactor         *int // 调度负载因子；nil 表示使用 Concurrency
	Status             string
	ErrorMessage       string
	LastUsedAt         *time.Time
	ExpiresAt          *time.Time
	AutoPauseOnExpired bool
	CreatedAt          time.Time
	UpdatedAt          time.Time

	Schedulable bool

	RateLimitedAt    *time.Time
	RateLimitResetAt *time.Time
	OverloadUntil    *time.Time

	TempUnschedulableUntil  *time.Time
	TempUnschedulableReason string

	// QuotaAutoPaused 是 OpenAI 账号配额自动暂停的运行时派生状态，不会持久化到数据库。
	QuotaAutoPaused bool `json:"-"`

	SessionWindowStart  *time.Time
	SessionWindowEnd    *time.Time
	SessionWindowStatus string

	ParentAccountID *int64 // non-nil → 影子账号（不持凭据，透传母账号凭据）
	QuotaDimension  string // 用量维度："" / "global" / "spark"

	Proxy         *Proxy
	AccountGroups []AccountGroup
	GroupIDs      []int64
	Groups        []*Group
}

type OpenAIEndpointCapability = accountcore.OpenAIEndpointCapability

const OpenAIEndpointCapabilityTextGeneration = accountcore.OpenAIEndpointCapabilityTextGeneration
const OpenAIEndpointCapabilityEmbeddings = accountcore.OpenAIEndpointCapabilityEmbeddings
const OpenAIEndpointCapabilityAlphaSearch = accountcore.OpenAIEndpointCapabilityAlphaSearch
const OpenAIEndpointCapabilityLive = accountcore.OpenAIEndpointCapabilityLive
const OpenAIEndpointCapabilityGrokMediaGeneration = accountcore.OpenAIEndpointCapabilityGrokMediaGeneration
const OpenAIEndpointCapabilityResponses = accountcore.OpenAIEndpointCapabilityResponses
const OpenAIEndpointCapabilityRemoteCompactionV2 = accountcore.OpenAIEndpointCapabilityRemoteCompactionV2

const openAIWorkloadCapabilitiesCredentialKey = accountcore.OpenAIWorkloadCapabilitiesCredentialKey

const GeminiProviderTypeCredentialKey = accountcore.GeminiProviderTypeCredentialKey
const GeminiProviderTypeThirdParty = accountcore.GeminiProviderTypeThirdParty

const GrokMediaEligibleExtraKey = accountcore.GrokMediaEligibleExtraKey

const OpenAIAuthModePersonalAccessToken = accountcore.OpenAIAuthModePersonalAccessToken
const openAIAuthModeCredentialKey = accountcore.OpenAIAuthModeCredentialKey

type TempUnschedulableRule = accountcore.TempUnschedulableRule

func (a *Account) IsActive() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Status: a.Status}
	}
	return view.IsActive()
}

func (a *Account) BillingRateMultiplier() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, RateMultiplier: a.RateMultiplier}
	}
	return view.BillingRateMultiplier()
}

func (a *Account) EffectiveLoadFactor() int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Concurrency: a.Concurrency,
			LoadFactor: a.LoadFactor}
	}
	return view.EffectiveLoadFactor()
}

func (a *Account) IsSchedulable() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, AutoPauseOnExpired: a.AutoPauseOnExpired,
			ExpiresAt:              a.ExpiresAt,
			Extra:                  a.Extra,
			OverloadUntil:          a.OverloadUntil,
			RateLimitResetAt:       a.RateLimitResetAt,
			Schedulable:            a.Schedulable,
			Status:                 a.Status,
			TempUnschedulableUntil: a.TempUnschedulableUntil,
			Type:                   a.Type,
			Now:                    time.Now}
	}
	return view.IsSchedulable()
}

func (a *Account) IsCredentialUsableForShadow() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, AutoPauseOnExpired: a.AutoPauseOnExpired,
			ExpiresAt:              a.ExpiresAt,
			Status:                 a.Status,
			TempUnschedulableUntil: a.TempUnschedulableUntil,
			Now:                    time.Now}
	}
	return view.IsCredentialUsableForShadow()
}

func (a *Account) IsRateLimited() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, RateLimitResetAt: a.RateLimitResetAt,
			Now: time.Now}
	}
	return view.IsRateLimited()
}

func (a *Account) IsOverloaded() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, OverloadUntil: a.OverloadUntil,
			Now: time.Now}
	}
	return view.IsOverloaded()
}

func (a *Account) IsOAuth() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Type: a.Type}
	}
	return view.IsOAuth()
}

func (a *Account) IsPrivacySet() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.IsPrivacySet()
}

func (a *Account) IsGemini() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsGemini()
}

func (a *Account) IsGrok() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsGrok()
}

func (a *Account) IsGrokOAuth() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsGrokOAuth()
}

func (a *Account) IsKimi() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsKimi()
}

func (a *Account) IsZhipu() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsZhipu()
}

func (a *Account) IsDeepseek() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsDeepseek()
}

func (a *Account) IsCNProvider() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsCNProvider()
}

func (a *Account) IsOpenAICompatible() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsOpenAICompatible()
}

func (a *Account) GeminiOAuthType() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GeminiOAuthType()
}

func (a *Account) GeminiTierID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GeminiTierID()
}

func (a *Account) IsGeminiThirdPartyProvider() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsGeminiThirdPartyProvider()
}

func (a *Account) HasGeminiThirdPartyBaseURL() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.HasGeminiThirdPartyBaseURL()
}

func (a *Account) IsGeminiCodeAssist() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsGeminiCodeAssist()
}

func (a *Account) IsGeminiGoogleOne() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsGeminiGoogleOne()
}

func (a *Account) CanGetUsage() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Type: a.Type}
	}
	return view.CanGetUsage()
}

func (a *Account) GetCredential(key string) string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetCredential(key)
}

func (a *Account) GetCredentialAsTime(key string) *time.Time {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetCredentialAsTime(key)
}

func (a *Account) GetCredentialAsInt64(key string) int64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetCredentialAsInt64(key)
}

func (a *Account) IsTempUnschedulableEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.IsTempUnschedulableEnabled()
}

func (a *Account) GetTempUnschedulableRules() []TempUnschedulableRule {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetTempUnschedulableRules()
}

const OpenAICompactModeForceOn = accountcore.OpenAICompactModeForceOn
const OpenAICompactModeForceOff = accountcore.OpenAICompactModeForceOff
const openAINativeCompactionV2ModeExtraKey = accountcore.OpenAINativeCompactionV2ModeExtraKey

func (a *Account) GetModelMapping() map[string]string {
	return accountcore.ResolveModelMapping(protocolRecord(a), legacyAccountModelDefaults())
}

func (a *Account) isFinalModelWhitelisted(finalModel string) bool {
	return protocolRecord(a).FinalModelWhitelisted(finalModel, legacyAccountModelDefaults(), legacyAccountModelRules(a))
}

func normalizeQoderModelForWhitelist(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if info, ok := lookupQoderModelAlias(trimmed); ok {
		return strings.TrimSpace(info.Key)
	}
	return trimmed
}

func (a *Account) IsModelSupported(requestedModel string) bool {
	return protocolRecord(a).IsModelSupported(requestedModel, legacyAccountModelDefaults(), legacyAccountModelRules(a))
}

func (a *Account) GetConfiguredRequestModels() []string {
	return protocolRecord(a).GetConfiguredRequestModels(legacyAccountModelDefaults())
}

// GetMappedModel 获取映射后的模型名（支持通配符，最长优先匹配）
// 如果未配置 mapping，返回原始模型名
func (a *Account) GetMappedModel(requestedModel string) string {
	mappedModel, _ := a.ResolveMappedModel(requestedModel)
	return mappedModel
}

func (a *Account) ResolveMappedModel(requestedModel string) (mappedModel string, matched bool) {
	mapping := a.GetModelMapping()
	if a.resolvedCandidate != nil {
		snapshot := accountcore.AccountSnapshot{ID: a.ID, Platform: a.Platform, ModelPolicy: accountcore.NewModelRoutingSnapshot(a.Platform, mapping)}
		candidate, matched := a.resolvedCandidate.ResolveModel(snapshot, requestedModel)
		return candidate.Models.AccountMappedModel, matched
	}
	return accountcore.ResolveMappedModel(a.Platform, mapping, requestedModel)
}

func (a *Account) GetOpenAICompactMode() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.GetOpenAICompactMode()
}

func (a *Account) AllowsOpenAICompact() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.AllowsOpenAICompact()
}

func (a *Account) GetOpenAINativeCompactionV2Mode() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.GetOpenAINativeCompactionV2Mode()
}

func (a *Account) AllowsOpenAINativeCompactionV2() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.AllowsOpenAINativeCompactionV2()
}

func (a *Account) GetCompactModelMapping() map[string]string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetCompactModelMapping()
}

func (a *Account) ResolveCompactMappedModel(requestedModel string) (mappedModel string, matched bool) {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.ResolveCompactMappedModel(requestedModel)
}

func (a *Account) GetBaseURL() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetBaseURL()
}

func (a *Account) GetGeminiBaseURL(defaultBaseURL string) string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetGeminiBaseURL(defaultBaseURL)
}

func (a *Account) GetExtraString(key string) string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetExtraString(key)
}

func (a *Account) GetClaudeUserID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Extra: a.Extra}
	}
	return view.GetClaudeUserID()
}

func matchWildcard(pattern, str string) bool { return accountcore.MatchWildcard(pattern, str) }

func (a *Account) IsCustomErrorCodesEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Type: a.Type}
	}
	return view.IsCustomErrorCodesEnabled()
}

func (a *Account) IsPoolMode() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Type: a.Type}
	}
	return view.IsPoolMode()
}

func (a *Account) GetPoolModeRetryCount() int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Type: a.Type}
	}
	return view.GetPoolModeRetryCount()
}

func (a *Account) GetPoolModeRetryStatusCodes() []int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetPoolModeRetryStatusCodes()
}

func (a *Account) IsPoolModeRetryableStatus(statusCode int) bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.IsPoolModeRetryableStatus(statusCode)
}

func (a *Account) GetCustomErrorCodes() []int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.GetCustomErrorCodes()
}

func (a *Account) ShouldHandleErrorCode(statusCode int) bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Type: a.Type}
	}
	return view.ShouldHandleErrorCode(statusCode)
}

func (a *Account) IsInterceptWarmupEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials}
	}
	return view.IsInterceptWarmupEnabled()
}

func (a *Account) IsBedrock() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsBedrock()
}

func (a *Account) IsBedrockAPIKey() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsBedrockAPIKey()
}

func (a *Account) IsAPIKeyOrBedrock() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Type: a.Type}
	}
	return view.IsAPIKeyOrBedrock()
}

func (a *Account) IsOpenAI() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsOpenAI()
}

func (a *Account) IsAnthropic() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsAnthropic()
}

func (a *Account) IsQoder() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.IsQoder()
}

func (a *Account) IsQoderCosy() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsQoderCosy()
}

func (a *Account) IsOpenAIOAuth() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsOpenAIOAuth()
}

func (a *Account) IsOpenAIOAuthLike() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsOpenAIOAuthLike()
}

func (a *Account) UsesOpenAICodexProtocol() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.UsesOpenAICodexProtocol()
}

func (a *Account) IsOpenAIChatGPTSubscription() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsOpenAIChatGPTSubscription()
}

func (a *Account) IsOpenAIPersonalAccessToken() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsOpenAIPersonalAccessToken()
}

func (a *Account) IsOpenAIApiKey() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsOpenAIApiKey()
}

func (a *Account) GetOpenAIBaseURL() string {
	return protocolRecord(a).OpenAIBaseURL(a.IsAdaptiveAPIProtocol())
}

func (a *Account) GetAccountMode() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.GetAccountMode()
}

func (a *Account) IsCodingPlan() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.IsCodingPlan()
}

// GetAPIProtocol 为原有平台适配器提供协议变体：请求副本使用已解析目标，
// 统一账号的地址/维护流程使用分协议模式，旧对象继续保留历史读取默认值。
func (a *Account) GetAPIProtocol() string {
	if a != nil && a.resolvedProtocol != "" {
		switch a.resolvedProtocol {
		case domain.ProtocolAnthropicMessages:
			return APIProtocolAnthropic
		case domain.ProtocolOpenAIResponses:
			return APIProtocolResponses
		case domain.ProtocolOpenAIChatCompletions:
			return APIProtocolChatCompletions
		}
	}

	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{Platform: a.Platform, Type: a.Type, Credentials: a.Credentials}
	}
	return view.ConfiguredAPIProtocol()
}

func (a *Account) SupportsNativeCNResponses() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform}
	}
	return view.SupportsNativeCNResponses()
}

// UsesNativeCNResponses 报告当前账号是否应按原生 Responses 协议转发
// （显式 responses，或 adaptive 且平台具备原生端点）。
func (a *Account) UsesNativeCNResponses() bool {
	if a == nil || !a.SupportsNativeCNResponses() {
		return false
	}
	switch a.GetAPIProtocol() {
	case APIProtocolResponses, APIProtocolAdaptive:
		return true
	default:
		return false
	}
}

// IsAdaptiveAPIProtocol 报告账号是否按入站协议动态选择供应商原生端点。
func (a *Account) IsAdaptiveAPIProtocol() bool {
	return a.GetAPIProtocol() == APIProtocolAdaptive
}

// GetCNProtocolBaseURL 返回国产供应商指定协议的上游 base URL。
// adaptive 账号优先使用 api_base_urls 中的分协议地址，缺失时按平台和
// account_mode 使用官方默认端点。base_url 继续作为 Chat Completions 地址兼容旧字段。
func (a *Account) GetCNProtocolBaseURL(protocol string) string {
	if a == nil || !a.IsCNProvider() {
		return ""
	}
	if _, unified := a.Credentials[upstreamProtocolsKey]; unified || a.IsAdaptiveAPIProtocol() {
		if baseURLs, ok := a.Credentials["api_base_urls"].(map[string]any); ok {
			if baseURL, ok := baseURLs[protocol].(string); ok && strings.TrimSpace(baseURL) != "" {
				return strings.TrimSpace(baseURL)
			}
		}
		if protocol == APIProtocolChatCompletions {
			if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
				return baseURL
			}
		}
	}
	return a.defaultCNProtocolBaseURL(protocol)
}

func (a *Account) defaultCNProtocolBaseURL(protocol string) string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.DefaultCNProtocolBaseURL(protocol)
}

// IsAnthropicProtocol 报告账号是否以原生 Anthropic 协议接入上游
// （/v1/messages 直通，适配 Claude Code 等客户端）。
func (a *Account) IsAnthropicProtocol() bool {
	return a.GetAPIProtocol() == APIProtocolAnthropic
}

// GetAnthropicProtocolBaseURL 返回 Anthropic 协议账号的上游 base_url
// （上游路径为 {base}/v1/messages）。优先取凭证 base_url，缺失时按
// 供应商 × 接入模式返回默认端点。非 Anthropic 协议账号返回空串。
func (a *Account) GetAnthropicProtocolBaseURL() string {
	if a == nil || (!a.IsAnthropicProtocol() && !a.IsAdaptiveAPIProtocol()) {
		return ""
	}
	if _, unified := a.Credentials[upstreamProtocolsKey]; unified || a.IsAdaptiveAPIProtocol() {
		return a.GetCNProtocolBaseURL(APIProtocolAnthropic)
	}
	if a.Type == AccountTypeAPIKey || a.Type == AccountTypeUpstream {
		if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
			return baseURL
		}
	}
	switch a.Platform {
	case PlatformKimi:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultKimiCodingAnthropicBaseURL
		}
		return DefaultKimiPayGAnthropicBaseURL
	case PlatformZhipu:
		return DefaultZhipuAnthropicBaseURL
	case PlatformDeepseek:
		return DefaultDeepseekAnthropicBaseURL
	default:
		return ""
	}
}

// GetOpenAIFormatBaseURL 返回供 OpenAI 格式端点（/v1/models、/v1/chat/completions
// 等）使用的 base。chat_completions / responses 协议下与 GetOpenAIBaseURL
// 一致；anthropic 协议下，官方端点映射到对应的 OpenAI 格式端点，自定义中继则
// 只移除末尾的 /anthropic 协议段，保留中继 host 与路径前缀。
func (a *Account) GetOpenAIFormatBaseURL() string {
	if a == nil {
		return ""
	}
	// 迁移后的固定 Messages 账号通过地址槽识别模型同步根，不能丢失中继前缀。
	anthropicBase := a.IsAnthropicProtocol()
	if _, unified := a.Credentials[upstreamProtocolsKey]; unified && a.IsCNProvider() {
		urls, _ := a.Credentials["api_base_urls"].(map[string]any)
		chat, _ := urls[APIProtocolChatCompletions].(string)
		messages, _ := urls[APIProtocolAnthropic].(string)
		anthropicBase = chat == "" && messages != "" && messages == a.GetCredential("base_url")
	}
	if !anthropicBase {
		return a.GetOpenAIBaseURL()
	}

	if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
		if !isDefaultCNAnthropicBaseURL(baseURL) {
			return stripCNAnthropicPathSuffix(baseURL)
		}
	}
	switch a.Platform {
	case PlatformKimi:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultKimiCodingBaseURL
		}
		return DefaultKimiPayGBaseURL
	case PlatformZhipu:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultZhipuCodingBaseURL
		}
		return DefaultZhipuPayGBaseURL
	case PlatformDeepseek:
		return DefaultDeepseekBaseURL
	default:
		return a.GetOpenAIBaseURL()
	}
}

func isDefaultCNAnthropicBaseURL(baseURL string) bool {
	return accountcore.IsDefaultCNAnthropicBaseURL(baseURL)
}

func stripCNAnthropicPathSuffix(baseURL string) string {
	return accountcore.StripCNAnthropicPathSuffix(baseURL)
}

func (a *Account) GetCNAPIKey() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.GetCNAPIKey()
}

func (a *Account) GetCodingPlanProvider() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.GetCodingPlanProvider()
}

func (a *Account) GetOpenAIAccessToken() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.GetOpenAIAccessToken()
}

func (a *Account) GetOpenAIRefreshToken() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIRefreshToken()
}

// GetGrokBaseURL 返回 Grok 文本与 Responses 流量使用的上游地址。
// 媒体流量必须通过 GetGrokMediaBaseURL 明确选择其独立的凭据边界。
// 存储的 base_url 只改写转发端点；OAuth 授权与令牌刷新始终使用官方认证端点。
func (a *Account) GetGrokBaseURL() string {
	if a == nil || !a.IsGrok() {
		return ""
	}
	if a.IsGrokOAuth() {
		return a.GetGrokBaseURLOr(xai.DefaultCLIBaseURL)
	}
	return a.GetGrokBaseURLOr(xai.DefaultBaseURL)
}

// GetGrokBaseURLOr 优先使用账号显式端点，无效时回退到调用方给定的默认地址。
// 官方 OAuth 端点在此归一化；自定义端点仍由构造请求的 URL 信任策略审核。
func (a *Account) GetGrokBaseURLOr(defaultBaseURL string) string {
	if a == nil || !a.IsGrok() {
		return ""
	}
	defaultBaseURL = strings.TrimRight(strings.TrimSpace(defaultBaseURL), "/")
	if defaultBaseURL == "" {
		if a.IsGrokOAuth() {
			defaultBaseURL = xai.DefaultCLIBaseURL
		} else {
			defaultBaseURL = xai.DefaultBaseURL
		}
	}
	baseURL := strings.TrimSpace(a.GetCredential("base_url"))
	if baseURL == "" {
		return defaultBaseURL
	}
	if !a.IsGrokOAuth() {
		return baseURL
	}
	// 显式区域、公共 API 或自定义端点保持固定；自定义端点由能读取配置的请求构造器执行 URL 策略校验。
	if validated, err := xai.ValidateTrustedBaseURL(baseURL); err == nil {
		return validated
	}
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		return strings.TrimRight(baseURL, "/")
	}
	return defaultBaseURL
}

// GetGrokMediaBaseURL 返回 Grok Imagine 媒体接口使用的上游地址。
// CLI 订阅网关会拒绝较大的 Base64 请求体，因此 OAuth 文本流量解析到 CLI 网关时，
// 媒体改走 api.x.ai；手工选择的官方、区域或自定义端点仍原样用于媒体。
func (a *Account) GetGrokMediaBaseURL() string {
	if !a.IsGrok() {
		return ""
	}
	baseURL := a.GetGrokBaseURL()
	if a.IsGrokOAuth() && isGrokCLIProxyTarget(baseURL) {
		return xai.DefaultBaseURL
	}
	return baseURL
}

func (a *Account) GetGrokAccessToken() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.GetGrokAccessToken()
}

func (a *Account) GetGrokRefreshToken() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetGrokRefreshToken()
}

func (a *Account) GetOpenAIIDToken() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIIDToken()
}

func (a *Account) GetOpenAIApiKey() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIApiKey()
}

func (a *Account) GetOpenAIProtocolAPIKey() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIProtocolAPIKey()
}

func (a *Account) GetOpenAIUserAgent() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform}
	}
	return view.GetOpenAIUserAgent()
}

func (a *Account) GetChatGPTAccountID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetChatGPTAccountID()
}

func (a *Account) IsChatGPTAccountFedRAMP() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsChatGPTAccountFedRAMP()
}

func (a *Account) GetOpenAIDeviceID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIDeviceID()
}

func (a *Account) GetOpenAISessionID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAISessionID()
}

func (a *Account) SupportsOpenAIEndpointCapability(capability OpenAIEndpointCapability) bool {
	return protocolRecord(a).SupportsOpenAIEndpointCapability(capability, a.GrokMediaGenerationEligibility)
}

// GrokMediaGenerationEligibility 判断 Grok 账号能否承接新的图片或视频生成请求。
// OAuth 媒体必须有明确的付费资格观测，否则按拒绝处理；管理员显式覆盖优先于探测数据。
func (a *Account) GrokMediaGenerationEligibility() (bool, string) {
	if a == nil || !a.IsGrok() {
		return false, "not_grok"
	}
	if override, ok := grokMediaEligibilityOverride(a.Extra); ok {
		if override {
			return true, "override_enabled"
		}
		return false, "override_disabled"
	}
	if a.Type != AccountTypeOAuth {
		return true, "non_oauth"
	}

	billing, err := grokBillingSnapshotFromExtra(a.Extra)
	if err != nil || billing == nil {
		return false, "billing_unobserved"
	}
	if billing.StatusCode == 403 || billing.WeeklyStatusCode == 403 || billing.MonthlyStatusCode == 403 {
		return false, "billing_forbidden"
	}
	if isKnownGrokFreeAccount(a) {
		return false, "billing_free_tier"
	}
	if !grokBillingHasAuthoritativeQuota(billing) {
		return false, "billing_inconclusive"
	}
	return true, "eligible"
}

func grokMediaEligibilityOverride(extra map[string]any) (bool, bool) {
	return accountcore.GrokMediaEligibilityOverride(extra)
}

func (a *Account) SupportsOpenAIImageCapability(capability OpenAIImagesCapability) bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.SupportsOpenAIImageCapability(capability)
}

func (a *Account) GetChatGPTUserID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetChatGPTUserID()
}

func (a *Account) GetOpenAIOrganizationID() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIOrganizationID()
}

func (a *Account) GetOpenAITokenExpiresAt() *time.Time {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAITokenExpiresAt()
}

func (a *Account) IsOpenAITokenExpired() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Credentials: a.Credentials,
			Platform: a.Platform,
			Type:     a.Type,
			Now:      time.Now}
	}
	return view.IsOpenAITokenExpired()
}

func (a *Account) IsMixedSchedulingEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.IsMixedSchedulingEnabled()
}

func (a *Account) IsOveragesEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.IsOveragesEnabled()
}

func (a *Account) IsOpenAIPassthroughEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.IsOpenAIPassthroughEnabled()
}

func (a *Account) IsOpenAIResponsesFlattenNamespacesEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsOpenAIResponsesFlattenNamespacesEnabled()
}

func (a *Account) IsOpenAIResponsesWebSocketV2Enabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsOpenAIResponsesWebSocketV2Enabled()
}

const OpenAIWSIngressModeOff = accountcore.OpenAIWSIngressModeOff
const OpenAIWSIngressModeShared = accountcore.OpenAIWSIngressModeShared
const OpenAIWSIngressModeDedicated = accountcore.OpenAIWSIngressModeDedicated
const OpenAIWSIngressModeCtxPool = accountcore.OpenAIWSIngressModeCtxPool
const OpenAIWSIngressModePassthrough = accountcore.OpenAIWSIngressModePassthrough
const OpenAIWSIngressModeHTTPBridge = accountcore.OpenAIWSIngressModeHTTPBridge

func normalizeOpenAIWSIngressDefaultMode(mode string) string {
	return accountcore.NormalizeOpenAIWSIngressDefaultMode(mode)
}

func (a *Account) ResolveOpenAIResponsesWebSocketV2Mode(defaultMode string) string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.ResolveOpenAIResponsesWebSocketV2Mode(defaultMode)
}

func (a *Account) IsOpenAIWSForceHTTPEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.IsOpenAIWSForceHTTPEnabled()
}

func (a *Account) IsOpenAIWSAllowStoreRecoveryEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform}
	}
	return view.IsOpenAIWSAllowStoreRecoveryEnabled()
}

func (a *Account) IsOpenAIOAuthPassthroughEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsOpenAIOAuthPassthroughEnabled()
}

func (a *Account) IsAnthropicAPIKeyPassthroughEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsAnthropicAPIKeyPassthroughEnabled()
}

// WebSearch 模拟三态常量
const (
	WebSearchModeDefault  = "default"  // 跟随渠道配置
	WebSearchModeEnabled  = "enabled"  // 强制开启
	WebSearchModeDisabled = "disabled" // 强制关闭
)

const OpenAIOAuthClientPolicyAny = accountcore.OpenAIOAuthClientPolicyAny
const OpenAIOAuthClientPolicyCodexOnly = accountcore.OpenAIOAuthClientPolicyCodexOnly
const OpenAIOAuthClientPolicyTLSRouterMatchedOnly = accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly

// GetWebSearchEmulationMode 返回账号的 WebSearch 模拟模式。
// 三态：default（跟随渠道）/ enabled（强制开启）/ disabled（强制关闭）。
// 兼容旧 bool 值：true→enabled, false→default（并记录 debug 日志）。
func (a *Account) GetWebSearchEmulationMode() string {
	if a == nil || a.Platform != PlatformAnthropic || a.Type != AccountTypeAPIKey || a.Extra == nil {
		return WebSearchModeDefault
	}
	raw := a.Extra[featureKeyWebSearchEmulation]
	// Tolerant: legacy bool values (pre-migration or stale writes)
	if b, ok := raw.(bool); ok {
		slog.Debug("legacy bool web_search_emulation value", "account_id", a.ID, "value", b)
		if b {
			return WebSearchModeEnabled
		}
		return WebSearchModeDefault
	}
	mode, ok := raw.(string)
	if !ok {
		return WebSearchModeDefault
	}
	switch mode {
	case WebSearchModeEnabled, WebSearchModeDisabled:
		return mode
	default:
		return WebSearchModeDefault
	}
}

func (a *Account) IsCodexCLIOnlyEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsCodexCLIOnlyEnabled()
}

func (a *Account) GetOpenAIOAuthClientPolicy() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetOpenAIOAuthClientPolicy()
}

func (a *Account) IsOpenAIOAuthTLSRouterMatchedOnly() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsOpenAIOAuthTLSRouterMatchedOnly()
}

func (a *Account) GetCodexCLIOnlyAllowedClients() []string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.GetCodexCLIOnlyAllowedClients()
}

type WindowCostSchedulability = accountcore.WindowCostSchedulability

const WindowCostSchedulable = accountcore.WindowCostSchedulable
const WindowCostStickyOnly = accountcore.WindowCostStickyOnly
const WindowCostNotSchedulable = accountcore.WindowCostNotSchedulable

func (a *Account) IsAnthropicOAuthOrSetupToken() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.IsAnthropicOAuthOrSetupToken()
}

func (a *Account) SupportsTLSFingerprint() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Platform: a.Platform,
			Type: a.Type}
	}
	return view.SupportsTLSFingerprint()
}

func (a *Account) IsTLSFingerprintEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsTLSFingerprintEnabled()
}

func (a *Account) GetTLSFingerprintProfileID() int64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetTLSFingerprintProfileID()
}

func (a *Account) GetTLSFingerprintRouterID() int64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetTLSFingerprintRouterID()
}

// GetUserMsgQueueMode 委托账号配置值规则，队列执行仍由原拥有者负责。
func (a *Account) GetUserMsgQueueMode() string {
	return (&accountcore.RuntimeConfig{Extra: a.Extra}).GetUserMsgQueueMode()
}

func (a *Account) IsSessionIDMaskingEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsSessionIDMaskingEnabled()
}

func (a *Account) IsCustomBaseURLEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsCustomBaseURLEnabled()
}

func (a *Account) GetCustomBaseURL() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetCustomBaseURL()
}

func (a *Account) IsCacheTTLOverrideEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Platform: a.Platform,
			Type:     a.Type}
	}
	return view.IsCacheTTLOverrideEnabled()
}

func (a *Account) GetCacheTTLOverrideTarget() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetCacheTTLOverrideTarget()
}

func (a *Account) GetQuotaLimit() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaLimit()
}

func (a *Account) GetQuotaUsed() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaUsed()
}

func (a *Account) GetQuotaDailyLimit() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaDailyLimit()
}

func (a *Account) GetQuotaDailyUsed() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaDailyUsed()
}

func (a *Account) GetQuotaWeeklyLimit() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaWeeklyLimit()
}

func (a *Account) GetQuotaWeeklyUsed() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaWeeklyUsed()
}

func (a *Account) getExtraBool(key string) bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetExtraBool(key)
}

func (a *Account) GetQuotaDailyResetMode() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaDailyResetMode()
}

func (a *Account) GetQuotaDailyResetHour() int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaDailyResetHour()
}

func (a *Account) GetQuotaWeeklyResetMode() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaWeeklyResetMode()
}

func (a *Account) GetQuotaWeeklyResetDay() int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaWeeklyResetDay()
}

func (a *Account) GetQuotaWeeklyResetHour() int {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaWeeklyResetHour()
}

func (a *Account) GetQuotaResetTimezone() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaResetTimezone()
}

// --- Quota Notification Getters ---

func (a *Account) QuotaNotifyConfig(dim string) (enabled bool, threshold float64, thresholdType string) {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.QuotaNotifyConfig(dim)
}

func (a *Account) GetQuotaNotifyDailyEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyDailyEnabled()
}

func (a *Account) GetQuotaNotifyDailyThreshold() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyDailyThreshold()
}

func (a *Account) GetQuotaNotifyDailyThresholdType() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyDailyThresholdType()
}

func (a *Account) GetQuotaNotifyWeeklyEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyWeeklyEnabled()
}

func (a *Account) GetQuotaNotifyWeeklyThreshold() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyWeeklyThreshold()
}

func (a *Account) GetQuotaNotifyWeeklyThresholdType() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyWeeklyThresholdType()
}

func (a *Account) GetQuotaNotifyTotalEnabled() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyTotalEnabled()
}

func (a *Account) GetQuotaNotifyTotalThreshold() float64 {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyTotalThreshold()
}

func (a *Account) GetQuotaNotifyTotalThresholdType() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.GetQuotaNotifyTotalThresholdType()
}

func ComputeQuotaResetAt(extra map[string]any) {
	accountcore.ComputeQuotaResetAt(extra, time.Now(), time.LoadLocation)
}

func NormalizeFixedQuotaWindows(extra map[string]any) {
	accountcore.NormalizeFixedQuotaWindows(extra, time.Now(), time.LoadLocation)
}

func ValidateQuotaResetConfig(extra map[string]any) error {
	return accountcore.ValidateQuotaResetConfig(extra, time.LoadLocation)
}

func (a *Account) HasAnyQuotaLimit() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.HasAnyQuotaLimit()
}

func (a *Account) IsDailyQuotaPeriodExpired() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.IsDailyQuotaPeriodExpired()
}

func (a *Account) IsWeeklyQuotaPeriodExpired() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.IsWeeklyQuotaPeriodExpired()
}

func (a *Account) IsQuotaExceeded() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra}
	}
	return view.IsQuotaExceeded()
}

func (a *Account) GetWindowCostLimit() float64 { return accountRuntimeConfig(a).GetWindowCostLimit() }

func (a *Account) GetWindowCostStickyReserve() float64 {
	return accountRuntimeConfig(a).GetWindowCostStickyReserve()
}

func (a *Account) GetMaxSessions() int { return accountRuntimeConfig(a).GetMaxSessions() }

func (a *Account) GetSessionIdleTimeoutMinutes() int {
	return accountRuntimeConfig(a).GetSessionIdleTimeoutMinutes()
}

func (a *Account) GetBaseRPM() int { return accountRuntimeConfig(a).GetBaseRPM() }

func (a *Account) GetRPMStrategy() string { return accountRuntimeConfig(a).GetRPMStrategy() }

func (a *Account) GetRPMStickyBuffer() int { return accountRuntimeConfig(a).GetRPMStickyBuffer() }

func (a *Account) CheckRPMSchedulability(currentRPM int) WindowCostSchedulability {
	return accountRuntimeConfig(a).CheckRPMSchedulability(currentRPM)
}

func (a *Account) CheckWindowCostSchedulability(currentWindowCost float64) WindowCostSchedulability {
	return accountRuntimeConfig(a).CheckWindowCostSchedulability(currentWindowCost)
}

func (a *Account) GetCurrentWindowStartTime() time.Time {
	return accountRuntimeConfig(a).GetCurrentWindowStartTime(time.Now())
}

// parseExtraInt 从 extra 字段解析 int 值
// ParseExtraInt 从 extra 字段的 any 值解析为 int。
// 支持 int, int64, float64, json.Number, string 类型，无法解析时返回 0。
func ParseExtraInt(value any) int {
	return parseExtraInt(value)
}

func parseExtraInt(value any) int { return accountcore.ParseExtraInt(value) }

func (a *Account) IsShadow() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, ParentAccountID: a.ParentAccountID}
	}
	return view.IsShadow()
}

func (a *Account) IsCredentialShadow() bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, ParentAccountID: a.ParentAccountID}
	}
	return view.IsCredentialShadow()
}

func (a *Account) QuotaDimensionOrDefault() string {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, QuotaDimension: a.QuotaDimension}
	}
	return view.QuotaDimensionOrDefault()
}

// accountRuntimeConfig 仅投影即时读取所需字段，不复制或增加缓存实例。
func accountRuntimeConfig(a *Account) *accountcore.RuntimeConfig {
	return &accountcore.RuntimeConfig{Extra: a.Extra, Concurrency: a.Concurrency, SessionWindowStart: a.SessionWindowStart, SessionWindowEnd: a.SessionWindowEnd}
}

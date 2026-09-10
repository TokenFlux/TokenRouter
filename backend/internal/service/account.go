// Package service provides business logic and domain services for the application.
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
)

type Account struct {
	// resolvedProtocol 仅属于本次转发副本，不能写入共享快照。
	resolvedProtocol GroupClientProtocol

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

	// model_mapping 热路径缓存（非持久化字段）
	modelMappingCache               map[string]string
	modelMappingCacheReady          bool
	modelMappingCacheCredentialsPtr uintptr
	modelMappingCacheRawPtr         uintptr
	modelMappingCacheRawLen         int
	modelMappingCacheRawSig         uint64

	// header_overrides 热路径缓存（非持久化字段，同 model_mapping 缓存先例）
	headerOverrideCache               map[string]string
	headerOverrideCacheReady          bool
	headerOverrideCacheCredentialsPtr uintptr
	headerOverrideCacheRawPtr         uintptr
	headerOverrideCacheRawLen         int
	headerOverrideCacheRawSig         uint64
}

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
	// 与其他能力不同：有效支持状态来自路由模式与 Responses 探测状态，而非
	// credentials["openai_workload_capabilities"] 配置集。仅用于生图意图的 /v1/responses
	// 调度，避免把请求调度到会在 forward 阶段被降级为 Chat Completions 的账号（#4417）。
	OpenAIEndpointCapabilityResponses OpenAIEndpointCapability = "responses"
	// OpenAIEndpointCapabilityRemoteCompactionV2 表示账号可承接原生 remote_compaction_v2。
	// 它仍要求普通 Responses 能力，额外受账号级 V2 模式和探测结果控制。
	OpenAIEndpointCapabilityRemoteCompactionV2 OpenAIEndpointCapability = "remote_compaction_v2"
)

const openAIWorkloadCapabilitiesCredentialKey = "openai_workload_capabilities"

const (
	GeminiProviderTypeCredentialKey = "provider_type"
	GeminiProviderTypeThirdParty    = "third_party"
	geminiOfficialAPIHost           = "generativelanguage.googleapis.com"
)

// GrokMediaEligibleExtraKey 是 accounts.extra 中可选的账号级覆盖：true 强制允许
// 媒体调度，false 禁用，缺失或 null 时使用上游观测自动判断。
const GrokMediaEligibleExtraKey = "grok_media_eligible"

const (
	OpenAIAuthModePersonalAccessToken = "personalAccessToken"
	openAIAuthModeCredentialKey       = "auth_mode"
	openAIAuthModeLegacyCredentialKey = "openai_auth_mode"
)

func isOpenAIPersonalAccessTokenAuthMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "personalaccesstoken", "personal_access_token":
		return true
	default:
		return false
	}
}

type TempUnschedulableRule struct {
	ErrorCode       int      `json:"error_code"`
	Keywords        []string `json:"keywords"`
	DurationMinutes int      `json:"duration_minutes"`
	Description     string   `json:"description"`
}

func (a *Account) IsActive() bool {
	return a.Status == StatusActive
}

// BillingRateMultiplier 返回账号计费倍率。
// - nil 表示未配置/旧缓存缺字段，按 1.0 处理
// - 允许 0，表示该账号计费为 0
// - 负数属于非法数据，出于安全考虑按 1.0 处理
func (a *Account) BillingRateMultiplier() float64 {
	if a == nil || a.RateMultiplier == nil {
		return 1.0
	}
	if *a.RateMultiplier < 0 {
		return 1.0
	}
	return *a.RateMultiplier
}

func (a *Account) EffectiveLoadFactor() int {
	if a == nil {
		return 1
	}
	if a.LoadFactor != nil && *a.LoadFactor > 0 {
		return *a.LoadFactor
	}
	if a.Concurrency > 0 {
		return a.Concurrency
	}
	return 1
}

func (a *Account) IsSchedulable() bool {
	if !a.IsActive() || !a.Schedulable {
		return false
	}
	now := time.Now()
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt) {
		return false
	}
	if a.OverloadUntil != nil && now.Before(*a.OverloadUntil) {
		return false
	}
	if a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt) {
		return false
	}
	if a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil) {
		return false
	}
	if a.IsAPIKeyOrBedrock() && a.IsQuotaExceeded() {
		return false
	}
	return true
}

// IsCredentialUsableForShadow 报告本账号(作为某 spark 影子的母账号)的凭据/传输是否可被影子透传使用。
//
// 检查「凭据/账号/传输可用性」:
//   - 账号 active(非禁用/删除);
//   - OAuth token 未过期(AutoPauseOnExpired+ExpiresAt);
//   - 未处于 TempUnschedulableUntil 冷却期 —— 对 OpenAI 账号该字段由 401 鉴权失败 /
//     token 刷新耗尽 / transport·proxy 故障写入(ratelimit/token_refresh/upstream_transport),
//     都代表**共享凭据或传输通道坏死**;影子共享母 token+proxy,故母处于该冷却期时影子也不可用。
//
// **刻意排除** global 维度的限流/过载窗口(RateLimitResetAt / OverloadUntil)与母账号自身的
// 手动 Schedulable 开关:spark 影子拥有独立 spark 配额窗口,母账号 global 429(走 RateLimitResetAt)
// 不应连坐 spark(否则重新耦合影子架构本应解耦的两条 429 道)。nil receiver 返回 false。
func (a *Account) IsCredentialUsableForShadow() bool {
	if a == nil || !a.IsActive() {
		return false
	}
	now := time.Now()
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt) {
		return false
	}
	if a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil) {
		return false
	}
	return true
}

func (a *Account) IsRateLimited() bool {
	if a.RateLimitResetAt == nil {
		return false
	}
	return time.Now().Before(*a.RateLimitResetAt)
}

func (a *Account) IsOverloaded() bool {
	if a.OverloadUntil == nil {
		return false
	}
	return time.Now().Before(*a.OverloadUntil)
}

func (a *Account) IsOAuth() bool {
	return a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken
}

// IsPrivacySet 检查账号的 privacy 是否已成功设置。
// OpenAI: privacy_mode == "training_off"
// Antigravity: privacy_mode == "privacy_set"
// 其他平台: 无 privacy 概念，始终返回 true
func (a *Account) IsPrivacySet() bool {
	switch a.Platform {
	case PlatformOpenAI:
		return a.getExtraString("privacy_mode") == PrivacyModeTrainingOff
	case PlatformAntigravity:
		return a.getExtraString("privacy_mode") == AntigravityPrivacySet
	default:
		return true
	}
}

func (a *Account) IsGemini() bool {
	return a.Platform == PlatformGemini
}

func (a *Account) IsGrok() bool {
	return a.Platform == PlatformGrok
}

func (a *Account) IsGrokOAuth() bool {
	return a.IsGrok() && a.Type == AccountTypeOAuth
}

// IsKimi / IsZhipu / IsDeepseek 标识国产 OpenAI 兼容供应商账号。
func (a *Account) IsKimi() bool {
	return a.Platform == PlatformKimi
}

func (a *Account) IsZhipu() bool {
	return a.Platform == PlatformZhipu
}

func (a *Account) IsDeepseek() bool {
	return a.Platform == PlatformDeepseek
}

// IsCNProvider 报告是否为国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）。
func (a *Account) IsCNProvider() bool {
	return a != nil && IsCNProvider(a.Platform)
}

// IsOpenAICompatible 报告账号是否走 OpenAI 网关（OpenAI 协议族）。
// openai/grok 原生走 OpenAI 网关；kimi/zhipu/deepseek 同为 OpenAI Chat Completions
// 兼容上游，也经 OpenAI 网关转发。
func (a *Account) IsOpenAICompatible() bool {
	return a != nil && (a.Platform == PlatformOpenAI || a.Platform == PlatformGrok ||
		a.Platform == PlatformKimi || a.Platform == PlatformZhipu || a.Platform == PlatformDeepseek)
}

func (a *Account) GeminiOAuthType() string {
	if a.Platform != PlatformGemini || a.Type != AccountTypeOAuth {
		return ""
	}
	oauthType := strings.TrimSpace(a.GetCredential("oauth_type"))
	if oauthType == "" && strings.TrimSpace(a.GetCredential("project_id")) != "" {
		return "code_assist"
	}
	return oauthType
}

func (a *Account) GeminiTierID() string {
	tierID := strings.TrimSpace(a.GetCredential("tier_id"))
	return tierID
}

// IsGeminiThirdPartyProvider 判断 Gemini API Key 是否通过第三方提供商接入。
// 该标记只影响本地官方配额模拟，不改变 Gemini 请求的认证和转发协议。
func (a *Account) IsGeminiThirdPartyProvider() bool {
	if a == nil || a.Platform != PlatformGemini || a.Type != AccountTypeAPIKey {
		return false
	}
	return strings.EqualFold(a.GetCredential(GeminiProviderTypeCredentialKey), GeminiProviderTypeThirdParty)
}

// HasGeminiThirdPartyBaseURL 判断第三方 Gemini API Key 是否配置了非官方端点。
// 第三方来源不能依赖官方默认地址，否则会在关闭官方模拟配额的同时仍请求 Google 端点。
func (a *Account) HasGeminiThirdPartyBaseURL() bool {
	if !a.IsGeminiThirdPartyProvider() {
		return false
	}
	baseURL := strings.TrimSpace(a.GetCredential("base_url"))
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return false
	}
	return !strings.EqualFold(parsed.Hostname(), geminiOfficialAPIHost)
}

func (a *Account) IsGeminiCodeAssist() bool {
	if a.Platform != PlatformGemini || a.Type != AccountTypeOAuth {
		return false
	}
	oauthType := a.GeminiOAuthType()
	if oauthType == "" {
		return strings.TrimSpace(a.GetCredential("project_id")) != ""
	}
	return oauthType == "code_assist"
}

// IsGeminiGoogleOne 判断账号是否使用旧版消费者 Gemini CLI / Code Assist OAuth 通道。
func (a *Account) IsGeminiGoogleOne() bool {
	return a.Platform == PlatformGemini && a.Type == AccountTypeOAuth && a.GeminiOAuthType() == "google_one"
}

func (a *Account) CanGetUsage() bool {
	return a.Type == AccountTypeOAuth
}

func (a *Account) GetCredential(key string) string {
	if a.Credentials == nil {
		return ""
	}
	v, ok := a.Credentials[key]
	if !ok || v == nil {
		return ""
	}

	// 支持多种类型（兼容历史数据中 expires_at 等字段可能是数字或字符串）
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		// GORM datatypes.JSONMap 使用 UseNumber() 解析，数字类型为 json.Number
		return val.String()
	case float64:
		// JSON 解析后数字默认为 float64
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case int:
		return strconv.Itoa(val)
	default:
		return ""
	}
}

// GetCredentialAsTime 解析凭证中的时间戳字段，支持多种格式
// 兼容以下格式：
//   - RFC3339 字符串: "2025-01-01T00:00:00Z"
//   - Unix 时间戳字符串: "1735689600"
//   - Unix 时间戳数字: 1735689600 (float64/int64/json.Number)
func (a *Account) GetCredentialAsTime(key string) *time.Time {
	s := a.GetCredential(key)
	if s == "" {
		return nil
	}
	// 尝试 RFC3339 格式
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	// 尝试 Unix 时间戳（纯数字字符串）
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		t := time.Unix(ts, 0)
		return &t
	}
	return nil
}

// GetCredentialAsInt64 解析凭证中的 int64 字段
// 用于读取 _token_version 等内部字段
func (a *Account) GetCredentialAsInt64(key string) int64 {
	if a == nil || a.Credentials == nil {
		return 0
	}
	val, ok := a.Credentials[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i
		}
	}
	return 0
}

func (a *Account) IsTempUnschedulableEnabled() bool {
	if a.Credentials == nil {
		return false
	}
	raw, ok := a.Credentials["temp_unschedulable_enabled"]
	if !ok || raw == nil {
		return false
	}
	enabled, ok := raw.(bool)
	return ok && enabled
}

func (a *Account) GetTempUnschedulableRules() []TempUnschedulableRule {
	if a.Credentials == nil {
		return nil
	}
	raw, ok := a.Credentials["temp_unschedulable_rules"]
	if !ok || raw == nil {
		return nil
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	rules := make([]TempUnschedulableRule, 0, len(arr))
	for _, item := range arr {
		entry, ok := item.(map[string]any)
		if !ok || entry == nil {
			continue
		}

		rule := TempUnschedulableRule{
			ErrorCode:       parseTempUnschedInt(entry["error_code"]),
			Keywords:        parseTempUnschedStrings(entry["keywords"]),
			DurationMinutes: parseTempUnschedInt(entry["duration_minutes"]),
			Description:     parseTempUnschedString(entry["description"]),
		}

		if rule.ErrorCode <= 0 || rule.DurationMinutes <= 0 || len(rule.Keywords) == 0 {
			continue
		}

		rules = append(rules, rule)
	}

	return rules
}

func parseTempUnschedString(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func parseTempUnschedStrings(value any) []string {
	if value == nil {
		return nil
	}

	var raw []string
	switch v := value.(type) {
	case []string:
		raw = v
	case []any:
		raw = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	default:
		return nil
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func normalizeAccountNotes(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func parseTempUnschedInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

const (
	// OpenAICompactModeForceOn 表示管理员启用对应压缩能力。
	OpenAICompactModeForceOn = "force_on"
	// OpenAICompactModeForceOff 表示管理员关闭对应压缩能力。
	OpenAICompactModeForceOff            = "force_off"
	openAINativeCompactionV2ModeExtraKey = "openai_native_compaction_v2_mode"
)

func stringMappingFromRaw(raw any) map[string]string {
	switch mapping := raw.(type) {
	case map[string]any:
		if len(mapping) == 0 {
			return nil
		}
		result := make(map[string]string, len(mapping))
		for key, value := range mapping {
			if str, ok := value.(string); ok {
				result[key] = str
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	case map[string]string:
		if len(mapping) == 0 {
			return nil
		}
		result := make(map[string]string, len(mapping))
		for key, value := range mapping {
			result[key] = value
		}
		return result
	default:
		return nil
	}
}

func (a *Account) GetModelMapping() map[string]string {
	credentialsPtr := mapPtr(a.Credentials)
	rawMapping, _ := a.Credentials["model_mapping"].(map[string]any)
	rawPtr := mapPtr(rawMapping)
	rawLen := len(rawMapping)
	rawSig := uint64(0)
	rawSigReady := false

	if a.modelMappingCacheReady &&
		a.modelMappingCacheCredentialsPtr == credentialsPtr &&
		a.modelMappingCacheRawPtr == rawPtr &&
		a.modelMappingCacheRawLen == rawLen {
		rawSig = modelMappingSignature(rawMapping)
		rawSigReady = true
		if a.modelMappingCacheRawSig == rawSig {
			return a.modelMappingCache
		}
	}

	mapping := a.resolveModelMapping(rawMapping)
	if !rawSigReady {
		rawSig = modelMappingSignature(rawMapping)
	}

	a.modelMappingCache = mapping
	a.modelMappingCacheReady = true
	a.modelMappingCacheCredentialsPtr = credentialsPtr
	a.modelMappingCacheRawPtr = rawPtr
	a.modelMappingCacheRawLen = rawLen
	a.modelMappingCacheRawSig = rawSig
	return mapping
}

func (a *Account) resolveModelMapping(rawMapping map[string]any) map[string]string {
	if a.Credentials == nil {
		// Antigravity 平台使用默认映射
		if a.Platform == domain.PlatformAntigravity {
			return domain.DefaultAntigravityModelMapping
		}
		// Bedrock 默认映射由 forwardBedrock 统一处理（需配合 region prefix 调整）
		return nil
	}
	if len(rawMapping) == 0 {
		if a.IsGeminiGoogleOne() {
			return geminicli.GoogleOneModelMapping()
		}
		// Antigravity 平台使用默认映射
		if a.Platform == domain.PlatformAntigravity {
			return domain.DefaultAntigravityModelMapping
		}
		return nil
	}

	result := make(map[string]string)
	for k, v := range rawMapping {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	if len(result) > 0 {
		if a.Platform == domain.PlatformAntigravity {
			ensureAntigravityDefaultPassthroughs(result, []string{
				"gemini-3-flash",
				"gemini-3.1-pro-high",
				"gemini-3.1-pro-low",
				"gemini-3.6-flash",
				"gemini-3.6-flash-high",
				"gemini-3.6-flash-low",
				"gemini-3.6-flash-medium",
				"gemini-3.6-flash-tiered",
			})
			applyAntigravityGemini31ProAliases(result)
		}
		return result
	}

	// Antigravity 平台使用默认映射
	if a.IsGeminiGoogleOne() {
		return geminicli.GoogleOneModelMapping()
	}
	if a.Platform == domain.PlatformAntigravity {
		return domain.DefaultAntigravityModelMapping
	}
	return nil
}

func mapPtr(m map[string]any) uintptr {
	if m == nil {
		return 0
	}
	return reflect.ValueOf(m).Pointer()
}

func modelMappingSignature(rawMapping map[string]any) uint64 {
	if len(rawMapping) == 0 {
		return 0
	}
	keys := make([]string, 0, len(rawMapping))
	for k := range rawMapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := fnv.New64a()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		if v, ok := rawMapping[k].(string); ok {
			_, _ = h.Write([]byte(v))
		} else {
			_, _ = h.Write([]byte{1})
		}
		_, _ = h.Write([]byte{0xff})
	}
	return h.Sum64()
}

func ensureAntigravityDefaultPassthrough(mapping map[string]string, model string) {
	if mapping == nil || model == "" {
		return
	}
	if _, exists := mapping[model]; exists {
		return
	}
	for pattern := range mapping {
		if matchWildcard(pattern, model) {
			return
		}
	}
	mapping[model] = model
}

func ensureAntigravityDefaultPassthroughs(mapping map[string]string, models []string) {
	for _, model := range models {
		ensureAntigravityDefaultPassthrough(mapping, model)
	}
}

// applyAntigravityGemini31ProAliases 将旧的 3.1 Pro 目标规范为实际 Pro Agent 路由。
func applyAntigravityGemini31ProAliases(mapping map[string]string) {
	target := strings.TrimSpace(mapping[domain.AntigravityGemini31ProAgentModel])
	if target == "" {
		return
	}

	aliases := []struct {
		model         string
		legacyTargets map[string]struct{}
	}{
		{
			model: "gemini-3.1-pro",
			legacyTargets: map[string]struct{}{
				"gemini-3.1-pro": {},
			},
		},
		{
			model: "gemini-3.1-pro-high",
			legacyTargets: map[string]struct{}{
				"gemini-3.1-pro-high": {},
			},
		},
		{
			model: "gemini-3.1-pro-preview",
			legacyTargets: map[string]struct{}{
				"gemini-3.1-pro-preview": {},
				"gemini-3.1-pro-high":    {},
			},
		},
	}

	for _, alias := range aliases {
		current, exists := mapping[alias.model]
		if exists {
			if _, legacy := alias.legacyTargets[current]; legacy {
				mapping[alias.model] = target
			}
			continue
		}
		if mappingHasWildcardForModel(mapping, alias.model) {
			continue
		}
		mapping[alias.model] = target
	}
}

// mappingHasWildcardForModel 判断现有映射是否已通过通配符覆盖指定模型。
func mappingHasWildcardForModel(mapping map[string]string, model string) bool {
	for pattern := range mapping {
		if matchWildcard(pattern, model) {
			return true
		}
	}
	return false
}

func normalizeRequestedModelForLookup(platform, requestedModel string) string {
	trimmed := strings.TrimSpace(requestedModel)
	if trimmed == "" {
		return ""
	}
	if platform != PlatformGemini && platform != PlatformAntigravity {
		return trimmed
	}
	if trimmed == "gemini-3.1-pro-preview-customtools" {
		return "gemini-3.1-pro-preview"
	}
	return trimmed
}

func resolveRequestedModelInMapping(mapping map[string]string, requestedModel string) (mappedModel string, matched bool) {
	if requestedModel == "" {
		return "", false
	}
	if mappedModel, exists := mapping[requestedModel]; exists {
		return mappedModel, true
	}
	return matchWildcardMappingResult(mapping, requestedModel)
}

// extractFinalModelWhitelist 从 model_mapping 中提取“最终模型白名单”。
// 约定：只有精确自映射（from == to，且不含通配符）的条目才算白名单。
// 这样既能兼容历史上把白名单持久化为 key=value 的做法，又不会把普通映射规则误判成白名单。
func extractFinalModelWhitelist(platform string, mapping map[string]string) map[string]struct{} {
	if len(mapping) == 0 {
		return nil
	}
	whitelist := make(map[string]struct{})
	for rawFrom, rawTo := range mapping {
		if strings.Contains(rawFrom, "*") {
			continue
		}
		from := normalizeRequestedModelForLookup(platform, rawFrom)
		to := normalizeRequestedModelForLookup(platform, strings.TrimSpace(rawTo))
		if from == "" || to == "" || from != to {
			continue
		}
		whitelist[to] = struct{}{}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}

// extractExplicitFinalModelWhitelist 从独立的 model_whitelist 字段提取最终模型白名单。
// 该字段是新的主持久化方式；仍忽略通配符，保持与前端 whitelist 视图一致。
func extractExplicitFinalModelWhitelist(platform string, rawWhitelist any) map[string]struct{} {
	if rawWhitelist == nil {
		return nil
	}
	values := make([]string, 0)
	switch typed := rawWhitelist.(type) {
	case []string:
		values = append(values, typed...)
	case []any:
		for _, raw := range typed {
			if raw == nil {
				continue
			}
			values = append(values, strings.TrimSpace(fmt.Sprint(raw)))
		}
	default:
		return nil
	}
	whitelist := make(map[string]struct{})
	for _, rawModel := range values {
		model := normalizeRequestedModelForLookup(platform, rawModel)
		if model == "" || strings.Contains(model, "*") {
			continue
		}
		whitelist[model] = struct{}{}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}

// resolveFinalModelWhitelist 优先读取独立的 model_whitelist 字段；
// 若该字段不存在，则回退到旧版“自映射即白名单”的兼容解析。
func resolveFinalModelWhitelist(platform string, credentials map[string]any, mapping map[string]string) (map[string]struct{}, bool) {
	if credentials != nil {
		if rawWhitelist, exists := credentials["model_whitelist"]; exists {
			return extractExplicitFinalModelWhitelist(platform, rawWhitelist), true
		}
	}
	if platform == PlatformQoder {
		return nil, false
	}
	return extractFinalModelWhitelist(platform, mapping), false
}

// isModelInFinalWhitelist 检查最终上游模型是否命中白名单。
// Gemini / Antigravity 仍复用既有归一化逻辑，避免 customtools 这类别名导致误判。
func isModelInFinalWhitelist(platform, model string, whitelist map[string]struct{}) bool {
	if len(whitelist) == 0 {
		return true
	}
	if _, ok := whitelist[model]; ok {
		return true
	}
	if platform == PlatformQoder {
		modelKey := normalizeQoderModelForWhitelist(model)
		for allowedModel := range whitelist {
			if normalizeQoderModelForWhitelist(allowedModel) == modelKey {
				return true
			}
		}
		return false
	}
	normalized := normalizeRequestedModelForLookup(platform, model)
	if normalized == model {
		return false
	}
	_, ok := whitelist[normalized]
	return ok
}

// isFinalModelWhitelisted 直接检查已经完成账号映射和平台规范化的最终模型，避免再次执行账号映射。
func (a *Account) isFinalModelWhitelisted(finalModel string) bool {
	if a == nil {
		return false
	}
	mapping := a.GetModelMapping()
	whitelist, _ := resolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	return isModelInFinalWhitelist(a.Platform, finalModel, whitelist)
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

// IsModelSupported 检查账号是否支持该请求模型。
// 规则：
// 1. 未配置 model_mapping 时，直接按最终白名单（model_whitelist）判断；未配置白名单则允许所有模型；
// 2. 已配置时，若请求模型命中映射/透传规则，则先映射，再对映射后的最终模型做白名单校验；
// 3. 若请求模型未命中映射，则把它当作隐式透传模型，直接按最终模型做白名单校验；
// 4. 当不存在任何白名单时，mapping 仅作为可选改写规则，不限制请求模型。
// 5. 为兼容旧数据，非 Qoder 平台若未配置独立 model_whitelist，会继续把精确自映射条目视作最终白名单。
// 6. OpenAI OAuth 非透传账号还会排除明确属于其他厂商的模型，避免 Codex 上游返回不可重试的 400。
func (a *Account) IsModelSupported(requestedModel string) bool {
	// OpenAI 透传模式仅替换认证，模型能力由上游决定；必须在 model_mapping
	// 分支前短路，否则调度快照中的历史映射会把可用账号误判为不支持模型。
	if a.IsOpenAIPassthroughEnabled() {
		return true
	}
	mapping := a.GetModelMapping()
	// Antigravity 仍保持“请求模型命中映射即可支持”的既有语义。
	// 该平台的最终模型（含 thinking 后缀）校验由网关层专门处理，不能在这里提前套用通用白名单规则。
	if a.Platform == PlatformAntigravity {
		if len(mapping) == 0 {
			return true
		}
		_, matched := a.ResolveMappedModel(requestedModel)
		return matched
	}
	whitelist, _ := resolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	if a.Platform == PlatformQoder {
		mappedModel, matched := a.ResolveMappedModel(requestedModel)
		if matched {
			// 显式账号 mapping 优先于站点默认模型限制。
			return isModelInFinalWhitelist(a.Platform, mappedModel, whitelist)
		}
		site, err := qoderSiteForAccount(a)
		if err != nil || !qoder.ModelCompatibleWithSite(site, requestedModel) {
			return false
		}
		return isModelInFinalWhitelist(a.Platform, requestedModel, whitelist)
	}
	if len(mapping) == 0 {
		if !isModelInFinalWhitelist(a.Platform, requestedModel, whitelist) {
			return false
		}
		if a.IsOpenAIOAuth() && !a.IsOpenAIPassthroughEnabled() {
			return isOpenAIOAuthServableModel(requestedModel)
		}
		return true
	}
	mappedModel, matched := a.ResolveMappedModel(requestedModel)
	if matched {
		return isModelInFinalWhitelist(a.Platform, mappedModel, whitelist)
	}
	return isModelInFinalWhitelist(a.Platform, requestedModel, whitelist)
}

// GetConfiguredRequestModels 返回账号显式配置的“可请求模型”列表。
// 只有存在最终白名单时，才返回有限的“可请求模型”集合：
// - 白名单中的模型可直接请求（隐式透传）；
// - mapping 的 key 也可请求（显式改写）。
// 若不存在白名单，则请求模型空间不受限制，返回 nil 表示调用方应回退到默认模型列表。
func (a *Account) GetConfiguredRequestModels() []string {
	mapping := a.GetModelMapping()
	whitelist, _ := resolveFinalModelWhitelist(a.Platform, a.Credentials, mapping)
	if a.Platform == PlatformQoder {
		return configuredQoderRequestModels(mapping, whitelist)
	}
	if len(whitelist) == 0 {
		return nil
	}
	modelSet := make(map[string]struct{})
	if len(mapping) > 0 {
		for model := range mapping {
			modelSet[model] = struct{}{}
		}
	}
	// 无论白名单来自显式字段还是 legacy 自映射，白名单模型本身都可直接请求。
	for model := range whitelist {
		modelSet[model] = struct{}{}
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// configuredQoderRequestModels 将 model_mapping key 作为 Qoder 请求和展示模型集合。
// 如果没有配置 mapping，model_whitelist 仍可作为仅白名单账号的显式请求模型列表。
func configuredQoderRequestModels(mapping map[string]string, whitelist map[string]struct{}) []string {
	if len(mapping) == 0 && len(whitelist) == 0 {
		return nil
	}
	modelSet := make(map[string]struct{}, len(mapping)+len(whitelist))
	if len(mapping) > 0 {
		for rawModel := range mapping {
			model := strings.TrimSpace(rawModel)
			if model != "" {
				modelSet[model] = struct{}{}
			}
		}
	} else {
		for rawModel := range whitelist {
			model := strings.TrimSpace(rawModel)
			if model != "" {
				modelSet[model] = struct{}{}
			}
		}
	}
	if len(modelSet) == 0 {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// GetMappedModel 获取映射后的模型名（支持通配符，最长优先匹配）
// 如果未配置 mapping，返回原始模型名
func (a *Account) GetMappedModel(requestedModel string) string {
	mappedModel, _ := a.ResolveMappedModel(requestedModel)
	return mappedModel
}

// ResolveMappedModel 获取映射后的模型名，并返回是否命中了账号级映射。
// matched=true 表示命中了精确映射或通配符映射，即使映射结果与原模型名相同。
func (a *Account) ResolveMappedModel(requestedModel string) (mappedModel string, matched bool) {
	mapping := a.GetModelMapping()
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := resolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	normalized := normalizeRequestedModelForLookup(a.Platform, requestedModel)
	if normalized != requestedModel {
		if mappedModel, matched := resolveRequestedModelInMapping(mapping, normalized); matched {
			return mappedModel, true
		}
	}
	return requestedModel, false
}

// GetOpenAICompactMode 返回管理员选择的旧版压缩开关。
// 缺失配置保持默认开启；历史输入由账号写入边界规范化。
func (a *Account) GetOpenAICompactMode() string {
	if a == nil || !a.IsOpenAI() {
		return OpenAICompactModeForceOff
	}
	mode, _ := a.Extra["openai_compact_mode"].(string)
	if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
		return OpenAICompactModeForceOff
	}
	return OpenAICompactModeForceOn
}

// AllowsOpenAICompact 判断管理员是否启用旧版压缩。
func (a *Account) AllowsOpenAICompact() bool {
	return a != nil && a.IsOpenAI() && a.GetOpenAICompactMode() == OpenAICompactModeForceOn
}

// GetOpenAINativeCompactionV2Mode 返回原生 V2 压缩的管理员开关。
func (a *Account) GetOpenAINativeCompactionV2Mode() string {
	if a == nil || !a.IsOpenAI() {
		return OpenAICompactModeForceOff
	}
	mode, _ := a.Extra[openAINativeCompactionV2ModeExtraKey].(string)
	if strings.EqualFold(strings.TrimSpace(mode), OpenAICompactModeForceOff) {
		return OpenAICompactModeForceOff
	}
	return OpenAICompactModeForceOn
}

// AllowsOpenAINativeCompactionV2 判断管理员是否启用原生 V2 压缩。
func (a *Account) AllowsOpenAINativeCompactionV2() bool {
	return a != nil && a.IsOpenAI() && a.GetOpenAINativeCompactionV2Mode() == OpenAICompactModeForceOn
}

// GetCompactModelMapping returns compact-only model remapping configuration.
// This mapping is intended for /responses/compact only and does not affect
// normal /responses traffic.
func (a *Account) GetCompactModelMapping() map[string]string {
	if a == nil || a.Credentials == nil {
		return nil
	}
	return stringMappingFromRaw(a.Credentials["compact_model_mapping"])
}

// ResolveCompactMappedModel resolves compact-only model remapping and reports
// whether a compact-specific mapping rule matched.
func (a *Account) ResolveCompactMappedModel(requestedModel string) (mappedModel string, matched bool) {
	mapping := a.GetCompactModelMapping()
	if len(mapping) == 0 {
		return requestedModel, false
	}
	if mappedModel, matched := resolveRequestedModelInMapping(mapping, requestedModel); matched {
		return mappedModel, true
	}
	return requestedModel, false
}

func (a *Account) GetBaseURL() string {
	if a.Type != AccountTypeAPIKey {
		return ""
	}
	baseURL := a.GetCredential("base_url")
	if baseURL == "" {
		return "https://api.anthropic.com"
	}
	if a.Platform == PlatformAntigravity {
		return strings.TrimRight(baseURL, "/") + "/antigravity"
	}
	return baseURL
}

// GetGeminiBaseURL 返回 Gemini 兼容端点的 base URL。
// Antigravity 平台的 APIKey 账号自动拼接 /antigravity。
func (a *Account) GetGeminiBaseURL(defaultBaseURL string) string {
	baseURL := strings.TrimSpace(a.GetCredential("base_url"))
	if baseURL == "" {
		return defaultBaseURL
	}
	if a.Platform == PlatformAntigravity && a.Type == AccountTypeAPIKey {
		return strings.TrimRight(baseURL, "/") + "/antigravity"
	}
	return baseURL
}

func (a *Account) GetExtraString(key string) string {
	if a.Extra == nil {
		return ""
	}
	if v, ok := a.Extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (a *Account) GetClaudeUserID() string {
	if v := strings.TrimSpace(a.GetExtraString("claude_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(a.GetExtraString("anthropic_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(a.GetCredential("claude_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(a.GetCredential("anthropic_user_id")); v != "" {
		return v
	}
	return ""
}

// matchAntigravityWildcard 通配符匹配（仅支持末尾 *）
// 用于 model_mapping 的通配符匹配
func matchAntigravityWildcard(pattern, str string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(str, prefix)
	}
	return pattern == str
}

// matchWildcard 通用通配符匹配（仅支持末尾 *）
// 复用 Antigravity 的通配符逻辑，供其他平台使用
func matchWildcard(pattern, str string) bool {
	return matchAntigravityWildcard(pattern, str)
}

func matchWildcardMappingResult(mapping map[string]string, requestedModel string) (string, bool) {
	// 收集所有匹配的 pattern，按长度降序排序（最长优先）
	type patternMatch struct {
		pattern string
		target  string
	}
	var matches []patternMatch

	for pattern, target := range mapping {
		if matchWildcard(pattern, requestedModel) {
			matches = append(matches, patternMatch{pattern, target})
		}
	}

	if len(matches) == 0 {
		return requestedModel, false // 无匹配，返回原始模型名
	}

	// 按 pattern 长度降序排序
	sort.Slice(matches, func(i, j int) bool {
		if len(matches[i].pattern) != len(matches[j].pattern) {
			return len(matches[i].pattern) > len(matches[j].pattern)
		}
		return matches[i].pattern < matches[j].pattern
	})

	return matches[0].target, true
}

func (a *Account) IsCustomErrorCodesEnabled() bool {
	if a.Type != AccountTypeAPIKey || a.Credentials == nil {
		return false
	}
	if v, ok := a.Credentials["custom_error_codes_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsPoolMode 检查 API Key 账号是否启用池模式。
// 池模式默认不根据上游错误写本地调度状态；管理员显式错误策略仍然优先，
// 只有配置的重试状态码会在同一账号上重试。
func (a *Account) IsPoolMode() bool {
	if !a.IsAPIKeyOrBedrock() || a.Credentials == nil {
		return false
	}
	if v, ok := a.Credentials["pool_mode"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

const (
	defaultPoolModeRetryCount = 3
	maxPoolModeRetryCount     = 10
)

// GetPoolModeRetryCount 返回池模式同账号重试次数。
// 未配置或配置非法时回退为默认值 3；小于 0 按 0 处理；过大则截断到 10。
func (a *Account) GetPoolModeRetryCount() int {
	if a == nil || !a.IsPoolMode() || a.Credentials == nil {
		return defaultPoolModeRetryCount
	}
	raw, ok := a.Credentials["pool_mode_retry_count"]
	if !ok || raw == nil {
		return defaultPoolModeRetryCount
	}
	count := parsePoolModeRetryCount(raw)
	if count < 0 {
		return 0
	}
	if count > maxPoolModeRetryCount {
		return maxPoolModeRetryCount
	}
	return count
}

func parsePoolModeRetryCount(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return defaultPoolModeRetryCount
}

// defaultPoolModeRetryableStatusCodes 池模式下默认触发同账号重试的状态码。
// 未在 Account.Credentials 中显式配置 pool_mode_retry_status_codes 时使用。
var defaultPoolModeRetryableStatusCodes = []int{401, 403, 429}

// isPoolModeRetryableStatus 池模式下应触发同账号重试的状态码（默认列表）。
func isPoolModeRetryableStatus(statusCode int) bool {
	for _, c := range defaultPoolModeRetryableStatusCodes {
		if c == statusCode {
			return true
		}
	}
	return false
}

// GetPoolModeRetryStatusCodes 返回账号自定义的池模式同账号重试状态码列表。
//
// 返回值语义：
//   - nil：未配置 → 调用方应回退到默认值 [401, 403, 429]
//   - 长度为 0 的切片：管理员显式置空 → 关闭按状态码触发的同账号重试
//   - 非空切片：去重、过滤为合法 HTTP 状态码（100-599）后的覆盖列表
func (a *Account) GetPoolModeRetryStatusCodes() []int {
	if a == nil || a.Credentials == nil {
		return nil
	}
	raw, ok := a.Credentials["pool_mode_retry_status_codes"]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	seen := make(map[int]struct{}, len(arr))
	codes := make([]int, 0, len(arr))
	for _, v := range arr {
		var code int
		switch n := v.(type) {
		case float64:
			code = int(n)
		case int:
			code = n
		case int64:
			code = int(n)
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				continue
			}
			code = int(i)
		case string:
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				continue
			}
			code = i
		default:
			continue
		}
		if code < 100 || code > 599 {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

// IsPoolModeRetryableStatus 在账号上下文中判断给定状态码是否应触发同账号重试。
// 若账号未配置 pool_mode_retry_status_codes，则回退到默认列表。
func (a *Account) IsPoolModeRetryableStatus(statusCode int) bool {
	codes := a.GetPoolModeRetryStatusCodes()
	if codes == nil {
		return isPoolModeRetryableStatus(statusCode)
	}
	for _, c := range codes {
		if c == statusCode {
			return true
		}
	}
	return false
}

func (a *Account) GetCustomErrorCodes() []int {
	if a.Credentials == nil {
		return nil
	}
	raw, ok := a.Credentials["custom_error_codes"]
	if !ok || raw == nil {
		return nil
	}
	if arr, ok := raw.([]any); ok {
		result := make([]int, 0, len(arr))
		for _, v := range arr {
			if f, ok := v.(float64); ok {
				result = append(result, int(f))
			}
		}
		return result
	}
	return nil
}

func (a *Account) ShouldHandleErrorCode(statusCode int) bool {
	if !a.IsCustomErrorCodesEnabled() {
		return true
	}
	codes := a.GetCustomErrorCodes()
	if len(codes) == 0 {
		return true
	}
	for _, code := range codes {
		if code == statusCode {
			return true
		}
	}
	return false
}

func (a *Account) IsInterceptWarmupEnabled() bool {
	if a.Credentials == nil {
		return false
	}
	if v, ok := a.Credentials["intercept_warmup_requests"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

func (a *Account) IsBedrock() bool {
	return a.Platform == PlatformAnthropic && a.Type == AccountTypeBedrock
}

func (a *Account) IsBedrockAPIKey() bool {
	return a.IsBedrock() && a.GetCredential("auth_mode") == "apikey"
}

// IsAPIKeyOrBedrock 返回账号类型是否支持配额和池模式等特性
func (a *Account) IsAPIKeyOrBedrock() bool {
	return a.Type == AccountTypeAPIKey || a.Type == AccountTypeBedrock
}

func (a *Account) IsOpenAI() bool {
	return a.Platform == PlatformOpenAI
}

func (a *Account) IsAnthropic() bool {
	return a.Platform == PlatformAnthropic
}

func (a *Account) IsQoder() bool {
	return a.Platform == PlatformQoder
}

func (a *Account) IsQoderCosy() bool {
	return a.IsQoder() && a.Type == AccountTypeCosy
}

func (a *Account) IsOpenAIOAuth() bool {
	return a.IsOpenAI() && a.Type == AccountTypeOAuth
}

// IsOpenAIOAuthLike reports OpenAI credentials that use the ChatGPT/Codex
// inference protocol. Setup tokens share that forwarding contract but do not
// participate in the refreshable OAuth credential lifecycle.
func (a *Account) IsOpenAIOAuthLike() bool {
	return a != nil && a.IsOpenAI() && (a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken)
}

// UsesOpenAICodexProtocol preserves legacy OpenAI gateway OAuth routing for
// accounts whose platform is implicit, while adding OpenAI SetupToken.
func (a *Account) UsesOpenAICodexProtocol() bool {
	return a != nil && (a.Type == AccountTypeOAuth || a.IsOpenAIOAuthLike())
}

func (a *Account) IsOpenAIChatGPTSubscription() bool {
	if !a.IsOpenAIOAuth() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(a.GetCredential("plan_type"))) {
	case "", "free", "abnormal":
		return false
	default:
		return true
	}
}

// IsOpenAIPersonalAccessToken 判断 OpenAI OAuth 账号是否使用 Codex PAT 认证模式。
func (a *Account) IsOpenAIPersonalAccessToken() bool {
	if a == nil || !a.IsOpenAIOAuth() {
		return false
	}
	return isOpenAIPersonalAccessTokenAuthMode(a.GetCredential(openAIAuthModeCredentialKey)) ||
		isOpenAIPersonalAccessTokenAuthMode(a.GetCredential(openAIAuthModeLegacyCredentialKey))
}

func (a *Account) IsOpenAIApiKey() bool {
	return a.IsOpenAI() && a.Type == AccountTypeAPIKey
}

// GetOpenAIBaseURL 解析 OpenAI 协议族账号的上游 base_url。
// 适用 openai 与国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）；grok 走 GetGrokBaseURL，
// 此处对 grok 返回 "" 以保持原有行为。
func (a *Account) GetOpenAIBaseURL() string {
	if !a.IsOpenAI() && !a.IsCNProvider() {
		return ""
	}
	if _, unified := a.Credentials[upstreamProtocolsKey]; a.IsCNProvider() && (unified || a.IsAdaptiveAPIProtocol()) {
		if baseURLs, ok := a.Credentials["api_base_urls"].(map[string]any); ok {
			if baseURL, ok := baseURLs[APIProtocolChatCompletions].(string); ok && strings.TrimSpace(baseURL) != "" {
				return strings.TrimSpace(baseURL)
			}
		}
	}
	if a.Type == AccountTypeAPIKey || a.Type == AccountTypeUpstream {
		if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
			return baseURL
		}
	}
	// 平台默认 base_url：CN 供应商按 account_mode 选择 payg / coding 默认值。
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
		return "https://api.openai.com"
	}
}

// GetAccountMode 返回国产供应商账号的接入模式（payg / coding）。历史账号缺少字段时
// 按 payg 读取；非国产供应商返回空串。存储于 credentials["account_mode"]。
func (a *Account) GetAccountMode() string {
	if a == nil || !a.IsCNProvider() {
		return ""
	}
	mode := strings.TrimSpace(a.GetCredential("account_mode"))
	if mode == AccountModePayG || mode == AccountModeCoding {
		return mode
	}
	return AccountModePayG
}

// IsCodingPlan 报告账号是否为 Coding Plan 模式（用于滚动用量窗口冷却）。
func (a *Account) IsCodingPlan() bool {
	return a.GetAccountMode() == AccountModeCoding
}

// GetAPIProtocol 为原有平台适配器提供协议变体：请求副本使用已解析目标，
// 统一账号的地址/维护流程使用分协议模式，旧对象继续保留历史读取默认值。
func (a *Account) GetAPIProtocol() string {
	if a != nil && a.resolvedProtocol != "" {
		switch a.resolvedProtocol {
		case ProtocolAnthropicMessages:
			return APIProtocolAnthropic
		case ProtocolOpenAIResponses:
			return APIProtocolResponses
		case ProtocolOpenAIChatCompletions:
			return APIProtocolChatCompletions
		}
	}

	if a == nil || !a.IsCNProvider() {
		return APIProtocolChatCompletions
	}
	if _, unified := a.Credentials[upstreamProtocolsKey]; unified {
		return APIProtocolAdaptive
	}
	switch strings.TrimSpace(a.GetCredential("api_protocol")) {
	case APIProtocolAdaptive:
		return APIProtocolAdaptive
	case APIProtocolAnthropic:
		return APIProtocolAnthropic
	case APIProtocolResponses:
		if a.SupportsNativeCNResponses() {
			return APIProtocolResponses
		}
	case APIProtocolChatCompletions:
		return APIProtocolChatCompletions
	}
	return APIProtocolChatCompletions
}

// SupportsNativeCNResponses 报告该国产供应商是否提供原生 Responses 端点。
// DeepSeek 官方为 /responses（无 /v1）；Kimi 按量付费与 Coding Plan 均为
// /v1/responses（moonshot.cn / kimi.com/coding）。
func (a *Account) SupportsNativeCNResponses() bool {
	if a == nil {
		return false
	}
	switch a.Platform {
	case PlatformDeepseek, PlatformKimi:
		return true
	default:
		return false
	}
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
	switch protocol {
	case APIProtocolAnthropic:
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
		}
	case APIProtocolChatCompletions, APIProtocolResponses:
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
		}
	}
	return ""
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

// isDefaultCNAnthropicBaseURL 只识别项目内置端点，避免把同 host 上的自定义中继
// 路径误判为官方端点并替换掉管理员配置的路径前缀。
func isDefaultCNAnthropicBaseURL(baseURL string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch normalized {
	case DefaultKimiPayGAnthropicBaseURL,
		DefaultKimiCodingAnthropicBaseURL,
		DefaultZhipuAnthropicBaseURL,
		DefaultDeepseekAnthropicBaseURL:
		return true
	default:
		return false
	}
}

// stripCNAnthropicPathSuffix 将自定义中继的 Anthropic 协议根转换为同一中继的
// OpenAI 格式根；无法解析时保留原值，由调用链既有 URL 校验负责报错。
func stripCNAnthropicPathSuffix(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "/anthropic" {
		parsed.Path = ""
		parsed.RawPath = ""
	} else if strings.HasSuffix(path, "/anthropic") {
		parsed.Path = strings.TrimSuffix(path, "/anthropic")
		parsed.RawPath = ""
	}
	return strings.TrimRight(parsed.String(), "/")
}

// GetCNAPIKey 返回国产 OpenAI 兼容供应商账号的 api_key 凭据（kimi/zhipu/deepseek）。
// 与 openai 的 GetOpenAIApiKey 区分：后者仅对 openai 平台返回。
func (a *Account) GetCNAPIKey() string {
	if a == nil || !a.IsCNProvider() {
		return ""
	}
	return a.GetCredential("api_key")
}

// GetCodingPlanProvider 根据账号平台识别 Coding Plan 供应商。管理员可以使用自定义
// 中继地址，因此不得通过 URL 内容反推供应商身份。
func (a *Account) GetCodingPlanProvider() string {
	if a == nil || a.GetAccountMode() != AccountModeCoding {
		return ""
	}
	switch a.Platform {
	case PlatformKimi:
		return PlatformKimi
	case PlatformZhipu:
		return PlatformZhipu
	default:
		return ""
	}
}

func (a *Account) GetOpenAIAccessToken() string {
	if !a.IsOpenAI() {
		return ""
	}
	return a.GetCredential("access_token")
}

func (a *Account) GetOpenAIRefreshToken() string {
	if !a.IsOpenAIOAuth() {
		return ""
	}
	return a.GetCredential("refresh_token")
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
	if !a.IsGrok() {
		return ""
	}
	return a.GetCredential("access_token")
}

func (a *Account) GetGrokRefreshToken() string {
	if !a.IsGrokOAuth() {
		return ""
	}
	return a.GetCredential("refresh_token")
}

func (a *Account) GetOpenAIIDToken() string {
	if !a.IsOpenAIOAuth() {
		return ""
	}
	return a.GetCredential("id_token")
}

func (a *Account) GetOpenAIApiKey() string {
	if !a.IsOpenAIApiKey() {
		return ""
	}
	return a.GetCredential("api_key")
}

// GetOpenAIProtocolAPIKey 返回 OpenAI 协议族 APIKey 账号的密钥。
// 覆盖 openai 原生账号与国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）账号，
// 供转发鉴权、模型列表同步等协议族共用路径使用。注意 IsOpenAIApiKey 语义上
// 仅指 openai 平台账号，调度倍率/WS 能力门控继续以其为准，不受本方法影响。
func (a *Account) GetOpenAIProtocolAPIKey() string {
	if a == nil {
		return ""
	}
	if a.IsCNProvider() {
		if a.Type != AccountTypeAPIKey {
			return ""
		}
		return a.GetCredential("api_key")
	}
	return a.GetOpenAIApiKey()
}

func (a *Account) GetOpenAIUserAgent() string {
	if !a.IsOpenAI() {
		return ""
	}
	return a.GetCredential("user_agent")
}

func (a *Account) GetChatGPTAccountID() string {
	if !a.IsOpenAIOAuthLike() {
		return ""
	}
	return a.GetCredential("chatgpt_account_id")
}

// IsChatGPTAccountFedRAMP 读取 ChatGPT 账号是否为 FedRAMP 环境。
func (a *Account) IsChatGPTAccountFedRAMP() bool {
	if !a.IsOpenAIOAuthLike() || a.Credentials == nil {
		return false
	}
	v, ok := a.Credentials["chatgpt_account_is_fedramp"]
	if !ok || v == nil {
		return false
	}
	switch value := v.(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return err == nil && parsed
	case json.Number:
		parsed, err := strconv.ParseBool(value.String())
		return err == nil && parsed
	case float64:
		return value != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	default:
		return false
	}
}

func (a *Account) GetOpenAIDeviceID() string {
	if !a.IsOpenAIOAuth() {
		return ""
	}
	return strings.TrimSpace(a.GetExtraString("openai_device_id"))
}

func (a *Account) GetOpenAISessionID() string {
	if !a.IsOpenAIOAuth() {
		return ""
	}
	return strings.TrimSpace(a.GetExtraString("openai_session_id"))
}

func (a *Account) SupportsOpenAIEndpointCapability(capability OpenAIEndpointCapability) bool {
	if a == nil {
		return false
	}
	if capability == "" {
		return true
	}
	if !a.IsOpenAICompatible() {
		return false
	}
	if _, unified := a.Credentials[upstreamProtocolsKey]; unified && !a.IsGrok() {
		enabled := a.UpstreamProtocols()
		has := func(p GroupClientProtocol) bool { return domain.HasGroupClientProtocol(enabled, p) }
		switch capability {
		case OpenAIEndpointCapabilityTextGeneration:
			if !has(ProtocolOpenAIResponses) && !has(ProtocolOpenAIChatCompletions) && !has(ProtocolAnthropicMessages) {
				return false
			}
		case OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityRemoteCompactionV2:
			if !has(ProtocolOpenAIResponses) {
				return false
			}
		case OpenAIEndpointCapabilityEmbeddings:
			if !has(domain.ProtocolEmbeddings) {
				return false
			}
		case OpenAIEndpointCapabilityLive:
			if !has(domain.ProtocolLive) {
				return false
			}
		case OpenAIEndpointCapabilityAlphaSearch:
			if !has(domain.ProtocolAlphaSearch) && !(a.IsOpenAIPersonalAccessToken() && has(ProtocolOpenAIResponses)) {
				return false
			}
		}
	}
	if a.IsGrok() {
		switch capability {
		case OpenAIEndpointCapabilityTextGeneration:
			return true
		case OpenAIEndpointCapabilityGrokMediaGeneration:
			eligible, reason := a.GrokMediaGenerationEligibility()
			// 尚无观测的 OAuth 账号仍作为调度候选，供请求路径在转发前执行计费探测。
			// 如果探测不可用或无法提供明确的付费资格证据，转发门控会拒绝该账号。
			return eligible || reason == "billing_unobserved"
		default:
			return false
		}
	}
	switch capability {
	case OpenAIEndpointCapabilityTextGeneration:
	case OpenAIEndpointCapabilityLive:
		return a.Platform == PlatformOpenAI &&
			a.Type == AccountTypeOAuth &&
			!a.IsOpenAIPersonalAccessToken() &&
			!a.IsOpenAIAgentIdentity()
	case OpenAIEndpointCapabilityRemoteCompactionV2:
		if !a.AllowsOpenAINativeCompactionV2() {
			return false
		}
		fallthrough
	case OpenAIEndpointCapabilityResponses:
		// 生图等原生 Responses 路径不能降级；使用 Responses 首选协议解析后，
		// 管理员只启用 Chat 的 APIKey 账号必须排除。
		if a.Type == AccountTypeAPIKey && openai_compat.ResolveUpstreamTextProtocol(
			a.Extra,
			openai_compat.TextProtocolResponses,
		) != openai_compat.TextProtocolResponses {
			return false
		}
		// 支持 Responses 的上游同样需具备普通文本能力：复用下方 text_generation
		// 配置集校验。
		capability = OpenAIEndpointCapabilityTextGeneration
	case OpenAIEndpointCapabilityAlphaSearch:
		// alpha/search 的转发按账号类型分流：OAuth/PAT 走
		// chatgpt.com/backend-api/codex/alpha/search，API key 走
		// {base_url}/v1/alpha/search（见 openAIAlphaSearchURL），两类账号
		// 都可承接独立搜索请求。上游不支持该端点时由转发层 failover 兜底。
		if a.Type != AccountTypeOAuth && a.Type != AccountTypeAPIKey {
			return false
		}
	case OpenAIEndpointCapabilityEmbeddings:
		if a.Type != AccountTypeAPIKey {
			return false
		}
	default:
		return false
	}

	configured, found := a.openAIWorkloadCapabilitySet()
	if !found {
		return true
	}
	if capability == OpenAIEndpointCapabilityAlphaSearch && configured[string(OpenAIEndpointCapabilityTextGeneration)] {
		return true
	}
	return configured[string(capability)]
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
	if extra == nil {
		return false, false
	}
	raw, exists := extra[GrokMediaEligibleExtraKey]
	if !exists || raw == nil {
		return false, false
	}
	value, ok := raw.(bool)
	return value, ok
}

func (a *Account) openAIWorkloadCapabilitySet() (map[string]bool, bool) {
	if a == nil || a.Credentials == nil {
		return nil, false
	}
	raw, found := a.Credentials[openAIWorkloadCapabilitiesCredentialKey]
	if !found || raw == nil {
		return nil, false
	}

	result := make(map[string]bool)
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		result[value] = true
	}

	// OAuth/SetupToken 的空容器视为历史未配置；API Key 空集合仍表示显式禁用。
	switch capabilities := raw.(type) {
	case []any:
		if len(capabilities) == 0 && a.IsOpenAIOAuthLike() {
			return nil, false
		}
		for _, item := range capabilities {
			if value, ok := item.(string); ok {
				add(value)
			}
		}
	case []string:
		if len(capabilities) == 0 && a.IsOpenAIOAuthLike() {
			return nil, false
		}
		for _, value := range capabilities {
			add(value)
		}
	case map[string]any:
		if len(capabilities) == 0 && a.IsOpenAIOAuthLike() {
			return nil, false
		}
		for key, value := range capabilities {
			enabled, ok := value.(bool)
			if ok && enabled {
				add(key)
			}
		}
	case map[string]bool:
		if len(capabilities) == 0 && a.IsOpenAIOAuthLike() {
			return nil, false
		}
		for key, enabled := range capabilities {
			if enabled {
				add(key)
			}
		}
	}

	return result, true
}

func (a *Account) SupportsOpenAIImageCapability(capability OpenAIImagesCapability) bool {
	if capability == "" {
		return true
	}
	if !a.IsOpenAI() {
		return false
	}
	switch capability {
	case OpenAIImagesCapabilityBasic, OpenAIImagesCapabilityNative:
		return a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken || a.Type == AccountTypeAPIKey
	default:
		return true
	}
}

func (a *Account) GetChatGPTUserID() string {
	if !a.IsOpenAIOAuth() {
		return ""
	}
	return a.GetCredential("chatgpt_user_id")
}

func (a *Account) GetOpenAIOrganizationID() string {
	if !a.IsOpenAIOAuth() {
		return ""
	}
	return a.GetCredential("organization_id")
}

func (a *Account) GetOpenAITokenExpiresAt() *time.Time {
	if !a.IsOpenAIOAuth() {
		return nil
	}
	return a.GetCredentialAsTime("expires_at")
}

func (a *Account) IsOpenAITokenExpired() bool {
	expiresAt := a.GetOpenAITokenExpiresAt()
	if expiresAt == nil {
		return false
	}
	return time.Now().Add(60 * time.Second).After(*expiresAt)
}

// IsMixedSchedulingEnabled 检查 antigravity 账户是否启用混合调度
// 启用后可参与 anthropic/gemini 分组的账户调度
func (a *Account) IsMixedSchedulingEnabled() bool {
	if a.Platform != PlatformAntigravity {
		return false
	}
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["mixed_scheduling"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsOveragesEnabled 检查 Antigravity 账号是否启用 AI Credits 超量请求。
func (a *Account) IsOveragesEnabled() bool {
	if a.Platform != PlatformAntigravity {
		return false
	}
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["allow_overages"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsOpenAIPassthroughEnabled 返回 OpenAI 账号是否启用"自动透传（仅替换认证）"。
//
// 新字段：accounts.extra.openai_passthrough。
// 兼容字段：accounts.extra.openai_oauth_passthrough（历史 OAuth 开关）。
// 字段缺失或类型不正确时，按 false（关闭）处理。
func (a *Account) IsOpenAIPassthroughEnabled() bool {
	if a == nil || !a.IsOpenAI() || a.Extra == nil {
		return false
	}
	if enabled, ok := a.Extra["openai_passthrough"].(bool); ok {
		return enabled
	}
	if enabled, ok := a.Extra["openai_oauth_passthrough"].(bool); ok {
		return enabled
	}
	return false
}

// IsOpenAIResponsesFlattenNamespacesEnabled 返回账号级 Codex namespace 工具摊平开关。
// 字段 accounts.extra.openai_responses_flatten_namespaces 缺省为 false，即原样保留。
// 该兼容开关仅对 OpenAI OAuth 账号生效，用于仍不支持 namespace 的中转上游。
func (a *Account) IsOpenAIResponsesFlattenNamespacesEnabled() bool {
	if a == nil || !a.IsOpenAIOAuth() || a.Extra == nil {
		return false
	}
	enabled, ok := a.Extra["openai_responses_flatten_namespaces"].(bool)
	return ok && enabled
}

// IsOpenAIResponsesWebSocketV2Enabled 返回 OpenAI 账号是否开启 Responses WebSocket v2。
//
// 分类型新字段：
// - OAuth 账号：accounts.extra.openai_oauth_responses_websockets_v2_enabled
// - API Key 账号：accounts.extra.openai_apikey_responses_websockets_v2_enabled
//
// 兼容字段：
// - accounts.extra.responses_websockets_v2_enabled
// - accounts.extra.openai_ws_enabled（历史开关）
//
// 优先级：
// 1. 按账号类型读取分类型字段
// 2. 分类型字段缺失时，回退兼容字段
func (a *Account) IsOpenAIResponsesWebSocketV2Enabled() bool {
	if a == nil || !a.IsOpenAI() || a.Extra == nil {
		return false
	}
	if a.IsOpenAIOAuthLike() {
		if enabled, ok := a.Extra["openai_oauth_responses_websockets_v2_enabled"].(bool); ok {
			return enabled
		}
	}
	if a.IsOpenAIApiKey() {
		if enabled, ok := a.Extra["openai_apikey_responses_websockets_v2_enabled"].(bool); ok {
			return enabled
		}
	}
	if enabled, ok := a.Extra["responses_websockets_v2_enabled"].(bool); ok {
		return enabled
	}
	if enabled, ok := a.Extra["openai_ws_enabled"].(bool); ok {
		return enabled
	}
	return false
}

const (
	OpenAIWSIngressModeOff         = "off"
	OpenAIWSIngressModeShared      = "shared"
	OpenAIWSIngressModeDedicated   = "dedicated"
	OpenAIWSIngressModeCtxPool     = "ctx_pool"
	OpenAIWSIngressModePassthrough = "passthrough"
	OpenAIWSIngressModeHTTPBridge  = "http_bridge"
)

func normalizeOpenAIWSIngressMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case OpenAIWSIngressModeOff:
		return OpenAIWSIngressModeOff
	case OpenAIWSIngressModeCtxPool:
		return OpenAIWSIngressModeCtxPool
	case OpenAIWSIngressModePassthrough:
		return OpenAIWSIngressModePassthrough
	case OpenAIWSIngressModeHTTPBridge:
		return OpenAIWSIngressModeHTTPBridge
	case OpenAIWSIngressModeShared:
		return OpenAIWSIngressModeShared
	case OpenAIWSIngressModeDedicated:
		return OpenAIWSIngressModeDedicated
	default:
		return ""
	}
}

func normalizeOpenAIWSIngressDefaultMode(mode string) string {
	if normalized := normalizeOpenAIWSIngressMode(mode); normalized != "" {
		if normalized == OpenAIWSIngressModeShared || normalized == OpenAIWSIngressModeDedicated {
			return OpenAIWSIngressModeCtxPool
		}
		return normalized
	}
	return OpenAIWSIngressModeCtxPool
}

// ResolveOpenAIResponsesWebSocketV2Mode 返回账号在 WSv2 ingress 下的有效模式（off/ctx_pool/passthrough/http_bridge）。
//
// 优先级：
// 1. 分类型 mode 新字段（string）
// 2. 分类型 enabled 旧字段（bool）
// 3. 兼容 enabled 旧字段（bool）
// 4. defaultMode（非法时回退 ctx_pool）
func (a *Account) ResolveOpenAIResponsesWebSocketV2Mode(defaultMode string) string {
	resolvedDefault := normalizeOpenAIWSIngressDefaultMode(defaultMode)
	if a == nil || !a.IsOpenAI() {
		return OpenAIWSIngressModeOff
	}
	if a.Extra == nil {
		return resolvedDefault
	}

	resolveModeString := func(key string) (string, bool) {
		raw, ok := a.Extra[key]
		if !ok {
			return "", false
		}
		mode, ok := raw.(string)
		if !ok {
			return "", false
		}
		normalized := normalizeOpenAIWSIngressMode(mode)
		if normalized == "" {
			return "", false
		}
		return normalized, true
	}
	resolveBoolMode := func(key string) (string, bool) {
		raw, ok := a.Extra[key]
		if !ok {
			return "", false
		}
		enabled, ok := raw.(bool)
		if !ok {
			return "", false
		}
		if enabled {
			return OpenAIWSIngressModeCtxPool, true
		}
		return OpenAIWSIngressModeOff, true
	}

	if a.IsOpenAIOAuthLike() {
		if mode, ok := resolveModeString("openai_oauth_responses_websockets_v2_mode"); ok {
			return mode
		}
		if mode, ok := resolveBoolMode("openai_oauth_responses_websockets_v2_enabled"); ok {
			return mode
		}
	}
	if a.IsOpenAIApiKey() {
		if mode, ok := resolveModeString("openai_apikey_responses_websockets_v2_mode"); ok {
			return mode
		}
		if mode, ok := resolveBoolMode("openai_apikey_responses_websockets_v2_enabled"); ok {
			return mode
		}
	}
	if mode, ok := resolveBoolMode("responses_websockets_v2_enabled"); ok {
		return mode
	}
	if mode, ok := resolveBoolMode("openai_ws_enabled"); ok {
		return mode
	}
	// 兼容旧值：shared/dedicated 语义都归并到 ctx_pool。
	if resolvedDefault == OpenAIWSIngressModeShared || resolvedDefault == OpenAIWSIngressModeDedicated {
		return OpenAIWSIngressModeCtxPool
	}
	return resolvedDefault
}

// IsOpenAIWSForceHTTPEnabled 返回账号级"强制 HTTP"开关。
// 字段：accounts.extra.openai_ws_force_http。
func (a *Account) IsOpenAIWSForceHTTPEnabled() bool {
	if a == nil || !a.IsOpenAI() || a.Extra == nil {
		return false
	}
	enabled, ok := a.Extra["openai_ws_force_http"].(bool)
	return ok && enabled
}

// IsOpenAIWSAllowStoreRecoveryEnabled 返回账号级 store 恢复开关。
// 字段：accounts.extra.openai_ws_allow_store_recovery。
func (a *Account) IsOpenAIWSAllowStoreRecoveryEnabled() bool {
	if a == nil || !a.IsOpenAI() || a.Extra == nil {
		return false
	}
	enabled, ok := a.Extra["openai_ws_allow_store_recovery"].(bool)
	return ok && enabled
}

// IsOpenAIOAuthPassthroughEnabled 兼容旧接口，等价于 OAuth 账号的 IsOpenAIPassthroughEnabled。
func (a *Account) IsOpenAIOAuthPassthroughEnabled() bool {
	return a != nil && a.IsOpenAIOAuth() && a.IsOpenAIPassthroughEnabled()
}

// IsAnthropicAPIKeyPassthroughEnabled 返回 Anthropic API Key 账号是否启用"自动透传（仅替换认证）"。
// 字段：accounts.extra.anthropic_passthrough。
// 字段缺失或类型不正确时，按 false（关闭）处理。
func (a *Account) IsAnthropicAPIKeyPassthroughEnabled() bool {
	if a == nil || a.Platform != PlatformAnthropic || a.Type != AccountTypeAPIKey || a.Extra == nil {
		return false
	}
	enabled, ok := a.Extra["anthropic_passthrough"].(bool)
	return ok && enabled
}

// WebSearch 模拟三态常量
const (
	WebSearchModeDefault  = "default"  // 跟随渠道配置
	WebSearchModeEnabled  = "enabled"  // 强制开启
	WebSearchModeDisabled = "disabled" // 强制关闭
)

const (
	// OpenAIOAuthClientPolicyAny 表示 OpenAI OAuth 账号允许任意客户端访问。
	OpenAIOAuthClientPolicyAny = "any"
	// OpenAIOAuthClientPolicyCodexOnly 表示仅允许官方 Codex 客户端访问。
	OpenAIOAuthClientPolicyCodexOnly = "codex_only"
	// OpenAIOAuthClientPolicyTLSRouterMatchedOnly 表示仅允许 TLS 路由器命中的 UA 访问。
	OpenAIOAuthClientPolicyTLSRouterMatchedOnly = "tls_router_matched_only"
)

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

// IsCodexCLIOnlyEnabled 返回 OpenAI OAuth 账号是否启用"仅允许 Codex 官方客户端"。
// 新字段 openai_oauth_client_policy 优先；旧字段 accounts.extra.codex_cli_only 仅用于兼容。
func (a *Account) IsCodexCLIOnlyEnabled() bool {
	return a.GetOpenAIOAuthClientPolicy() == OpenAIOAuthClientPolicyCodexOnly
}

// GetOpenAIOAuthClientPolicy 返回 OpenAI OAuth 账号的客户端访问策略。
func (a *Account) GetOpenAIOAuthClientPolicy() string {
	if a == nil || !a.IsOpenAIOAuth() || a.Extra == nil {
		return OpenAIOAuthClientPolicyAny
	}
	if policy, ok := a.Extra["openai_oauth_client_policy"].(string); ok {
		switch strings.TrimSpace(policy) {
		case OpenAIOAuthClientPolicyCodexOnly:
			return OpenAIOAuthClientPolicyCodexOnly
		case OpenAIOAuthClientPolicyTLSRouterMatchedOnly:
			return OpenAIOAuthClientPolicyTLSRouterMatchedOnly
		case OpenAIOAuthClientPolicyAny:
			return OpenAIOAuthClientPolicyAny
		}
	}
	enabled, ok := a.Extra["codex_cli_only"].(bool)
	if ok && enabled {
		return OpenAIOAuthClientPolicyCodexOnly
	}
	return OpenAIOAuthClientPolicyAny
}

// IsOpenAIOAuthTLSRouterMatchedOnly 返回账号是否仅允许 TLS 路由器命中的客户端。
func (a *Account) IsOpenAIOAuthTLSRouterMatchedOnly() bool {
	return a.GetOpenAIOAuthClientPolicy() == OpenAIOAuthClientPolicyTLSRouterMatchedOnly
}

// GetCodexCLIOnlyAllowedClients 返回 codex_cli_only 之上额外放行的命名客户端预设 ID 列表。
// 仅 OpenAI OAuth 账号生效；缺失或类型不符时返回空。预设 ID 的具体匹配规则由
// openai 包的 registry 固化，配置只能引用预设键、不能自定义规则。
func (a *Account) GetCodexCLIOnlyAllowedClients() []string {
	if a == nil || !a.IsOpenAIOAuth() || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra["codex_cli_only_allowed_clients"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		result := make([]string, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// WindowCostSchedulability 窗口费用调度状态
type WindowCostSchedulability int

const (
	// WindowCostSchedulable 可正常调度
	WindowCostSchedulable WindowCostSchedulability = iota
	// WindowCostStickyOnly 仅允许粘性会话
	WindowCostStickyOnly
	// WindowCostNotSchedulable 完全不可调度
	WindowCostNotSchedulable
)

// IsAnthropicOAuthOrSetupToken 判断是否为 Anthropic OAuth 或 SetupToken 类型账号
// 仅这两类账号支持 5h 窗口额度控制和会话数量控制
func (a *Account) IsAnthropicOAuthOrSetupToken() bool {
	if a == nil {
		return false
	}
	return a.Platform == PlatformAnthropic && (a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken)
}

// SupportsTLSFingerprint 返回账号是否支持 TLS 指纹伪装。
// 当前支持 Anthropic OAuth/SetupToken、OpenAI OAuth 与 Qoder COSY。
func (a *Account) SupportsTLSFingerprint() bool {
	if a == nil {
		return false
	}
	if a.IsAnthropicOAuthOrSetupToken() {
		return true
	}
	return (a.Platform == PlatformOpenAI && a.Type == AccountTypeOAuth) ||
		(a.IsCNProvider() && a.Type == AccountTypeAPIKey) ||
		a.IsQoderCosy()
}

// IsTLSFingerprintEnabled 检查是否启用 TLS 指纹伪装
// 仅适用于支持 TLS 指纹伪装的账号，启用后模拟 Node.js/Claude Code/Codex CLI 客户端握手特征。
func (a *Account) IsTLSFingerprintEnabled() bool {
	if !a.SupportsTLSFingerprint() {
		return false
	}
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["enable_tls_fingerprint"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetTLSFingerprintProfileID 获取账号绑定的 TLS 指纹模板 ID
// 返回 0 表示未绑定（使用内置默认 profile）
func (a *Account) GetTLSFingerprintProfileID() int64 {
	if a.Extra == nil {
		return 0
	}
	v, ok := a.Extra["tls_fingerprint_profile_id"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case float64:
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	case json.Number:
		if i, err := id.Int64(); err == nil {
			return i
		}
	}
	return 0
}

// GetTLSFingerprintRouterID 获取账号绑定的 TLS 路由器 ID。
// 返回 0 表示未绑定路由器。
func (a *Account) GetTLSFingerprintRouterID() int64 {
	if a.Extra == nil {
		return 0
	}
	v, ok := a.Extra["tls_fingerprint_router_id"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case float64:
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	case json.Number:
		if i, err := id.Int64(); err == nil {
			return i
		}
	}
	return 0
}

// GetUserMsgQueueMode 获取用户消息队列模式
// "serialize" = 串行队列, "throttle" = 软性限速, "" = 未设置（使用全局配置）
func (a *Account) GetUserMsgQueueMode() string {
	if a.Extra == nil {
		return ""
	}
	// 优先读取新字段 user_msg_queue_mode（白名单校验，非法值视为未设置）
	if mode, ok := a.Extra["user_msg_queue_mode"].(string); ok && mode != "" {
		if mode == config.UMQModeSerialize || mode == config.UMQModeThrottle {
			return mode
		}
		return "" // 非法值 fallback 到全局配置
	}
	// 向后兼容: user_msg_queue_enabled: true → "serialize"
	if enabled, ok := a.Extra["user_msg_queue_enabled"].(bool); ok && enabled {
		return config.UMQModeSerialize
	}
	return ""
}

// IsSessionIDMaskingEnabled 检查是否启用会话ID伪装
// 仅适用于 Anthropic OAuth/SetupToken 类型账号
// 启用后将在一段时间内（15分钟）固定 metadata.user_id 中的 session ID，
// 使上游认为请求来自同一个会话
func (a *Account) IsSessionIDMaskingEnabled() bool {
	if !a.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["session_id_masking_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsCustomBaseURLEnabled 检查是否启用自定义 base URL 中继转发
// 仅适用于 Anthropic OAuth/SetupToken 类型账号
func (a *Account) IsCustomBaseURLEnabled() bool {
	if !a.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["custom_base_url_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetCustomBaseURL 返回自定义中继服务的 base URL
func (a *Account) GetCustomBaseURL() string {
	return a.GetExtraString("custom_base_url")
}

// IsCacheTTLOverrideEnabled 检查是否启用缓存 TTL 强制替换
// 仅适用于 Anthropic OAuth/SetupToken 类型账号
// 启用后将所有 cache creation tokens 归入指定的 TTL 类型（5m 或 1h）
func (a *Account) IsCacheTTLOverrideEnabled() bool {
	if !a.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra["cache_ttl_override_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetCacheTTLOverrideTarget 获取缓存 TTL 强制替换的目标类型
// 返回 "5m" 或 "1h"，默认 "5m"
func (a *Account) GetCacheTTLOverrideTarget() string {
	if a.Extra == nil {
		return "5m"
	}
	if v, ok := a.Extra["cache_ttl_override_target"]; ok {
		if target, ok := v.(string); ok && (target == "5m" || target == "1h") {
			return target
		}
	}
	return "5m"
}

// GetQuotaLimit 获取 API Key 账号的配额限制（美元）
// 返回 0 表示未启用
func (a *Account) GetQuotaLimit() float64 {
	return a.getExtraFloat64("quota_limit")
}

// GetQuotaUsed 获取 API Key 账号的已用配额（美元）
func (a *Account) GetQuotaUsed() float64 {
	return a.getExtraFloat64("quota_used")
}

// GetQuotaDailyLimit 获取日额度限制（美元），0 表示未启用
func (a *Account) GetQuotaDailyLimit() float64 {
	return a.getExtraFloat64("quota_daily_limit")
}

// GetQuotaDailyUsed 获取当日已用额度（美元）
func (a *Account) GetQuotaDailyUsed() float64 {
	return a.getExtraFloat64("quota_daily_used")
}

// GetQuotaWeeklyLimit 获取周额度限制（美元），0 表示未启用
func (a *Account) GetQuotaWeeklyLimit() float64 {
	return a.getExtraFloat64("quota_weekly_limit")
}

// GetQuotaWeeklyUsed 获取本周已用额度（美元）
func (a *Account) GetQuotaWeeklyUsed() float64 {
	return a.getExtraFloat64("quota_weekly_used")
}

// getExtraFloat64 从 Extra 中读取指定 key 的 float64 值
func (a *Account) getExtraFloat64(key string) float64 {
	if a.Extra == nil {
		return 0
	}
	if v, ok := a.Extra[key]; ok {
		return parseExtraFloat64(v)
	}
	return 0
}

// getExtraTime 从 Extra 中读取 RFC3339 时间戳
func (a *Account) getExtraTime(key string) time.Time {
	if a.Extra == nil {
		return time.Time{}
	}
	if v, ok := a.Extra[key]; ok {
		return parseExtraTime(v)
	}
	return time.Time{}
}

// getExtraBool 从 Extra 中读取指定 key 的 bool 值
func (a *Account) getExtraBool(key string) bool {
	if a.Extra == nil {
		return false
	}
	if v, ok := a.Extra[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// getExtraString 从 Extra 中读取指定 key 的字符串值
func (a *Account) getExtraString(key string) string {
	if a.Extra == nil {
		return ""
	}
	if v, ok := a.Extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getExtraStringDefault 从 Extra 中读取指定 key 的字符串值，不存在时返回 defaultVal
func (a *Account) getExtraStringDefault(key, defaultVal string) string {
	if v := a.getExtraString(key); v != "" {
		return v
	}
	return defaultVal
}

// getExtraInt 从 Extra 中读取指定 key 的 int 值
func (a *Account) getExtraInt(key string) int {
	if a.Extra == nil {
		return 0
	}
	if v, ok := a.Extra[key]; ok {
		return int(parseExtraFloat64(v))
	}
	return 0
}

// GetQuotaDailyResetMode 获取日额度重置模式："rolling"（默认）或 "fixed"
func (a *Account) GetQuotaDailyResetMode() string {
	if m := a.getExtraString("quota_daily_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaDailyResetHour 获取固定重置的小时（0-23），默认 0
func (a *Account) GetQuotaDailyResetHour() int {
	return a.getExtraInt("quota_daily_reset_hour")
}

// GetQuotaWeeklyResetMode 获取周额度重置模式："rolling"（默认）或 "fixed"
func (a *Account) GetQuotaWeeklyResetMode() string {
	if m := a.getExtraString("quota_weekly_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaWeeklyResetDay 获取固定重置的星期几（0=周日, 1=周一, ..., 6=周六），默认 1（周一）
func (a *Account) GetQuotaWeeklyResetDay() int {
	if a.Extra == nil {
		return 1
	}
	if _, ok := a.Extra["quota_weekly_reset_day"]; !ok {
		return 1
	}
	return a.getExtraInt("quota_weekly_reset_day")
}

// GetQuotaWeeklyResetHour 获取周配额固定重置的小时（0-23），默认 0
func (a *Account) GetQuotaWeeklyResetHour() int {
	return a.getExtraInt("quota_weekly_reset_hour")
}

// GetQuotaResetTimezone 获取固定重置的时区名（IANA），默认 "UTC"
func (a *Account) GetQuotaResetTimezone() string {
	if tz := a.getExtraString("quota_reset_timezone"); tz != "" {
		return tz
	}
	return "UTC"
}

// --- Quota Notification Getters ---

// QuotaNotifyConfig returns the notify configuration for a given quota dimension.
// dim must be one of quotaDimDaily, quotaDimWeekly, quotaDimTotal.
func (a *Account) QuotaNotifyConfig(dim string) (enabled bool, threshold float64, thresholdType string) {
	enabled = a.getExtraBool("quota_notify_" + dim + "_enabled")
	threshold = a.getExtraFloat64("quota_notify_" + dim + "_threshold")
	thresholdType = a.getExtraStringDefault("quota_notify_"+dim+"_threshold_type", thresholdTypeFixed)
	return
}

func (a *Account) GetQuotaNotifyDailyEnabled() bool {
	e, _, _ := a.QuotaNotifyConfig(quotaDimDaily)
	return e
}

func (a *Account) GetQuotaNotifyDailyThreshold() float64 {
	_, t, _ := a.QuotaNotifyConfig(quotaDimDaily)
	return t
}

func (a *Account) GetQuotaNotifyDailyThresholdType() string {
	_, _, tt := a.QuotaNotifyConfig(quotaDimDaily)
	return tt
}

func (a *Account) GetQuotaNotifyWeeklyEnabled() bool {
	e, _, _ := a.QuotaNotifyConfig(quotaDimWeekly)
	return e
}

func (a *Account) GetQuotaNotifyWeeklyThreshold() float64 {
	_, t, _ := a.QuotaNotifyConfig(quotaDimWeekly)
	return t
}

func (a *Account) GetQuotaNotifyWeeklyThresholdType() string {
	_, _, tt := a.QuotaNotifyConfig(quotaDimWeekly)
	return tt
}

func (a *Account) GetQuotaNotifyTotalEnabled() bool {
	e, _, _ := a.QuotaNotifyConfig(quotaDimTotal)
	return e
}

func (a *Account) GetQuotaNotifyTotalThreshold() float64 {
	_, t, _ := a.QuotaNotifyConfig(quotaDimTotal)
	return t
}

func (a *Account) GetQuotaNotifyTotalThresholdType() string {
	_, _, tt := a.QuotaNotifyConfig(quotaDimTotal)
	return tt
}

// nextFixedDailyReset 计算在 after 之后的下一个每日固定重置时间点
func nextFixedDailyReset(hour int, tz *time.Location, after time.Time) time.Time {
	t := after.In(tz)
	today := time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, tz)
	if !after.Before(today) {
		return today.AddDate(0, 0, 1)
	}
	return today
}

// lastFixedDailyReset 计算 now 之前最近一次的每日固定重置时间点
func lastFixedDailyReset(hour int, tz *time.Location, now time.Time) time.Time {
	t := now.In(tz)
	today := time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, tz)
	if now.Before(today) {
		return today.AddDate(0, 0, -1)
	}
	return today
}

// nextFixedWeeklyReset 计算在 after 之后的下一个每周固定重置时间点
// day: 0=Sunday, 1=Monday, ..., 6=Saturday
func nextFixedWeeklyReset(day, hour int, tz *time.Location, after time.Time) time.Time {
	t := after.In(tz)
	todayReset := time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, tz)
	currentDay := int(todayReset.Weekday())

	daysForward := (day - currentDay + 7) % 7
	if daysForward == 0 && !after.Before(todayReset) {
		daysForward = 7
	}
	return todayReset.AddDate(0, 0, daysForward)
}

// lastFixedWeeklyReset 计算 now 之前最近一次的每周固定重置时间点
func lastFixedWeeklyReset(day, hour int, tz *time.Location, now time.Time) time.Time {
	t := now.In(tz)
	todayReset := time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, tz)
	currentDay := int(todayReset.Weekday())

	daysBack := (currentDay - day + 7) % 7
	if daysBack == 0 && now.Before(todayReset) {
		daysBack = 7
	}
	return todayReset.AddDate(0, 0, -daysBack)
}

// isFixedDailyPeriodExpired 检查日配额是否在固定时间模式下已过期
func (a *Account) isFixedDailyPeriodExpired(periodStart time.Time) bool {
	if periodStart.IsZero() {
		return true
	}
	tz, err := time.LoadLocation(a.GetQuotaResetTimezone())
	if err != nil {
		tz = time.UTC
	}
	lastReset := lastFixedDailyReset(a.GetQuotaDailyResetHour(), tz, time.Now())
	return periodStart.Before(lastReset)
}

// isFixedWeeklyPeriodExpired 检查周配额是否在固定时间模式下已过期
func (a *Account) isFixedWeeklyPeriodExpired(periodStart time.Time) bool {
	if periodStart.IsZero() {
		return true
	}
	tz, err := time.LoadLocation(a.GetQuotaResetTimezone())
	if err != nil {
		tz = time.UTC
	}
	lastReset := lastFixedWeeklyReset(a.GetQuotaWeeklyResetDay(), a.GetQuotaWeeklyResetHour(), tz, time.Now())
	return periodStart.Before(lastReset)
}

// ComputeQuotaResetAt 根据当前配置计算并填充 extra 中的 quota_daily_reset_at / quota_weekly_reset_at
// 在保存账号配置时调用
func ComputeQuotaResetAt(extra map[string]any) {
	now := time.Now()
	tzName, _ := extra["quota_reset_timezone"].(string)
	if tzName == "" {
		tzName = "UTC"
	}
	tz, err := time.LoadLocation(tzName)
	if err != nil {
		tz = time.UTC
	}

	// 日配额固定重置时间
	if mode, _ := extra["quota_daily_reset_mode"].(string); mode == "fixed" {
		hour := int(parseExtraFloat64(extra["quota_daily_reset_hour"]))
		if hour < 0 || hour > 23 {
			hour = 0
		}
		resetAt := nextFixedDailyReset(hour, tz, now)
		extra["quota_daily_reset_at"] = resetAt.UTC().Format(time.RFC3339)
	} else {
		delete(extra, "quota_daily_reset_at")
	}

	// 周配额固定重置时间
	if mode, _ := extra["quota_weekly_reset_mode"].(string); mode == "fixed" {
		day := 1 // 默认周一
		if d, ok := extra["quota_weekly_reset_day"]; ok {
			day = int(parseExtraFloat64(d))
		}
		if day < 0 || day > 6 {
			day = 1
		}
		hour := int(parseExtraFloat64(extra["quota_weekly_reset_hour"]))
		if hour < 0 || hour > 23 {
			hour = 0
		}
		resetAt := nextFixedWeeklyReset(day, hour, tz, now)
		extra["quota_weekly_reset_at"] = resetAt.UTC().Format(time.RFC3339)
	} else {
		delete(extra, "quota_weekly_reset_at")
	}
}

// 将保留的用量窗口对齐到当前固定重置周期。
func NormalizeFixedQuotaWindows(extra map[string]any) {
	if extra == nil {
		return
	}
	now := time.Now()
	tzName, _ := extra["quota_reset_timezone"].(string)
	if tzName == "" {
		tzName = "UTC"
	}
	tz, err := time.LoadLocation(tzName)
	if err != nil {
		tz = time.UTC
	}

	if mode, _ := extra["quota_daily_reset_mode"].(string); mode == "fixed" && parseExtraFloat64(extra["quota_daily_limit"]) > 0 {
		hour := int(parseExtraFloat64(extra["quota_daily_reset_hour"]))
		if hour < 0 || hour > 23 {
			hour = 0
		}
		lastReset := lastFixedDailyReset(hour, tz, now)
		start := parseExtraTime(extra["quota_daily_start"])
		if start.IsZero() || start.Before(lastReset) {
			extra["quota_daily_used"] = 0.0
			extra["quota_daily_start"] = lastReset.UTC().Format(time.RFC3339)
		}
	}

	if mode, _ := extra["quota_weekly_reset_mode"].(string); mode == "fixed" && parseExtraFloat64(extra["quota_weekly_limit"]) > 0 {
		day := 1
		if rawDay, ok := extra["quota_weekly_reset_day"]; ok {
			day = int(parseExtraFloat64(rawDay))
		}
		if day < 0 || day > 6 {
			day = 1
		}
		hour := int(parseExtraFloat64(extra["quota_weekly_reset_hour"]))
		if hour < 0 || hour > 23 {
			hour = 0
		}
		lastReset := lastFixedWeeklyReset(day, hour, tz, now)
		start := parseExtraTime(extra["quota_weekly_start"])
		if start.IsZero() || start.Before(lastReset) {
			extra["quota_weekly_used"] = 0.0
			extra["quota_weekly_start"] = lastReset.UTC().Format(time.RFC3339)
		}
	}
}

// ValidateQuotaResetConfig 校验配额固定重置时间配置的合法性
func ValidateQuotaResetConfig(extra map[string]any) error {
	if extra == nil {
		return nil
	}
	// 校验时区
	if tz, ok := extra["quota_reset_timezone"].(string); ok && tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return errors.New("invalid quota_reset_timezone: must be a valid IANA timezone name")
		}
	}
	// 日配额重置模式
	if mode, ok := extra["quota_daily_reset_mode"].(string); ok {
		if mode != "rolling" && mode != "fixed" {
			return errors.New("quota_daily_reset_mode must be 'rolling' or 'fixed'")
		}
	}
	// 日配额重置小时
	if v, ok := extra["quota_daily_reset_hour"]; ok {
		hour := int(parseExtraFloat64(v))
		if hour < 0 || hour > 23 {
			return errors.New("quota_daily_reset_hour must be between 0 and 23")
		}
	}
	// 周配额重置模式
	if mode, ok := extra["quota_weekly_reset_mode"].(string); ok {
		if mode != "rolling" && mode != "fixed" {
			return errors.New("quota_weekly_reset_mode must be 'rolling' or 'fixed'")
		}
	}
	// 周配额重置星期几
	if v, ok := extra["quota_weekly_reset_day"]; ok {
		day := int(parseExtraFloat64(v))
		if day < 0 || day > 6 {
			return errors.New("quota_weekly_reset_day must be between 0 (Sunday) and 6 (Saturday)")
		}
	}
	// 周配额重置小时
	if v, ok := extra["quota_weekly_reset_hour"]; ok {
		hour := int(parseExtraFloat64(v))
		if hour < 0 || hour > 23 {
			return errors.New("quota_weekly_reset_hour must be between 0 and 23")
		}
	}
	return nil
}

// HasAnyQuotaLimit 检查是否配置了任一维度的配额限制
func (a *Account) HasAnyQuotaLimit() bool {
	return a.GetQuotaLimit() > 0 || a.GetQuotaDailyLimit() > 0 || a.GetQuotaWeeklyLimit() > 0
}

// isPeriodExpired 检查指定周期（自 periodStart 起经过 dur）是否已过期
func isPeriodExpired(periodStart time.Time, dur time.Duration) bool {
	if periodStart.IsZero() {
		return true // 从未使用过，视为过期（下次 increment 会初始化）
	}
	return time.Since(periodStart) >= dur
}

// IsDailyQuotaPeriodExpired 检查日配额周期是否已过期（用于显示层判断是否需要将 used 归零）
func (a *Account) IsDailyQuotaPeriodExpired() bool {
	start := a.getExtraTime("quota_daily_start")
	if a.GetQuotaDailyResetMode() == "fixed" {
		return a.isFixedDailyPeriodExpired(start)
	}
	return isPeriodExpired(start, 24*time.Hour)
}

// IsWeeklyQuotaPeriodExpired 检查周配额周期是否已过期（用于显示层判断是否需要将 used 归零）
func (a *Account) IsWeeklyQuotaPeriodExpired() bool {
	start := a.getExtraTime("quota_weekly_start")
	if a.GetQuotaWeeklyResetMode() == "fixed" {
		return a.isFixedWeeklyPeriodExpired(start)
	}
	return isPeriodExpired(start, 7*24*time.Hour)
}

// IsQuotaExceeded 检查 API Key 账号配额是否已超限（任一维度超限即返回 true）
func (a *Account) IsQuotaExceeded() bool {
	// 总额度
	if limit := a.GetQuotaLimit(); limit > 0 && a.GetQuotaUsed() >= limit {
		return true
	}
	// 日额度（周期过期视为未超限，下次 increment 会重置）
	if limit := a.GetQuotaDailyLimit(); limit > 0 {
		start := a.getExtraTime("quota_daily_start")
		var expired bool
		if a.GetQuotaDailyResetMode() == "fixed" {
			expired = a.isFixedDailyPeriodExpired(start)
		} else {
			expired = isPeriodExpired(start, 24*time.Hour)
		}
		if !expired && a.GetQuotaDailyUsed() >= limit {
			return true
		}
	}
	// 周额度
	if limit := a.GetQuotaWeeklyLimit(); limit > 0 {
		start := a.getExtraTime("quota_weekly_start")
		var expired bool
		if a.GetQuotaWeeklyResetMode() == "fixed" {
			expired = a.isFixedWeeklyPeriodExpired(start)
		} else {
			expired = isPeriodExpired(start, 7*24*time.Hour)
		}
		if !expired && a.GetQuotaWeeklyUsed() >= limit {
			return true
		}
	}
	return false
}

// GetWindowCostLimit 获取 5h 窗口费用阈值（美元）
// 返回 0 表示未启用
func (a *Account) GetWindowCostLimit() float64 {
	if a.Extra == nil {
		return 0
	}
	if v, ok := a.Extra["window_cost_limit"]; ok {
		return parseExtraFloat64(v)
	}
	return 0
}

// GetWindowCostStickyReserve 获取粘性会话预留额度（美元）
// 默认值为 10
func (a *Account) GetWindowCostStickyReserve() float64 {
	if a.Extra == nil {
		return 10.0
	}
	if v, ok := a.Extra["window_cost_sticky_reserve"]; ok {
		val := parseExtraFloat64(v)
		if val > 0 {
			return val
		}
	}
	return 10.0
}

// GetMaxSessions 获取最大并发会话数
// 返回 0 表示未启用
func (a *Account) GetMaxSessions() int {
	if a.Extra == nil {
		return 0
	}
	if v, ok := a.Extra["max_sessions"]; ok {
		return parseExtraInt(v)
	}
	return 0
}

// GetSessionIdleTimeoutMinutes 获取会话空闲超时分钟数
// 默认值为 5 分钟
func (a *Account) GetSessionIdleTimeoutMinutes() int {
	if a.Extra == nil {
		return 5
	}
	if v, ok := a.Extra["session_idle_timeout_minutes"]; ok {
		val := parseExtraInt(v)
		if val > 0 {
			return val
		}
	}
	return 5
}

// GetBaseRPM 获取基础 RPM 限制
// 返回 0 表示未启用（负数视为无效配置，按 0 处理）
func (a *Account) GetBaseRPM() int {
	if a.Extra == nil {
		return 0
	}
	if v, ok := a.Extra["base_rpm"]; ok {
		val := parseExtraInt(v)
		if val > 0 {
			return val
		}
	}
	return 0
}

// GetRPMStrategy 获取 RPM 策略
// "tiered" = 三区模型（默认）, "sticky_exempt" = 粘性豁免
func (a *Account) GetRPMStrategy() string {
	if a.Extra == nil {
		return "tiered"
	}
	if v, ok := a.Extra["rpm_strategy"]; ok {
		if s, ok := v.(string); ok && s == "sticky_exempt" {
			return "sticky_exempt"
		}
	}
	return "tiered"
}

// GetRPMStickyBuffer 获取 RPM 粘性缓冲数量
// Cache-driven: buffer = concurrency + maxSessions（覆盖幽灵窗口 + 稳态会话需求）
// floor = baseRPM / 5（向后兼容 maxSessions=0 且 concurrency=0 场景）
func (a *Account) GetRPMStickyBuffer() int {
	if a.Extra == nil {
		return 0
	}

	// 手动 override 最高优先级
	if v, ok := a.Extra["rpm_sticky_buffer"]; ok {
		val := parseExtraInt(v)
		if val > 0 {
			return val
		}
	}

	base := a.GetBaseRPM()
	if base <= 0 {
		return 0
	}

	// Cache-driven buffer = concurrency + maxSessions
	conc := a.Concurrency
	if conc < 0 {
		conc = 0
	}
	sess := a.GetMaxSessions()
	if sess < 0 {
		sess = 0
	}

	buffer := conc + sess

	// floor: 向后兼容
	floor := base / 5
	if floor < 1 {
		floor = 1
	}
	if buffer < floor {
		buffer = floor
	}

	return buffer
}

// CheckRPMSchedulability 根据当前 RPM 计数检查调度状态
// 复用 WindowCostSchedulability 三态：Schedulable / StickyOnly / NotSchedulable
func (a *Account) CheckRPMSchedulability(currentRPM int) WindowCostSchedulability {
	baseRPM := a.GetBaseRPM()
	if baseRPM <= 0 {
		return WindowCostSchedulable
	}

	if currentRPM < baseRPM {
		return WindowCostSchedulable
	}

	strategy := a.GetRPMStrategy()
	if strategy == "sticky_exempt" {
		return WindowCostStickyOnly // 粘性豁免无红区
	}

	// tiered: 黄区 + 红区
	buffer := a.GetRPMStickyBuffer()
	if currentRPM < baseRPM+buffer {
		return WindowCostStickyOnly
	}
	return WindowCostNotSchedulable
}

// CheckWindowCostSchedulability 根据当前窗口费用检查调度状态
// - 费用 < 阈值: WindowCostSchedulable（可正常调度）
// - 费用 >= 阈值 且 < 阈值+预留: WindowCostStickyOnly（仅粘性会话）
// - 费用 >= 阈值+预留: WindowCostNotSchedulable（不可调度）
func (a *Account) CheckWindowCostSchedulability(currentWindowCost float64) WindowCostSchedulability {
	limit := a.GetWindowCostLimit()
	if limit <= 0 {
		return WindowCostSchedulable
	}

	if currentWindowCost < limit {
		return WindowCostSchedulable
	}

	stickyReserve := a.GetWindowCostStickyReserve()
	if currentWindowCost < limit+stickyReserve {
		return WindowCostStickyOnly
	}

	return WindowCostNotSchedulable
}

// GetCurrentWindowStartTime 获取当前有效的窗口开始时间
// 逻辑：
// 1. 如果窗口未过期（SessionWindowEnd 存在且在当前时间之后），使用记录的 SessionWindowStart
// 2. 否则（窗口过期或未设置），使用新的预测窗口开始时间（从当前整点开始）
func (a *Account) GetCurrentWindowStartTime() time.Time {
	now := time.Now()

	// 窗口未过期，使用记录的窗口开始时间
	if a.SessionWindowStart != nil && a.SessionWindowEnd != nil && now.Before(*a.SessionWindowEnd) {
		return *a.SessionWindowStart
	}

	// 窗口已过期或未设置，预测新的窗口开始时间（从当前整点开始）
	// 与 ratelimit_service.go 中 UpdateSessionWindow 的预测逻辑保持一致
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
}

// parseExtraFloat64 从 extra 字段解析 float64 值
func parseExtraFloat64(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return 0
}

func parseExtraTime(value any) time.Time {
	if s, ok := value.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseExtraInt 从 extra 字段解析 int 值
// ParseExtraInt 从 extra 字段的 any 值解析为 int。
// 支持 int, int64, float64, json.Number, string 类型，无法解析时返回 0。
func ParseExtraInt(value any) int {
	return parseExtraInt(value)
}

func parseExtraInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

// IsShadow 报告账号是否为影子账号（parent_account_id 非空；当前唯一预设是 spark 维度）。
func (a *Account) IsShadow() bool { return a != nil && a.ParentAccountID != nil }

// IsCredentialShadow 语义别名，供「凭据消费者跳过影子」处使用（管理/后台 OAuth 路径）。
func (a *Account) IsCredentialShadow() bool { return a.IsShadow() }

// QuotaDimensionOrDefault 返回账号的用量维度，未设置时回退 "global"。
func (a *Account) QuotaDimensionOrDefault() string {
	if a == nil || strings.TrimSpace(a.QuotaDimension) == "" {
		return QuotaDimensionGlobal
	}
	return a.QuotaDimension
}

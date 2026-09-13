// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	json "encoding/json"
	errors "errors"
	maps "maps"
	strings "strings"
	sync "sync"
	time "time"
)

// GeminiQuotaService 拥有唯一策略缓存，构造不读取设置也不启动后台任务。
type GeminiQuotaService struct {
	options  GeminiQuotaOptions
	mu       sync.Mutex
	cachedAt time.Time
	policy   *GeminiQuotaPolicy
}

func NewGeminiQuotaService(options GeminiQuotaOptions) *GeminiQuotaService {
	options.StaticTiers = cloneGeminiTierOverrides(options.StaticTiers)
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	return &GeminiQuotaService{options: options}
}

type GeminiTierPolicy struct {
	Quota    GeminiQuota
	Cooldown time.Duration
}

type GeminiQuotaPolicy struct {
	tiers map[string]GeminiTierPolicy
}

const geminiQuotaCacheTTL = time.Minute

type geminiQuotaOverridesV1 struct {
	Tiers map[string]GeminiTierQuotaOverride `json:"tiers"`
}

type geminiQuotaOverridesV2 struct {
	QuotaRules map[string]GeminiQuotaRuleOverride `json:"quota_rules"`
}

type GeminiQuotaRuleOverride struct {
	SharedRPD   *int64                    `json:"shared_rpd,omitempty"`
	SharedRPM   *int64                    `json:"rpm,omitempty"`
	GeminiPro   *GeminiModelQuotaOverride `json:"gemini_pro,omitempty"`
	GeminiFlash *GeminiModelQuotaOverride `json:"gemini_flash,omitempty"`
	Desc        *string                   `json:"desc,omitempty"`
}

type GeminiModelQuotaOverride struct {
	RPD *int64 `json:"rpd,omitempty"`
	RPM *int64 `json:"rpm,omitempty"`
}

func (s *GeminiQuotaService) Policy(ctx context.Context) *GeminiQuotaPolicy {
	if s == nil {
		return NewGeminiQuotaPolicy()
	}

	now := s.options.Now()
	s.mu.Lock()
	if s.policy != nil && now.Sub(s.cachedAt) < geminiQuotaCacheTTL {
		policy := s.policy.Clone()
		s.mu.Unlock()
		return policy
	}
	s.mu.Unlock()

	policy := NewGeminiQuotaPolicy()
	{
		policy.ApplyOverrides(s.options.StaticTiers)
		if strings.TrimSpace(s.options.StaticPolicy) != "" {
			raw := []byte(s.options.StaticPolicy)
			var overridesV2 geminiQuotaOverridesV2
			if err := json.Unmarshal(raw, &overridesV2); err == nil && len(overridesV2.QuotaRules) > 0 {
				policy.ApplyQuotaRulesOverrides(overridesV2.QuotaRules)
			} else {
				var overridesV1 geminiQuotaOverridesV1
				if err := json.Unmarshal(raw, &overridesV1); err != nil {
					s.options.Log("gemini quota: parse config policy failed: %v", err)
				} else {
					policy.ApplyOverrides(overridesV1.Tiers)
				}
			}
		}
	}

	if s.options.LoadPolicy != nil {
		value, err := s.options.LoadPolicy(ctx)
		if err != nil && !errors.Is(err, s.options.NotFound) {
			s.options.Log("gemini quota: load setting failed: %v", err)
		} else if strings.TrimSpace(value) != "" {
			raw := []byte(value)
			var overridesV2 geminiQuotaOverridesV2
			if err := json.Unmarshal(raw, &overridesV2); err == nil && len(overridesV2.QuotaRules) > 0 {
				policy.ApplyQuotaRulesOverrides(overridesV2.QuotaRules)
			} else {
				var overridesV1 geminiQuotaOverridesV1
				if err := json.Unmarshal(raw, &overridesV1); err != nil {
					s.options.Log("gemini quota: parse setting failed: %v", err)
				} else {
					policy.ApplyOverrides(overridesV1.Tiers)
				}
			}
		}
	}

	s.mu.Lock()
	s.policy = policy
	s.cachedAt = now
	s.mu.Unlock()

	return policy.Clone()
}

func (s *GeminiQuotaService) QuotaForAccount(ctx context.Context, account *Record) (GeminiQuota, bool) {
	if account == nil || account.Platform != PlatformGemini {
		return GeminiQuota{}, false
	}

	// 将 oauth_type 与 tier_id 转为稳定的策略键。
	// 上游等级字符串的历史别名不会改变策略表。
	tierKey := GeminiQuotaTierKeyForAccount(account)
	if tierKey == "" {
		return GeminiQuota{}, false
	}

	policy := s.Policy(ctx)
	return policy.QuotaForTier(tierKey)
}

func (s *GeminiQuotaService) CooldownForTier(ctx context.Context, tierID string) time.Duration {
	policy := s.Policy(ctx)
	return policy.CooldownForTier(tierID)
}

func (s *GeminiQuotaService) CooldownForAccount(ctx context.Context, account *Record) time.Duration {
	if s == nil || account == nil || account.Platform != PlatformGemini {
		return 5 * time.Minute
	}
	tierKey := GeminiQuotaTierKeyForAccount(account)
	if strings.TrimSpace(tierKey) == "" {
		return 5 * time.Minute
	}
	return s.CooldownForTier(ctx, tierKey)
}

func NewGeminiQuotaPolicy() *GeminiQuotaPolicy {
	return &GeminiQuotaPolicy{
		tiers: map[string]GeminiTierPolicy{
			// AI Studio / API Key 使用分模型配额。
			// aistudio_free 免费档：
			// Pro 为 50 RPD / 2 RPM。
			// Flash 为 1500 RPD / 15 RPM。
			GeminiTierAIStudioFree: {Quota: GeminiQuota{ProRPD: 50, ProRPM: 2, FlashRPD: 1500, FlashRPM: 15}, Cooldown: 30 * time.Minute},
			// aistudio_paid 的 RPD=-1 表示不限量或按量计费。
			GeminiTierAIStudioPaid: {Quota: GeminiQuota{ProRPD: -1, ProRPM: 1000, FlashRPD: -1, FlashRPM: 2000}, Cooldown: 5 * time.Minute},

			// Google One 使用共享配额。
			GeminiTierGoogleOneFree: {Quota: GeminiQuota{SharedRPD: 1000, SharedRPM: 60}, Cooldown: 30 * time.Minute},
			GeminiTierGoogleAIPro:   {Quota: GeminiQuota{SharedRPD: 1500, SharedRPM: 120}, Cooldown: 5 * time.Minute},
			GeminiTierGoogleAIUltra: {Quota: GeminiQuota{SharedRPD: 2000, SharedRPM: 120}, Cooldown: 5 * time.Minute},

			// GCP Code Assist 使用共享配额。
			GeminiTierGCPStandard:   {Quota: GeminiQuota{SharedRPD: 1500, SharedRPM: 120}, Cooldown: 5 * time.Minute},
			GeminiTierGCPEnterprise: {Quota: GeminiQuota{SharedRPD: 2000, SharedRPM: 120}, Cooldown: 5 * time.Minute},
		},
	}
}

func (p *GeminiQuotaPolicy) ApplyOverrides(tiers map[string]GeminiTierQuotaOverride) {
	if p == nil || len(tiers) == 0 {
		return
	}
	for rawID, override := range tiers {
		tierID := NormalizeGeminiTierID(rawID)
		if tierID == "" {
			continue
		}
		policy, ok := p.tiers[tierID]
		if !ok {
			policy = GeminiTierPolicy{Cooldown: 5 * time.Minute}
		}
		// 保留旧覆盖输入的解释方式：
		// 共享档位将 pro_rpd 解释为 shared_rpd。
		// 其余档位分别覆盖各模型配额。
		if override.ProRPD != nil {
			if policy.Quota.SharedRPD > 0 {
				policy.Quota.SharedRPD = clampGeminiQuotaInt64WithUnlimited(*override.ProRPD)
			} else {
				policy.Quota.ProRPD = clampGeminiQuotaInt64WithUnlimited(*override.ProRPD)
			}
		}
		if override.FlashRPD != nil {
			if policy.Quota.SharedRPD > 0 {
				// 共享档位没有独立的 Flash RPD。
			} else {
				policy.Quota.FlashRPD = clampGeminiQuotaInt64WithUnlimited(*override.FlashRPD)
			}
		}
		if override.CooldownMinutes != nil {
			minutes := clampGeminiQuotaInt(*override.CooldownMinutes)
			policy.Cooldown = time.Duration(minutes) * time.Minute
		}
		p.tiers[tierID] = policy
	}
}

func (p *GeminiQuotaPolicy) ApplyQuotaRulesOverrides(rules map[string]GeminiQuotaRuleOverride) {
	if p == nil || len(rules) == 0 {
		return
	}
	for rawID, override := range rules {
		tierID := NormalizeGeminiTierID(rawID)
		if tierID == "" {
			continue
		}
		policy, ok := p.tiers[tierID]
		if !ok {
			policy = GeminiTierPolicy{Cooldown: 5 * time.Minute}
		}

		if override.SharedRPD != nil {
			policy.Quota.SharedRPD = clampGeminiQuotaInt64WithUnlimited(*override.SharedRPD)
		}
		if override.SharedRPM != nil {
			policy.Quota.SharedRPM = clampGeminiQuotaRPM(*override.SharedRPM)
		}
		if override.GeminiPro != nil {
			if override.GeminiPro.RPD != nil {
				policy.Quota.ProRPD = clampGeminiQuotaInt64WithUnlimited(*override.GeminiPro.RPD)
			}
			if override.GeminiPro.RPM != nil {
				policy.Quota.ProRPM = clampGeminiQuotaRPM(*override.GeminiPro.RPM)
			}
		}
		if override.GeminiFlash != nil {
			if override.GeminiFlash.RPD != nil {
				policy.Quota.FlashRPD = clampGeminiQuotaInt64WithUnlimited(*override.GeminiFlash.RPD)
			}
			if override.GeminiFlash.RPM != nil {
				policy.Quota.FlashRPM = clampGeminiQuotaRPM(*override.GeminiFlash.RPM)
			}
		}

		p.tiers[tierID] = policy
	}
}

func (p *GeminiQuotaPolicy) QuotaForTier(tierID string) (GeminiQuota, bool) {
	policy, ok := p.policyForTier(tierID)
	if !ok {
		return GeminiQuota{}, false
	}
	return policy.Quota, true
}

func (p *GeminiQuotaPolicy) CooldownForTier(tierID string) time.Duration {
	policy, ok := p.policyForTier(tierID)
	if ok && policy.Cooldown > 0 {
		return policy.Cooldown
	}
	return 5 * time.Minute
}

func (p *GeminiQuotaPolicy) policyForTier(tierID string) (GeminiTierPolicy, bool) {
	if p == nil {
		return GeminiTierPolicy{}, false
	}
	normalized := NormalizeGeminiTierID(tierID)
	if policy, ok := p.tiers[normalized]; ok {
		return policy, true
	}
	return GeminiTierPolicy{}, false
}

func NormalizeGeminiTierID(tierID string) string {
	tierID = strings.TrimSpace(tierID)
	if tierID == "" {
		return ""
	}
	// 优先使用包含历史别名的等级归一化规则。
	if canonical := CanonicalGeminiTierID(tierID); canonical != "" {
		return canonical
	}
	// 继续接受历史大写策略键。
	switch strings.ToUpper(tierID) {
	case "AISTUDIO_FREE":
		return GeminiTierAIStudioFree
	case "AISTUDIO_PAID":
		return GeminiTierAIStudioPaid
	case "GOOGLE_ONE_FREE":
		return GeminiTierGoogleOneFree
	case "GOOGLE_AI_PRO":
		return GeminiTierGoogleAIPro
	case "GOOGLE_AI_ULTRA":
		return GeminiTierGoogleAIUltra
	case "GCP_STANDARD":
		return GeminiTierGCPStandard
	case "GCP_ENTERPRISE":
		return GeminiTierGCPEnterprise
	}
	return strings.ToLower(tierID)
}

func clampGeminiQuotaInt64WithUnlimited(value int64) int64 {
	if value < -1 {
		return 0
	}
	return value
}

func clampGeminiQuotaInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func clampGeminiQuotaRPM(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func GeminiCooldownForTier(tierID string) time.Duration {
	policy := NewGeminiQuotaPolicy()
	return policy.CooldownForTier(tierID)
}

func GeminiQuotaTierKeyForAccount(account *Record) string {
	if account == nil || account.Platform != PlatformGemini {
		return ""
	}
	// 第三方提供商没有 Google 官方账号等级，不能套用本地模拟配额。
	if account.IsGeminiThirdPartyProvider() {
		return ""
	}

	// 历史记录带 project_id 时，GeminiOAuthType 仍默认返回 code_assist。
	oauthType := strings.ToLower(strings.TrimSpace(account.GeminiOAuthType()))
	rawTier := strings.TrimSpace(account.GeminiTierID())

	// 优先使用凭据中已保存的规范等级。
	if tierID := CanonicalGeminiTierIDForOAuthType(oauthType, rawTier); tierID != "" && tierID != GeminiTierGoogleOneUnknown {
		return tierID
	}

	// 等级缺失或未知时保留原默认档位。
	switch oauthType {
	case "google_one":
		return GeminiTierGoogleOneFree
	case "code_assist":
		return GeminiTierGCPStandard
	case "ai_studio":
		return GeminiTierAIStudioFree
	default:
		// API Key 的 oauth_type 为空时仍按 AI Studio 处理。
		return GeminiTierAIStudioFree
	}
}

// Clone 仅复制策略值，不创建新的服务缓存或加载状态。
func (p *GeminiQuotaPolicy) Clone() *GeminiQuotaPolicy {
	if p == nil {
		return nil
	}
	out := *p
	out.tiers = maps.Clone(p.tiers)
	return &out
}

package pricing

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

var (
	OpenAIModelDatePattern = regexp.MustCompile(`-(?:\d{8}|\d{4}-\d{2}-\d{2})$`)
	OpenAIModelBasePattern = regexp.MustCompile(`^(gpt-\d+(?:\.\d+)?)(?:-|$)`)
	// 只移除已知档位，保留版本和产品名；Spark 的价格重定向仍由专用回退处理。
	GeminiThinkingTierPattern = regexp.MustCompile(`^(gemini-\d+(?:\.\d+)?-(?:pro|flash))-(?:high|low|medium|tiered)$`)
	OpenAIThinkingTierPattern = regexp.MustCompile(`^(gpt-\d+(?:\.\d+)?(?:-(?:mini|nano|pro|sol|terra|luna|astra|codex))?)-(none|minimal|low|medium|high|xhigh|max)$`)
	// 次版本最多两位，避免把八位日期误认为版本号。
	ClaudeVersionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(claude-(?:opus|sonnet|haiku|fable)-\d+)([.-])(\d{1,2})(-.*)?$`),
		regexp.MustCompile(`^(claude-\d+)([.-])(\d{1,2})(-(?:opus|sonnet|haiku|fable)(?:-.*)?)$`),
	}
	// AboveTierPricePattern 匹配目录中的长上下文绝对价字段。
	// 服务档后缀和 cache 侧字段不参与阈值及倍率折算。
	AboveTierPricePattern = regexp.MustCompile(`^(input|output)_cost_per_token_above_(\d+)k_tokens$`)
	// CacheTierPricePattern 匹配 cache 侧长上下文绝对价字段，用于数据契约告警。
	// 组 1 为缓存基础价字段，组 2 为 1 小时缓存时长段，组 3 为服务档后缀。
	CacheTierPricePattern       = regexp.MustCompile(`^(cache_(?:creation|read)_input_token_cost)(_above_1hr)?_above_\d+k_tokens((?:_[a-z]+)?)$`)
	ClaudeOpus48FallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   5e-06,  // 每百万 token $5
		OutputCostPerToken:                  25e-06, // 每百万 token $25
		CacheCreationInputTokenCost:         6.25e-06,
		CacheCreationInputTokenCostAbove1hr: 10e-06,
		CacheReadInputTokenCost:             0.5e-06,
		LiteLLMProvider:                     "anthropic",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
		// Claude Opus 4.8 Fast mode 官方价格是常规定价的 2 倍，复用通用 service_tier 倍率即可。
		SupportsServiceTier: true,
	}
	OpenAIGPT55FallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:               5e-06,    // $5 per MTok
		InputCostPerTokenPriority:       12.5e-06, // $12.5 per MTok
		OutputCostPerToken:              3e-05,    // $30 per MTok
		OutputCostPerTokenPriority:      7.5e-05,  // $75 per MTok
		CacheCreationInputTokenCost:     5e-06,    // $5 per MTok
		CacheReadInputTokenCost:         5e-07,    // $0.5 per MTok
		CacheReadInputTokenCostPriority: 1.25e-06, // $1.25 per MTok
		SupportsServiceTier:             true,
		LiteLLMProvider:                 "openai",
		Mode:                            "chat",
		SupportsPromptCaching:           true,
	}
	// GPT-6 Astra 静态回退只固化官方标准价，避免目录缺失时误落到旧型号。
	OpenAIGPT6AstraPricing = &LiteLLMModelPricing{
		InputCostPerToken:           10e-6,   // 每百万 token $10
		OutputCostPerToken:          50e-6,   // 每百万 token $50
		CacheCreationInputTokenCost: 12.5e-6, // 每百万 token $12.50
		CacheReadInputTokenCost:     1e-6,    // 每百万 token $1
		SupportsServiceTier:         true,
		LiteLLMProvider:             "openai",
		Mode:                        "chat",
		SupportsPromptCaching:       true,
	}
	OpenAIGPT56SolPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   5e-06,   // $5 per MTok
		InputCostPerTokenPriority:           1e-05,   // $10 per MTok
		OutputCostPerToken:                  3e-05,   // $30 per MTok
		OutputCostPerTokenPriority:          6e-05,   // $60 per MTok
		CacheCreationInputTokenCost:         6.25e-6, // $6.25 per MTok
		CacheCreationInputTokenCostPriority: 1.25e-5, // $12.5 per MTok
		CacheReadInputTokenCost:             5e-07,   // $0.50 per MTok
		CacheReadInputTokenCostPriority:     1e-06,   // $1 per MTok
		SupportsServiceTier:                 true,
		LiteLLMProvider:                     "openai",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
	}
	OpenAIGPT56TerraPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   2e-06,   // 每百万 token $2
		InputCostPerTokenPriority:           4e-06,   // 每百万 token $4
		OutputCostPerToken:                  1.2e-05, // 每百万 token $12
		OutputCostPerTokenPriority:          2.4e-05, // 每百万 token $24
		CacheCreationInputTokenCost:         2.5e-6,  // 每百万 token $2.50
		CacheCreationInputTokenCostPriority: 5e-6,    // 每百万 token $5
		CacheReadInputTokenCost:             2e-07,   // 每百万 token $0.20
		CacheReadInputTokenCostPriority:     4e-07,   // 每百万 token $0.40
		SupportsServiceTier:                 true,
		LiteLLMProvider:                     "openai",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
	}
	OpenAIGPT56LunaPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   2e-07,   // 每百万 token $0.20
		InputCostPerTokenPriority:           4e-07,   // 每百万 token $0.40
		OutputCostPerToken:                  1.2e-06, // 每百万 token $1.20
		OutputCostPerTokenPriority:          2.4e-06, // 每百万 token $2.40
		CacheCreationInputTokenCost:         2.5e-7,  // 每百万 token $0.25
		CacheCreationInputTokenCostPriority: 5e-7,    // 每百万 token $0.50
		CacheReadInputTokenCost:             2e-08,   // 每百万 token $0.02
		CacheReadInputTokenCostPriority:     4e-08,   // 每百万 token $0.04
		SupportsServiceTier:                 true,
		LiteLLMProvider:                     "openai",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
	}
	OpenAIGPT55ProFallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:               3e-05,   // $30 per MTok
		InputCostPerTokenPriority:       7.5e-05, // $75 per MTok
		OutputCostPerToken:              1.8e-04, // $180 per MTok
		OutputCostPerTokenPriority:      4.5e-04, // $450 per MTok
		CacheCreationInputTokenCost:     3e-05,   // $30 per MTok
		CacheReadInputTokenCost:         3e-06,   // $3 per MTok
		CacheReadInputTokenCostPriority: 7.5e-06, // $7.5 per MTok
		SupportsServiceTier:             true,
		LiteLLMProvider:                 "openai",
		Mode:                            "responses",
		SupportsPromptCaching:           true,
	}
	OpenAIGPT54FallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:       2.5e-06, // $2.5 per MTok
		OutputCostPerToken:      1.5e-05, // $15 per MTok
		CacheReadInputTokenCost: 2.5e-07, // $0.25 per MTok
		LiteLLMProvider:         "openai",
		Mode:                    "chat",
		SupportsPromptCaching:   true,
	}
	OpenAIGPT54MiniFallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:       7.5e-07,
		OutputCostPerToken:      4.5e-06,
		CacheReadInputTokenCost: 7.5e-08,
		LiteLLMProvider:         "openai",
		Mode:                    "chat",
		SupportsPromptCaching:   true,
	}
	OpenAIGPT54NanoFallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:       2e-07,
		OutputCostPerToken:      1.25e-06,
		CacheReadInputTokenCost: 2e-08,
		LiteLLMProvider:         "openai",
		Mode:                    "chat",
		SupportsPromptCaching:   true,
	}
)

// DeriveLongContextFromAboveTierFields 将目录中的 above_XXXk 绝对价折算为本 fork
// 计费模型使用的阈值和倍率。多个阈值同时存在时取最小阈值；cache 侧 above 价由
// 计费核心按输入倍率统一处理，不在此处单独写入结构体。
func DeriveLongContextFromAboveTierFields(rawEntry json.RawMessage, pricing *LiteLLMModelPricing) {
	if pricing == nil ||
		pricing.LongContextInputTokenThreshold > 0 ||
		pricing.LongContextInputCostMultiplier > 0 ||
		pricing.LongContextOutputCostMultiplier > 0 {
		return
	}
	if !bytes.Contains(rawEntry, []byte("_above_")) {
		return
	}
	var fields map[string]any
	if err := json.Unmarshal(rawEntry, &fields); err != nil {
		return
	}
	type tierPrices struct{ input, output float64 }
	tiers := make(map[int]*tierPrices)
	for key, value := range fields {
		match := AboveTierPricePattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		price, ok := value.(float64)
		if !ok || price <= 0 {
			continue
		}
		thousands, err := strconv.Atoi(match[2])
		if err != nil || thousands <= 0 {
			continue
		}
		threshold := thousands * 1000
		tier := tiers[threshold]
		if tier == nil {
			tier = &tierPrices{}
			tiers[threshold] = tier
		}
		if match[1] == "input" {
			tier.input = price
		} else {
			tier.output = price
		}
	}
	if len(tiers) == 0 {
		return
	}
	threshold := 0
	for candidate := range tiers {
		if threshold == 0 || candidate < threshold {
			threshold = candidate
		}
	}
	tier := tiers[threshold]
	inputMultiplier, outputMultiplier := 1.0, 1.0
	if tier.input > 0 && pricing.InputCostPerToken > 0 {
		inputMultiplier = tier.input / pricing.InputCostPerToken
	}
	if tier.output > 0 && pricing.OutputCostPerToken > 0 {
		outputMultiplier = tier.output / pricing.OutputCostPerToken
	}
	// above 价格没有高于基础价时不创建阶梯，避免错误目录导致降价。
	if inputMultiplier <= 1 && outputMultiplier <= 1 {
		return
	}
	pricing.LongContextInputTokenThreshold = threshold
	pricing.LongContextInputCostMultiplier = inputMultiplier
	pricing.LongContextOutputCostMultiplier = outputMultiplier
}

// IsLopsidedLongContextLadder 判断折算后的阶梯是否只有输入或输出一侧有附加费。
func IsLopsidedLongContextLadder(pricing *LiteLLMModelPricing) bool {
	if pricing == nil || pricing.LongContextInputTokenThreshold <= 0 {
		return false
	}
	return (pricing.LongContextInputCostMultiplier > 1) != (pricing.LongContextOutputCostMultiplier > 1)
}

// OrphanCacheTierFields 找出没有可回落基础价的 cache above 字段，供加载时告警。
func OrphanCacheTierFields(rawEntry json.RawMessage) []string {
	if !bytes.Contains(rawEntry, []byte("_above_")) {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(rawEntry, &fields); err != nil {
		return nil
	}
	positive := func(key string) bool {
		price, ok := fields[key].(float64)
		return ok && price > 0
	}
	var orphans []string
	for key := range fields {
		match := CacheTierPricePattern.FindStringSubmatch(key)
		if match == nil || !positive(key) {
			continue
		}
		stem, hourly, tier := match[1], match[2], match[3]
		if positive(stem+hourly+tier) || positive(stem+hourly) || positive(stem+tier) || positive(stem) {
			continue
		}
		orphans = append(orphans, key)
	}
	sort.Strings(orphans)
	return orphans
}

// MergePricingOverrideEntry 在 JSON 字段层浅合并：patch 字段覆盖 base 同名字段，
// 值为 null 的 patch 字段从结果中删除，base 为空时结果即 patch 本身。
// patch 不是 JSON 对象时返回 ok=false。
func MergePricingOverrideEntry(base, patch json.RawMessage) (json.RawMessage, bool) {
	var patchFields map[string]any
	if err := json.Unmarshal(patch, &patchFields); err != nil || patchFields == nil {
		return nil, false
	}
	merged := make(map[string]any, len(patchFields))
	if len(base) > 0 {
		// base 非对象时忽略，仅以 patch 为准。
		if err := json.Unmarshal(base, &merged); err != nil {
			merged = make(map[string]any, len(patchFields))
		}
	}
	for k, v := range patchFields {
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, false
	}
	return out, true
}

// 模型广场可下发的模态取值白名单与固定输出顺序。
var MarketplaceModalityOrder = []string{"text", "image", "audio", "video"}

// SanitizeModalities 过滤定价文件中的非模态取值并去重，按固定顺序输出。
func SanitizeModalities(values []string) []string {
	present := make(map[string]bool, len(values))
	for _, value := range values {
		present[strings.ToLower(strings.TrimSpace(value))] = true
	}
	out := make([]string, 0, len(MarketplaceModalityOrder))
	for _, modality := range MarketplaceModalityOrder {
		if present[modality] {
			out = append(out, modality)
		}
	}
	return out
}

// DeriveModalities 从定价条目合成输入/输出模态：supported_modalities 缺失的一侧
// 用 mode 兜底，再用 supports_* 标记和图片输入价补充（图片编辑体现为图片输入价）。
func DeriveModalities(p *LiteLLMModelPricing) ([]string, []string) {
	if p == nil {
		return nil, nil
	}

	// 复制后再追加，避免并发查询时写共享底层数组（pricingData 里的切片被多个请求复用）。
	input := append([]string{}, p.SupportedModalities...)
	output := append([]string{}, p.SupportedOutputModalities...)
	if len(input) == 0 || len(output) == 0 {
		modeInput, modeOutput := ModalitiesFromMode(p.Mode)
		if len(input) == 0 {
			input = modeInput
		}
		if len(output) == 0 {
			output = modeOutput
		}
	}
	if p.SupportsVision {
		input = append(input, "image")
	}
	if p.SupportsAudioInput {
		input = append(input, "audio")
	}
	if p.SupportsAudioOutput {
		output = append(output, "audio")
	}
	if p.SupportsVideoInput {
		input = append(input, "video")
	}
	if p.InputCostPerImageToken > 0 {
		input = append(input, "image")
	}

	in := SanitizeModalities(input)
	out := SanitizeModalities(output)
	if len(in) == 0 || len(out) == 0 {
		return nil, nil
	}
	return in, out
}

// ModalitiesFromMode 按 LiteLLM mode 推断基础模态；未知 mode 一律按文字模型处理。
func ModalitiesFromMode(mode string) ([]string, []string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "image_generation":
		return []string{"text"}, []string{"image"}
	case "audio_transcription":
		return []string{"audio"}, []string{"text"}
	case "audio_speech":
		return []string{"text"}, []string{"audio"}
	case "realtime":
		return []string{"text", "audio"}, []string{"text", "audio"}
	default:
		// chat/responses/completion 等对话类模式至少支持文字输入输出。
		return []string{"text"}, []string{"text"}
	}
}

func IsClaudeOpus48Model(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || !strings.Contains(model, "opus") {
		return false
	}
	return strings.Contains(model, "4.8") || strings.Contains(model, "4-8")
}

// BuildModelLookupCandidates 为目录与价卡查价提供同一组明确身份候选，不依赖目录是否有价格。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
func BuildModelLookupCandidates(model string, grokAlias func(string) (string, bool)) []string {
	candidates := BuildModelIdentityCandidates(model)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		seen[candidate] = struct{}{}
	}
	// 别名目标再次走名称规范化；已访问集合同时阻止默认模型形成自引用或循环。
	for i := 0; i < len(candidates); i++ {
		model := NormalizeModelNameForPricing(LastSegment(candidates[i]))
		alias := NormalizeGeminiThinkingTierAlias(model)
		if alias == model {
			alias = NormalizeOpenAIThinkingTierAlias(model)
		}
		if grokAlias != nil {
			if target, ok := grokAlias(model); ok {
				alias = target
			}
		}
		if alias == model {
			continue
		}
		for _, target := range BuildModelIdentityCandidates(alias) {
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			candidates = append(candidates, target)
		}
	}
	return candidates
}

// BuildModelIdentityCandidates 只生成完整 ID 的资源路径及等价写法，不展开模型别名。
func BuildModelIdentityCandidates(model string) []string {
	modelLower := strings.ToLower(strings.TrimSpace(model))
	if modelLower == "" {
		return nil
	}
	candidates := []string{
		modelLower,
		strings.TrimPrefix(modelLower, "models/"),
		LastSegment(modelLower),
		LastSegment(strings.TrimPrefix(modelLower, "models/")),
		NormalizeModelNameForPricing(modelLower),
	}
	// 所有完整 ID 优先于等价版本写法，后者只改变版本分隔符。
	for _, candidate := range candidates {
		for _, pattern := range ClaudeVersionPatterns {
			if parts := pattern.FindStringSubmatch(candidate); len(parts) > 0 {
				separator := "."
				if parts[2] == "." {
					separator = "-"
				}
				candidates = append(candidates, parts[1]+separator+parts[3]+parts[4])
			}
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

func NormalizeModelNameForPricing(model string) string {
	// 这里只清理资源路径和名称写法，不移除档位或改成其它产品。
	model = strings.TrimSpace(model)
	model = strings.TrimLeft(model, "/")
	model = strings.TrimPrefix(model, "models/")
	model = strings.TrimPrefix(model, "publishers/google/models/")

	if idx := strings.LastIndex(model, "/publishers/google/models/"); idx != -1 {
		model = model[idx+len("/publishers/google/models/"):]
	}
	if idx := strings.LastIndex(model, "/models/"); idx != -1 {
		model = model[idx+len("/models/"):]
	}

	model = strings.TrimLeft(model, "/")
	if canonical := capability.CanonicalizeOpenAIModelAliasSpelling(model); canonical != "" {
		return canonical
	}
	return model
}

// NormalizeGeminiThinkingTierAlias 生成同版本 Pro/Flash 基名，不接受重复或未知后缀。
func NormalizeGeminiThinkingTierAlias(model string) string {
	if parts := GeminiThinkingTierPattern.FindStringSubmatch(model); len(parts) > 0 {
		return parts[1]
	}
	return model
}

// NormalizeOpenAIThinkingTierAlias 只剥离已知推理档位，保留产品名且不解析日期快照。
func NormalizeOpenAIThinkingTierAlias(model string) string {
	if parts := OpenAIThinkingTierPattern.FindStringSubmatch(model); len(parts) > 0 && parts[1] != "gpt-5.6" && capability.OpenAIModelSupportsReasoningEffort(parts[1], parts[2]) {
		return parts[1]
	}
	return model
}

func LastSegment(model string) string {
	if idx := strings.LastIndex(model, "/"); idx != -1 {
		return model[idx+1:]
	}
	return model
}

// IsNumeric 检查字符串是否为纯数字
func IsNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

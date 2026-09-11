package pricing

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// GetModelPricing 按目录、日期变体和厂商回退策略查询模型价格。
func (s *CatalogQuery) GetModelPricing(modelName string) *LiteLLMModelPricing {

	modelLower := strings.ToLower(strings.TrimSpace(modelName))
	if modelLower == "" {
		return nil
	}

	// 1. 查询目录：完整 ID、等价名称写法和明确别名，兼容模型资源路径。
	lookupCandidates := s.modelLookupCandidates(modelLower)
	if pricing := s.LookupModelCatalogEntry(lookupCandidates); pricing != nil {
		return pricing
	}
	fallbackModel := NormalizeModelNameForPricing(LastSegment(modelLower))

	// 2. 去除日期和部署版本段后，按基础名称模糊匹配。
	// claude-opus-4-5-20251101 -> claude-opus-4-5
	baseName := s.ExtractBaseName(fallbackModel)
	for key, pricing := range s.Entries {
		keyBase := s.ExtractBaseName(strings.ToLower(key))
		if keyBase == baseName {
			return pricing
		}
	}

	// 3. Claude Opus 4.8 专属静态兜底，避免误用旧 Opus 系列价格。
	for _, candidate := range lookupCandidates {
		if IsClaudeOpus48Model(candidate) {
			return ClaudeOpus48FallbackPricing
		}
	}

	// 4. 基于模型系列匹配（Claude）
	if pricing := s.MatchByModelFamily(fallbackModel); pricing != nil {
		return pricing
	}

	// 5. OpenAI 模型回退策略
	if strings.HasPrefix(fallbackModel, "gpt-") {
		return s.MatchOpenAIModel(fallbackModel)
	}

	return nil
}

// GetModelModalities 查询模型的输入/输出模态元数据（供模型广场下发能力标签）。
// 与 GetModelPricing 不同，能力数据只做精确（及别名）匹配：系列模糊回退会把旧模型
// 的能力错配给新模型，价格可以接受这种近似，能力不行。查询不到时返回 nil，
// 由展示层降级为本地规则。
func (s *CatalogQuery) GetModelModalities(modelName string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}

	modelLower := strings.ToLower(strings.TrimSpace(modelName))
	if modelLower == "" {
		return nil, nil
	}

	lookupCandidates := s.modelLookupCandidates(modelLower)
	return DeriveModalities(s.LookupModelCatalogEntry(lookupCandidates))
}

// LookupModelCatalogEntry 按候选顺序查询显式目录；调用期间目录保持只读。
func (s *CatalogQuery) LookupModelCatalogEntry(candidates []string) *LiteLLMModelPricing {
	for _, candidate := range candidates {
		if pricing := s.Entries[candidate]; pricing != nil {
			return pricing
		}
	}
	return nil
}

// extractBaseName 提取基础模型名称（去掉日期版本号）
func (s *CatalogQuery) ExtractBaseName(model string) string {
	// 移除日期后缀 (如 -20251101, -20241022)
	parts := strings.Split(model, "-")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		// 跳过看起来像日期的部分（8位数字）
		if len(part) == 8 && IsNumeric(part) {
			continue
		}
		// 跳过版本号（如 v1:0）
		if strings.Contains(part, ":") {
			continue
		}
		result = append(result, part)
	}
	return strings.Join(result, "-")
}

// matchByModelFamily 基于模型系列匹配
func (s *CatalogQuery) MatchByModelFamily(model string) *LiteLLMModelPricing {
	// modelFamily 定义一个模型系列的匹配和定价查找规则。
	type modelFamily struct {
		name    string   // 系列名称
		match   []string // 用于将模型归类到此系列的模式（strings.Contains 匹配）
		pricing []string // 用于在定价数据中查找价格的模式（nil 则复用 match；可包含低版本 fallback）
	}

	// 按特异性降序排列：高版本号在前，避免 "claude-opus-4"（opus-4 系列）
	// 因子串关系误匹配 "claude-opus-4-7"（opus-4.7 系列）。
	// 注意：原 map 实现存在 Go map 迭代随机性导致的同类 bug，此处改为有序切片修复。
	families := []modelFamily{
		{name: "opus-5", match: []string{"claude-opus-5"}, pricing: []string{"claude-opus-5", "claude-opus-4-8", "claude-opus-4.8"}},
		{name: "opus-4.7", match: []string{"claude-opus-4-7", "claude-opus-4.7"}, pricing: []string{"claude-opus-4-7", "claude-opus-4.7", "claude-opus-4-6"}},
		{name: "opus-4.6", match: []string{"claude-opus-4-6", "claude-opus-4.6"}},
		{name: "opus-4.5", match: []string{"claude-opus-4-5", "claude-opus-4.5"}},
		{name: "opus-4", match: []string{"claude-opus-4", "claude-3-opus"}},
		{name: "sonnet-4.5", match: []string{"claude-sonnet-4-5", "claude-sonnet-4.5"}},
		{name: "sonnet-4", match: []string{"claude-sonnet-4", "claude-3-5-sonnet"}},
		{name: "sonnet-3.5", match: []string{"claude-3-5-sonnet", "claude-3.5-sonnet"}},
		{name: "sonnet-3", match: []string{"claude-3-sonnet"}},
		{name: "haiku-3.5", match: []string{"claude-3-5-haiku", "claude-3.5-haiku"}},
		{name: "haiku-3", match: []string{"claude-3-haiku"}},
	}

	// Phase 1: 按有序切片归类（最具体的系列优先匹配）
	var matched *modelFamily
	for i := range families {
		for _, pattern := range families[i].match {
			if strings.Contains(model, pattern) || strings.Contains(model, strings.ReplaceAll(pattern, "-", "")) {
				matched = &families[i]
				break
			}
		}
		if matched != nil {
			break
		}
	}

	// Phase 2: 二次兜底——当模型 ID 不含已知模式串时，按关键字粗分
	if matched == nil {
		var fallbackName string
		switch {
		case strings.Contains(model, "opus"):
			switch {
			case strings.Contains(model, "opus-5") || strings.Contains(model, "opus5"):
				fallbackName = "opus-5"
			case strings.Contains(model, "4.7") || strings.Contains(model, "4-7"):
				fallbackName = "opus-4.7"
			case strings.Contains(model, "4.6") || strings.Contains(model, "4-6"):
				fallbackName = "opus-4.6"
			case strings.Contains(model, "4.5") || strings.Contains(model, "4-5"):
				fallbackName = "opus-4.5"
			default:
				fallbackName = "opus-4"
			}
		case strings.Contains(model, "sonnet"):
			switch {
			case strings.Contains(model, "4.5") || strings.Contains(model, "4-5"):
				fallbackName = "sonnet-4.5"
			case strings.Contains(model, "3-5") || strings.Contains(model, "3.5"):
				fallbackName = "sonnet-3.5"
			default:
				fallbackName = "sonnet-4"
			}
		case strings.Contains(model, "haiku"):
			switch {
			case strings.Contains(model, "3-5") || strings.Contains(model, "3.5"):
				fallbackName = "haiku-3.5"
			default:
				fallbackName = "haiku-3"
			}
		}
		if fallbackName != "" {
			for i := range families {
				if families[i].name == fallbackName {
					matched = &families[i]
					break
				}
			}
		}
	}

	if matched == nil {
		return nil
	}

	// Phase 3: 在定价数据中查找该系列的价格
	lookups := matched.pricing
	if lookups == nil {
		lookups = matched.match
	}
	for _, pattern := range lookups {
		for key, pricing := range s.Entries {
			keyLower := strings.ToLower(key)
			if strings.Contains(keyLower, pattern) {
				s.legacyf("[Pricing] Fuzzy matched %s -> %s", model, key)
				return pricing
			}
		}
	}

	return nil
}

// matchOpenAIModel OpenAI 模型回退匹配策略
// 回退顺序：
// 1. gpt-5.3-codex-spark* -> gpt-5.1-codex（按业务要求固定计费）
// 2. 同产品日期变体及已有专属静态价格；未注册的裸 GPT-5.6 不借用其它型号
// 3. 通用变体及既有跨型号回退
// 4. 最终回退到 DefaultTestModel (gpt-5.1-codex)
func (s *CatalogQuery) MatchOpenAIModel(model string) *LiteLLMModelPricing {
	if strings.HasPrefix(model, "gpt-5.3-codex-spark") {
		if pricing, ok := s.Entries["gpt-5.1-codex"]; ok {
			s.legacyf("[Pricing][SparkBilling] %s -> %s billing", model, "gpt-5.1-codex")
			s.info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, "gpt-5.1-codex"))
			return pricing
		}
	}

	// 日期快照只在价格路径回退到同产品，不给能力查询提供推断依据。
	withoutDate := OpenAIModelDatePattern.ReplaceAllString(model, "")
	if withoutDate != model {
		if pricing := s.LookupModelCatalogEntry(s.modelLookupCandidates(withoutDate)); pricing != nil {
			return pricing
		}
	}
	// 普通 GPT 的协议/推理后缀不能绕过同产品动态价；Spark 保留原有独立策略。
	sameModel := withoutDate
	if !strings.HasPrefix(model, "gpt-5.3-codex-spark") {
		sameModel = NormalizeOpenAIThinkingTierAlias(strings.TrimSuffix(withoutDate, "-openai-compact"))
		if sameModel != model {
			if pricing := s.Entries[sameModel]; pricing != nil {
				return pricing
			}
		}
	}
	// 裸 GPT-5.6 不注册为内置型号，缺少显式目录时不得借用 Sol 或默认 GPT 价格。
	if sameModel == "gpt-5.6" {
		return nil
	}
	if parts := OpenAIThinkingTierPattern.FindStringSubmatch(sameModel); len(parts) > 0 && parts[1] == "gpt-5.6" {
		return nil
	}

	// 保留专属产品的识别顺序，先查该产品动态价，再用它自己的静态价。
	var product string
	var fallback *LiteLLMModelPricing
	switch {
	case strings.HasPrefix(model, "gpt-5.5-pro"):
		product, fallback = "gpt-5.5-pro", OpenAIGPT55ProFallbackPricing
	case capability.IsOpenAIGPT6AstraModel(model):
		product, fallback = "gpt-6-astra", OpenAIGPT6AstraPricing
	case strings.HasPrefix(model, "gpt-5.6-sol"):
		product, fallback = "gpt-5.6-sol", OpenAIGPT56SolPricing
	case strings.HasPrefix(model, "gpt-5.6-terra"):
		product, fallback = "gpt-5.6-terra", OpenAIGPT56TerraPricing
	case strings.HasPrefix(model, "gpt-5.6-luna"):
		product, fallback = "gpt-5.6-luna", OpenAIGPT56LunaPricing
	case strings.HasPrefix(model, "gpt-5.5"):
		product, fallback = "gpt-5.5", OpenAIGPT55FallbackPricing
	case strings.HasPrefix(model, "gpt-5.4-mini"):
		product, fallback = "gpt-5.4-mini", OpenAIGPT54MiniFallbackPricing
	case strings.HasPrefix(model, "gpt-5.4-nano"):
		product, fallback = "gpt-5.4-nano", OpenAIGPT54NanoFallbackPricing
	case strings.HasPrefix(model, "gpt-5.4"):
		product, fallback = "gpt-5.4", OpenAIGPT54FallbackPricing
	}
	if fallback != nil {
		if pricing := s.Entries[product]; pricing != nil {
			s.info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, product))
			return pricing
		}
		s.info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s(static)", model, product))
		return fallback
	}

	// 专属价均未命中后，才继续原有通用变体与跨型号回退。
	for _, variant := range s.GenerateOpenAIModelVariants(model, OpenAIModelDatePattern) {
		if pricing, ok := s.Entries[variant]; ok {
			s.info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, variant))
			return pricing
		}
	}
	if strings.HasPrefix(model, "gpt-5.3-codex") {
		if pricing, ok := s.Entries["gpt-5.2-codex"]; ok {
			s.info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, "gpt-5.2-codex"))
			return pricing
		}
	}

	if s.isImageGenerationModel(model) {
		for _, candidate := range []string{"gpt-image-2", "gpt-image-1.5", "gpt-image-1"} {
			if pricing, ok := s.Entries[candidate]; ok {
				s.legacyf("[Pricing] OpenAI image fallback matched %s -> %s", model, candidate)
				return pricing
			}
		}
		return nil
	}

	// 最终回退到 DefaultTestModel
	defaultModel := strings.ToLower(s.DefaultOpenAIModel)
	if pricing, ok := s.Entries[defaultModel]; ok {
		s.legacyf("[Pricing] OpenAI fallback to default model %s -> %s", model, defaultModel)
		return pricing
	}

	return nil
}

// generateOpenAIModelVariants 生成 OpenAI 模型的回退变体列表
func (s *CatalogQuery) GenerateOpenAIModelVariants(model string, datePattern *regexp.Regexp) []string {
	seen := make(map[string]bool)
	var variants []string

	addVariant := func(v string) {
		if v != model && !seen[v] {
			seen[v] = true
			variants = append(variants, v)
		}
	}

	// 1. 去掉日期版本号: gpt-5.2-20251222 -> gpt-5.2
	withoutDate := datePattern.ReplaceAllString(model, "")
	if withoutDate != model {
		addVariant(withoutDate)
	}

	// 2. 提取基础版本号: gpt-5.2-codex -> gpt-5.2
	// 只匹配纯数字版本号格式 gpt-X 或 gpt-X.Y，不匹配 gpt-4o 这种带字母后缀的
	if matches := OpenAIModelBasePattern.FindStringSubmatch(model); len(matches) > 1 {
		addVariant(matches[1])
	}

	// 3. 同时去掉日期后再提取基础版本号
	if withoutDate != model {
		if matches := OpenAIModelBasePattern.FindStringSubmatch(withoutDate); len(matches) > 1 {
			addVariant(matches[1])
		}
	}

	return variants
}

// CatalogQuery 只持有调用方提供的目录和能力快照，不加载数据或维护缓存。
// 每次查询独立创建，诊断由 provider 在同一读锁范围内输出。
type CatalogQuery struct {
	Entries            map[string]*LiteLLMModelPricing
	Candidates         func(string) []string
	IsImageModel       func(string) bool
	DefaultOpenAIModel string
	Diagnostics        []CatalogDiagnostic
}

// CatalogDiagnostic 保留原日志的结构化/兼容输出类别，不依赖日志库。
type CatalogDiagnostic struct {
	Message    string
	Structured bool
}

func (s *CatalogQuery) legacyf(format string, args ...any) {
	s.Diagnostics = append(s.Diagnostics, CatalogDiagnostic{Message: fmt.Sprintf(format, args...)})
}
func (s *CatalogQuery) info(message string) {
	s.Diagnostics = append(s.Diagnostics, CatalogDiagnostic{Message: message, Structured: true})
}
func (s *CatalogQuery) modelLookupCandidates(model string) []string {
	if s.Candidates != nil {
		return s.Candidates(model)
	}
	return BuildModelLookupCandidates(model, nil)
}
func (s *CatalogQuery) isImageGenerationModel(model string) bool {
	return s.IsImageModel != nil && s.IsImageModel(model)
}

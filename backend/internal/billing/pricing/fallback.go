package pricing

import (
	"strings"
)

// getFallbackPricing 根据模型系列获取回退价格
func LookupFallbackPrice(prices map[string]*ModelPricing, model string, policy ModelPolicy) *ModelPricing {
	modelLower := strings.ToLower(model)

	// 按模型系列匹配
	// Fable 5.1 的别名必须先于 Fable 5，避免降级到旧缓存读取价。
	if strings.Contains(modelLower, "fable-5-1") || strings.Contains(modelLower, "fable-5.1") ||
		strings.Contains(modelLower, "fable5.1") || strings.Contains(modelLower, "fable51") {
		return prices["claude-fable-5-1"]
	}
	if strings.Contains(modelLower, "fable-5") || strings.Contains(modelLower, "fable5") {
		return prices["claude-fable-5"]
	}
	if strings.Contains(modelLower, "opus") {
		if strings.Contains(modelLower, "opus-5") || strings.Contains(modelLower, "opus5") {
			return prices["claude-opus-5"]
		}
		if strings.Contains(modelLower, "4.8") || strings.Contains(modelLower, "4-8") {
			return prices["claude-opus-4.8"]
		}
		if strings.Contains(modelLower, "4.7") || strings.Contains(modelLower, "4-7") {
			return prices["claude-opus-4.7"]
		}
		if strings.Contains(modelLower, "4.6") || strings.Contains(modelLower, "4-6") {
			return prices["claude-opus-4.6"]
		}
		if strings.Contains(modelLower, "4.5") || strings.Contains(modelLower, "4-5") {
			return prices["claude-opus-4.5"]
		}
		return prices["claude-3-opus"]
	}
	if strings.Contains(modelLower, "sonnet") {
		if strings.Contains(modelLower, "4") && !strings.Contains(modelLower, "3") {
			return prices["claude-sonnet-4"]
		}
		return prices["claude-3-5-sonnet"]
	}
	if strings.Contains(modelLower, "haiku") {
		if strings.Contains(modelLower, "3-5") || strings.Contains(modelLower, "3.5") {
			return prices["claude-3-5-haiku"]
		}
		return prices["claude-3-haiku"]
	}
	// Claude 未知型号统一回退到 Sonnet，避免计费中断。
	if strings.Contains(modelLower, "claude") {
		return prices["claude-sonnet-4"]
	}
	if strings.Contains(modelLower, "gemini-3.1-pro") || strings.Contains(modelLower, "gemini-3-1-pro") {
		return prices["gemini-3.1-pro"]
	}
	if strings.Contains(modelLower, "gemini-3.5-flash") || strings.Contains(modelLower, "gemini-3-5-flash") {
		return prices["gemini-3.5-flash"]
	}
	if strings.Contains(modelLower, "gemini-3.6-flash") || strings.Contains(modelLower, "gemini-3-6-flash") {
		return prices["gemini-3.6-flash"]
	}

	// DeepSeek 官方模型按专属价卡，版本化名称和其它 deepseek-* 按 Flash 价卡兜底。
	if strings.Contains(modelLower, "deepseek-v4-flash-vision-exp") {
		return prices["deepseek-v4-flash-vision-exp"]
	}
	if strings.Contains(modelLower, "deepseek-v4-flash") {
		return prices["deepseek-v4-flash"]
	}
	if strings.Contains(modelLower, "deepseek-v4-pro") {
		return prices["deepseek-v4-pro"]
	}
	if strings.HasPrefix(modelLower, "deepseek-") {
		return prices["deepseek-v4-flash"]
	}
	// 带小数点的具体型号必须先于裸 glm-5 匹配，避免被子串规则抢走。
	if strings.Contains(modelLower, "glm-5.2") {
		return prices["glm-5.2"]
	}
	if strings.Contains(modelLower, "glm-5.1") {
		return prices["glm-5.1"]
	}
	if strings.Contains(modelLower, "glm-5-turbo") || strings.Contains(modelLower, "glm-5turbo") {
		return prices["glm-5-turbo"]
	}
	if strings.Contains(modelLower, "glm-5") {
		return prices["glm-5"]
	}
	if strings.Contains(modelLower, "glm-4.7-flashx") {
		return prices["glm-4.7-flashx"]
	}
	if strings.Contains(modelLower, "glm-4.7-flash") {
		return prices["glm-4.7-flash"]
	}
	if strings.Contains(modelLower, "glm-4.7") {
		return prices["glm-4.7"]
	}
	if strings.Contains(modelLower, "glm-4.6") {
		return prices["glm-4.6"]
	}
	if strings.Contains(modelLower, "glm-4.5-flash") {
		return prices["glm-4.5-flash"]
	}
	if strings.Contains(modelLower, "glm-4.5-x") || strings.Contains(modelLower, "glm-4.5x") {
		return prices["glm-4.5-x"]
	}
	if strings.Contains(modelLower, "glm-4.5-airx") || strings.Contains(modelLower, "glm-4.5airx") {
		return prices["glm-4.5-airx"]
	}
	if strings.Contains(modelLower, "glm-4.5-air") || strings.Contains(modelLower, "glm-4.5air") {
		return prices["glm-4.5-air"]
	}
	if strings.Contains(modelLower, "glm-4.5") {
		return prices["glm-4.5"]
	}
	if strings.Contains(modelLower, "glm-4-32b") {
		return prices["glm-4-32b-0414-128k"]
	}
	if strings.Contains(modelLower, "kimi-for-coding") {
		return prices["kimi-for-coding"]
	}
	// Kimi Code 使用无厂商前缀的 bare ID；这里只做完整 ID 或路径尾段匹配，
	// 避免把客户端上下文语法 kimi-k3[1m] 和其他近似名称误计为 K3。
	if modelLower == "kimi-k3" || strings.HasSuffix(modelLower, "/kimi-k3") ||
		modelLower == "k3" || modelLower == "k3-256k" ||
		strings.HasSuffix(modelLower, "/k3") || strings.HasSuffix(modelLower, "/k3-256k") {
		return prices["kimi-k3"]
	}
	if strings.Contains(modelLower, "kimi-k2.6") || strings.Contains(modelLower, "kimi-k2-6") {
		return prices["kimi-k2.6"]
	}
	if strings.Contains(modelLower, "kimi-k2.5") || strings.Contains(modelLower, "kimi-k2-5") {
		return prices["kimi-k2.5"]
	}
	if strings.Contains(modelLower, "kimi-k2-thinking") {
		return prices["kimi-k2-thinking"]
	}
	if strings.Contains(modelLower, "kimi-k2") || strings.Contains(modelLower, "kimi/k2") {
		return prices["kimi-k2"]
	}
	if strings.Contains(modelLower, "minimax-m3") {
		return prices["minimax-m3"]
	}
	if strings.Contains(modelLower, "minimax-m2.7-highspeed") || strings.Contains(modelLower, "minimax-m2-7-highspeed") {
		return prices["minimax-m2.7-highspeed"]
	}
	if strings.Contains(modelLower, "minimax-m2.7") || strings.Contains(modelLower, "minimax-m2-7") {
		return prices["minimax-m2.7"]
	}
	if strings.Contains(modelLower, "minimax-m2.5") || strings.Contains(modelLower, "minimax-m2-5") {
		return prices["minimax-m2.5"]
	}
	if strings.Contains(modelLower, "minimax-m2.1") || strings.Contains(modelLower, "minimax-m2-1") {
		return prices["minimax-m2.1"]
	}
	if strings.Contains(modelLower, "minimax-m2") || strings.Contains(modelLower, "minimax-m-2") {
		return prices["minimax-m2"]
	}
	if strings.Contains(modelLower, "doubao-embedding-vision") {
		return prices["doubao-embedding-vision"]
	}

	// OpenAI 仅匹配已知 GPT/Codex 族，避免未知 OpenAI 型号误计价。
	if normalized := policy.NormalizedOpenAIModel; normalized != "" {
		switch normalized {
		case "gpt-6-astra":
			return prices["gpt-6-astra"]
		case "gpt-5.6-sol":
			return prices["gpt-5.6-sol"]
		case "gpt-5.6-terra":
			return prices["gpt-5.6-terra"]
		case "gpt-5.6-luna":
			return prices["gpt-5.6-luna"]
		case "gpt-5.5-pro":
			return prices["gpt-5.5-pro"]
		case "gpt-5.5":
			return prices["gpt-5.5"]
		case "gpt-5.4-mini":
			return prices["gpt-5.4-mini"]
		case "gpt-5.4-nano":
			return prices["gpt-5.4-nano"]
		case "gpt-5.4":
			return prices["gpt-5.4"]
		case "gpt-5.2":
			return prices["gpt-5.2"]
		case "gpt-5.3-codex", "gpt-5.3-codex-spark":
			return prices["gpt-5.3-codex"]
		}
	}

	switch modelLower {
	case "grok", "grok-latest", "grok-4.6", "grok-4.6-latest":
		return prices["grok-4.6"]
	case "grok-4.5", "grok-4.5-latest":
		return prices["grok-4.5"]
	case "grok-3-mini":
		return prices["grok-3-mini"]
	case "grok-3-mini-fast":
		return prices["grok-3-mini-fast"]
	case "grok-4.3":
		return prices["grok-4.3"]
	case "grok-4.20-0309-reasoning",
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-multi-agent-0309",
		"grok-4.20-reasoning",
		"grok-4.20-non-reasoning":
		return prices["grok-4.20"]
	case "grok-build", "grok-build-latest", "grok-build-0.1", "grok-composer", "grok-composer-2.5-fast", "composer-2.5":
		return prices["grok-build-0.1"]
	}

	// 未知 Grok 文本模型（如 grok-5、日期快照或带供应商前缀的名称）沿用当前默认文本价，
	// 避免新模型上线后被静默按零费用结算。
	if pricing := GrokUnknownTextFamilyFallback(prices, policy.NativeGrokModel); pricing != nil {
		return pricing
	}

	return nil
}

func GrokUnknownTextFamilyFallback(prices map[string]*ModelPricing, native string) *ModelPricing {
	if prices == nil || !IsGrokUnknownTextFamilyModel(native) {
		return nil
	}
	return prices["grok-4.6"]
}

func IsGrokUnknownTextFamilyModel(native string) bool {
	if IsGrokMediaFamilyModel(native) {
		return false
	}
	switch {
	case native == "grok", native == "grok-latest":
		return true
	case strings.HasPrefix(native, "grok-build"),
		strings.HasPrefix(native, "grok-composer"),
		strings.HasPrefix(native, "composer-"):
		return true
	case len(native) > 5 && strings.HasPrefix(native, "grok-"):
		rest := native[len("grok-"):]
		return rest[0] >= '0' && rest[0] <= '9'
	default:
		return false
	}
}

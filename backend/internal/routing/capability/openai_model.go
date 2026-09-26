package capability

import (
	"strconv"
	"strings"
)

func LastOpenAIModelSegment(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = parts[len(parts)-1]
	}
	return strings.TrimSpace(model)
}

func CanonicalizeOpenAIModelAliasSpelling(model string) string {
	model = strings.ToLower(LastOpenAIModelSegment(model))
	if model == "" {
		return ""
	}

	normalized := strings.ReplaceAll(model, "_", "-")
	normalized = strings.Join(strings.Fields(normalized), "-")
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}

	if strings.HasPrefix(normalized, "gpt5") {
		normalized = "gpt-5" + strings.TrimPrefix(normalized, "gpt5")
	}
	if !strings.HasPrefix(normalized, "gpt-") && !strings.Contains(normalized, "codex") {
		return ""
	}

	replacements := []struct {
		from string
		to   string
	}{
		{"gpt-5.6sol", "gpt-5.6-sol"},
		{"gpt-5.6terra", "gpt-5.6-terra"},
		{"gpt-5.6luna", "gpt-5.6-luna"},
		{"gpt-5.5pro", "gpt-5.5-pro"},
		{"gpt-5.4mini", "gpt-5.4-mini"},
		{"gpt-5.4nano", "gpt-5.4-nano"},
		{"gpt-5.3-codexspark", "gpt-5.3-codex-spark"},
		{"gpt-5.3codexspark", "gpt-5.3-codex-spark"},
		{"gpt-5.3codex", "gpt-5.3-codex"},
	}
	for _, replacement := range replacements {
		normalized = strings.ReplaceAll(normalized, replacement.from, replacement.to)
	}
	return normalized
}

func OpenAIModelSupportsReasoningEffort(model string, effort string) bool {
	value := strings.ToLower(strings.TrimSpace(effort))
	if value == "" {
		return false
	}
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	switch value {
	case "max":
		return OpenAIModelSupportsMaxReasoningEffort(model)
	case "ultra":
		// Ultra 不是上游 reasoning effort，任何模型都不应声明支持。
		return false
	default:
		return true
	}
}

func OpenAIModelSupportsMaxReasoningEffort(model string) bool {
	if IsOpenAIModelAtLeastVersion(model, 5, 6) {
		return true
	}

	// 国产模型的原生 max 档位与 GPT-5.6 使用同一 usage 语义。
	normalized := strings.ToLower(LastOpenAIModelSegment(model))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch {
	case strings.HasPrefix(normalized, "deepseek-v4"):
		return true
	case strings.HasPrefix(normalized, "glm-"):
		return true
	case strings.HasPrefix(normalized, "kimi-"), strings.HasPrefix(normalized, "moonshot-"):
		return true
	case normalized == "k3" || strings.HasPrefix(normalized, "k3-"):
		return true
	default:
		return false
	}
}

func IsOpenAIModelAtLeastVersion(model string, minMajor, minMinor int) bool {
	major, minor, ok := ParseOpenAIModelVersion(model)
	if !ok {
		return false
	}
	if major != minMajor {
		return major > minMajor
	}
	return minor >= minMinor
}

func ParseOpenAIModelVersion(model string) (major int, minor int, ok bool) {
	normalized := CanonicalizeOpenAIModelAliasSpelling(model)
	if normalized == "" || !strings.HasPrefix(normalized, "gpt-") {
		return 0, 0, false
	}

	rest := strings.TrimPrefix(normalized, "gpt-")
	majorEnd := 0
	for majorEnd < len(rest) && rest[majorEnd] >= '0' && rest[majorEnd] <= '9' {
		majorEnd++
	}
	if majorEnd == 0 {
		return 0, 0, false
	}

	major, err := strconv.Atoi(rest[:majorEnd])
	if err != nil {
		return 0, 0, false
	}

	minor = 0
	if majorEnd < len(rest) && rest[majorEnd] == '.' {
		minorStart := majorEnd + 1
		minorEnd := minorStart
		for minorEnd < len(rest) && rest[minorEnd] >= '0' && rest[minorEnd] <= '9' {
			minorEnd++
		}
		if minorEnd == minorStart {
			return 0, 0, false
		}
		minor, err = strconv.Atoi(rest[minorStart:minorEnd])
		if err != nil {
			return 0, 0, false
		}
	}

	return major, minor, true
}

// IsOpenAIGPT6AstraModel 判断是否 GPT-6 Astra 模型；支持带供应商前缀和版本后缀的名称。
func IsOpenAIGPT6AstraModel(model string) bool {
	normalized := CanonicalizeOpenAIModelAliasSpelling(model)
	return normalized == "gpt-6-astra" || strings.HasPrefix(normalized, "gpt-6-astra-")
}

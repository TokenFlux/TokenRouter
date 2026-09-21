package openai

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func AppendResponsesPathSuffix(baseURL, suffix string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	// 兜底：调用方漏了校验时，这里也不会把不合规的片段拼进上游 URL。
	trimmedSuffix, ok := upstream.SanitizedUpstreamPathSuffix(suffix)
	if !ok || trimmedBase == "" || trimmedSuffix == "" {
		return trimmedBase
	}
	return trimmedBase + trimmedSuffix
}

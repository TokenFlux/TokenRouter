package openai

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// IsCodexPlanGatedModelError 保留 ChatGPT OAuth 套餐拒绝目标模型的确定性错误识别。
func IsCodexPlanGatedModelError(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	normalized := upstream.NormalizeModelErrorBody(body)
	return normalized != "" && strings.Contains(normalized, "model is not supported when using codex")
}

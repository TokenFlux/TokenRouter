package provider

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// CanRetryOpenAI429 只决定原同账号恢复窗口资格；实际重试循环仍由网关拥有。
func CanRetryOpenAI429(state *account.RuntimeBlockState, value *account.Record, headers http.Header, body []byte) bool {
	if state == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() ||
		state.Blocked(value.ID, func() string { return account.RefreshCredentialIdentity(value) }) {
		return false
	}
	disposition, _ := ClassifyOpenAI429(headers, body)
	if disposition != account.OpenAI429Transient {
		return false
	}
	return state.RetryWindowActive(value.ID)
}

// ClassifyOpenAI429 先识别明确耗尽，再保持原重置头及正文回退顺序。
func ClassifyOpenAI429(headers http.Header, body []byte) (account.OpenAI429Disposition, *time.Time) {
	if kind, reset := account.OpenAIExhaustedWindow(openai.ParseCodexRateLimitHeaders(headers), time.Now); kind != account.OpenAI429Transient {
		return kind, reset
	}
	if reset := account.OpenAI429ResetTime(openai.ParseCodexRateLimitHeaders(headers), time.Now, slog.Info); reset != nil {
		return account.OpenAI429QuotaReset, reset
	}
	if unix := openai.ParseUsageLimitResetTime(body, time.Now); unix != nil {
		reset := time.Unix(*unix, 0)
		return account.OpenAI429QuotaReset, &reset
	}
	return account.OpenAI429Transient, nil
}

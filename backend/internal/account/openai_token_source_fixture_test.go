//go:build unit

package account

import (
	"context"
	"log/slog"
	"time"
)

// 原缓存断言直接构造原生令牌源，不复刻刷新或等待算法。
func newOpenAITokenSourceContract(cache AccessTokenCache) *OpenAITokenSource {
	return &OpenAITokenSource{
		Cache: cache, Metrics: &OpenAITokenMetricsStore{}, Policy: OpenAIProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn,
	}
}

type openAITokenStateWriter struct {
	RefreshRepository
	setErrorCalls int
	lastErrorMsg  string
}

func (w *openAITokenStateWriter) SetError(_ context.Context, _ int64, message string) error {
	w.setErrorCalls++
	w.lastErrorMsg = message
	return nil
}

type openAITokenBlockRecorder struct {
	accounts []*Record
	reasons  []string
}

func (r *openAITokenBlockRecorder) record(value *Record, _ time.Time, reason string) {
	r.accounts = append(r.accounts, value)
	r.reasons = append(r.reasons, reason)
}

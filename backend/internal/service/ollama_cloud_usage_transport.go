// 旧执行入口只投影受控 Cookie、观察时刻与同一 HTTP 池，S15/S16 清理。
package service

import (
	"context"
	"net/http"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/ollama"
)

func (s *OllamaCloudUsageService) FetchOllamaCloudUsage(ctx context.Context, input acctcore.OllamaUsageFetchInput) (*acctcore.OllamaUsageObservation, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	return ollama.FetchUsage(ctx, ollama.FetchInput{ObservedAt: input.ObservedAt, Cookie: input.Cookie}, ollama.FetchOptions{Do: func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, input.ProxyURL, input.AccountID, input.Concurrency)
	}, Context: WithHTTPUpstreamRedirectsDisabled, Unavailable: ErrOllamaCloudUsageUnavailable})
}

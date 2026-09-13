package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// OllamaUsageFetch 仅转交供应商 HTTP/HTML 观测，失败计数和快照仍归 account；S09 改绑。
type OllamaUsageFetch struct {
	Source *service.OllamaCloudUsageService
}

func (p OllamaUsageFetch) Fetch(ctx context.Context, input account.OllamaUsageFetchInput) (*account.OllamaUsageObservation, error) {
	return p.Source.FetchOllamaCloudUsage(ctx, input)
}

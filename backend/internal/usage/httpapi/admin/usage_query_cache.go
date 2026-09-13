package admin

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func (h *UsageHandler) getStatsCached(ctx context.Context, f usage.UsageLogFilters) (*usage.UsageStats, bool, error) {
	return h.usageService.GetStatsCached(ctx, f)
}

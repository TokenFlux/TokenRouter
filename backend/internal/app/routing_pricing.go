// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

func providePricingConfigService(repo *routingpostgres.PricingConfigStore, groups *routingpostgres.GroupStore, invalidator apikey.APIKeyAuthCacheInvalidator) *routing.PricingConfigService {
	return routing.NewPricingConfigService(repo, invalidator, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation, ReadGroup: func(ctx context.Context, id int64) (*routing.Group, error) {
		access, ok := apikey.AccessSnapshotFromContext(ctx)
		if ok {
			key := access.KeyView()
			if key != nil && key.Group != nil && key.Group.ID == id && key.Group.Hydrated {
				return key.Group, nil
			}
		}
		return groups.GetByIDLite(ctx, id)
	}})
}

func providePricingCatalog(calculator *billing.Calculator, prices *pricingprovider.PricingService) *routing.PricingCatalog {
	return &routing.PricingCatalog{Prices: calculator, NamesByProvider: prices.ListModelNamesByProvider, QoderModels: qoder.DefaultRequestModelIDs, Snapshot: func() routing.DefaultPricingSnapshot {
		snapshot := prices.ReadOnlySnapshot()
		data := snapshot.Snapshot()
		frozen := calculator.WithPriceCatalog(snapshot)
		entries := make(map[string]string)
		modes := make(map[string]string)
		for model, value := range data.Data {
			if value == nil {
				continue
			}
			platform := value.LiteLLMProvider
			switch platform {
			case "xai":
				platform = "grok"
			case "moonshot":
				platform = "kimi"
			}
			if platform == "" {
				platform = "other"
			}
			entries[model] = platform
			modes[model] = value.Mode
		}
		for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "qoder", "grok"} {
			for _, model := range provider.DefaultGroupModelCandidates(platform) {
				if _, exists := entries[model]; !exists {
					entries[model] = platform
				}
			}
		}
		for _, model := range frozen.ListSupportedModels() {
			if _, exists := entries[model]; exists {
				continue
			}
			platform := ""
			switch {
			case strings.HasPrefix(model, "claude"):
				platform = "anthropic"
			case strings.HasPrefix(model, "gpt"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
				platform = "openai"
			case strings.HasPrefix(model, "gemini"):
				platform = "gemini"
			case strings.HasPrefix(model, "grok"):
				platform = "grok"
			case strings.HasPrefix(model, "deepseek"):
				platform = "deepseek"
			case strings.HasPrefix(model, "glm"):
				platform = "zhipu"
			case strings.HasPrefix(model, "kimi"), strings.HasPrefix(model, "moonshot"):
				platform = "kimi"
			default:
				platform = "other"
			}
			entries[model] = platform
		}
		names := make([]string, 0, len(entries))
		for model := range entries {
			names = append(names, model)
		}
		sort.Strings(names)
		result := routing.DefaultPricingSnapshot{UpdatedAt: data.LastUpdated}
		for _, model := range names {
			mode := modes[model]
			if strings.Contains(model, "grok-imagine-video") {
				mode = "video"
			}
			result.Prices = append(result.Prices, frozen.DefaultModelPrice(model, entries[model], mode))
		}
		return result
	}}
}

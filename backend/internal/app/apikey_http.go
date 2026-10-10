package app

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

// provideKeyHTTP 为 Key HTTP 入口绑定业务用例和容量展示数据。
func provideKeyHTTP(keys *apikey.APIKeyService, capacity *routing.CapacityService, catalogue *routing.RequestableCatalogue, attributes *routing.ModelAttributeService) *keyhttp.APIKeyHandler[dto.Group] {
	handler := keyhttp.NewAPIKeyHandler(keys, func(group *routing.Group, summary *accessview.GroupCapacitySummary) *dto.Group {
		result := dto.GroupFromRouting(apikey.RoutingGroup(group))
		if result != nil && summary != nil {
			result.Capacity = dto.GroupCapacityFromSummary(summary)
		}
		return result
	})
	handler.SetGroupCapacityService(capacity)
	handler.SetGroupPresentation(func(ctx context.Context, group *routing.Group, summary *accessview.GroupCapacitySummary, includeModels bool) *dto.Group {
		result := dto.GroupFromRouting(apikey.RoutingGroup(group), locale.FromContext(ctx))
		if summary != nil {
			result.Capacity = dto.GroupCapacityFromSummary(summary)
		}
		if !includeModels {
			return result
		}
		resolved := catalogue.ResolveRequestableModels(ctx, &group.ID, "")
		result.Models = make([]string, 0, len(resolved.Models))
		result.ModelProtocols = make(map[string][]protocol.ProtocolID)
		for _, model := range resolved.Models {
			result.Models = append(result.Models, model.ID)
			result.ModelProtocols[model.ID] = model.Protocols
		}
		var err error
		result.ModelAttributes, err = attributes.ResolveModels(ctx, group.ID, resolved.Models)
		if err != nil {
			slog.Warn("failed to read group model attributes", "group_id", group.ID, "error", err)
		}
		return result
	})
	return handler
}

// provideKeyAdminHTTP 为管理员 HTTP 入口注入 Key 管理用例。
func provideKeyAdminHTTP(keys *apikey.Admin) *keyhttp.AdminAPIKeyHandler[dto.Group] {
	return keyhttp.NewAdminAPIKeyHandler(keys, func(group *routing.Group) *dto.Group {
		return dto.GroupFromRouting(apikey.RoutingGroup(group))
	})
}

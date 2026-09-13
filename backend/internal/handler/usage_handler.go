// 旧用户用量构造仅投影依赖，HTTP 实现由 usage/httpapi 拥有。
package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	native "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

type UsageHandler = native.UsageHandler

func NewUsageHandler(s *service.UsageService, keys *service.APIKeyService, errors *service.OpsService, settings *service.SettingService) *UsageHandler {
	var core *usage.UsageService
	if s != nil {
		core = s.UsageService
	}
	var e ports.UserErrors
	if errors != nil {
		e = errors
	}
	var config ports.Settings
	if settings != nil {
		config = settings
	}
	return native.NewUsageHandler(core, legacyUsageKeys(keys), e, config)
}
func legacyUsageKeys(keys *service.APIKeyService) ports.KeyReader {
	if keys == nil {
		return nil
	}
	return ports.KeyQueries{Lookup: func(ctx context.Context, id int64) (*ports.KeyReference, error) {
		v, e := keys.GetByID(ctx, id)
		if v == nil {
			return nil, e
		}
		return &ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}, e
	}, Ownership: keys.VerifyOwnership, Search: func(ctx context.Context, id int64, q string, n int) ([]ports.KeyReference, error) {
		rows, e := keys.SearchAPIKeys(ctx, id, q, n)
		if e != nil {
			return nil, e
		}
		out := make([]ports.KeyReference, len(rows))
		for i, v := range rows {
			out[i] = ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}
		}
		return out, nil
	}}
}

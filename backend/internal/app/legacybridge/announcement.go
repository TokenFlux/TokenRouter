package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// AnnouncementUsers 在 S05 的 identity 迁移后退出。
type AnnouncementUsers struct{ Repository service.UserRepository }

func (a *AnnouncementUsers) GetByID(ctx context.Context, id int64) (*site.UserSnapshot, error) {
	user, err := a.Repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}
	return &site.UserSnapshot{ID: user.ID, Email: user.Email, Username: user.Username, Balance: user.Balance}, nil
}
func (a *AnnouncementUsers) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters site.UserListFilters) ([]site.UserSnapshot, *pagination.PaginationResult, error) {
	users, page, err := a.Repository.ListWithFilters(ctx, params, service.UserListFilters{Search: filters.Search})
	if err != nil {
		return nil, page, err
	}
	out := make([]site.UserSnapshot, len(users))
	for i, user := range users {
		out[i] = site.UserSnapshot{ID: user.ID, Email: user.Email, Username: user.Username, Balance: user.Balance}
	}
	return out, page, nil
}

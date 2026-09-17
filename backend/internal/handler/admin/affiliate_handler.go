// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package admin

import (
	"context"

	native "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type AffiliateHandler = native.AffiliateHandler

type UpdateAffiliateUserRequest = native.UpdateAffiliateUserRequest
type BatchSetRateRequest = native.BatchSetRateRequest
type AffiliateUserSummary = native.AffiliateUserSummary

// NewAffiliateHandler 为旧调用者投影用户查找端口，生产构造由 app 直接完成。
func NewAffiliateHandler(affiliate *service.AffiliateService, adminService service.AdminService) *AffiliateHandler {
	return native.NewAffiliateHandler(affiliate, func(ctx context.Context, keyword string) ([]native.AffiliateUserSummary, error) {
		users, _, err := adminService.ListUsers(ctx, 1, 20, service.UserListFilters{Search: keyword}, "email", "asc")
		if err != nil {
			return nil, err
		}
		out := make([]native.AffiliateUserSummary, len(users))
		for i, u := range users {
			out[i] = native.AffiliateUserSummary{ID: u.ID, Email: u.Email, Username: u.Username}
		}
		return out, nil
	})
}

// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	"context"
	"fmt"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// AdminUpdateAPIKeyGroupIDResult is the result of AdminUpdateAPIKeyGroupID.
type AdminUpdateAPIKeyGroupIDResult struct {
	APIKey                 *APIKey
	AutoGrantedGroupAccess bool   // true if a new exclusive group permission was auto-added
	GrantedGroupID         *int64 // the group ID that was auto-granted
	GrantedGroupName       string // the group name that was auto-granted
}

// Admin 管理 Key 的分组绑定与消费重置意图，不接管用户或分组规则。
type Admin struct {
	Keys        APIKeyRepository
	Users       UserRepository
	Groups      GroupRepository
	Mutations   GroupGrantMutations
	Invalidator interface{ InvalidateAuthCacheByKey(context.Context, string) }
	RateLimits  interface {
		InvalidateAPIKeyRateLimit(context.Context, int64) error
	}
}
type GroupGrantMutations interface {
	GrantGroupAndUpdateFields(context.Context, *APIKey, APIKeyUpdateFields, int64) error
}

// AdminUpdateAPIKeyGroupID 管理员修改 API Key 分组绑定
// groupID: nil=不修改, 指向0=解绑, 指向正整数=绑定到目标分组
// AdminUpdateAPIKeyGroupID 保留原管理入口，分组校验和写入委托统一用例。
func (s *Admin) AdminUpdateAPIKeyGroupID(ctx context.Context, id int64, gid *int64) (*AdminUpdateAPIKeyGroupIDResult, error) {
	return s.updateManagedFields(ctx, id, gid, false, true)
}

// AdminResetAPIKeyRateLimitUsage 保留内部单独重置入口的复合 Key 支持。
func (s *Admin) AdminResetAPIKeyRateLimitUsage(ctx context.Context, id int64) (*APIKey, error) {
	v, e := s.updateManagedFields(ctx, id, nil, true, false)
	if v == nil {
		return nil, e
	}
	return v.APIKey, e
}

// UpdateManagedFields 保证同一个 HTTP 请求的配置与消费重置一起提交。
func (s *Admin) UpdateManagedFields(ctx context.Context, id int64, gid *int64, reset bool) (*AdminUpdateAPIKeyGroupIDResult, error) {
	return s.updateManagedFields(ctx, id, gid, reset, true)
}
func (s *Admin) updateManagedFields(ctx context.Context, id int64, gid *int64, reset, checkComposite bool) (*AdminUpdateAPIKeyGroupIDResult, error) {
	key, err := s.Keys.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if checkComposite && key.IsComposite {
		return nil, ErrCompositeKeyGroupConflict
	}
	out := &AdminUpdateAPIKeyGroupIDResult{APIKey: key}
	var fields APIKeyUpdateFields
	var grant *int64
	if gid != nil {
		if *gid < 0 {
			return nil, infraerrors.BadRequest("INVALID_GROUP_ID", "group_id must be non-negative")
		}
		fields.GroupID = true
		if *gid == 0 {
			key.GroupID = nil
			key.Group = nil
		} else {
			group, err := s.Groups.GetByID(ctx, *gid)
			if err != nil {
				return nil, err
			}
			if group.Status != StatusActive {
				return nil, infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
			}
			if !group.IsExclusive {
				if s.Users == nil {
					return nil, infraerrors.InternalServer("USER_REPOSITORY_UNAVAILABLE", "user repository unavailable")
				}
				user, err := s.Users.GetByID(ctx, key.UserID)
				if err != nil {
					return nil, fmt.Errorf("get user: %w", err)
				}
				if !user.CanBindGroup(group.ID, group.IsExclusive) {
					return nil, ErrGroupNotAllowed
				}
			}
			value := *gid
			key.GroupID = &value
			key.Group = group
			if group.IsExclusive {
				grant = &value
				out.AutoGrantedGroupAccess = true
				out.GrantedGroupID = &value
				out.GrantedGroupName = group.Name
			}
		}
	}
	if reset {
		fields.RateLimitUsage = true
		key.Usage5h = 0
		key.Usage1d = 0
		key.Usage7d = 0
		key.Window5hStart = nil
		key.Window1dStart = nil
		key.Window7dStart = nil
	}
	if !fields.GroupID && !fields.RateLimitUsage {
		return out, nil
	}
	if grant != nil {
		if err := s.Mutations.GrantGroupAndUpdateFields(ctx, key, fields, *grant); err != nil {
			return nil, err
		}
	} else if err := s.Keys.Update(ctx, key, fields); err != nil {
		if reset && gid == nil {
			return nil, fmt.Errorf("reset api key rate limit usage: %w", err)
		}
		return nil, fmt.Errorf("update api key: %w", err)
	}
	// 所有成功副作用都在唯一写入/事务提交后执行；数据库触发器继续负责 outbox。
	if s.Invalidator != nil {
		s.Invalidator.InvalidateAuthCacheByKey(ctx, key.Key)
	}
	if reset && s.RateLimits != nil {
		_ = s.RateLimits.InvalidateAPIKeyRateLimit(ctx, key.ID)
	}
	return out, nil
}
func (s *Admin) GetUserAPIKeys(ctx context.Context, userID int64, page, pageSize int, sortBy, sortOrder string) ([]APIKey, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	keys, result, err := s.Keys.ListByUserID(ctx, userID, params, APIKeyListFilters{})
	if err != nil {
		return nil, 0, err
	}
	return keys, result.Total, nil
}

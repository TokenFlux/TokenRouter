package httpapi

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

func (s *adminUpdateFixture) AdminResetAPIKeyRateLimitUsage(ctx context.Context, keyID int64) (*apikey.APIKey, error) {
	for i := range s.apiKeys {
		if s.apiKeys[i].ID == keyID {
			s.apiKeys[i].Usage5h = 0
			s.apiKeys[i].Usage1d = 0
			s.apiKeys[i].Usage7d = 0
			s.apiKeys[i].Window5hStart = nil
			s.apiKeys[i].Window1dStart = nil
			s.apiKeys[i].Window7dStart = nil
			k := s.apiKeys[i]
			return &k, nil
		}
	}
	return nil, apikey.ErrAPIKeyNotFound
}

func (s *adminUpdateFixture) AdminUpdateAPIKeyGroupID(ctx context.Context, keyID int64, groupID *int64) (*apikey.AdminUpdateAPIKeyGroupIDResult, error) {
	for i := range s.apiKeys {
		if s.apiKeys[i].ID == keyID {
			k := s.apiKeys[i]
			if groupID != nil {
				if *groupID == 0 {
					k.GroupID = nil
				} else {
					gid := *groupID
					k.GroupID = &gid
				}
			}
			return &apikey.AdminUpdateAPIKeyGroupIDResult{APIKey: &k}, nil
		}
	}
	return nil, apikey.ErrAPIKeyNotFound
}

// AdminUpdateAPIKeyFields 模拟原子管理用例返回；此替身不作为事务正确性证据。
func (s *adminUpdateFixture) UpdateManagedFields(ctx context.Context, id int64, gid *int64, reset bool) (*apikey.AdminUpdateAPIKeyGroupIDResult, error) {
	result, err := s.AdminUpdateAPIKeyGroupID(ctx, id, gid)
	if err != nil {
		return nil, err
	}
	if reset {
		key, err := s.AdminResetAPIKeyRateLimitUsage(ctx, id)
		if err != nil {
			return nil, err
		}
		result.APIKey = key
	}
	return result, nil
}

// adminUpdateFixture 仅模拟 Key 管理返回，事务保证由真实 PostgreSQL 断言验证。
type adminUpdateFixture struct{ apiKeys []apikey.APIKey }

func newAdminUpdateFixture() *adminUpdateFixture {
	now := time.Now().UTC()
	return &adminUpdateFixture{apiKeys: []apikey.APIKey{{ID: 10, UserID: 1, Key: "sk-test", Name: "test", Status: apikey.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

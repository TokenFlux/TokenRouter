package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"time"
)

func (s *adminUserFixture) ListUsers(ctx context.Context, page, pageSize int, filters identity.UserListFilters, sortBy, sortOrder string) ([]identity.User, int64, error) {
	s.lastListUsers.page = page
	s.lastListUsers.pageSize = pageSize
	s.lastListUsers.filters = filters
	s.lastListUsers.sortBy = sortBy
	s.lastListUsers.sortOrder = sortOrder
	s.lastListUsers.calls++
	return s.users, int64(len(s.users)), nil
}

func (s *adminUserFixture) GetUser(ctx context.Context, id int64) (*identity.User, error) {
	if s.getUserErr != nil {
		return nil, s.getUserErr
	}
	for i := range s.users {
		if s.users[i].ID == id {
			return &s.users[i], nil
		}
	}
	user := identity.User{ID: id, Email: "user@example.com", Status: billing.StatusActive}
	return &user, nil
}

func (s *adminUserFixture) GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetUser(ctx, id)
}

func (s *adminUserFixture) CreateUser(ctx context.Context, input *identity.CreateUserInput) (*identity.User, error) {
	user := identity.User{ID: 100, Email: input.Email, Status: billing.StatusActive}
	return &user, nil
}

func (s *adminUserFixture) UpdateUser(ctx context.Context, id int64, input *identity.UpdateUserInput) (*identity.User, error) {
	user := identity.User{ID: id, Email: "updated@example.com", Status: billing.StatusActive}
	return &user, nil
}

func (s *adminUserFixture) DeleteUser(ctx context.Context, id int64) error {
	return nil
}

func (s *adminUserFixture) UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*identity.User, error) {
	user := identity.User{ID: userID, Balance: balance, Status: billing.StatusActive}
	return &user, nil
}

func (s *adminUserFixture) GetUserAPIKeys(ctx context.Context, userID int64, page, pageSize int, sortBy, sortOrder string) ([]apikey.APIKey, int64, error) {
	return s.apiKeys, int64(len(s.apiKeys)), nil
}

func (s *adminUserFixture) GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error) {
	return map[string]any{"user_id": userID}, nil
}

func (s *adminUserFixture) GetUserRPMStatus(ctx context.Context, userID int64) (*identity.UserRPMStatus, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &identity.UserRPMStatus{
		UserRPMUsed:  0,
		UserRPMLimit: user.RPMLimit,
	}, nil
}

func (s *adminUserFixture) BindUserAuthIdentity(ctx context.Context, userID int64, input identity.AdminBindAuthIdentityInput) (*identity.AdminBoundAuthIdentity, error) {
	s.boundAuthIdentityFor = userID
	copied := input
	if input.Metadata != nil {
		copied.Metadata = map[string]any{}
		for key, value := range input.Metadata {
			copied.Metadata[key] = value
		}
	}
	if input.Channel != nil {
		channel := *input.Channel
		if input.Channel.Metadata != nil {
			channel.Metadata = map[string]any{}
			for key, value := range input.Channel.Metadata {
				channel.Metadata[key] = value
			}
		}
		copied.Channel = &channel
	}
	s.boundAuthIdentity = &copied

	now := time.Now().UTC()
	result := &identity.AdminBoundAuthIdentity{
		UserID:          userID,
		ProviderType:    input.ProviderType,
		ProviderKey:     input.ProviderKey,
		ProviderSubject: input.ProviderSubject,
		VerifiedAt:      &now,
		Issuer:          input.Issuer,
		Metadata:        input.Metadata,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if input.Channel != nil {
		result.Channel = &identity.AdminBoundAuthIdentityChannel{
			Channel:        input.Channel.Channel,
			ChannelAppID:   input.Channel.ChannelAppID,
			ChannelSubject: input.Channel.ChannelSubject,
			Metadata:       input.Channel.Metadata,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return result, nil
}

// adminUserFixture 只提供身份管理端口与只读 Key 列表，其他领域不在夹具内重建。
type adminUserFixture struct {
	UserAdministration
	users                []identity.User
	apiKeys              []apikey.APIKey
	getUserErr           error
	boundAuthIdentityFor int64
	boundAuthIdentity    *identity.AdminBindAuthIdentityInput
	lastListUsers        struct {
		page, pageSize, calls int
		filters               identity.UserListFilters
		sortBy, sortOrder     string
	}
}

func newAdminUserFixture() *adminUserFixture {
	now := time.Now().UTC()
	return &adminUserFixture{users: []identity.User{{ID: 1, Email: "user@example.com", Role: identity.RoleUser, Status: identity.StatusActive, CreatedAt: now, UpdatedAt: now}}, apiKeys: []apikey.APIKey{{ID: 10, UserID: 1, Key: "sk-test", Name: "test", Status: apikey.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

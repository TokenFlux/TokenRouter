package httpapi

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// adminUserStub 只实现本组 HTTP 测试实际调用的身份端口。
type adminUserStub struct {
	UserAdministration
	users         []identity.User
	lastListUsers struct {
		page, pageSize, calls int
		filters               identity.UserListFilters
		sortBy, sortOrder     string
	}
}

func newAdminUserStub() *adminUserStub {
	now := time.Now().UTC()
	return &adminUserStub{users: []identity.User{{ID: 1, Email: "user@example.com", Role: identity.RoleUser, Status: identity.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

func (s *adminUserStub) ListUsers(_ context.Context, page, size int, filters identity.UserListFilters, sortBy, order string) ([]identity.User, int64, error) {
	s.lastListUsers.page, s.lastListUsers.pageSize = page, size
	s.lastListUsers.filters, s.lastListUsers.sortBy, s.lastListUsers.sortOrder = filters, sortBy, order
	s.lastListUsers.calls++
	return s.users, int64(len(s.users)), nil
}

func (s *adminUserStub) GetUser(_ context.Context, id int64) (*identity.User, error) {
	for i := range s.users {
		if s.users[i].ID == id {
			return &s.users[i], nil
		}
	}
	return &identity.User{ID: id, Email: "user@example.com", Status: identity.StatusActive}, nil
}

func (s *adminUserStub) GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetUser(ctx, id)
}

func (s *adminUserStub) CreateUser(_ context.Context, input *identity.CreateUserInput) (*identity.User, error) {
	return &identity.User{ID: 100, Email: input.Email, Status: identity.StatusActive}, nil
}

func (s *adminUserStub) UpdateUser(_ context.Context, id int64, _ *identity.UpdateUserInput) (*identity.User, error) {
	return &identity.User{ID: id, Email: "updated@example.com", Status: identity.StatusActive}, nil
}

// newAdminUserTestHandler 保留原缺少认证主体时的 step-up 拒绝路径。
func newAdminUserTestHandler(users UserAdministration) *AdminUserHandler[struct{}] {
	return NewAdminUserHandler[struct{}](users, nil, nil, func(c *gin.Context) bool {
		return EnforceStepUp(c, nil, nil, nil)
	})
}

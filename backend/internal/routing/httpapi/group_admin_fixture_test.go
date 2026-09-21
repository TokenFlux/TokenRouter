package httpapi

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	groupdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/gin-gonic/gin"
)

// groupAdminFixture 仅提供路由管理 HTTP 所需的数据，不聚合用户与账号管理。
type groupAdminFixture struct {
	GroupAdministration
	groups        []routing.Group
	getGroupCalls int
}

func setupGroupAdminContractRouter() (*gin.Engine, *groupAdminFixture) {
	now := time.Now().UTC()
	source := &groupAdminFixture{groups: []routing.Group{{ID: 2, Name: "group", Platform: capability.PlatformAnthropic, Status: billing.StatusActive, CreatedAt: now, UpdatedAt: now}}}
	key := &apikey.APIKey{ID: 10, UserID: 1, Key: "sk-test", Name: "test", Status: billing.StatusActive, CreatedAt: now, UpdatedAt: now}
	handler := NewGroupHandler(source, GroupResources{Keys: func(context.Context, int64, int, int) ([]keydto.APIKey[groupdto.Group], int64, error) {
		return []keydto.APIKey[groupdto.Group]{*keydto.APIKeyFromKey(key, func(g *routing.Group) *groupdto.Group { return groupdto.GroupFromRouting(apikey.RoutingGroup(g)) })}, 1, nil
	}})
	router := gin.New()
	router.GET("/api/v1/admin/groups", handler.List)
	router.GET("/api/v1/admin/groups/all", handler.GetAll)
	router.GET("/api/v1/admin/groups/:id/models-list-candidates", handler.GetModelsListCandidates)
	router.GET("/api/v1/admin/groups/:id", handler.GetByID)
	router.POST("/api/v1/admin/groups", handler.Create)
	router.PUT("/api/v1/admin/groups/:id", handler.Update)
	router.DELETE("/api/v1/admin/groups/:id", handler.Delete)
	router.GET("/api/v1/admin/groups/:id/stats", handler.GetStats)
	router.GET("/api/v1/admin/groups/:id/api-keys", handler.GetGroupAPIKeys)
	return router, source
}
func (s *groupAdminFixture) ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]routing.Group, int64, error) {
	return s.groups, int64(len(s.groups)), nil
}

func (s *groupAdminFixture) GetAllGroups(ctx context.Context) ([]routing.Group, error) {
	return s.groups, nil
}

func (s *groupAdminFixture) GetAllGroupsByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	return s.groups, nil
}

func (s *groupAdminFixture) GetAllGroupsIncludingInactive(ctx context.Context) ([]routing.Group, error) {
	return s.groups, nil
}

func (s *groupAdminFixture) GetGroup(ctx context.Context, id int64) (*routing.Group, error) {
	s.getGroupCalls++
	group := routing.Group{ID: id, Name: "group", Status: billing.StatusActive}
	return &group, nil
}

func (s *groupAdminFixture) GetGroupModelsListCandidates(ctx context.Context, id int64, platform string) ([]string, error) {
	if platform == capability.PlatformOpenAI {
		return []string{"gpt-5.5", "gpt-5.4"}, nil
	}
	return []string{"claude-sonnet-4-6"}, nil
}

func (s *groupAdminFixture) CreateGroup(ctx context.Context, input *routing.CreateGroupInput) (*routing.Group, error) {
	group := routing.Group{ID: 200, Name: input.Name, Status: billing.StatusActive}
	return &group, nil
}

func (s *groupAdminFixture) DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*routing.Group, error) {
	group := routing.Group{ID: 201, Name: "group (Copy)", Status: "inactive"}
	return &group, nil
}

func (s *groupAdminFixture) RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*routing.Group, error) {
	return nil, nil
}

func (s *groupAdminFixture) UpdateGroup(ctx context.Context, id int64, input *routing.UpdateGroupInput) (*routing.Group, error) {
	group := routing.Group{ID: id, Name: input.Name, Status: billing.StatusActive}
	return &group, nil
}

func (s *groupAdminFixture) DeleteGroup(ctx context.Context, id int64) error {
	return nil
}

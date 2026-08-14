package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildContentModerationInputTeamKeyUsesActorAndKeepsBillingAttribution(t *testing.T) {
	c := newContentModerationHelperTestContext()
	teamID := int64(301)
	apiKey := &service.APIKey{
		ID:        401,
		UserID:    202,
		TeamID:    &teamID,
		User:      &service.User{ID: 101, Email: "owner@example.com"},
		ActorUser: &service.User{ID: 202, Email: "member@example.com"},
	}

	input := buildContentModerationInput(c, apiKey, middleware2.AuthSubject{UserID: 101}, service.ContentModerationProtocolOpenAIChat, "gpt-5", []byte(`{"messages":[]}`))

	require.Equal(t, int64(202), input.UserID)
	require.Equal(t, "member@example.com", input.UserEmail)
	require.Equal(t, int64(101), input.BillingUserID)
	require.Equal(t, teamID, *input.TeamID)
	// 风控快照必须复制团队 ID，不能与认证对象共享可变指针。
	require.NotSame(t, apiKey.TeamID, input.TeamID)
}

func TestBuildOpenAICyberWarningInputTeamKeyUsesActorEmail(t *testing.T) {
	c := newContentModerationHelperTestContext()
	teamID := int64(301)
	apiKey := &service.APIKey{
		ID:        401,
		UserID:    202,
		TeamID:    &teamID,
		User:      &service.User{ID: 101, Email: "owner@example.com"},
		ActorUser: &service.User{ID: 202, Email: "member@example.com"},
	}

	input := buildOpenAICyberWarningInput(c, apiKey, nil, "gpt-5", http.StatusBadRequest, nil, "cyber_policy", "prompt")

	require.Equal(t, int64(202), input.UserID)
	require.Equal(t, "member@example.com", input.UserEmail)
	require.Equal(t, int64(101), input.BillingUserID)
	require.Equal(t, teamID, *input.TeamID)
}

func TestResolveContentModerationIdentityPersonalKeyFallsBackToBillingUser(t *testing.T) {
	apiKey := &service.APIKey{
		UserID: 303,
		User:   &service.User{ID: 303, Email: "personal@example.com"},
	}

	identity := resolveContentModerationIdentity(apiKey, middleware2.AuthSubject{UserID: 303})

	require.Equal(t, int64(303), identity.UserID)
	require.Equal(t, int64(303), identity.BillingUserID)
	require.Equal(t, "personal@example.com", identity.UserEmail)
	require.Nil(t, identity.TeamID)
}

func TestResolveContentModerationIdentityTeamKeyWithoutActorUsesKeyOwnerAndMembership(t *testing.T) {
	teamID := int64(404)
	apiKey := &service.APIKey{
		UserID:         505,
		TeamID:         &teamID,
		User:           &service.User{ID: 606, Email: "owner@example.com"},
		TeamMembership: &service.TeamMembership{UserID: 505, Email: "member@example.com"},
	}

	identity := resolveContentModerationIdentity(apiKey, middleware2.AuthSubject{UserID: 606})

	require.Equal(t, int64(505), identity.UserID)
	require.Equal(t, "member@example.com", identity.UserEmail)
	require.Equal(t, int64(606), identity.BillingUserID)
	require.Equal(t, teamID, *identity.TeamID)
}

func TestResolveContentModerationIdentityWithoutAPIKeyFallsBackToSubject(t *testing.T) {
	identity := resolveContentModerationIdentity(nil, middleware2.AuthSubject{UserID: 707})

	require.Equal(t, int64(707), identity.UserID)
	require.Empty(t, identity.UserEmail)
	require.Equal(t, int64(707), identity.BillingUserID)
	require.Nil(t, identity.TeamID)
}

// newContentModerationHelperTestContext 构造具备请求路径的最小网关上下文。
func newContentModerationHelperTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

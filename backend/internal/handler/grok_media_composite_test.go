package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// compositeGrokVideoCacheStub 仅在指定分组返回任务绑定账号。
type compositeGrokVideoCacheStub struct {
	groupID   int64
	accountID int64
	ownerID   int64
}

func (s *compositeGrokVideoCacheStub) GetSessionAccountID(_ context.Context, groupID int64, _ string) (int64, error) {
	if groupID == s.groupID {
		return s.accountID, nil
	}
	return 0, errors.New("not found")
}

func (s *compositeGrokVideoCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (s *compositeGrokVideoCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *compositeGrokVideoCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (s *compositeGrokVideoCacheStub) SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error) {
	return true, nil
}

func (s *compositeGrokVideoCacheStub) GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error) {
	if s.ownerID > 0 {
		return s.ownerID, nil
	}
	return 0, errors.New("not found")
}

func TestResolveCompositeGrokVideoAPIKeyUsesPersistedOwnerAfterMappingRemoval(t *testing.T) {
	cache := &compositeGrokVideoCacheStub{groupID: 20, accountID: 88, ownerID: 20}
	gateway := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, cache, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := &OpenAIGatewayHandler{gatewayService: gateway}
	apiKey := &service.APIKey{ID: 33, UserID: 44, IsComposite: true}

	selected, accountID, err := handler.resolveCompositeGrokVideoAPIKey(context.Background(), apiKey, "video-123", apiKey.UserID)

	require.NoError(t, err)
	require.Equal(t, int64(88), accountID)
	require.Equal(t, int64(20), *selected.GroupID)
	require.Equal(t, service.PlatformGrok, selected.Group.Platform)
}

func (s *compositeGrokVideoCacheStub) RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error {
	return nil
}

func TestResolveCompositeGrokVideoAPIKeyRestoresBoundGroup(t *testing.T) {
	openAIGroup := &service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	grokGroup := &service.Group{ID: 20, Platform: service.PlatformGrok, Status: service.StatusActive}
	cache := &compositeGrokVideoCacheStub{groupID: grokGroup.ID, accountID: 88}
	gateway := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, cache, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := &OpenAIGatewayHandler{gatewayService: gateway}
	apiKey := &service.APIKey{
		ID: 33, UserID: 44, IsComposite: true,
		CompositeGroups: []service.APIKeyCompositeGroup{
			{GroupID: openAIGroup.ID, Prefix: "GPT", Group: openAIGroup},
			{GroupID: grokGroup.ID, Prefix: "Grok", Group: grokGroup},
		},
	}

	selected, accountID, err := handler.resolveCompositeGrokVideoAPIKey(context.Background(), apiKey, "video-123", apiKey.UserID)

	require.NoError(t, err)
	require.Equal(t, int64(88), accountID)
	require.NotNil(t, selected.GroupID)
	require.Equal(t, grokGroup.ID, *selected.GroupID)
	require.Same(t, grokGroup, selected.Group)
	require.Nil(t, apiKey.GroupID, "请求级恢复不能修改认证缓存中的复合 Key")
}

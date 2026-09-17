//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

func TestProtocolGroupPersistenceAndCacheIsolation(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := prepareRoutingAdmin(&adminServiceImpl{groupRepo: repo})
	created, err := svc.CreateGroup(context.Background(), &CreateGroupInput{Name: "protocol", Platform: PlatformOpenAI, RateMultiplier: 1, AllowedProtocols: []domain.ProtocolID{domain.ProtocolAnthropicMessages, domain.ProtocolImagesEdits}, ProtocolFallbacks: map[domain.ProtocolID]domain.ProtocolID{domain.ProtocolAnthropicMessages: domain.ProtocolOpenAIResponses}, ResponsesImagePolicy: "disabled"})
	require.NoError(t, err)
	require.False(t, created.AllowsClientProtocol(domain.ProtocolOpenAIResponses))
	require.True(t, created.AllowImageGeneration)
	repo.getByID = created
	created.ID = 1
	snapshot := authGroupSnapshotFromGroup(created)
	restored := groupFromAuthSnapshot(snapshot)
	restored.ProtocolFallbacks[domain.ProtocolAnthropicMessages] = domain.ProtocolOpenAIChatCompletions
	require.Equal(t, domain.ProtocolOpenAIResponses, snapshot.ProtocolFallbacks[domain.ProtocolAnthropicMessages])
	require.Equal(t, "disabled", restored.ResponsesImagePolicy)
	protocols := []domain.ProtocolID{domain.ProtocolOpenAIChatCompletions}
	updated, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{AllowedProtocols: &protocols, LegacyProtocolInput: true})
	require.NoError(t, err)
	require.Contains(t, updated.AllowedProtocols, domain.ProtocolImagesEdits)
	require.NotContains(t, updated.AllowedProtocols, domain.ProtocolImagesGenerations)
	updated, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.NotContains(t, updated.AllowedProtocols, domain.ProtocolImagesGenerations)
	empty := []domain.ProtocolID{}
	updated, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{AllowedProtocols: &empty, ProtocolFallbacks: map[domain.ProtocolID]domain.ProtocolID{}, ResponsesImagePolicy: "block"})
	require.NoError(t, err)
	require.Empty(t, updated.AllowedProtocols)
	require.Empty(t, updated.ProtocolFallbacks)
	_, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{ProtocolFallbacks: map[domain.ProtocolID]domain.ProtocolID{domain.ProtocolEmbeddings: domain.ProtocolOpenAIResponses}})
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
}

// 显式集合必须先接受校验，旧媒体补丁不能吞掉重复、未知或不支持的项。
func TestGroupProtocolLegacyPatchDoesNotHideInvalidInput(t *testing.T) {
	for _, protocols := range [][]domain.ProtocolID{
		{"unknown"},
		{domain.ProtocolImagesEdits, domain.ProtocolImagesEdits},
		{domain.ProtocolGeminiGenerateContent},
	} {
		t.Run(string(protocols[0]), func(t *testing.T) {
			enabled := true
			repo := &groupRepoStubForAdmin{}
			svc := prepareRoutingAdmin(&adminServiceImpl{groupRepo: repo})
			_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name: "invalid", Platform: PlatformOpenAI, RateMultiplier: 1,
				AllowedProtocols: protocols, LegacyProtocolInput: true, AllowImageGeneration: enabled,
			})
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", infraerrors.Reason(err))
			require.Nil(t, repo.created)
			repo.getByID = &Group{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 1, AllowedProtocols: []domain.ProtocolID{domain.ProtocolOpenAIResponses}}
			_, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
				AllowedProtocols: &protocols, LegacyProtocolInput: true, AllowImageGeneration: &enabled,
			})
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", infraerrors.Reason(err))
			require.Nil(t, repo.updated)
		})
	}
}

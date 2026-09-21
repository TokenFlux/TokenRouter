//go:build unit

package routing_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

func TestProtocolGroupPersistenceAndCacheIsolation(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	created, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{Name: "protocol", Platform: capability.PlatformOpenAI, RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolImagesEdits}, ProtocolFallbacks: map[protocol.ProtocolID]protocol.ProtocolID{protocol.ProtocolAnthropicMessages: protocol.ProtocolOpenAIResponses}, ResponsesImagePolicy: "disabled"})
	require.NoError(t, err)
	require.False(t, created.AllowsClientProtocol(protocol.ProtocolOpenAIResponses))
	require.True(t, created.AllowImageGeneration)
	repo.getByID = created
	created.ID = 1
	snapshot := apikey.KeyAuthGroupSnapshotFromGroup(created)
	restored := apikey.KeyGroupFromAuthSnapshot(snapshot)
	restored.ProtocolFallbacks[protocol.ProtocolAnthropicMessages] = protocol.ProtocolOpenAIChatCompletions
	require.Equal(t, protocol.ProtocolOpenAIResponses, snapshot.ProtocolFallbacks[protocol.ProtocolAnthropicMessages])
	require.Equal(t, "disabled", restored.ResponsesImagePolicy)
	protocols := []protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions}
	updated, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{AllowedProtocols: &protocols, LegacyProtocolInput: true})
	require.NoError(t, err)
	require.Contains(t, updated.AllowedProtocols, protocol.ProtocolImagesEdits)
	require.NotContains(t, updated.AllowedProtocols, protocol.ProtocolImagesGenerations)
	updated, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.NotContains(t, updated.AllowedProtocols, protocol.ProtocolImagesGenerations)
	empty := []protocol.ProtocolID{}
	updated, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{AllowedProtocols: &empty, ProtocolFallbacks: map[protocol.ProtocolID]protocol.ProtocolID{}, ResponsesImagePolicy: "block"})
	require.NoError(t, err)
	require.Empty(t, updated.AllowedProtocols)
	require.Empty(t, updated.ProtocolFallbacks)
	_, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{ProtocolFallbacks: map[protocol.ProtocolID]protocol.ProtocolID{protocol.ProtocolEmbeddings: protocol.ProtocolOpenAIResponses}})
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
}

// 显式集合必须先接受校验，旧媒体补丁不能吞掉重复、未知或不支持的项。
func TestGroupProtocolLegacyPatchDoesNotHideInvalidInput(t *testing.T) {
	for _, protocols := range [][]protocol.ProtocolID{
		{"unknown"},
		{protocol.ProtocolImagesEdits, protocol.ProtocolImagesEdits},
		{protocol.ProtocolGeminiGenerateContent},
	} {
		t.Run(string(protocols[0]), func(t *testing.T) {
			enabled := true
			repo := &groupRepoStubForAdmin{}
			svc := newOriginalGroupAdmin(repo, nil, nil)
			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: "invalid", Platform: capability.PlatformOpenAI, RateMultiplier: 1,
				AllowedProtocols: protocols, LegacyProtocolInput: true, AllowImageGeneration: enabled,
			})
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", apperror.Reason(err))
			require.Nil(t, repo.created)
			repo.getByID = &routing.Group{ID: 1, Platform: capability.PlatformOpenAI, RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}
			_, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
				AllowedProtocols: &protocols, LegacyProtocolInput: true, AllowImageGeneration: &enabled,
			})
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", apperror.Reason(err))
			require.Nil(t, repo.updated)
		})
	}
}

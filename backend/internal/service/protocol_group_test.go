//go:build unit

package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestProtocolGroupPersistenceAndCacheIsolation(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}
	created, err := svc.CreateGroup(context.Background(), &CreateGroupInput{Name: "protocol", Platform: PlatformOpenAI, RateMultiplier: 1, AllowedProtocols: []GroupClientProtocol{GroupClientProtocolAnthropicMessages, domain.ProtocolImagesEdits}, ProtocolFallbacks: map[GroupClientProtocol]GroupClientProtocol{GroupClientProtocolAnthropicMessages: GroupClientProtocolOpenAIResponses}, ResponsesImagePolicy: "disabled"})
	require.NoError(t, err)
	require.False(t, created.AllowsClientProtocol(GroupClientProtocolOpenAIResponses))
	require.True(t, created.AllowImageGeneration)
	repo.getByID = created
	created.ID = 1
	snapshot := authGroupSnapshotFromGroup(created)
	restored := groupFromAuthSnapshot(snapshot)
	restored.ProtocolFallbacks[GroupClientProtocolAnthropicMessages] = GroupClientProtocolOpenAIChatCompletions
	require.Equal(t, GroupClientProtocolOpenAIResponses, snapshot.ProtocolFallbacks[GroupClientProtocolAnthropicMessages])
	require.Equal(t, "disabled", restored.ResponsesImagePolicy)
	protocols := []GroupClientProtocol{GroupClientProtocolOpenAIChatCompletions}
	updated, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{AllowedProtocols: &protocols, LegacyProtocolInput: true})
	require.NoError(t, err)
	require.Contains(t, updated.AllowedProtocols, domain.ProtocolImagesEdits)
	require.NotContains(t, updated.AllowedProtocols, domain.ProtocolImagesGenerations)
	updated, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.NotContains(t, updated.AllowedProtocols, domain.ProtocolImagesGenerations)
	empty := []GroupClientProtocol{}
	updated, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{AllowedProtocols: &empty, ProtocolFallbacks: map[GroupClientProtocol]GroupClientProtocol{}, ResponsesImagePolicy: "block"})
	require.NoError(t, err)
	require.Empty(t, updated.AllowedProtocols)
	require.Empty(t, updated.ProtocolFallbacks)
	_, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{ProtocolFallbacks: map[GroupClientProtocol]GroupClientProtocol{domain.ProtocolEmbeddings: GroupClientProtocolOpenAIResponses}})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
}

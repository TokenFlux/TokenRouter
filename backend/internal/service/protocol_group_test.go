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
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
}

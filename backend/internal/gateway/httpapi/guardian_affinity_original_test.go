package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	clientmeta "github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func guardianAffinityTestContext(t *testing.T, model, subagent, parentHeader, metadata string) context.Context {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set(clientmeta.OpenAISubagentHeader, subagent)
	if parentHeader != "" {
		c.Request.Header.Set(clientmeta.CodexParentThreadIDHeader, parentHeader)
	}
	if metadata != "" {
		c.Request.Header.Set(clientmeta.CodexTurnMetadataHeader, metadata)
	}
	return WithOpenAIGuardianParentAffinity(context.Background(), c, nil, model)
}

func TestWithOpenAIGuardianParentAffinityRequiresUnambiguousReviewLineage(t *testing.T) {
	parentID := "11111111-1111-4111-8111-111111111111"
	wantHash, _ := scheduler.DeriveSessionHashes(parentID)

	for _, subagent := range []string{"guardian", "review", "GUARDIAN"} {
		t.Run(subagent, func(t *testing.T) {
			ctx := guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, subagent, parentID, `{"parent_thread_id":"`+parentID+`"}`)
			affinity, ok := requeststate.GuardianParentAffinityFromContext(ctx)
			require.True(t, ok)
			require.Equal(t, wantHash, affinity.CurrentSessionHash)
		})
	}

	t.Run("metadata only", func(t *testing.T) {
		ctx := guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, "guardian", "", `{"parent_thread_id":"`+parentID+`"}`)
		_, ok := requeststate.GuardianParentAffinityFromContext(ctx)
		require.True(t, ok)
	})

	t.Run("websocket envelope metadata", func(t *testing.T) {

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
		body := []byte(`{"type":"response.create","response":{"model":"codex-auto-review","client_metadata":{"x-codex-turn-metadata":"{\"parent_thread_id\":\"` + parentID + `\",\"subagent_kind\":\"guardian\"}"}}}`)
		ctx := WithOpenAIGuardianParentAffinity(context.Background(), c, body, clientmeta.CodexAutoReviewModel)
		affinity, ok := requeststate.GuardianParentAffinityFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, wantHash, affinity.CurrentSessionHash)
	})

	for name, ctx := range map[string]context.Context{
		"ordinary model":       guardianAffinityTestContext(t, "gpt-5.6-sol", "guardian", parentID, ""),
		"ordinary subagent":    guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, "collab_spawn", parentID, ""),
		"missing parent":       guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, "guardian", "", ""),
		"conflicting lineage":  guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, "guardian", parentID, `{"parent_thread_id":"different-parent"}`),
		"conflicting subagent": guardianAffinityTestContext(t, clientmeta.CodexAutoReviewModel, "guardian", parentID, `{"parent_thread_id":"`+parentID+`","subagent_kind":"collab_spawn"}`),
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := requeststate.GuardianParentAffinityFromContext(ctx)
			require.False(t, ok)
		})
	}
}

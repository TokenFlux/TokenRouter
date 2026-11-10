package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// redaction_scenario_test.go covers redaction.go, output.go, and execute.go
// through the real SDK and browser confirmation workflow. The fixture observes
// the exact payload delivered after approval and rejects requests whose hidden
// credentials or public configuration no longer match the approved digest.

// TestMCPConfirmationRedactionKeepsDispatchAndDigest submits mixed configuration
// and credentials through the SDK with default tool result settings. The preview
// masks credentials, changed values cannot reuse approval, and both dispatch and
// the tool result retain the original JSON values.
func TestMCPConfirmationRedactionKeepsDispatchAndDigest(t *testing.T) {
	f := newMCPFixture(t)
	op := &adminapi.Operation{
		ID: "fixture.redaction.update", Method: http.MethodPost, Path: "/api/v1/admin/fixture-redaction",
		Sensitive: true, Response: adminapi.ResponseJSON,
		Input: adminapi.Arguments("/api/v1/admin/fixture-redaction", nil, map[string]any{}),
	}
	received := make(chan []byte, 1)
	registerFixtureOperation(t, f, op, func(c *gin.Context) {
		raw, err := c.GetRawData()
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		// The buffered handoff records dispatch before HTTP completion without
		// sharing a mutable response variable between the server goroutine and test assertions.
		received <- raw
		c.Data(http.StatusOK, "application/json", raw)
	})
	body := json.RawMessage(`{"max_tokens":null,"min_tokens":0,"total_tokens":9007199254740993,"api_key":123456,"input_price_per_token":0.1234567890123456789}`)
	args := map[string]any{"operation_id": op.ID, "body": body}
	result, data := f.invoke(t, "execute_operation", args)
	require.False(t, result.IsError)
	require.Equal(t, "confirmation_required", data["status"])
	id := jsonTestValue[string](t, data["confirmation_id"])
	approval, err := f.handler.state.GetApproval(t.Context(), id)
	require.NoError(t, err)
	require.Contains(t, approval.Preview, `"max_tokens": null`)
	require.Contains(t, approval.Preview, `"min_tokens": 0`)
	require.Contains(t, approval.Preview, `"total_tokens": 9007199254740993`)
	require.Contains(t, approval.Preview, `"input_price_per_token": 0.1234567890123456789`)
	require.Contains(t, approval.Preview, `"api_key": "***"`)
	digest := approval.Digest
	response := f.browser(t, http.MethodPost, "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser")
	require.Equal(t, http.StatusOK, response.StatusCode)
	args["confirmation_id"] = id
	for _, changed := range []string{
		strings.Replace(string(body), "123456,", "654321,", 1),
		strings.Replace(string(body), `"max_tokens":null`, `"max_tokens":256000`, 1),
	} {
		args["body"] = json.RawMessage(changed)
		result, _ = f.invoke(t, "execute_operation", args)
		require.True(t, result.IsError)
		require.Empty(t, received)
	}
	args["body"] = body
	result, data = f.invoke(t, "execute_operation", args)
	require.False(t, result.IsError)
	require.Equal(t, "succeeded", data["status"])
	select {
	case raw := <-received:
		require.Equal(t, string(body), string(raw))
	default:
		t.Fatal("approved operation did not reach its handler")
	}
	approval, err = f.handler.state.GetApproval(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, digest, approval.Digest)
	require.Equal(t, "consumed", approval.State)
	text := jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text
	require.Contains(t, text, `"max_tokens":null`)
	require.Contains(t, text, `"api_key":123456`)
	require.Contains(t, text, `"total_tokens":9007199254740993`)
}

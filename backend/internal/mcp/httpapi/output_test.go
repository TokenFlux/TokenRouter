package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// TestOperationResultRedactionCoversSuccessAndErrors feeds exact HTTP JSON into
// output.go with masking off and on. Successful and failed results retain
// metadata and numeric spelling and serialize the same fields in both representations.
func TestOperationResultRedactionCoversSuccessAndErrors(t *testing.T) {
	f := newMCPFixture(t)
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			response := newCapture(t.Context(), func() {})
			defer response.close()
			response.WriteHeader(status)
			_, err := response.Write([]byte(`{"code":"BUSINESS_STATUS","data":{"max_tokens":null,"min_tokens":0,"total_tokens":9007199254740993,"input_price_per_token":0.1234567890123456789,"api_key":123456}}`))
			require.NoError(t, err)
			op := &adminapi.Operation{ID: "fixture.update"}
			plain := f.handler.operationResult(t.Context(), f.token, op, response)
			require.Equal(t, status != http.StatusOK, plain.IsError)
			require.Contains(t, jsonTestValue[*mcp.TextContent](t, plain.Content[0]).Text, `"api_key":123456`)

			token := *f.token
			token.RedactToolResults = true
			result := f.handler.operationResult(t.Context(), &token, op, response)
			require.Equal(t, status != http.StatusOK, result.IsError)
			encoded, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.Len(t, result.Content, 1)
			require.Equal(t, string(encoded), jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text)
			require.Contains(t, string(encoded), `"code":"BUSINESS_STATUS"`)
			require.Contains(t, string(encoded), `"max_tokens":null`)
			require.Contains(t, string(encoded), `"min_tokens":0`)
			require.Contains(t, string(encoded), `"total_tokens":9007199254740993`)
			require.Contains(t, string(encoded), `"input_price_per_token":0.1234567890123456789`)
			require.Contains(t, string(encoded), `"api_key":"***"`)
		})
	}
}

// TestStreamResultRedactionRetainsProgressAndErrorCodes submits finite success
// and error streams. Events preserve metadata and exact numbers while code values
// outside error events and credential fields are masked only for opted-in tokens.
func TestStreamResultRedactionRetainsProgressAndErrorCodes(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, Options{})
	for _, failed := range []bool{false, true} {
		events := "data: {\"type\":\"progress\",\"max_tokens\":null,\"total_tokens\":9007199254740993,\"refresh_token\":123456,\"code\":\"private\"}\n\n"
		if failed {
			events += "data: {\"type\":\"error\",\"code\":\"UPSTREAM_FAILED\",\"authorization\":\"private\"}\n\n"
		}
		events += "data: {\"type\":\"done\",\"success\":true}\n\n"
		op := &adminapi.Operation{ID: "fixture.stream", TerminalEvent: "done"}
		plain := h.streamResult(&identity.MCPToken{}, op, []byte(events), "fixture")
		require.Equal(t, failed, plain.IsError)
		require.Contains(t, jsonTestValue[*mcp.TextContent](t, plain.Content[0]).Text, `"refresh_token":123456`)

		token := &identity.MCPToken{RedactToolResults: true}
		result := h.streamResult(token, op, []byte(events), "fixture")
		require.Equal(t, failed, result.IsError)
		encoded, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		require.Equal(t, string(encoded), jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text)
		require.Contains(t, string(encoded), `"max_tokens":null`)
		require.Contains(t, string(encoded), `"total_tokens":9007199254740993`)
		require.Contains(t, string(encoded), `"refresh_token":"***"`)
		require.NotContains(t, string(encoded), "private")
		if failed {
			require.Contains(t, string(encoded), `"code":"UPSTREAM_FAILED"`)
		}
	}
}

// TestProtectedResultsRetainOriginalBytes exercises explicit exports and large
// ordinary JSON. Files keep their unmodified payload while inline MCP results
// expose only the existing protected-download metadata and never its credentials.
func TestProtectedResultsRetainOriginalBytes(t *testing.T) {
	f := newMCPFixture(t)
	for _, mode := range []string{adminapi.ResponseFile, adminapi.ResponseJSON} {
		t.Run(mode, func(t *testing.T) {
			raw := `{"max_tokens":null,"key":"private"}`
			if mode == adminapi.ResponseJSON {
				raw = `{"max_tokens":null,"key":"private","padding":"` + strings.Repeat("x", maxInlineResultBytes) + `"}`
			}
			response := newCapture(t.Context(), func() {})
			defer response.close()
			response.Header().Set("Content-Type", "application/json")
			_, err := response.Write([]byte(raw))
			require.NoError(t, err)
			result := f.handler.operationResult(t.Context(), f.token, &adminapi.Operation{ID: "fixture.get", ReadOnly: true, Response: mode}, response)
			data := jsonTestValue[map[string]any](t, result.StructuredContent)
			require.Equal(t, "file_ready", data["status"])
			file, err := f.handler.state.GetArtifact(t.Context(), jsonTestValue[string](t, data["file_id"]))
			require.NoError(t, err)
			require.Equal(t, raw, string(file.Data))
			require.NotContains(t, jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text, "private")
		})
	}
}

package codeassist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// client_test.go verifies Code Assist discovery, onboarding, and long-running
// operation polling requests and headers against a local HTTP fixture.

// TestClient_DefaultMetadataAndGetOperation exercises LoadCodeAssist,
// OnboardUser, and GetOperation with nil default request bodies and operation
// path prefixes. Requests must carry IDE_UNSPECIFIED, legacy-tier, and the
// GeminiCLI User-Agent, while GetOperation must reject path traversal names.
func TestClient_DefaultMetadataAndGetOperation(t *testing.T) {
	var seenLoad LoadCodeAssistRequest
	var seenOnboard OnboardUserRequest
	var seenUA string
	var seenGetPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &seenLoad))
			_, _ = w.Write([]byte(`{"cloudaicompanionProject":{"id":"proj-123"},"currentTier":"standard-tier"}`))
		case "/v1internal:onboardUser":
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &seenOnboard))
			_, _ = w.Write([]byte(`{"done":false,"name":"operations/op-42"}`))
		case "/v1internal/operations/op-42":
			require.Equal(t, http.MethodGet, r.Method)
			seenGetPath = r.URL.Path
			_, _ = w.Write([]byte(`{"done":true,"name":"operations/op-42","response":{"cloudaicompanionProject":{"id":"proj-42"}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL}

	loadResp, err := client.LoadCodeAssist(context.Background(), "token-1", "", nil)
	require.NoError(t, err)
	require.Equal(t, "IDE_UNSPECIFIED", seenLoad.Metadata.IDEType)
	require.Equal(t, "GEMINI", seenLoad.Metadata.PluginType)
	require.Equal(t, GeminiCLIUserAgent, seenUA)
	require.Equal(t, "proj-123", loadResp.CloudAICompanionProject)

	onboardResp, err := client.OnboardUser(context.Background(), "token-1", "", nil)
	require.NoError(t, err)
	require.Equal(t, "legacy-tier", seenOnboard.TierID)
	require.Equal(t, "IDE_UNSPECIFIED", seenOnboard.Metadata.IDEType)
	require.False(t, onboardResp.Done)

	opResp, err := client.GetOperation(context.Background(), "token-1", "", "/v1internal/operations/op-42")
	require.NoError(t, err)
	require.Equal(t, "/v1internal/operations/op-42", seenGetPath)
	require.Equal(t, "proj-42", opResp.ProjectID())

	_, err = client.GetOperation(context.Background(), "token-1", "", "operations/../secret")
	require.Error(t, err)
}

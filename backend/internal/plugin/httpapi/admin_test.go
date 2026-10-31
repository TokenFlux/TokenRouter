package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// TestInstallationResponseExcludesConfigurationAndPaths serializes a domain record containing synthetic plaintext, ciphertext, and filesystem locations.
// The administrative projection exposes none of these internal representations, including those in the rollback snapshot.
func TestInstallationResponseExcludesConfigurationAndPaths(t *testing.T) {
	item := &plugin.Installation{
		PluginID:        "test.plugin",
		LocalDirectory:  "/private/dev",
		VersionSnapshot: plugin.VersionSnapshot{Version: "1.0.0", Config: map[string]any{"private": "plaintext-value"}, SecretConfig: "ciphertext-value"},
		Previous:        &plugin.VersionSnapshot{Version: "0.9.0", SecretConfig: "old-ciphertext"},
	}
	data, err := json.Marshal(installationDTO(item, plugin.Status{State: plugin.StateStopped}))
	require.NoError(t, err)
	for _, private := range []string{"/private/dev", "plaintext-value", "ciphertext-value", "old-ciphertext", "secret_config"} {
		require.NotContains(t, string(data), private)
	}
	require.Contains(t, string(data), "0.9.0")
}

// TestMalformedConfigurationDoesNotEchoSecretBytes supplies malformed and trailing JSON with synthetic secret content.
// Parser diagnostics remain generic and never include submitted values in the error response.
func TestMalformedConfigurationDoesNotEchoSecretBytes(t *testing.T) {
	for _, body := range []string{`{"values":{"color":secret-input}}`, `{"values":{"color":"secret-input"}} {}`} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/test.plugin/config", strings.NewReader(body))
		var input ConfigInput
		require.False(t, decodeBody(ctx, &input))
		require.NotContains(t, recorder.Body.String(), "secret-input")
	}
}

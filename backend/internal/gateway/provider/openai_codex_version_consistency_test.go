//go:build unit

package provider

import (
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

func TestCodexVersionConstants_Consistency(t *testing.T) {
	require.GreaterOrEqual(t, clientmeta.CompareVersions(openai.CodexCLIVersion, openai.CodexUpstreamMinVersion), 0,
		"codexCLIVersion must not be below the upstream minimum")

	require.True(t, strings.Contains(openai.CodexCLIUserAgent, openai.CodexDefaultOriginator+"/"+openai.CodexCLIVersion),
		"codexCLIUserAgent must embed codexCLIVersion")

	require.True(t, strings.Contains(gateway.DefaultOpenAICodexUserAgent, openai.CodexCLIVersion),
		"DefaultOpenAICodexUserAgent must embed codexCLIVersion")
}

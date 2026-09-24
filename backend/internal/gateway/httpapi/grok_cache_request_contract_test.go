//go:build unit

package httpapi

import (
	"testing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/stretchr/testify/require"
)

func TestGrokResponsesCacheIdentityIncludesPromotedCodexTools(t *testing.T) {
	c := newGrokCacheTestContext(4503)
	lookupBody := []byte(`{"model":"grok","input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]},{"type":"message","role":"user","content":"same prompt"}]}`)
	readBody := []byte(`{"model":"grok","input":[{"type":"additional_tools","tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}]},{"type":"message","role":"user","content":"same prompt"}]}`)

	patchedLookup, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(lookupBody, "grok-4.5")
	require.NoError(t, err)
	patchedRead, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(readBody, "grok-4.5")
	require.NoError(t, err)

	lookupIdentity := ResolveGrokCacheIdentity(c, patchedLookup, "", "grok-4.5")
	readIdentity := ResolveGrokCacheIdentity(c, patchedRead, "", "grok-4.5")
	require.NotEmpty(t, lookupIdentity)
	require.NotEmpty(t, readIdentity)
	require.NotEqual(t, lookupIdentity, readIdentity)
}

package modelrules

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// TestOpenCodeProtocolsSeparatesModesAndCopiesDefaults compares an exact shared
// model across the bundled service offerings. Mutating one result cannot alter
// subsequent route lookups or another service mode.
func TestOpenCodeProtocolsSeparatesModesAndCopiesDefaults(t *testing.T) {
	payg, goMode := OpenCodeProtocols("payg"), OpenCodeProtocols("go")
	require.Equal(t, "openai_chat_completions", payg["minimax-m3"])
	require.Equal(t, "anthropic_messages", goMode["minimax-m3"])
	require.Equal(t, "systemone", payg["jev-1.13"])
	payg["minimax-m3"] = "changed"
	require.Equal(t, "openai_chat_completions", OpenCodeProtocols("payg")["minimax-m3"])
	require.Empty(t, OpenCodeProtocols("unknown"))
}

// TestParseOpenCodeOfferingsUsesDeclaredSDK gives one provider default and
// several explicit model SDKs. Unknown encodings stay unavailable rather than
// being guessed from otherwise familiar model names.
func TestParseOpenCodeOfferingsUsesDeclaredSDK(t *testing.T) {
	body := []byte(`{"models":{"openai/gpt-4":{"name":"GPT-4"}},"providers":{"openai":{"models":{"gpt-4":{"name":"GPT-4","cost":{"input":1}}}},"opencode":{"npm":"@ai-sdk/openai-compatible","models":{"chat":{},"response":{"provider":{"npm":"@ai-sdk/openai"}},"messages":{"provider":{"npm":"@ai-sdk/anthropic"}},"google":{"provider":{"npm":"@ai-sdk/google"}},"decision":{"type":"decision"},"gpt-unknown":{"provider":{"npm":"unknown"}}}}}}`)
	catalog, err := modelcatalog.Parse(body)
	require.NoError(t, err)
	routes := NewView(catalog.Publication()).OpenCodeProtocols("payg")
	require.Len(t, routes, 5)
	require.Equal(t, "gemini_generate_content", routes["google"])
	require.Equal(t, "systemone", routes["decision"])
	require.NotContains(t, routes, "gpt-unknown")
}

// TestParseOpenCodeOfferingsUsesUniqueExactPublicModels resolves missing canonical
// declarations from public author records. Explicit references win, ambiguous
// authors stay unknown, and model name fragments never select a public identity.
func TestParseOpenCodeOfferingsUsesUniqueExactPublicModels(t *testing.T) {
	body := []byte(`{"models":{"anthropic/claude-opus-4-7":{},"openai/shared":{},"another/shared":{},"openai/shared-longer":{},"third/shared":{}},"providers":{"opencode":{"npm":"@ai-sdk/anthropic","models":{"claude-opus-4-7":{},"shared":{},"shared-longer":{},"claude-opus-4-7-free":{},"shared-alias":{"canonical_model_id":"openai/shared"}}},"opencode-go":{"npm":"@ai-sdk/anthropic","models":{"claude-opus-4-7":{},"shared":{"canonical_model_id":"another/shared"}}}}}`)
	catalog, err := modelcatalog.Parse(body)
	require.NoError(t, err)
	offerings := catalog.Publication().ServiceOfferings("opencode")
	require.Equal(t, "anthropic/claude-opus-4-7", offerings["claude-opus-4-7"].CanonicalModelID)
	require.Equal(t, "anthropic/claude-opus-4-7", catalog.Publication().Offering("opencode-go", "claude-opus-4-7").CanonicalModelID)
	require.Empty(t, offerings["shared"].CanonicalModelID)
	require.Empty(t, offerings["claude-opus-4-7-free"].CanonicalModelID)
	require.Equal(t, "openai/shared-longer", offerings["shared-longer"].CanonicalModelID)
	require.Equal(t, "openai/shared", offerings["shared-alias"].CanonicalModelID)
	require.Equal(t, "another/shared", catalog.Publication().Offering("opencode-go", "shared").CanonicalModelID)
}

package provider_test

import (
	"net/http"
	"testing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestPatchGrokResponsesBodyCombinesAdditionalToolsAndDropsOrphanControls(t *testing.T) {
	body := []byte(`{
		"input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup"}]},{"role":"user","content":"hi"}],
		"tools":[{"type":"function","name":"lookup"}],
		"tool_choice":"auto","parallel_tool_calls":true
	}`)
	patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.Equal(t, int64(1), gjson.GetBytes(patched, "tools.#").Int())
	require.Equal(t, "lookup", gjson.GetBytes(patched, "tools.0.name").String())
	require.Equal(t, "message", gjson.GetBytes(patched, "input.0.type").String())

	withoutTools, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody([]byte(`{"input":"hi","tool_choice":"auto","parallel_tool_calls":true}`), "grok-4.5")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(withoutTools, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(withoutTools, "parallel_tool_calls").Exists())

	for _, malformedTools := range []string{"null", `{}`, `"invalid"`} {
		malformedBody := []byte(`{"input":"hi","tools":` + malformedTools + `,"tool_choice":"auto","parallel_tool_calls":true}`)
		patched, err := gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(malformedBody, "grok-4.5")
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(patched, "tool_choice").Exists(), string(patched))
		require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists(), string(patched))
	}
}

func TestSanitizeGrokCompactionReplayBodyPreservesVisibleSummary(t *testing.T) {
	body := []byte(`{
		"previous_response_id":"resp_stale",
		"input":[{"type":"compaction","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"visible history"}]},{"type":"message","role":"user","content":"continue"}]
	}`)
	patched, changed, err := gatewayprovider.GrokBodyCodec().SanitizeGrokCompactionReplayBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(patched, "previous_response_id").Exists())
	require.False(t, gjson.GetBytes(patched, `input.#(type=="reasoning")`).Exists())
	require.Contains(t, gjson.GetBytes(patched, "input.0.content.0.text").String(), "visible history")
	require.Equal(t, "continue", gjson.GetBytes(patched, "input.1.content").String())
	require.True(t, gatewayprovider.GrokBodyCodec().IsGrokCompactionReplayDecodeError(http.StatusBadRequest, []byte(`{"error":{"message":"failed to deserialize compaction summary history"}}`)))
	require.True(t, gatewayprovider.GrokBodyCodec().IsGrokCompactionReplayDecodeError(http.StatusBadRequest, []byte(`{"error":{"type":"invalid_request_error"},"detail":"could not decode compaction blob"}`)))
}

func TestGrokStructuredErrorCandidatesDoNotShadowTopLevelMessages(t *testing.T) {
	shadowedInvalidEncrypted := []byte(`{"code":"invalid-argument","error":{"type":"invalid_request_error"},"message":"Could not decrypt encrypted_content because it was modified"}`)
	require.True(t, gatewayprovider.GrokBodyCodec().IsGrokInvalidEncryptedContentResponse(http.StatusBadRequest, shadowedInvalidEncrypted))
	require.True(t, gatewayprovider.GrokBodyCodec().IsGrokCompactionReplayDecodeError(http.StatusBadRequest, []byte(`{"error":{"type":"invalid_request_error"},"message":"could not decode compaction history"}`)))
}

func TestGrokDecoderCompatibility422FailsOverWithoutCooldown(t *testing.T) {
	body := []byte(`{"detail":"data did not match any variant of untagged enum ModelInput at input[3]"}`)
	require.True(t, grok.IsGrokDecoderCompatibilityError(http.StatusUnprocessableEntity, body))
	require.True(t, grok.IsGrokDecoderCompatibilityError(http.StatusUnprocessableEntity, []byte(`{"message":"could not decode ModelInput at input.3"}`)))
	require.True(t, grok.IsGrokDecoderCompatibilityError(http.StatusUnprocessableEntity, []byte(`{"error":{"type":"invalid_request_error"},"message":"could not deserialize ModelInput at input[3]"}`)))
	require.True(t, grok.IsGrokDecoderCompatibilityError(http.StatusUnprocessableEntity, []byte(`{"error":"Failed to deserialize the JSON body into the target type: messages[1]: data did not match any variant of untagged enum Content at line 1 column 6577"}`)))
	require.True(t, gatewayprovider.ShouldFailoverGrokResponse(http.StatusUnprocessableEntity, body))
	decision := grok.ClassifyGrokUpstreamFailure(http.StatusUnprocessableEntity, body, "grok-4.5")
	require.False(t, decision.ShouldCooldown)
	require.Equal(t, grok.GrokFailureNone, decision.Class)

	require.False(t, grok.IsGrokDecoderCompatibilityError(http.StatusUnprocessableEntity, []byte(`{"error":{"message":"invalid user parameter"}}`)))
	require.False(t, grok.IsGrokDecoderCompatibilityError(http.StatusUnprocessableEntity, []byte(`{"error":"messages[1].content is required"}`)))
	require.False(t, grok.IsGrokDecoderCompatibilityError(http.StatusBadRequest, body))
}

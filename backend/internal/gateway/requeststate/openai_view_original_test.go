package requeststate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRequestView_ExtractsRawScalars(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":" gpt-5 ","stream":true,"prompt_cache_key":" ses-1 ","previous_response_id":" resp-1 ","service_tier":" fast ","reasoning":{"effort":" medium "}}`))

	require.Equal(t, "gpt-5", view.Model)
	require.True(t, view.Stream)
	require.Equal(t, "ses-1", view.PromptCacheKey)
	require.Equal(t, "resp-1", view.PreviousResponseID)
	require.Equal(t, "fast", view.ServiceTier)
	require.True(t, view.HasServiceTier)
	require.Equal(t, "medium", view.ReasoningEffort)
}

func TestOpenAIRequestView_ExtractsFieldsAfterLargeInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"content":"` + strings.Repeat("payload", 1024) + `"}],"stream":true,"prompt_cache_key":"session-1","previous_response_id":"resp-1","service_tier":"flex","reasoning":{"effort":"high"}}`)

	view := NewOpenAIRequestView(body)

	require.Equal(t, "gpt-5", view.Model)
	require.True(t, view.Stream)
	require.Equal(t, "session-1", view.PromptCacheKey)
	require.Equal(t, "resp-1", view.PreviousResponseID)
	require.Equal(t, "flex", view.ServiceTier)
	require.Equal(t, "high", view.ReasoningEffort)
}

func TestOpenAIRequestView_KeepsFirstDuplicateField(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5","model":"gpt-5.1","reasoning":{"effort":"low"},"reasoning":{"effort":"high"}}`))

	require.Equal(t, "gpt-5", view.Model)
	require.Equal(t, "low", view.ReasoningEffort)
}

func TestOpenAIRequestView_KeepsLenientPrefixExtraction(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5","stream":true,"input":[`))

	require.Equal(t, "gpt-5", view.Model)
	require.True(t, view.Stream)
}

func TestOpenAIRequestView_DecodeKeepsFullMapBehavior(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5","stream":true,"input":[{"type":"message","content":"hi"}]}`))

	reqBody, err := view.Decode()
	require.NoError(t, err)
	require.Equal(t, "gpt-5", reqBody["model"])
	require.IsType(t, []any{}, reqBody["input"])
}

func TestOpenAIRequestView_ApplyPatches(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5","previous_response_id":"resp_1","reasoning":{"effort":"minimal"},"input":[{"type":"message","content":"hi"}]}`))
	view.MarkPatchSet("model", "gpt-5.1")
	view.MarkPatchDelete("previous_response_id")
	view.MarkPatchSet("reasoning.effort", "none")

	patched, err := view.ApplyPatches()
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5.1","reasoning":{"effort":"none"},"input":[{"type":"message","content":"hi"}]}`, string(patched))
}

func TestOpenAIRequestView_RejectsEscapedPatchPath(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"metadata":{"user.id":"old"}}`))
	view.MarkPatchSet(`metadata.user\.id`, "new")

	require.False(t, view.HasPatches())
	_, err := view.ApplyPatches()
	require.Error(t, err)
}

func TestOpenAIRequestView_ApplyPatchesDisabled(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5"}`))
	view.MarkPatchSet("model", "gpt-5.1")
	view.DisablePatches()

	_, err := view.ApplyPatches()
	require.Error(t, err)
}

func TestOpenAIRequestView_HasPatches(t *testing.T) {
	view := NewOpenAIRequestView([]byte(`{"model":"gpt-5"}`))
	require.False(t, view.HasPatches())

	view.MarkPatchSet("model", "gpt-5.1")
	require.True(t, view.HasPatches())

	view.DisablePatches()
	require.False(t, view.HasPatches())
}

func TestExtractOpenAIRequestMetaFromBody(t *testing.T) {
	tests := []struct {
		name          string
		body          []byte
		wantModel     string
		wantStream    bool
		wantPromptKey string
	}{
		{
			name:          "完整字段",
			body:          []byte(`{"model":"gpt-5","stream":true,"prompt_cache_key":" ses-1 "}`),
			wantModel:     "gpt-5",
			wantStream:    true,
			wantPromptKey: "ses-1",
		},
		{
			name:          "缺失可选字段",
			body:          []byte(`{"model":"gpt-4"}`),
			wantModel:     "gpt-4",
			wantStream:    false,
			wantPromptKey: "",
		},
		{
			name:          "空请求体",
			body:          nil,
			wantModel:     "",
			wantStream:    false,
			wantPromptKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, stream, promptKey := OpenAIRequestMetaFromBody(tt.body)
			require.Equal(t, tt.wantModel, model)
			require.Equal(t, tt.wantStream, stream)
			require.Equal(t, tt.wantPromptKey, promptKey)
		})
	}
}

func TestGetOpenAIRequestBodyMap_ParseError(t *testing.T) {
	_, err := DecodeOpenAIRequestBody([]byte(`{invalid-json`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse request")
}

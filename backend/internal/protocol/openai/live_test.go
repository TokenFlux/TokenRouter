package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateLiveCallRequest_RejectsDuplicateAndCaseVariantSessionFields
// supplies valid empty or populated session objects alongside duplicate and
// case-variant session fields. Validation must accept canonical objects while
// rejecting ambiguous session keys before Live routing or model rewriting runs.
func TestValidateLiveCallRequest_RejectsDuplicateAndCaseVariantSessionFields(t *testing.T) {
	require.NoError(t, ValidateLiveCallRequest(&LiveCallRequest{
		SDP:     "v=0\r\n",
		Session: json.RawMessage(`{}`),
	}))
	require.NoError(t, ValidateLiveCallRequest(&LiveCallRequest{
		SDP:     "v=0\r\n",
		Session: json.RawMessage(`{"model":"gpt-realtime","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"Model":{"type":"string"}}}}]}`),
	}))

	for _, rawSession := range []string{
		`{"model":"cheap","model":"expensive"}`,
		`{"model":"cheap","Model":"expensive"}`,
		`{"model":"gpt-realtime","tools":[{"type":"image_generation","Model":"gpt-image-2"}]}`,
	} {
		err := ValidateLiveCallRequest(&LiveCallRequest{
			SDP:     "v=0\r\n",
			Session: json.RawMessage(rawSession),
		})
		require.Error(t, err, rawSession)
	}
}

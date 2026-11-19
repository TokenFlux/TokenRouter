package live

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// stubLiveResolver maps any supplied model to fixed routing and upstream names
// for RewriteClientPayload unit tests.
type stubLiveResolver struct{}

// ResolveModel returns deterministic group and upstream names for keyTarget.
func (stubLiveResolver) ResolveModel(_ context.Context, _ *int64, keyTarget string) (string, string, error) {
	return keyTarget, "upstream-" + keyTarget, nil
}

// TestRewriteClientPayloadRejectsDuplicateAndCaseVariantSessionFields submits
// canonical and ambiguous session.update frames to the Live client rewriter.
// Ambiguous payloads must fail instead of rewriting only the first session.model.
func TestRewriteClientPayloadRejectsDuplicateAndCaseVariantSessionFields(t *testing.T) {
	record := &session.LiveCallRecord{
		GroupID:            7,
		Model:              "gpt-realtime",
		RequestedModel:     "alias-live",
		APIKeyModelMapping: map[string]string{"alias-live": "gpt-realtime"},
	}

	rewritten, clientModel, _, err := RewriteClientPayload(
		context.Background(),
		record,
		stubLiveResolver{},
		[]byte(`{"type":"session.update","session":{"model":"alias-live"}}`),
	)
	require.NoError(t, err)
	require.Equal(t, "alias-live", clientModel)
	require.Contains(t, string(rewritten), `"upstream-gpt-realtime"`)

	for _, bad := range []string{
		`{"type":"session.update","session":{"model":"alias-live","model":"forbidden-model"}}`,
		`{"type":"session.update","session":{"model":"alias-live","Model":"forbidden-model"}}`,
		`{"type":"session.update","Session":{"model":"forbidden-model"}}`,
	} {
		out, _, _, err := RewriteClientPayload(context.Background(), record, stubLiveResolver{}, []byte(bad))
		require.Error(t, err, bad)
		require.Equal(t, bad, string(out))
	}
}

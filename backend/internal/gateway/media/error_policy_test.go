package media

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMediaErrorPoliciesPreserveLazySideEffectsAndPriority(t *testing.T) {
	t.Run("embedding invalid input", func(t *testing.T) {
		var order []string
		err := ResolveEmbeddingFailure(400, EmbeddingFailurePorts{InvalidRequest: func() bool { return true }, Forward: func() { order = append(order, "forward") }, ApplyPolicy: func() { t.Fatal("invalid request must not write account health") }})
		require.EqualError(t, err, "upstream invalid request: 400")
		require.Equal(t, []string{"forward"}, order)
	})
	t.Run("grok content wins over custom generic", func(t *testing.T) {
		var response ErrorResponse
		err := ResolveGrokFailure(403, "upstream", GrokFailurePorts{ContentRejection: func() (bool, string) { return true, "policy" }, Observe: func(kind, message string) { require.Equal(t, "http_error", kind); require.Equal(t, "policy", message) }, Write: func(v ErrorResponse) { response = v }, Generic: func() bool { t.Fatal("content rejection must precede generic policy"); return true }})
		require.EqualError(t, err, "grok content policy rejection: policy")
		require.Equal(t, 403, response.Status)
	})
	t.Run("alpha 401 leaves health alone", func(t *testing.T) {
		var prepared bool
		expected := errors.New("typed failure")
		err := ResolveAlphaFailure(401, AlphaFailurePorts{Failover: func() bool { return true }, Prepare: func() { prepared = true }, ApplySideEffects: func() bool { t.Fatal("401 tool rejection is not global credential failure"); return false }, NewFailover: func(disabled bool) error { require.True(t, prepared); require.False(t, disabled); return expected }})
		require.ErrorIs(t, err, expected)
	})
	t.Run("image auth recovery never enters ordinary policy", func(t *testing.T) {
		retry, err := ResolveImageFailure(ImageFailurePorts{Recover: func() (bool, error) { return true, nil }, Failover: func() bool { t.Fatal("already recovered"); return true }})
		require.True(t, retry)
		require.NoError(t, err)
	})
}

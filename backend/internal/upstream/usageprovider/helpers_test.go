package usageprovider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// helpers_test.go provides shared JSON encoding helpers for usageprovider unit
// tests.

// mustJSONMarshal encodes a test value and fails the test when encoding fails.
// It marks itself as a helper so serialization assertions report the calling
// fixture rather than the wrapper.
func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

//go:build unit

package antigravity

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAntigravityAccountSwitchError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedOK    bool
		expectedID    int64
		expectedModel string
	}{
		{
			name:       "nil error",
			err:        nil,
			expectedOK: false,
		},
		{
			name:       "generic error",
			err:        fmt.Errorf("some error"),
			expectedOK: false,
		},
		{
			name: "account switch error",
			err: &AntigravityAccountSwitchError{
				OriginalAccountID: 123,
				RateLimitedModel:  "claude-sonnet-4-5",
				IsStickySession:   true,
			},
			expectedOK:    true,
			expectedID:    123,
			expectedModel: "claude-sonnet-4-5",
		},
		{
			name: "wrapped account switch error",
			err: fmt.Errorf("wrapped: %w", &AntigravityAccountSwitchError{
				OriginalAccountID: 456,
				RateLimitedModel:  "gemini-3-flash",
				IsStickySession:   false,
			}),
			expectedOK:    true,
			expectedID:    456,
			expectedModel: "gemini-3-flash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switchErr, ok := IsAntigravityAccountSwitchError(tt.err)
			require.Equal(t, tt.expectedOK, ok)
			if tt.expectedOK {
				require.NotNil(t, switchErr)
				require.Equal(t, tt.expectedID, switchErr.OriginalAccountID)
				require.Equal(t, tt.expectedModel, switchErr.RateLimitedModel)
			} else {
				require.Nil(t, switchErr)
			}
		})
	}
}

func TestAntigravityAccountSwitchError_Error(t *testing.T) {
	err := &AntigravityAccountSwitchError{
		OriginalAccountID: 789,
		RateLimitedModel:  "claude-opus-4-5",
		IsStickySession:   true,
	}
	msg := err.Error()
	require.Contains(t, msg, "789")
	require.Contains(t, msg, "claude-opus-4-5")
}

func TestNormalizeAntigravityModelName(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{
			name:     "plain model name",
			model:    "gemini-1.5-pro",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "models/ prefix",
			model:    "models/gemini-1.5-pro",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "publishers/google/models/ prefix",
			model:    "publishers/google/models/gemini-1.5-pro",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "projects/.../publishers/google/models/ path",
			model:    "projects/my-proj/locations/us-central1/publishers/google/models/gemini-2.5-flash",
			expected: "gemini-2.5-flash",
		},
		{
			name:     "publishers/anthropic/models/ prefix",
			model:    "publishers/anthropic/models/claude-sonnet-4-5",
			expected: "claude-sonnet-4-5",
		},
		{
			name:     "projects/.../publishers/anthropic/models/ path",
			model:    "projects/my-proj/locations/global/publishers/anthropic/models/claude-sonnet-4-5",
			expected: "claude-sonnet-4-5",
		},
		{
			name:     "mixed case and spaces",
			model:    "  Models/Gemini-1.5-Pro  ",
			expected: "gemini-1.5-pro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := NormalizeAntigravityModelName(tt.model)
			require.Equal(t, tt.expected, actual)
		})
	}
}

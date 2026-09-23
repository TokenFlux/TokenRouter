//go:build unit

package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/stretchr/testify/require"
)

func TestAccount_IsInterceptWarmupEnabled(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
		expected    bool
	}{
		{
			name:        "nil credentials",
			credentials: nil,
			expected:    false,
		},
		{
			name:        "empty map",
			credentials: map[string]any{},
			expected:    false,
		},
		{
			name:        "field not present",
			credentials: map[string]any{"access_token": "tok"},
			expected:    false,
		},
		{
			name:        "field is true",
			credentials: map[string]any{"intercept_warmup_requests": true},
			expected:    true,
		},
		{
			name:        "field is false",
			credentials: map[string]any{"intercept_warmup_requests": false},
			expected:    false,
		},
		{
			name:        "field is string true",
			credentials: map[string]any{"intercept_warmup_requests": "true"},
			expected:    false,
		},
		{
			name:        "field is int 1",
			credentials: map[string]any{"intercept_warmup_requests": 1},
			expected:    false,
		},
		{
			name:        "field is nil",
			credentials: map[string]any{"intercept_warmup_requests": nil},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &accountcore.Record{Credentials: tt.credentials}
			result := a.IsInterceptWarmupEnabled()
			require.Equal(t, tt.expected, result)
		})
	}
}

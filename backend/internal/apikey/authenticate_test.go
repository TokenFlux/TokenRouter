package apikey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// authenticate_test.go verifies that APIKeyService.Authenticate copies the
// stored force_claude_1h_cache flag from active APIKey records into the
// returned AccessSnapshot. Authentication snapshots carry Key-level gateway
// policies so downstream forwarding reads the configured cache lifetime without
// querying storage again.

// TestAPIKeyService_Authenticate_CopiesForceClaude1hCache authenticates active
// credentials with disabled and enabled Claude one-hour cache flags. It checks
// the returned access snapshot exposes each credential's configured override.
func TestAPIKeyService_Authenticate_CopiesForceClaude1hCache(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		repo := &authRepoStub{
			getByKeyForAuth: func(_ context.Context, key string) (*APIKey, error) {
				return &APIKey{
					ID:                 21,
					UserID:             3,
					Key:                key,
					Status:             StatusActive,
					ForceClaude1hCache: enabled,
					User: &User{
						ID:     3,
						Status: StatusActive,
					},
				}, nil
			},
		}
		svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, &Options{})
		svc.Start()
		t.Cleanup(svc.Stop)

		access, err := svc.Authenticate(context.Background(), "sk-auth-claude-cache", AuthenticationInput{})
		require.NoError(t, err)
		require.NotNil(t, access)
		require.Equal(t, enabled, access.ForceClaude1hCache())
	}
}

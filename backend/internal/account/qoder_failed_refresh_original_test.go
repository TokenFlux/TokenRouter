package account

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

func cloneQoderFailedCredentials(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if nested, ok := value.(map[string]any); ok {
			dst[key] = cloneQoderFailedCredentials(nested)
			continue
		}
		dst[key] = value
	}
	return dst
}

func TestQoderGatewayRefreshExecutorNeedsRefreshUsesFailedCredentialSnapshot(t *testing.T) {
	now := time.Now()
	failedAccount := Record{
		ID:       96,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "failed-token",
			"refresh_token":        "failed-refresh",
			"machine_id":           "machine-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	rotatedAccount := failedAccount
	rotatedAccount.Credentials = cloneQoderFailedCredentials(failedAccount.Credentials)
	rotatedAccount.Credentials["security_oauth_token"] = "rotated-token"
	rotatedAccount.Credentials["refresh_token"] = "rotated-refresh"

	failedCredentials := QoderRefreshCredentialsHash(failedAccount.Credentials)

	require.True(t, NeedsRefreshQoderAfterFailure(&failedAccount, failedCredentials, 15*time.Minute))

	failedWithoutExpiry := failedAccount
	failedWithoutExpiry.Credentials = cloneQoderFailedCredentials(failedAccount.Credentials)
	delete(failedWithoutExpiry.Credentials, "expires_at")
	failedCredentials = QoderRefreshCredentialsHash(failedWithoutExpiry.Credentials)
	require.True(t, NeedsRefreshQoderAfterFailure(&failedWithoutExpiry, failedCredentials, 15*time.Minute))

	changedMapping := failedAccount
	changedMapping.Credentials = cloneQoderFailedCredentials(failedAccount.Credentials)
	changedMapping.Credentials["model_mapping"] = map[string]any{"qwen3.7-plus": "qmodel"}
	failedCredentials = QoderRefreshCredentialsHash(failedAccount.Credentials)
	require.True(t, NeedsRefreshQoderAfterFailure(&changedMapping, failedCredentials, 15*time.Minute))

	failedCredentials = QoderRefreshCredentialsHash(failedAccount.Credentials)
	require.False(t, NeedsRefreshQoderAfterFailure(&rotatedAccount, failedCredentials, 15*time.Minute))

	missingRefreshToken := failedAccount
	missingRefreshToken.Credentials = cloneQoderFailedCredentials(failedAccount.Credentials)
	delete(missingRefreshToken.Credentials, "refresh_token")
	require.False(t, NeedsRefreshQoderAfterFailure(&missingRefreshToken, failedCredentials, 15*time.Minute))
	require.False(t, NeedsRefreshQoderAfterFailure(&failedAccount, "", 15*time.Minute))
}

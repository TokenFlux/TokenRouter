package account

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 成功用量查询的迟到恢复只能清理本轮观察到的错误身份。
type usageRecoveryRaceRepo struct {
	OAuthUsageReader
	current *Record
	writes  int
}

func (r *usageRecoveryRaceRepo) ClearError(context.Context, int64) error {
	r.writes++
	r.current.Status = StatusActive
	r.current.ErrorMessage = ""
	return nil
}
func TestUsageRecoveryCannotClearNewAdministratorState(t *testing.T) {
	for _, change := range []string{"none", "name", "credentials", "status", "error", "proxy"} {
		t.Run(change, func(t *testing.T) {
			observed := &Record{ID: 721, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: StatusError, ErrorMessage: "token refresh failed", Credentials: map[string]any{"refresh_token": "observed"}}
			current := *observed
			switch change {
			case "name":
				current.Name = "renamed"
			case "credentials":
				current.Credentials = map[string]any{"refresh_token": "administrator"}
			case "status":
				current.Status = StatusDisabled
			case "error":
				current.ErrorMessage = "administrator forbidden"
			case "proxy":
				id := int64(8)
				current.ProxyID = &id
			}
			beforeStatus, beforeError := current.Status, current.ErrorMessage
			repo := &usageRecoveryRaceRepo{current: &current}
			_, err := RecoverUsageAccountError(context.Background(), observed, repo)
			require.NoError(t, err)
			if change == "none" || change == "name" {
				require.Equal(t, 1, repo.writes)
				require.Equal(t, StatusActive, current.Status)
				return
			}
			require.Zero(t, repo.writes)
			require.Equal(t, beforeStatus, current.Status)
			require.Equal(t, beforeError, current.ErrorMessage)
			require.Equal(t, StatusError, observed.Status)
		})
	}
}

//go:build unit

package account_test

import (
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccountIsSchedulable_QuotaExceeded(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		account *accountcore.Record
		want    bool
	}{
		{
			name: "apikey daily quota exceeded",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: false,
		},
		{
			name: "apikey weekly quota exceeded",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_weekly_limit": 50.0,
					"quota_weekly_used":  50.0,
					"quota_weekly_start": now.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
			want: false,
		},
		{
			name: "apikey total quota exceeded",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_limit": 100.0,
					"quota_used":  100.0,
				},
			},
			want: false,
		},
		{
			name: "apikey quota not exceeded",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  5.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "apikey expired daily period restores schedulable",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-25 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "oauth ignores quota exceeded",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeOAuth,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "bedrock quota exceeded",
			account: &accountcore.Record{LoadLocation: time.LoadLocation,
				Status:      accountcore.StatusActive,
				Schedulable: true,
				Type:        capability.AccountTypeBedrock,
				Extra: map[string]any{
					"quota_limit": 200.0,
					"quota_used":  200.0,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.IsSchedulable())
		})
	}
}

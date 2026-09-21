//go:build integration

package app_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 通过真实组合根绑定 PostgreSQL；旧装配壳没有仓储，所有观测直接走原生入口。
func TestS16NativeAccountHealthAssembly(t *testing.T) {
	f := newDatabaseFixture(t)
	store := accountpostgres.NewAccountStore(f.client, f.db, accountpostgres.AccountStoreOptions{})
	cfg := &config.Config{}
	assembly := service.NewRateLimitService(nil, nil, cfg, nil, nil)
	app.NewS16AccountRecovery(store, nil, assembly, nil, cfg, nil, nil, nil, nil, nil)
	observer := assembly.UpstreamHealth()
	require.Same(t, observer, assembly.UpstreamHealth())
	require.Same(t, observer.Core, assembly.HealthCore())
	require.Same(t, observer.Team, assembly.TeamLinkedHealth())
	require.Same(t, observer.Limits, assembly.RateLimitObserver())

	t.Run("anthropic window", func(t *testing.T) {
		row, err := f.client.Account.Create().SetName("s16-health-window").SetPlatform(account.PlatformAnthropic).SetType(account.AccountTypeOAuth).Save(t.Context())
		require.NoError(t, err)
		value, err := store.GetByID(t.Context(), row.ID)
		require.NoError(t, err)
		reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
		headers := make(http.Header)
		headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.02")
		headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset.Unix(), 10))
		observer.ApplyUpstreamError(t.Context(), value, accountprovider.HealthObservation{Status: 429, Headers: headers, Body: []byte("rate limited")})
		stored, err := store.GetByID(t.Context(), row.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.RateLimitResetAt)
		require.Equal(t, reset.Unix(), stored.RateLimitResetAt.Unix())
		require.NotNil(t, stored.SessionWindowEnd)
		require.Equal(t, reset.Unix(), stored.SessionWindowEnd.Unix())
		require.Equal(t, "rejected", stored.SessionWindowStatus)
	})

	t.Run("image scope", func(t *testing.T) {
		row, err := f.client.Account.Create().SetName("s16-health-image").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeAPIKey).Save(t.Context())
		require.NoError(t, err)
		value, err := store.GetByID(t.Context(), row.ID)
		require.NoError(t, err)
		require.True(t, accountprovider.ObserveOpenAIImageRateLimit(t.Context(), observer.Core, value, 429, http.Header{}, []byte(`{"error":{"message":"Rate limit reached for gpt-image. Please try again in 2s."}}`)))
		stored, err := store.GetByID(t.Context(), row.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.ModelRateLimitResetAt(account.OpenAIImageGenerationRateLimitKey))
		require.Nil(t, stored.RateLimitResetAt)
	})

	t.Run("shadow credential owner", func(t *testing.T) {
		parent, err := f.client.Account.Create().SetName("s16-health-parent").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeOAuth).SetCredentials(map[string]any{"refresh_token": "fixture-refresh"}).Save(t.Context())
		require.NoError(t, err)
		shadow, err := f.client.Account.Create().SetName("s16-health-shadow").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeOAuth).SetParentAccountID(parent.ID).SetQuotaDimension(account.QuotaDimensionSpark).Save(t.Context())
		require.NoError(t, err)
		value, err := store.GetByID(t.Context(), shadow.ID)
		require.NoError(t, err)
		result := observer.ApplyUpstreamError(t.Context(), value, accountprovider.HealthObservation{Status: 401, Body: []byte("unauthorized")})
		require.True(t, result.StopScheduling)
		owner, err := store.GetByID(t.Context(), parent.ID)
		require.NoError(t, err)
		stored, err := store.GetByID(t.Context(), shadow.ID)
		require.NoError(t, err)
		require.NotNil(t, owner.TempUnschedulableUntil)
		require.Equal(t, account.StatusActive, stored.Status)
		require.Nil(t, stored.TempUnschedulableUntil)
		require.Equal(t, "fixture-refresh", owner.GetCredential("refresh_token"))
	})
}

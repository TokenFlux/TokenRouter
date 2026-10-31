//go:build integration

package app

import (
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// plugin_read_privacy_scenario_integration_test.go covers plugin_queries.go and
// identity's exact-profile and binding storage together. Deliberately sensitive
// account fields and upstream metadata verify the complete read projection rather
// than relying only on a protocol struct's declared field list.

// TestPluginIdentityProjectionKeepsOnlyApprovedMetadata seeds an email identity and an upstream identity containing extra credentials and profile claims.
// Only the third-party subject and standardized display hints leave the host, and deleted accounts cannot expose their retained bindings.
func TestPluginIdentityProjectionKeepsOnlyApprovedMetadata(t *testing.T) {
	db := postgrescontainer.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	account, err := client.User.Create().SetEmail("private-profile@example.invalid").SetPasswordHash("private-password-hash").SetUsername("Public name").SetNotes("private-administrator-note").Save(t.Context())
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().SetUserID(account.ID).SetProviderType("email").SetProviderKey("email").SetProviderSubject(account.Email).Save(t.Context())
	require.NoError(t, err)
	verified := time.Now().UTC().Truncate(time.Second)
	_, err = client.AuthIdentity.Create().SetUserID(account.ID).SetProviderType("oidc").SetProviderKey("oidc-main").SetProviderSubject("opaque-subject").SetVerifiedAt(verified).SetMetadata(map[string]any{
		"username":     "upstream-user",
		"display_name": "Public display",
		"email":        "hidden-email@example.invalid",
		"access_token": "private-access-token",
		"nested":       map[string]any{"secret": "private-nested"},
	}).Save(t.Context())
	require.NoError(t, err)
	users := identity.NewUserService(identitypostgres.NewUserStore(client, db), nil, nil, nil, nil)
	query := &pluginUserQueries{service: users}
	values, err := query.Identities(t.Context(), account.ID)
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, "oidc", values[0].ProviderType)
	require.Equal(t, "opaque-subject", values[0].Subject)
	require.Equal(t, "upstream-user", values[0].Username)
	require.Equal(t, "Public display", values[0].DisplayName)
	require.Equal(t, verified.Unix(), values[0].VerifiedUnix)
	data, err := json.Marshal(values)
	require.NoError(t, err)
	for _, secret := range []string{"private-", "hidden-email", "access_token", "nested", account.Email} {
		require.NotContains(t, string(data), secret)
	}
	profile, err := plugin.NewQueries(plugin.QueryReaders{Users: query}, nil).User(t.Context(), plugin.Binding{PluginID: "test.privacy", Permissions: []string{"user.read"}}, account.ID)
	require.NoError(t, err)
	require.Empty(t, profile.Email)
	data, err = json.Marshal(profile)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-")
	require.NoError(t, client.User.UpdateOneID(account.ID).SetDeletedAt(time.Now()).Exec(t.Context()))
	_, err = query.Identities(t.Context(), account.ID)
	require.ErrorIs(t, err, plugin.ErrHostUserNotFound)
}

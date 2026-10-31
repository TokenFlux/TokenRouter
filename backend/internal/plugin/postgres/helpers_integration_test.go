//go:build integration

package postgres

import (
	"database/sql"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// persistenceFixture creates an isolated migrated database and its shared Ent-backed store.
// Cleanup closes the application connection before the suite drops this database;
// no user database or deployment credentials participate in these persistence checks.
func persistenceFixture(t *testing.T) (*Store, *ent.Client, *sql.DB) {
	t.Helper()
	db := pluginDatabaseSuite.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return New(client), client, db
}

// storedInstallation constructs complete current and rollback metadata for persistence assertions.
func storedInstallation(t *testing.T) *plugin.Installation {
	t.Helper()
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"publisher":{"name":"Test"},"license":"MIT","frontend":{"entry":"frontend/index.js","pages":[]}}`)
	declaration, err := manifest.Parse(raw)
	require.NoError(t, err)
	return &plugin.Installation{
		PluginID: "test.plugin",
		Enabled:  true,
		VersionSnapshot: plugin.VersionSnapshot{
			Version:            "1.0.0",
			Source:             plugin.SourceUpload,
			Manifest:           declaration,
			ManifestJSON:       raw,
			ManifestSHA256:     strings.Repeat("a", 64),
			Config:             map[string]any{"label": "current"},
			SecretConfig:       "encrypted-current",
			GrantedPermissions: []string{"wallet.credit"},
		},
	}
}

//go:build integration

package postgres

import (
	"os"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// pluginDatabaseSuite shares one migrated PostgreSQL template while each persistence test owns an isolated clone with real transaction behavior.
var pluginDatabaseSuite postgrescontainer.Suite

// TestMain closes the shared PostgreSQL container after every test and its cleanup finish.
// Cleanup failure changes the exit status instead of leaving successful assertions with leaked resources.
func TestMain(m *testing.M) { os.Exit(pluginDatabaseSuite.Run(m)) }

//go:build integration

package ops_test

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// dashboardLatencySuite migrates one template database and gives each dashboard
// latency scenario its own clone for database-side delay and cancellation.
var (
	dashboardLatencySuite postgrescontainer.Suite
)

// TestMain selects Gin's test mode and runs the migrated dashboard fixtures.
// The suite closes its shared PostgreSQL container before returning an exit status.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(dashboardLatencySuite.Run(m))
}

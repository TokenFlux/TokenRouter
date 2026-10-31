//go:build integration

package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

var (
	// databaseSuite and installationBinary bind this test process to its isolated migrated database template and freshly compiled synthetic plugin executable.
	databaseSuite      postgrescontainer.Suite
	installationBinary string
)

// TestMain builds the synthetic plugin before running real installation and process scenarios.
// The runner owns temporary binaries and container cleanup, so all resources are removed before the final exit code is returned.
func TestMain(m *testing.M) { os.Exit(runInstallationTests(m)) }

// runInstallationTests compiles the real SDK fixture and closes owned resources afterward.
// Build failures stop the suite before any PostgreSQL container is needed. Database
// cleanup remains owned by Suite.Run after all per-test process cleanup completes.
func runInstallationTests(m *testing.M) int {
	directory, err := os.MkdirTemp("", "tokenrouter-installation-fixture-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(directory) }()
	installationBinary = filepath.Join(directory, "plugin")
	if runtime.GOOS == "windows" {
		installationBinary += ".exe"
	}
	command := exec.Command("go", "build", "-o", installationBinary, "./testdata/installation")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build installation fixture: %v\n%s", err, output)
		return 1
	}
	return databaseSuite.Run(m)
}

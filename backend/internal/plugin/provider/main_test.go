package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Runtime tests share freshly compiled fixture executables, keeping process assertions independent of installed packages or user-owned application binaries.
var fixtureBinaries map[string]string

// TestMain builds isolated SDK fixture executables before runtime assertions.
// Its helper owns temporary files and cleanup, so test failures and setup
// failures cannot leave compiled fixture artifacts in the repository.
func TestMain(m *testing.M) { os.Exit(runRuntimeTests(m)) }

// runRuntimeTests builds SDK fault fixtures and an isolated parent-death host.
// Cleanup runs before returning to TestMain. Build diagnostics identify fixture
// failures without executing any application server or loading deployment credentials.
func runRuntimeTests(m *testing.M) int {
	directory, err := os.MkdirTemp("", "tokenrouter-plugin-tests-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(directory) }()
	root, err := filepath.Abs("../../../../plugin-sdk/go")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fixtureBinaries = make(map[string]string)
	for _, name := range []string{"crashy", "slowstart", "envdump"} {
		binary := filepath.Join(directory, name)
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		command := exec.Command("go", "build", "-o", binary, "./internal/testplugins/"+name)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", name, err, output)
			return 1
		}
		fixtureBinaries[name] = binary
	}
	binary := filepath.Join(directory, "hostdeath")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.Command("go", "build", "-o", binary, "./testdata/hostdeath")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build hostdeath: %v\n%s", err, output)
		return 1
	}
	fixtureBinaries["hostdeath"] = binary
	binary = filepath.Join(directory, "configuration")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command = exec.Command("go", "build", "-o", binary, "./testdata/configuration")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build configuration: %v\n%s", err, output)
		return 1
	}
	fixtureBinaries["configuration"] = binary
	fixtureBinaries["nowatcher"] = binary
	return m.Run()
}

package logger_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	legacy "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// TestCompatibilitySharesBackendAndCaller 验证新旧入口共享后端，且不会把 caller 误定位到兼容包装。
func TestCompatibilitySharesBackendAndCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caller.json")
	err := legacy.Init(legacy.InitOptions{
		Level:  "info",
		Format: "json",
		Caller: true,
		Output: legacy.OutputOptions{ToFile: true, FilePath: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.L() != logging.L() {
		t.Fatal("new and legacy loggers must share the same backend")
	}
	legacy.LegacyPrintf("compatibility", "caller marker")
	legacy.Sync()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "compatibility_test.go") {
		t.Fatalf("caller must point at the consumer: %s", content)
	}
}

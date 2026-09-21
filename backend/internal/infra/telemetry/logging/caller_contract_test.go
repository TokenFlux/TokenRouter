package logging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// TestLegacyPrintfCaller 验证日志调用者仍指向实际消费文件。
func TestLegacyPrintfCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caller.json")
	err := logging.Init(logging.InitOptions{
		Level:  "info",
		Format: "json",
		Caller: true,
		Output: logging.OutputOptions{ToFile: true, FilePath: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	logging.LegacyPrintf("compatibility", "caller marker")
	logging.Sync()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "caller_contract_test.go") {
		t.Fatalf("caller must point at the consumer: %s", content)
	}
}

package requestdebug

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCaptureOnlyForRetainedDiagnosticOrEnabledLogging disables both capture
// switches before enabling retention on the same Trace. The retained line must
// redact the Bearer secret while the disabled call returns an empty string.
func TestCaptureOnlyForRetainedDiagnosticOrEnabledLogging(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "https://example.test/v1/messages", nil)
	request.Header.Set("Authorization", "Bearer fixture-secret")
	trace := New("", false)
	if line := trace.Capture(request, nil, nil, "apikey", false, false); line != "" {
		t.Fatalf("关闭诊断时不应生成快照：%s", line)
	}
	line := trace.Capture(request, nil, nil, "oauth", true, true)
	if line == "" || !strings.Contains(line, "[redacted]") || strings.Contains(line, "fixture-secret") {
		t.Fatalf("保留的诊断必须包含脱敏后的身份：%s", line)
	}
}

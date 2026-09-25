package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerInMemoryLogSink struct {
	mu     sync.Mutex
	events []*logging.LogEvent
}

func (s *handlerInMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		for k, v := range event.Fields {
			cloned.Fields[k] = v
		}
	}
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

func (s *handlerInMemoryLogSink) ContainsMessageAtLevel(substr, level string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	wantLevel := strings.ToLower(strings.TrimSpace(level))
	for _, ev := range s.events {
		if ev == nil {
			continue
		}
		if strings.Contains(ev.Message, substr) && strings.ToLower(strings.TrimSpace(ev.Level)) == wantLevel {
			return true
		}
	}
	return false
}

func (s *handlerInMemoryLogSink) ContainsFieldValue(field, substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev == nil || ev.Fields == nil {
			continue
		}
		if v, ok := ev.Fields[field]; ok && strings.Contains(fmt.Sprint(v), substr) {
			return true
		}
	}
	return false
}

func (s *handlerInMemoryLogSink) FieldValueForMessage(message, field string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event == nil || event.Message != message || event.Fields == nil {
			continue
		}
		if value, ok := event.Fields[field]; ok {
			return value, true
		}
	}
	return nil, false
}

func captureHandlerStructuredLog(t *testing.T) (*handlerInMemoryLogSink, func()) {
	t.Helper()
	handlerStructuredLogCaptureMu.Lock()

	err := logging.Init(logging.InitOptions{
		Level:       "debug",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
		Sampling: logging.SamplingOptions{Enabled: false},
	})
	require.NoError(t, err)

	sink := &handlerInMemoryLogSink{}
	logging.SetSink(sink)
	return sink, func() {
		logging.SetSink(nil)
		handlerStructuredLogCaptureMu.Unlock()
	}
}

func TestIsOpenAILegacyCompactPath(t *testing.T) {
	require.False(t, IsOpenAIResponsesCompactPath(nil))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	require.True(t, IsOpenAIResponsesCompactPath(c))

	c.Request = httptest.NewRequest(http.MethodPost, "/responses/compact/", nil)
	require.True(t, IsOpenAIResponsesCompactPath(c))

	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact/detail", nil)
	require.True(t, IsOpenAIResponsesCompactPath(c))

	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.False(t, IsOpenAIResponsesCompactPath(c))
}

func TestLogOpenAIRemoteCompactOutcome_Succeeded(t *testing.T) {

	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.125.0")
	c.Set(OpsModelKey, "gpt-5.3-codex")
	c.Set(OpsAccountIDKey, int64(123))
	c.Header("x-request-id", "rid-compact-ok")
	c.Status(http.StatusOK)

	h := &OpenAITextHandler{}
	h.LogRemoteCompactOutcome(c, time.Now().Add(-8*time.Millisecond))

	require.True(t, logSink.ContainsMessageAtLevel("codex.remote_compact.succeeded", "info"))
	require.True(t, logSink.ContainsFieldValue("compact_outcome", "succeeded"))
	require.True(t, logSink.ContainsFieldValue("status_code", "200"))
	require.True(t, logSink.ContainsFieldValue("path", "/v1/responses/compact"))
	require.True(t, logSink.ContainsFieldValue("request_model", "gpt-5.3-codex"))
	require.True(t, logSink.ContainsFieldValue("account_id", "123"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_id", "rid-compact-ok"))
}

func TestLogOpenAIRemoteCompactOutcome_Failed(t *testing.T) {

	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/responses/compact", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.125.0")
	c.Status(http.StatusBadGateway)

	h := &OpenAITextHandler{}
	h.LogRemoteCompactOutcome(c, time.Now())

	require.True(t, logSink.ContainsMessageAtLevel("codex.remote_compact.failed", "warn"))
	require.True(t, logSink.ContainsFieldValue("compact_outcome", "failed"))
	require.True(t, logSink.ContainsFieldValue("status_code", "502"))
	require.True(t, logSink.ContainsFieldValue("path", "/responses/compact"))
}

func TestLogOpenAIRemoteCompactOutcome_NonCompactSkips(t *testing.T) {

	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Status(http.StatusOK)

	h := &OpenAITextHandler{}
	h.LogRemoteCompactOutcome(c, time.Now())

	require.False(t, logSink.ContainsMessageAtLevel("codex.remote_compact.succeeded", "info"))
	require.False(t, logSink.ContainsMessageAtLevel("codex.remote_compact.failed", "warn"))
}

var handlerStructuredLogCaptureMu sync.Mutex

// ContainsMessage 保留跨入口日志合同的子串匹配，不附加级别条件。
func (s *handlerInMemoryLogSink) ContainsMessage(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event != nil && strings.Contains(event.Message, substr) {
			return true
		}
	}
	return false
}

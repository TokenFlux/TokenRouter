package googleforward_test

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// structuredLogCaptureMu serializes fixtures that replace the process logger
// sink so one test restore cannot remove another test capture.
var structuredLogCaptureMu sync.Mutex

// inMemoryLogSink records shallow-copied structured events for response
// diagnostic assertions.
type inMemoryLogSink struct {
	// mu protects events during append and message scanning. WriteLogEvent
	// copies the outer event and field map before publication; mutable
	// values within that map remain shared.
	mu     sync.Mutex          // event collection access mutex
	events []*logging.LogEvent // captured structured event pointers
}

// TestHandleNativeNonStreamingResponse_FeedsImageCounter reads a native
// response containing an inline image under a custom alias. The attempt counter
// and returned count selection both recognize that observed image.
func TestHandleNativeNonStreamingResponse_FeedsImageCounter(t *testing.T) {
	c := newGeminiImageTestContext(t)
	c.Images = 0

	body := geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{"Content-Type": []string{"application/json"}},

		Body: io.NopCloser(strings.NewReader(body)),
	}

	svc := newGeminiFixture(geminiDependencies{})
	usage, err := googleforward.GeminiResponseForTest(svc, c, session.GeminiToolScope{}).HandleNativeNonStreamingResponse(upstream.NewOutputContext(c.Sink()), resp, false)
	require.NoError(t, err)
	require.NotNil(t, usage)

	require.Equal(t, 1, c.Images)
	require.Equal(t, 1, c.ImageCountForTest("nana-banana-2", "nana-banana-2"))
}

// TestGeminiHandleNativeNonStreamingResponse_DebugDisabledDoesNotEmitHeaderLogs
// reads native JSON with response headers while debug output is disabled. The
// body is presented successfully and captured structured messages contain no
// Gemini header-log prefix.
func TestGeminiHandleNativeNonStreamingResponse_DebugDisabledDoesNotEmitHeaderLogs(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	svc := newGeminiFixture(geminiDependencies{
		cfg: &googleforward.Options{DebugHeaders: false},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,

		Header: http.Header{
			"Content-Type":      []string{"application/json"},
			"X-RateLimit-Limit": []string{"60"},
		},

		Body: io.NopCloser(strings.NewReader(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}`)),
	}

	output := gatewayhttp.NewGoogleBoundary(c, svc.Options, false)
	adapter := googleforward.GeminiResponseForTest(svc, &googleforward.AttemptForTest{Output: output}, session.GeminiToolScope{})
	usage, err := adapter.HandleNativeNonStreamingResponse(upstream.NewOutputContext(output.Sink()), resp, false)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotEmpty(t, w.Body.Bytes())
	require.False(t, logSink.ContainsMessage("[GeminiAPI]"), "debug 关闭时不应输出 Gemini 响应头日志")
}

// WriteLogEvent copies a nonnil event and its outer field map before appending
// under the mutex. Callers keep the source stable during copying; nested
// mutable field values remain shared.
func (s *inMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		maps.Copy(cloned.Fields, event.Fields)
	}
	// Publish the copied event under the same mutex used by message lookup
	// so assertions scan a stable collection while logging can append
	// another observation.
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

// ContainsMessage scans captured nonnil events under the collection mutex for a
// substring. The lock pairs with append, letting assertions use one stable
// event collection during each search.
func (s *inMemoryLogSink) ContainsMessage(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev != nil && strings.Contains(ev.Message, substr) {
			return true
		}
	}
	return false
}

// captureStructuredLog installs a process logger sink and returns it with a
// restore callback. Successful callers invoke restore after synchronous capture
// assertions; the callback clears the sink before releasing the package mutex.
//
// The mutex stays held across initialization, sink publication, and the
// caller's observations so another capture cannot replace that sink before this
// test clears it. Initialization failure ends the test before a restore
// callback is returned. Captured events use their separate collection mutex for
// append and lookup while this outer mutex governs ownership of the process
// sink.
func captureStructuredLog(t *testing.T) (*inMemoryLogSink, func()) {
	t.Helper()
	// Hold the package capture mutex until the returned restore clears the
	// process sink, preventing overlapping fixture ownership of global
	// logging output.
	structuredLogCaptureMu.Lock()

	err := logging.Init(logging.InitOptions{
		Level: "debug",

		Format: "json",

		ServiceName: "tokenrouter",

		Environment: "test",

		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},

		Sampling: logging.SamplingOptions{Enabled: false},
	})
	require.NoError(t, err)

	sink := &inMemoryLogSink{}
	logging.SetSink(sink)
	return sink, func() {
		// Clear this fixture sink before releasing capture ownership so
		// the next test cannot install a sink that this restore would
		// remove.
		logging.SetSink(nil)
		structuredLogCaptureMu.Unlock()
	}
}

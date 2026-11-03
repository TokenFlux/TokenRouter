package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_stream_test.go checks native framing and completion through the
// Antigravity adapter and shared Gemini reader. Fixtures retain copied output
// events so terminal flags, candidate order, and JSON separators can be checked
// without connecting to an upstream provider or weakening production parsing.

// nativeFramingSink captures native response bytes and their detached event metadata.
type nativeFramingSink struct {
	bytes.Buffer // delivered native bytes
	// events copies payload storage so later formatter reuse cannot alter the
	// terminal flags and data inspected after the complete reader has returned.
	events []upstream.OutputEvent
}

// TestCollectClaudeStreamResponsePreservesSizeClassification exceeds the shared
// frame limit before content arrives. Compatibility callers must still recognize
// the scanner-size sentinel while retaining the shared reader's typed failure.
func TestCollectClaudeStreamResponsePreservesSizeClassification(t *testing.T) {
	adapter := ResponseAdapter{Options: ResponseOptions{MaxLineSize: 10}}
	response := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"response\":{}}\n\n"))}
	_, _, err := adapter.CollectClaudeStreamResponse(response, time.Now(), "model")
	require.ErrorIs(t, err, bufio.ErrTooLong)
	var streamError *upstream.GeminiStreamError
	require.ErrorAs(t, err, &streamError)
	require.Equal(t, "stream_frame_too_large", streamError.Kind)
}

// Begin accepts response metadata without producing any native fixture bytes.
func (s *nativeFramingSink) Begin(upstream.OutputHead) error { return nil }

// Emit retains each event independently of the formatter's borrowed payload storage.
// Successful writes append to the fixture buffer, while terminal notifications
// remain visible even when they contain no additional response bytes.
func (s *nativeFramingSink) Emit(event upstream.OutputEvent) error {
	event.Data = bytes.Clone(event.Data)
	s.events = append(s.events, event)
	_, err := s.Write(event.Data)
	return err
}

// TestGeminiNativeStreamFraming unwraps two candidates and a sparse usage tail.
// The native stream must contain complete data-only JSON frames, preserve vendor
// fields, consume DONE, and announce completion only after both candidates finish.
func TestGeminiNativeStreamFraming(t *testing.T) {
	body := "data: {\"response\":{\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"first\"}]},\"finishReason\":\"STOP\"}],\"vendor\":\"retained\",\"usageMetadata\":{\"promptTokenCount\":1}}}\n\n" +
		"data: {\"response\":{\"candidates\":[{\"index\":1,\"content\":{\"parts\":[{\"text\":\"second\"}]},\"finishReason\":\"STOP\"}]}}\n\n" +
		"data: {\"response\":{\"usageMetadata\":{\"candidatesTokenCount\":2}}}\n\ndata: [DONE]\n\n"
	response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	sink := &nativeFramingSink{}
	adapter := ResponseAdapter{Options: ResponseOptions{Candidates: 2}}
	result, err := adapter.HandleGeminiStreamingResponse(upstream.NewOutputContext(sink), response, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.NotContains(t, sink.String(), "\n\n\n")
	require.NotContains(t, sink.String(), "[DONE]")
	require.NotContains(t, sink.String(), "event:")
	require.Contains(t, sink.String(), `"vendor":"retained"`)
	var terminals []bool
	for _, event := range sink.events {
		if len(event.Data) > 0 {
			terminals = append(terminals, event.Terminal)
		}
	}
	require.Equal(t, []bool{false, true, false}, terminals)
	for frame := range strings.SplitSeq(strings.TrimSuffix(sink.String(), "\n\n"), "\n\n") {
		require.True(t, strings.HasPrefix(frame, "data: "), frame)
		require.True(t, json.Valid([]byte(strings.TrimPrefix(frame, "data: "))), frame)
	}
}

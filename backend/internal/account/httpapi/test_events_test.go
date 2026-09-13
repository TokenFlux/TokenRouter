package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 准备与提交两个时机分别保留旧自适应/完整 SSE Header 行为。
func TestTestEventSinkPreservesPreludeAndFrames(t *testing.T) {
	recorder := httptest.NewRecorder()
	sink := NewTestEventSink(recorder)
	require.NoError(t, sink.Begin(context.Background(), false))
	require.False(t, recorder.Flushed)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Empty(t, recorder.Header().Get("Connection"))
	require.NoError(t, sink.Begin(context.Background(), true))
	require.True(t, recorder.Flushed)
	require.Equal(t, "keep-alive", recorder.Header().Get("Connection"))
	require.Equal(t, "no", recorder.Header().Get("X-Accel-Buffering"))
	require.NoError(t, sink.Emit(context.Background(), account.TestEvent{Type: "content", Text: "line\n"}))
	require.Equal(t, "data: {\"type\":\"content\",\"text\":\"line\\n\"}\n\n", recorder.Body.String())
}

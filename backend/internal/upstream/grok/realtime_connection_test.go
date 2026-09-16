package grok

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试侧仅适配真实本地 WS 库，不把 SDK 引入原生帧契约。
type testWSFrames struct {
	conn   *websocket.Conn
	closed *atomic.Int64
}

func (f testWSFrames) ReadFrame(ctx context.Context) (upstream.FrameKind, []byte, error) {
	kind, data, err := f.conn.Read(ctx)
	return upstream.FrameKind(kind), data, err
}
func (f testWSFrames) WriteFrame(ctx context.Context, kind upstream.FrameKind, data []byte) error {
	return f.conn.Write(ctx, websocket.MessageType(kind), data)
}
func (f testWSFrames) Close() error { f.closed.Add(1); return f.conn.CloseNow() }

type testDownFrames struct {
	input  chan []byte
	output chan []byte
}

func (f testDownFrames) ReadFrame(ctx context.Context) (upstream.FrameKind, []byte, error) {
	select {
	case data := <-f.input:
		return upstream.FrameBinary, data, nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}
func (f testDownFrames) WriteFrame(ctx context.Context, _ upstream.FrameKind, data []byte) error {
	select {
	case f.output <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f testDownFrames) Close() error { return nil }

// 本地 TLS WebSocket 验证上行 JSON、下行音频观测、取消及连接所有权。
func TestRealtimeNativeLocalWebSocket(t *testing.T) {
	incoming := make(chan []byte, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
		assert.Equal(t, "grok-voice-latest", r.URL.Query().Get("model"))
		assert.Equal(t, "fixture", r.Header.Get("X-Test"))
		conn, err := websocket.Accept(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		kind, data, err := conn.Read(ctx)
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, websocket.MessageText, kind)
		incoming <- data
		assert.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.audio.delta","delta":"YQ=="}`)))
		_, _, _ = conn.Read(ctx)
	}))
	defer server.Close()
	var entered, released, closed atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := DialRealtime(ctx, RealtimeDialOptions{
		BaseURL:      server.URL + "/v1/realtime",
		Token:        "fixture-token",
		ApplyHeaders: func(h http.Header) { h.Set("X-Test", "fixture") },
		Enter:        func() (func(), error) { entered.Add(1); return func() { released.Add(1) }, nil },
		Dial: func(ctx context.Context, target string, headers http.Header) (upstream.FrameConn, int, error) {
			conn, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: headers})
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			if err != nil {
				return nil, status, err
			}
			return testWSFrames{conn, &closed}, status, nil
		},
	})
	require.NoError(t, err)
	require.True(t, session.Ready())
	require.EqualValues(t, 1, entered.Load())
	require.Zero(t, released.Load())
	down := testDownFrames{make(chan []byte, 1), make(chan []byte, 1)}
	down.input <- []byte(`{"type":"input_audio_buffer.append","audio":"Yg=="}`)
	type result struct {
		audio bool
		err   error
	}
	done := make(chan result, 1)
	go func() { audio, err := RelayRealtime(ctx, down, session); done <- result{audio, err} }()
	select {
	case payload := <-incoming:
		require.JSONEq(t, `{"type":"input_audio_buffer.append","audio":"Yg=="}`, string(payload))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case payload := <-down.output:
		require.JSONEq(t, `{"type":"response.audio.delta","delta":"YQ=="}`, string(payload))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	got := <-done
	require.True(t, got.audio)
	require.Error(t, got.err)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = session.Close() }()
	}
	wg.Wait()
	require.EqualValues(t, 1, closed.Load())
	require.EqualValues(t, 1, released.Load())
}

// 握手失败释放活动登记；探测仍返回原错误而不是升级错误包装。
func TestRealtimeNativeDialFailureAndProbeError(t *testing.T) {
	sentinel := errors.New("fixture unavailable")
	var released atomic.Int64
	options := RealtimeDialOptions{
		BaseURL: "https://fixture.invalid/v1/realtime",
		Enter:   func() (func(), error) { return func() { released.Add(1) }, nil },
		Dial:    func(context.Context, string, http.Header) (upstream.FrameConn, int, error) { return nil, 403, sentinel },
	}
	_, err := DialRealtime(context.Background(), options)
	var failure *RealtimeDialError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, 403, failure.StatusCode)
	require.ErrorIs(t, err, sentinel)
	require.EqualValues(t, 1, released.Load())
	err = ProbeRealtime(context.Background(), options)
	require.Same(t, sentinel, err)
	require.EqualValues(t, 2, released.Load())
}

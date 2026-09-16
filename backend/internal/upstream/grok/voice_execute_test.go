package grok

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 捕获同步输出，不另建队列或输出协程。
type voiceOutput struct {
	head   upstream.OutputHead
	events []upstream.OutputEvent
}

func (s *voiceOutput) Begin(head upstream.OutputHead) error { s.head = head; return nil }
func (s *voiceOutput) Emit(event upstream.OutputEvent) error {
	s.events = append(s.events, event)
	return nil
}

type voiceBody struct {
	io.ReadCloser
	closed *atomic.Int64
}

func (b *voiceBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

// 本地 HTTP 从构造请求到同步输出，核对原生协议、音频单位和响应关闭。
func TestVoiceExecuteLocalHTTP(t *testing.T) {
	cases := []struct {
		endpoint                       string
		proto                          protocol.ProtocolID
		request, response, contentType string
		units                          float64
	}{
		{"tts", protocol.ProtocolTTS, `{"input":"你好世界"}`, "audio-bytes", "audio/mpeg", 4.0 / 1_000_000},
		{"stt", protocol.ProtocolSTT, `{}`, `{"duration":3600,"text":"hello"}`, "application/json", 1},
		{"custom-voices", protocol.ProtocolCustomVoices, `{"name":"voice"}`, `{"id":"v1"}`, "application/json", 0},
	}
	for _, tt := range cases {
		t.Run(tt.endpoint, func(t *testing.T) {
			var calls, closed, active atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
				assert.Equal(t, "fixture", r.Header.Get("X-Test"))
				data, _ := io.ReadAll(r.Body)
				assert.Equal(t, tt.request, string(data))
				w.Header().Set("Content-Type", tt.contentType)
				w.Header().Set("xai-request-id", "upstream-fixture")
				_, _ = io.WriteString(w, tt.response)
			}))
			defer server.Close()
			req, err := BuildVoiceRequest(context.Background(), http.MethodPost, server.URL, "fixture-token", "", []byte(tt.request), func(h http.Header) { h.Set("X-Test", "fixture") })
			require.NoError(t, err)
			target := &VoiceTarget{
				Endpoint:     tt.endpoint,
				BaseEndpoint: tt.endpoint,
				Request:      req,
				Enter:        func() (func(), error) { active.Add(1); return func() { active.Add(-1) }, nil },
				Do: func(r *http.Request) (*http.Response, error) {
					resp, err := server.Client().Do(r)
					if resp != nil {
						resp.Body = &voiceBody{resp.Body, &closed}
					}
					return resp, err
				},
				ReadBody:    io.ReadAll,
				CopyHeaders: func(dst, src http.Header) { dst.Set("Content-Type", src.Get("Content-Type")) },
			}
			sink := &voiceOutput{}
			result, err := (VoiceExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: tt.proto, Body: []byte(tt.request), Target: target}, sink)
			require.NoError(t, err)
			require.EqualValues(t, 1, calls.Load())
			require.EqualValues(t, 1, closed.Load())
			require.Zero(t, active.Load())
			require.Equal(t, "upstream-fixture", result.RequestID)
			require.Equal(t, http.StatusOK, sink.head.Status)
			require.Equal(t, tt.contentType, sink.head.Header.Get("Content-Type"))
			require.Len(t, sink.events, 1)
			require.Equal(t, tt.response, string(sink.events[0].Data))
			require.True(t, result.Served)
			if tt.units == 0 {
				require.Nil(t, result.AudioUsage)
				require.False(t, result.HasUsage)
			} else {
				require.True(t, result.HasUsage)
				require.Equal(t, tt.endpoint, result.AudioUsage.Mode)
				require.InDelta(t, tt.units, result.AudioUsage.DurationOrUnits, 1e-12)
			}
		})
	}
}

// 失败策略的处理发生在输出和计量之前，同时仍由执行器关闭响应。
func TestVoiceExecuteHTTPErrorDoesNotEmitOrMeter(t *testing.T) {
	var closed atomic.Int64
	req, err := http.NewRequest(http.MethodPost, "https://fixture.invalid", strings.NewReader(`{}`))
	require.NoError(t, err)
	sentinel := io.ErrUnexpectedEOF
	target := &VoiceTarget{
		Request: req,
		Do: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: &voiceBody{io.NopCloser(strings.NewReader("denied")), &closed}}, nil
		},
		ReadBody:       func(io.Reader) ([]byte, error) { t.Fatal("错误响应由原策略处理"); return nil, nil },
		BeforeResponse: func(resp *http.Response) (bool, error) { require.Equal(t, 403, resp.StatusCode); return true, sentinel },
	}
	sink := &voiceOutput{}
	result, err := (VoiceExecutor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolTTS, Target: target}, sink)
	require.ErrorIs(t, err, sentinel)
	require.Nil(t, result.AudioUsage)
	require.Empty(t, sink.events)
	require.EqualValues(t, 1, closed.Load())
}

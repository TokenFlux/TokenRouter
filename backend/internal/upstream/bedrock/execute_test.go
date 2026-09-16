// 直接验证 Bedrock 新执行入口的帧顺序、用量事实和关闭责任。
package bedrock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

type executionSink struct {
	body   bytes.Buffer
	events []upstream.OutputEvent
}

func (s *executionSink) Begin(upstream.OutputHead) error { return nil }
func (s *executionSink) Emit(event upstream.OutputEvent) error {
	event.Data = bytes.Clone(event.Data)
	s.events = append(s.events, event)
	_, _ = s.body.Write(event.Data)
	return nil
}

type executionBody struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (b *executionBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }
func executionFrame(eventType string, payload []byte) []byte {
	// Build headers
	var headersBuf bytes.Buffer
	// :event-type header
	_ = headersBuf.WriteByte(byte(len(":event-type")))
	_, _ = headersBuf.WriteString(":event-type")
	_ = headersBuf.WriteByte(7) // string type
	_ = binary.Write(&headersBuf, binary.BigEndian, uint16(len(eventType)))
	_, _ = headersBuf.WriteString(eventType)
	// :message-type header
	_ = headersBuf.WriteByte(byte(len(":message-type")))
	_, _ = headersBuf.WriteString(":message-type")
	_ = headersBuf.WriteByte(7)
	_ = binary.Write(&headersBuf, binary.BigEndian, uint16(len("event")))
	_, _ = headersBuf.WriteString("event")

	headers := headersBuf.Bytes()
	headersLen := uint32(len(headers))
	// total = 12 (prelude) + headers + payload + 4 (message_crc)
	totalLen := uint32(12 + len(headers) + len(payload) + 4)

	// Prelude: total_length(4) + headers_length(4)
	var preludeBuf bytes.Buffer
	_ = binary.Write(&preludeBuf, binary.BigEndian, totalLen)
	_ = binary.Write(&preludeBuf, binary.BigEndian, headersLen)
	preludeBytes := preludeBuf.Bytes()
	preludeCRC := crc32.Checksum(preludeBytes, crc32.IEEETable)

	// Build frame: prelude + prelude_crc + headers + payload
	var frame bytes.Buffer
	_, _ = frame.Write(preludeBytes)
	_ = binary.Write(&frame, binary.BigEndian, preludeCRC)
	_, _ = frame.Write(headers)
	_, _ = frame.Write(payload)

	// Message CRC covers everything before itself
	messageCRC := crc32.Checksum(frame.Bytes(), crc32.IEEETable)
	_ = binary.Write(&frame, binary.BigEndian, messageCRC)
	return frame.Bytes()
}

func TestExecuteBedrockWireAndRelease(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "nonstream"}[stream], func(t *testing.T) {
			var closed, released atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
				w.Header().Set("x-amzn-requestid", "bedrock-fixture")
				if stream {
					for _, event := range []string{`{"type":"message_start","message":{"usage":{"input_tokens":0}}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"visible"}}`, `{"type":"message_delta","amazon-bedrock-invocationMetrics":{"inputTokenCount":0,"outputTokenCount":2}}`, `{"type":"message_stop"}`} {
						payload := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(event)) + `"}`)
						_, _ = w.Write(executionFrame("chunk", payload))
						_ = http.NewResponseController(w).Flush()
					}
				} else {
					_, _ = io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"visible"}],"usage":{"input_tokens":0,"output_tokens":0},"future":true}`)
				}
			}))
			defer server.Close()
			target := &Target{AccountID: 7, ReadBody: io.ReadAll, Enter: func() (func(), error) { return func() { released.Add(1) }, nil }, Request: RequestOptions{ModelID: "anthropic.fixture", Region: "us-east-1", APIKeyMode: true, APIKey: "fixture-key", Stream: stream, Do: func(r *http.Request) (*http.Response, error) {
				// 本地服务器只替换目的地，保留原生构造的路径、载荷和认证头。
				local, _ := http.NewRequestWithContext(r.Context(), r.Method, server.URL+r.URL.RequestURI(), r.Body)
				local.Header = r.Header.Clone()
				res, err := server.Client().Do(local)
				if res != nil {
					res.Body = &executionBody{ReadCloser: res.Body, closed: &closed}
				}
				return res, err
			}}, Retry: RetryPolicy{MaxAttempts: 1, MaxElapsed: time.Second}}
			sink := &executionSink{}
			result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Target: target, Protocol: protocol.ProtocolAnthropicMessages, Body: []byte(`{"messages":[]}`), ResponseModel: "fixture", Stream: stream}, sink)
			require.NoError(t, err)
			require.True(t, result.HasUsage)
			require.True(t, result.Served)
			require.NotNil(t, result.FirstSemanticOutput)
			require.Equal(t, "bedrock-fixture", result.RequestID)
			require.EqualValues(t, 1, closed.Load())
			require.EqualValues(t, 1, released.Load())
			require.Contains(t, sink.body.String(), "visible")
			require.NotContains(t, sink.body.String(), "amazon-bedrock-invocationMetrics")
			if stream {
				require.Equal(t, 2, result.Usage.OutputTokens)
				require.NotNil(t, result.FirstTokenMs)
			} else {
				require.Zero(t, result.Usage.OutputTokens)
				require.Nil(t, result.FirstTokenMs)
				require.Contains(t, sink.body.String(), "future")
			}
		})
	}
}

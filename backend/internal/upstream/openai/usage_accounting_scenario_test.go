package openai

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// usage_accounting_scenario_test.go covers response_chat.go and
// response_messages.go with the shared protocol usage accumulator. Client
// completion must preserve image accounting after sparse terminal observations.

// accountingStreamSink captures converted stream payloads for terminal assertions.
type accountingStreamSink struct {
	bytes.Buffer // captured client stream bytes
}

// Begin accepts response metadata without changing captured client payloads.
func (s *accountingStreamSink) Begin(upstream.OutputHead) error { return nil }

// Emit copies each synchronous payload into the retained client stream.
func (s *accountingStreamSink) Emit(event upstream.OutputEvent) error {
	_, err := s.Write(event.Data)
	return err
}

// TestConvertedResponsesStreamsPreserveImageAccounting drives both client readers
// through sparse, absent, and reset terminal usage. The emitted completion and
// returned billing buckets must reflect the same response without dropping images.
func TestConvertedResponsesStreamsPreserveImageAccounting(t *testing.T) {
	for _, client := range []string{"messages", "chat"} {
		for _, fixture := range []struct {
			name  string            // terminal fixture description
			usage string            // optional terminal usage member
			want  wire.ForwardUsage // expected final billing buckets
		}{
			{
				name: "absent usage",
				want: wire.ForwardUsage{InputTokens: 100, OutputTokens: 3, CacheReadInputTokens: 20, ImageInputTokens: 40, ImageOutputTokens: 2},
			},
			{
				name:  "sparse usage",
				usage: `,"usage":{"output_tokens":10}`,
				want:  wire.ForwardUsage{InputTokens: 100, OutputTokens: 10, CacheReadInputTokens: 20, ImageInputTokens: 40, ImageOutputTokens: 2},
			},
			{
				name:  "explicit zero images",
				usage: `,"usage":{"input_tokens_details":{"image_tokens":0},"output_tokens_details":{"image_tokens":0}}`,
				want:  wire.ForwardUsage{InputTokens: 100, OutputTokens: 3, CacheReadInputTokens: 20},
			},
			{
				name:  "complete zero reset",
				usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`,
			},
		} {
			t.Run(client+"/"+fixture.name, func(t *testing.T) {
				body := "data: " + `{"type":"response.created","response":{"id":"resp_images","model":"model","usage":{"input_tokens":100,"output_tokens":3,"input_tokens_details":{"cached_tokens":20,"image_tokens":40},"output_tokens_details":{"image_tokens":2}}}}` + "\n\n" +
					"data: " + `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"OK"}` + "\n\n" +
					"data: " + `{"type":"response.completed","response":{"id":"resp_images","status":"completed","output":[]` + fixture.usage + `}}` + "\n\n"
				response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
				sink := &accountingStreamSink{}
				output := upstream.NewOutputContext(sink)
				options := ChatResponseOptions{
					Runtime:          bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
					RequestContext:   t.Context(),
					Headers:          func(http.Header, http.Header) {},
					ServiceTier:      func() string { return "" },
					Scanner:          func(reader io.Reader) *bufio.Scanner { return NewCompatSSEScanner(reader, 1<<20) },
					StreamInterval:   func() time.Duration { return 0 },
					RestoreToolNames: func(payload []byte) []byte { return payload },
					Observe:          func([]byte, string) {},
				}
				var result *CompatResponseResult
				var err error
				if client == "messages" {
					result, err = ReadMessagesStreaming(response, output, MessagesResponseOptions{
						ChatResponseOptions: options,
						MissingUsage:        func(*wire.ForwardUsage, string, bool) {},
					}, "model", "upstream-model", time.Now())
					require.Contains(t, sink.String(), `"type":"message_stop"`)
				} else {
					result, err = ReadChatStreaming(response, output, options, "model", "upstream-model", time.Now(), 0)
					require.Contains(t, sink.String(), "data: [DONE]")
				}
				require.NoError(t, err)
				require.Equal(t, fixture.want, result.Usage)
				require.Contains(t, sink.String(), "OK")
			})
		}
	}
}

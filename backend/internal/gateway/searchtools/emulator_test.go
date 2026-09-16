package searchtools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	"github.com/stretchr/testify/require"
)

type searchStub struct {
	calls   int
	request contract.SearchRequest
	err     error
	events  *[]string
}

func (s *searchStub) SearchWithBestProvider(_ context.Context, r contract.SearchRequest) (*contract.SearchResponse, string, error) {
	s.calls++
	s.request = r
	if s.events != nil {
		*s.events = append(*s.events, "search")
	}
	return &contract.SearchResponse{Query: r.Query, Results: []contract.SearchResult{{URL: "https://example.test/page", Title: "title", Snippet: "snippet"}}}, "brave", s.err
}

type sourceStub struct{ searcher Searcher }

func (s sourceStub) Current() Searcher { return s.searcher }

type settingStub struct {
	enabled bool
	calls   int
}

func (s *settingStub) IsWebSearchEmulationEnabled(context.Context) bool { s.calls++; return s.enabled }

type channelStub struct {
	calls   int
	enabled bool
}

func (s *channelStub) Enabled(context.Context, int64, string) (bool, error) {
	s.calls++
	return s.enabled, nil
}

type outputStub struct {
	events  []string
	bodies  [][]byte
	json    []byte
	failAt  int
	flushes int
	started bool
}

func (o *outputStub) StartStream() { o.started = true }
func (o *outputStub) WriteEvent(name string, body []byte) error {
	o.events = append(o.events, name)
	o.bodies = append(o.bodies, append([]byte(nil), body...))
	if len(o.events) == o.failAt {
		return errors.New("write failed")
	}
	return nil
}
func (o *outputStub) WriteJSON(body []byte) { o.json = append([]byte(nil), body...) }
func (o *outputStub) Flush()                { o.flushes++ }
func newTestEmulator(provider *searchStub) *Emulator {
	return NewEmulator(sourceStub{provider}, &settingStub{enabled: true}, nil, time.Now, func() string { return "12345678-1234-1234-1234-123456789abc" }, nil)
}
func TestEmulatorSyntheticUsageAndEventOrder(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			order := []string{}
			provider := &searchStub{events: &order}
			core := newTestEmulator(provider)
			out := &outputStub{}
			result, err := core.Execute(context.Background(), Request{Body: []byte(`{"messages":[{"role":"user","content":"query"}]}`), ProxyURL: "http://proxy.test", Stream: stream, OnAccepted: func() { order = append(order, "accepted") }}, out)
			require.NoError(t, err)
			require.Equal(t, []string{"accepted", "search"}, order)
			require.Equal(t, 1, provider.calls)
			require.Equal(t, contract.SearchRequest{Query: "query", MaxResults: 5, ProxyURL: "http://proxy.test"}, provider.request)
			require.Equal(t, "claude-sonnet-4-6", result.Model)
			require.Zero(t, result.Usage.InputTokens)
			require.Zero(t, result.Usage.OutputTokens)
			if stream {
				require.True(t, out.started)
				require.Equal(t, 1, out.flushes)
				require.Equal(t, []string{"message_start", "content_block_start", "content_block_stop", "content_block_start", "content_block_stop", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}, out.events)
				var event struct {
					Usage struct {
						Output int `json:"output_tokens"`
					} `json:"usage"`
				}
				require.NoError(t, json.Unmarshal(out.bodies[8], &event))
				require.Greater(t, event.Usage.Output, 0)
			} else {
				var msg struct {
					Content []json.RawMessage `json:"content"`
					Usage   struct {
						Output int `json:"output_tokens"`
					} `json:"usage"`
				}
				require.NoError(t, json.Unmarshal(out.json, &msg))
				require.Len(t, msg.Content, 3)
				require.Greater(t, msg.Usage.Output, 0)
			}
		})
	}
}
func TestEmulatorWriteFailureStopsEventsWithoutInventingBillableUsage(t *testing.T) {
	provider := &searchStub{}
	out := &outputStub{failAt: 3}
	result, err := newTestEmulator(provider).Execute(context.Background(), Request{Body: []byte(`{"messages":[{"role":"user","content":"query"}]}`), Stream: true}, out)
	require.NoError(t, err)
	require.Len(t, out.events, 3)
	require.Equal(t, 1, out.flushes)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
}
func TestEmulatorProxyFailureRetainsCause(t *testing.T) {
	provider := &searchStub{err: search.ErrProxyUnavailable}
	out := &outputStub{}
	result, err := newTestEmulator(provider).Execute(context.Background(), Request{Body: []byte(`{"messages":[{"role":"user","content":"query"}]}`)}, out)
	require.Nil(t, result)
	require.ErrorIs(t, err, search.ErrProxyUnavailable)
	var failure *ProxyFailure
	require.ErrorAs(t, err, &failure)
	require.Empty(t, out.events)
	require.Empty(t, out.json)
}
func TestEmulatorPolicyShortCircuit(t *testing.T) {
	settings := &settingStub{enabled: true}
	channel := &channelStub{enabled: true}
	id := int64(1)
	core := NewEmulator(sourceStub{}, settings, channel, nil, nil, nil)
	input := PolicyInput{Body: []byte(`{"tools":[{"type":"web_search"}]}`), GroupID: &id}
	require.False(t, core.ShouldEmulate(context.Background(), input))
	require.Zero(t, settings.calls)
	core.source = sourceStub{&searchStub{}}
	input.Body = []byte(`{"tools":[]}`)
	require.False(t, core.ShouldEmulate(context.Background(), input))
	require.Zero(t, settings.calls)
	input.Body = []byte(`{"tools":[{"type":"web_search"}]}`)
	input.Mode = "enabled"
	require.True(t, core.ShouldEmulate(context.Background(), input))
	require.Zero(t, channel.calls)
	input.Mode = "disabled"
	require.False(t, core.ShouldEmulate(context.Background(), input))
	require.Zero(t, channel.calls)
	input.Mode = "default"
	require.True(t, core.ShouldEmulate(context.Background(), input))
	require.Equal(t, 1, channel.calls)
}

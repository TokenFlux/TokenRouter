package testkit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder.go captures submitted requests and provides stream clients for Qoder
// execution tests. The client captures each attempted request and copies its
// body before either returning an injected error, supplying a successful
// response stream, or calling the provided request doer.

// QoderClient captures requests and supplies response fixtures to the
// production executor.
type QoderClient struct {
	// Request and Requests identify the latest request and ordered request history.
	// Both retain the constructed pointers, including any changes made by a supplied
	// doer; tests serialize calls and later inspection of those requests.
	Request  *http.Request   // most recently constructed HTTP request
	Requests []*http.Request // constructed HTTP request pointer history
	Body     string          // preset successful stream response text
	Bodies   [][]byte        // copied submitted request body history
	Err      error           // injected request execution failure
	// Headers retains the most recent supplied map. Tests keep that map stable
	// while checking it; body history uses copied bytes and has separate ownership.
	Headers map[string]string // retained submitted request header map
}

// QoderFixture holds runtime dependencies; blocking-stream tests can replace
// Client before execution.
type QoderFixture struct {
	Runtime *gatewayprovider.QoderRuntime       // shared production Qoder runtime fixture
	Tokens  *provideradapter.QoderTokenProvider // shared production Qoder token source
	// Client is read through qoderFixtureClient at each execution call. Tests
	// replace it before execution begins and serialize replacement with runtime use.
	Client qoder.StreamClient // replaceable Qoder stream client fixture
}

// qoderFixtureClient reads the fixture's replaceable stream client for runtime
// calls.
type qoderFixtureClient struct {
	fixture *QoderFixture // owning mutable Qoder runtime fixture
}

// StreamRequestContext captures a context-bound HTTP request and copies its
// body for assertions. The supplied header map is retained; Err returns after
// capture, otherwise Body becomes a caller-owned successful response stream.
func (c *QoderClient) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, body []byte, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	c.Request = req
	c.Requests = append(c.Requests, req)
	c.Bodies = append(c.Bodies, append([]byte(nil), body...))
	c.Headers = headers
	if c.Err != nil {
		return nil, c.Err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(c.Body))}, nil
}

// StreamRequestContextWithDoer captures a context-bound request and copied
// body before invoking the supplied doer. Err returns before that invocation;
// headers and request pointers remain retained for serial test inspection.
func (c *QoderClient) StreamRequestContextWithDoer(ctx context.Context, _ *qoder.SessionContext, _ string, body []byte, headers map[string]string, doer qoder.RequestDoer) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	c.Request = req
	c.Requests = append(c.Requests, req)
	c.Bodies = append(c.Bodies, append([]byte(nil), body...))
	c.Headers = headers
	if c.Err != nil {
		return nil, c.Err
	}
	return doer(req)
}

// BodyAt returns a byte copy of the recorded body at index. Nil clients
// and out-of-range or negative indices return nil, allowing assertions to
// query absent attempts safely.
func (c *QoderClient) BodyAt(index int) []byte {
	if c == nil || index < 0 || index >= len(c.Bodies) {
		return nil
	}
	return append([]byte(nil), c.Bodies[index]...)
}

// BodyCount returns the number of captured bodies for this client. Nil clients
// report zero; callers serialize the query with stream calls that append
// request history.
func (c *QoderClient) BodyCount() int {
	if c == nil {
		return 0
	}
	return len(c.Bodies)
}

// StreamRequestContext delegates through the fixture's current client with the
// supplied Qoder session and bytes. Callers finish replacing Client before
// execution; the delegate returns its response body and error unchanged.
func (c qoderFixtureClient) StreamRequestContext(ctx context.Context, session *qoder.SessionContext, path string, body []byte, headers map[string]string) (*http.Response, error) {
	return c.fixture.Client.StreamRequestContext(ctx, session, path, body, headers)
}

// NewQoderFixture binds the supplied token source, client, and conversation
// store to production runtime execution. Its proxy client reads the fixture's
// current Client, allowing callers to replace that dependency before starting
// attempts.
func NewQoderFixture(tokens *provideradapter.QoderTokenProvider, client qoder.StreamClient, conversations *qoder.QoderConversationStore) *QoderFixture {
	f := &QoderFixture{Tokens: tokens, Client: client}
	f.Runtime = gatewayprovider.NewQoderRuntime(gatewayprovider.QoderRuntimeOptions{Tokens: tokens, Client: qoderFixtureClient{fixture: f}, Conversations: conversations})
	return f
}

// NewDefaultQoderFixture builds a COSY Qoder provider and preloads matching
// cached credentials for execution tests. Its stream supplies OK, one input
// token, one output token, and completion; callers own the returned fixture
// lifetime.
func NewDefaultQoderFixture() (*provider.Record, *QoderFixture, *QoderClient) {
	value := &provider.Record{ID: 8801, Name: "qoder", Platform: provider.PlatformQoder, Type: provider.ProviderTypeCosy, Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-4-6": "ultimate"}}}
	SetQoderCatalog(value)
	client := &QoderClient{Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":2}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"}
	tokens := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{})
	tokens.Core.Sessions = map[int64]provider.QoderSessionCacheEntry[*qoder.SessionContext]{value.ID: {CredentialsHash: provider.QoderCredentialsHash(value.Credentials), Session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}}}}
	return value, NewQoderFixture(tokens, client, qoder.NewQoderConversationStore(qoder.QoderConversationTTL)), client
}

// SetQoderCatalog attaches synthetic current capabilities to an execution fixture.
// Call it after changing authentication fields so the observation belongs to
// that fixture identity. These values exercise conversion independently of
// account entitlements or live upstream discovery.
func SetQoderCatalog(value *provider.Record) {
	models := []qodermodel.Model{
		{Key: "auto", Alias: "auto", Enabled: true, InputLimit: 1000000, ContextWindows: []int{200000, 1000000}},
		{Key: "ultimate", Alias: "ultimate", Enabled: true, Reasoning: new(true), DefaultThinking: true, DefaultEffort: "high", Efforts: []string{"low", "medium", "high", "xhigh", "max"}, InputLimit: 1000000, ContextWindows: []int{200000, 1000000}},
		{Key: "qmodel_38max", Alias: "qwen3.8-max", Enabled: true, Reasoning: new(true), CanDisable: new(true), DefaultThinking: true, DefaultEffort: "medium", Efforts: []string{"low", "medium", "xhigh"}, InputLimit: 1000000, ContextWindows: []int{200000, 1000000}},
		{Key: "dmodel", Alias: "deepseek-v4-pro", Enabled: true, Reasoning: new(true), CanDisable: new(true), DefaultThinking: true, DefaultEffort: "max", Efforts: []string{"high", "max"}, InputLimit: 1000000, ContextWindows: []int{200000, 1000000}},
		{Key: "gmodel", Alias: "glm-5.3", Enabled: true, Reasoning: new(true), DefaultThinking: true, DefaultEffort: "high", Efforts: []string{"low", "high", "max"}, InputLimit: 1000000, ContextWindows: []int{200000, 1000000}},
	}
	if value.Extra == nil {
		value.Extra = map[string]any{}
	}
	value.Extra[provider.QoderCatalogExtraKey] = &provider.QoderCatalogSnapshot{Identity: provider.QoderCatalogIdentity(value), Version: "fixture", FetchedAt: time.Now(), Models: models}
}

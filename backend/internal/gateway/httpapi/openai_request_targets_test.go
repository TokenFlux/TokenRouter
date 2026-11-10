package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Raw Chat finalizes headers immediately before its transport sends the
// request. A recording transport captures that point so the fixture can compare
// the caller's OpenCode conversation ID with a conflicting provider override on
// the trusted origin.

// openCodeSessionHTTPUpstream records the outgoing Chat request and returns
// empty JSON.
type openCodeSessionHTTPUpstream struct {
	// request retains the supplied request pointer for assertions after
	// SendChat. The test reads its final headers once the synchronous
	// exchange completes.
	request *http.Request
}

// Do records the outgoing request and supplies a successful JSON response. The
// fixture retains the request pointer for header assertions; callers own the
// returned synthetic response body and close it after inspection.
func (u *openCodeSessionHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.request = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

// DoWithTLS delegates TLS-aware fixture exchanges to the same request recording
// path.
func (u *openCodeSessionHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, concurrency)
}

// TestOpenCodeSessionForwardedByRawChatCompletionsAfterProviderOverride sends
// Chat with conflicting conversation metadata. It checks the recorded transport
// request contains one caller-supplied OpenCode session header after provider
// overrides.
func TestOpenCodeSessionForwardedByRawChatCompletionsAfterProviderOverride(t *testing.T) {
	upstream := &openCodeSessionHTTPUpstream{}
	svc := openCodeSessionTestService()
	svc.Requests.Transport = upstream
	if svc.Grok != nil {
		svc.Grok.Transport = svc.Requests.Transport
	}
	provider := openCodeSessionTestProvider("https://opencode.ai/zen/v1")
	c := newOpenCodeSessionTestContext(t, "conversation-789")

	resp, err := svc.Requests.SendChat(
		context.Background(), c, provider,
		"https://opencode.ai/zen/v1/chat/completions", []byte(`{"model":"gpt-5"}`),
		false, "token", "", "",
	)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NotNil(t, upstream.request)
	requireSingleOpenCodeSessionHeader(t, upstream.request.Header, "conversation-789")
}

// TestSendChat_NormalizesDeepSeekDeveloperMessages sends both developer
// content forms alongside a tool exchange. It checks the exact outgoing body
// for DeepSeek and preserves the original bytes for an OpenAI target.
func TestSendChat_NormalizesDeepSeekDeveloperMessages(t *testing.T) {
	const body = `{
		"model":"deepseek-v4-pro","stream":true,
		"messages":[
			{"role":"developer","name":"policy","content":"Keep the word developer."},
			{"role":"user","content":"Look this up."},
			{"role":"assistant","content":null,"reasoning_content":"Use lookup","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"found"},
			{"role":"developer","content":[{"type":"text","text":"Keep the result. "},{"type":"text","text":"Cite it."}]}
		],
		"metadata":{"role":"developer","large_id":9007199254740993}
	}`
	const deepseekBody = `{
		"model":"deepseek-v4-pro","stream":true,
		"messages":[
			{"role":"system","name":"policy","content":"Keep the word developer."},
			{"role":"user","content":"Look this up."},
			{"role":"assistant","content":null,"reasoning_content":"Use lookup","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"found"},
			{"role":"system","content":"Keep the result. \n\nCite it."}
		],
		"metadata":{"role":"developer","large_id":9007199254740993}
	}`

	for platform, want := range map[string]string{
		capability.PlatformDeepseek: deepseekBody,
		capability.PlatformOpenAI:   body,
	} {
		t.Run(platform, func(t *testing.T) {
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}}
			svc := newResponsesFixture(responsesFixtureInputs{options: protocolHTTPOptions(), transport: upstream})
			provider := adaptiveProtocolTestProvider(platform, nil)
			requestBody := []byte(body)
			c := adaptiveProtocolTestContext("/v1/chat/completions", requestBody)

			resp, err := svc.Requests.SendChat(
				t.Context(), c, provider, "http://chat.example/v1/chat/completions",
				requestBody, true, "token", "", "",
			)

			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, want, string(upstream.lastBody))
			require.Equal(t, body, string(requestBody))
		})
	}
}

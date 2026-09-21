package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// 本夹具只记录实际传输与同步事件，执行使用原生目标句柄。
type geminiTestTransportFixture struct {
	request *http.Request
	body    string
}

func (f *geminiTestTransportFixture) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	f.request = req
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	f.body = string(body)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\ndata: [DONE]\n\n"))}, nil
}

type geminiTestSinkFixture struct{ events []account.TestEvent }

func (*geminiTestSinkFixture) Begin(context.Context, bool) error { return nil }
func (f *geminiTestSinkFixture) Emit(_ context.Context, event account.TestEvent) error {
	f.events = append(f.events, event)
	return nil
}

func TestGeminiAccountTestNativeCredentialRoutes(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    string
		project string
		path    string
		bearer  string
		apiKey  string
	}{
		{name: "api_key", kind: account.AccountTypeAPIKey, path: "/v1beta/models/gemini-2.5-flash:streamGenerateContent", apiKey: "test-key"},
		{name: "oauth_studio", kind: account.AccountTypeOAuth, path: "/v1beta/models/gemini-2.5-flash:streamGenerateContent", bearer: "Bearer test-token"},
		{name: "oauth_codeassist", kind: account.AccountTypeOAuth, project: "test-project", path: "/v1internal:streamGenerateContent", bearer: "Bearer test-token"},
		{name: "service_account", kind: account.AccountTypeServiceAccount, project: "test-project", path: "/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent", bearer: "Bearer vertex-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := &account.Record{ID: 7, Platform: account.PlatformGemini, Type: test.kind, Credentials: map[string]any{"api_key": "test-key", "access_token": "test-token", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339), "project_id": test.project}}
			transport := &geminiTestTransportFixture{}
			executor := &GeminiAccountTest{Transport: transport, Tokens: &account.GeminiTokenSource{Options: account.GeminiTokenOptions{Vertex: func(context.Context, *account.Record) (string, error) { return "vertex-token", nil }}}, ValidateURL: func(raw string) (string, error) { return raw, nil }}
			sink := &geminiTestSinkFixture{}
			err := executor.Target(value).Execute(t.Context(), account.PreparedTestRequest{TestRequest: account.TestRequest{AccountID: value.ID, Model: "gemini-2.5-flash", Prompt: "hello", Automatic: true, UserAgent: "fixture-agent"}, TestType: account.AccountTestTypeText}, sink)
			require.NoError(t, err)
			require.NotNil(t, transport.request)
			require.Equal(t, test.path, transport.request.URL.Path)
			require.Equal(t, "sse", transport.request.URL.Query().Get("alt"))
			require.Equal(t, test.bearer, transport.request.Header.Get("Authorization"))
			require.Equal(t, test.apiKey, transport.request.Header.Get("x-goog-api-key"))
			require.Equal(t, "fixture-agent", transport.request.Header.Get("User-Agent"))
			require.Contains(t, transport.body, "hello")
			require.Len(t, sink.events, 3)
			require.Equal(t, []string{"test_start", "content", "test_complete"}, []string{sink.events[0].Type, sink.events[1].Type, sink.events[2].Type})
			require.Equal(t, "", executor.UserAgent)
		})
	}
}

package vertex_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildVertexGeminiURL(t *testing.T) {
	got, err := vertex.BuildVertexGeminiURL("my-project", "us-central1", "gemini-3-pro", "streamGenerateContent", true)
	require.NoError(t, err)
	require.Equal(t, "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-3-pro:streamGenerateContent?alt=sse", got)
}

func TestBuildVertexGeminiURLUsesGlobalEndpointHost(t *testing.T) {
	got, err := vertex.BuildVertexGeminiURL("my-project", "global", "gemini-3-flash-preview", "streamGenerateContent", true)
	require.NoError(t, err)
	require.Equal(t, "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/publishers/google/models/gemini-3-flash-preview:streamGenerateContent?alt=sse", got)
}

func TestBuildVertexAnthropicURL(t *testing.T) {
	got, err := vertex.BuildVertexAnthropicURL("my-project", "us-east5", "claude-sonnet-4-5@20250929", false)
	require.NoError(t, err)
	require.Equal(t, "https://us-east5-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-5@20250929:rawPredict", got)
}

func TestBuildVertexAnthropicURLUsesGlobalEndpointHost(t *testing.T) {
	got, err := vertex.BuildVertexAnthropicURL("my-project", "global", "claude-haiku-4-5@20251001", true)
	require.NoError(t, err)
	require.Equal(t, "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/publishers/anthropic/models/claude-haiku-4-5@20251001:streamRawPredict", got)
}

func TestNormalizeVertexAnthropicModelID(t *testing.T) {
	require.Equal(t, "claude-sonnet-4-5@20250929", vertex.NormalizeVertexAnthropicModelID("claude-sonnet-4-5-20250929"))
	require.Equal(t, "claude-sonnet-4-5@20250929", vertex.NormalizeVertexAnthropicModelID("claude-sonnet-4-5@20250929"))
	require.Equal(t, "claude-sonnet-4-6", vertex.NormalizeVertexAnthropicModelID("claude-sonnet-4-6"))
}

func TestBuildVertexAnthropicRequestBody(t *testing.T) {
	got, err := vertex.BuildVertexAnthropicRequestBody([]byte(`{"model":"claude-sonnet-4-5","anthropic_version":"2023-06-01","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.Equal(t, "", gjson.GetBytes(got, "model").String())
	require.Equal(t, vertex.AnthropicVersion, gjson.GetBytes(got, "anthropic_version").String())
	require.Equal(t, int64(64), gjson.GetBytes(got, "max_tokens").Int())
	require.Equal(t, "hi", gjson.GetBytes(got, "messages.0.content").String())
}

func TestBuildVertexGeminiURLRejectsInvalidLocation(t *testing.T) {
	_, err := vertex.BuildVertexGeminiURL("my-project", "us-central1/path", "gemini-3-pro", "generateContent", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid vertex location")
}

func TestVertexServiceAccountHTTPClientRecordsDependency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := vertex.NewServiceAccountHTTPClient("")
	require.NoError(t, err)
	collector := timing.New(time.Now())
	ctx := timing.WithCollector(context.Background(), collector)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Contains(t, collector.HeaderValue(time.Now(), "bypass"), "dep_http;dur=")
}

func TestExchangeVertexServiceAccountTokenUsesProxy(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	seenProxyRequest := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenProxyRequest <- r.URL.String()
		require.Equal(t, "oauth2.googleapis.com", r.URL.Host)
		require.Equal(t, "/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.PostForm.Get("grant_type"))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(google.ServiceAccountTokenResponse{
			AccessToken: "proxied-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		}))
	}))
	defer proxy.Close()

	key := &google.ServiceAccountKey{
		ClientEmail: "svc@example.iam.gserviceaccount.com",
		PrivateKey:  string(pemBytes),
		TokenURI:    "http://oauth2.googleapis.com/token",
	}

	token, ttl, err := vertex.ExchangeServiceAccountToken(contextWithTestTimeout(t), key, proxy.URL)
	require.NoError(t, err)
	require.Equal(t, "proxied-token", token)
	require.Equal(t, 55*time.Minute, ttl)

	select {
	case got := <-seenProxyRequest:
		require.Equal(t, "http://oauth2.googleapis.com/token", got)
	default:
		t.Fatal("token request did not reach proxy")
	}
}

func contextWithTestTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

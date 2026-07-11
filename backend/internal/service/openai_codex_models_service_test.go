package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func newCodexModelsTestAccount() *Account {
	return &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acc-123",
		},
	}
}

func withCodexModelsTestServer(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })
}

func TestFetchCodexModelsManifestPassthrough(t *testing.T) {
	manifestBody := `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5"}]}`
	var gotAuth, gotAccountID, gotOriginator, gotClientVersion, gotVersion string

	withCodexModelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("Originator")
		gotClientVersion = r.URL.Query().Get("client_version")
		gotVersion = r.Header.Get("Version")
		w.Header().Set("ETag", `W/"abc123"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(manifestBody))
	}))

	svc := &OpenAIGatewayService{}
	manifest, err := svc.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", "")
	require.NoError(t, err)
	require.Equal(t, manifestBody, string(manifest.Body))
	require.Equal(t, `W/"abc123"`, manifest.ETag)
	require.Equal(t, "Bearer test-access-token", gotAuth)
	require.Equal(t, "acc-123", gotAccountID)
	require.Equal(t, "codex_cli_rs", gotOriginator)
	require.Equal(t, "0.137.0", gotClientVersion)
	require.Equal(t, "0.137.0", gotVersion)
}

func TestFetchCodexModelsManifestDefaultClientVersion(t *testing.T) {
	var gotClientVersion string
	withCodexModelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientVersion = r.URL.Query().Get("client_version")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))

	svc := &OpenAIGatewayService{}
	_, err := svc.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "", "")
	require.NoError(t, err)
	require.Equal(t, openAICodexProbeVersion, gotClientVersion)
}

func TestFetchCodexModelsManifestNotModified(t *testing.T) {
	var gotIfNoneMatch string
	withCodexModelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `W/"abc123"`)
		w.WriteHeader(http.StatusNotModified)
	}))

	svc := &OpenAIGatewayService{}
	manifest, err := svc.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", `W/"abc123"`)
	require.NoError(t, err)
	require.True(t, manifest.NotModified)
	require.Equal(t, `W/"abc123"`, manifest.ETag)
	require.Equal(t, `W/"abc123"`, gotIfNoneMatch)
}

func TestFetchCodexModelsManifestRejectsUpstreamError(t *testing.T) {
	withCodexModelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"boom"}`, http.StatusInternalServerError)
	}))

	svc := &OpenAIGatewayService{}
	_, err := svc.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", "")
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
}

func TestFetchCodexModelsManifestRejectsMissingToken(t *testing.T) {
	account := newCodexModelsTestAccount()
	delete(account.Credentials, "access_token")

	svc := &OpenAIGatewayService{}
	_, err := svc.FetchCodexModelsManifest(context.Background(), account, "0.137.0", "")
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
}

func TestFetchCodexModelsManifestRejectsAPIKeyAccount(t *testing.T) {
	account := newCodexModelsTestAccount()
	account.Type = AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "sk-test"}

	svc := &OpenAIGatewayService{}
	_, err := svc.FetchCodexModelsManifest(context.Background(), account, "0.137.0", "")
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
	require.Equal(t, "OPENAI_CODEX_MODELS_ACCOUNT_UNSUPPORTED", infraerrors.Reason(err))
}

func TestFetchCodexModelsManifestRejectsOversizedResponse(t *testing.T) {
	withCodexModelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), int(codexModelsManifestBodyLimit+1)))
	}))

	svc := &OpenAIGatewayService{}
	_, err := svc.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", "")
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
	require.Contains(t, err.Error(), ErrUpstreamResponseBodyTooLarge.Error())
}

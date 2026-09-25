package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexAccountIdentityRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	account *gatewayprovider.ExecutionAccount
}

func (s *codexAccountIdentityRepoStub) GetByID(_ context.Context, _ int64) (*gatewayprovider.ExecutionAccount, error) {
	return s.account, nil
}

func TestCodexAccountIdentitySourceResolvesShadowAndOverwritesFailoverContext(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	parentID := int64(11)
	parent := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": "team-account",
		"chatgpt_user_id":    "user-1",
	}}}
	shadow := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 111, ParentAccountID: &parentID, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	service := newResponsesFixture(responsesFixtureInputs{accounts: &codexAccountIdentityRepoStub{account: parent}})

	resolved, err := PrepareCodexIdentity(context.Background(), c, service.Requests.Accounts, shadow)
	require.NoError(t, err)
	require.Same(t, parent.View(), resolved)
	require.Same(t, parent.View(), CodexIdentityRecord(c, shadow.View()))

	req, err := service.Requests.Build(
		context.Background(), c, shadow,
		[]byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`),
		"token", true, "client-session", true,
	)
	require.NoError(t, err)
	require.Equal(t, openai.IsolateOpenAIUpstreamSessionID(0, accountprovider.CodexIdentityNamespace(parent.View()), "client-session"), req.Header.Get("session_id"))

	next := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 19, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": "other-account",
		"chatgpt_user_id":    "user-2",
	}}}
	resolved, err = PrepareCodexIdentity(context.Background(), c, service.Requests.Accounts, next)
	require.NoError(t, err)
	require.Same(t, next.View(), resolved)
	require.Same(t, next.View(), CodexIdentityRecord(c, shadow.View()))
}

func TestBuildUpstreamRequestNamespacesCodexIdentityByOAuthAccount(t *testing.T) {

	svc := newResponsesFixture(responsesFixtureInputs{})
	body := []byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`)

	build := func(accountID int64, chatgptAccountID string) http.Header {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Set("api_key", &apikey.APIKey{ID: 77})
		c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.0")
		c.Request.Header.Set("x-codex-installation-id", "client-installation")
		c.Request.Header.Set("x-codex-window-id", "client-window")
		c.Request.Header.Set("session-id", "client-session")
		c.Request.Header.Set("thread-id", "client-thread")
		c.Request.Header.Set("x-client-request-id", "client-request")
		c.Request.Header.Set("x-codex-turn-metadata", `{"installation_id":"client-installation","session_id":"client-session","thread_id":"client-thread","turn_id":"client-turn","window_id":"client-window"}`)

		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: accountID,
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Credentials: map[string]any{
				"chatgpt_account_id": chatgptAccountID,
			}},
		}
		req, err := svc.Requests.Build(
			context.Background(), c, account, body, "oauth-token", true, "client-session", true,
		)
		require.NoError(t, err)
		return req.Header
	}

	first := build(11, "chatgpt-account-11")
	firstAgain := build(11, "chatgpt-account-11")
	second := build(19, "chatgpt-account-19")

	identityHeaders := []string{
		"x-codex-installation-id",
		"x-codex-window-id",
		"session-id",
		"session_id",
		"conversation_id",
		"thread-id",
		"x-client-request-id",
		"x-codex-turn-metadata",
	}
	checked := 0
	for _, header := range identityHeaders {
		if first.Get(header) == "" && second.Get(header) == "" {
			continue
		}
		checked++
		require.NotEmpty(t, first.Get(header), header)
		require.Equal(t, first.Get(header), firstAgain.Get(header), "same account must retain stable identity: %s", header)
		require.NotEqual(t, first.Get(header), second.Get(header), "account failover must rotate upstream identity: %s", header)
	}
	require.GreaterOrEqual(t, checked, 5, "test must exercise the real outbound identity surface")
}

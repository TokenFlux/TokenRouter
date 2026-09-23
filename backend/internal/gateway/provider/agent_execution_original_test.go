package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func newTestAgentIdentityKey(t *testing.T) (openai.AgentIdentityKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	return openai.AgentIdentityKey{
		RuntimeID:  "runtime-test",
		PrivateKey: privateKey,
		TaskID:     "task-test",
	}, base64.StdEncoding.EncodeToString(der)
}

func TestEnsureAgentIdentityTaskPersistsAndRedactsCredentials(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"task_id":"task-persisted"}`))
	}))
	defer server.Close()
	coordinator := &accountcore.OpenAITaskCoordinator{}
	register := func(ctx context.Context, value *accountcore.Record) (string, error) {
		return accountprovider.RegisterAgentIdentityTask(ctx, value, server.URL)
	}

	repo := &agentIdentityCredentialsRepo{}
	account := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7, Type: capability.AccountTypeOAuth, Platform: capability.PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":   key.RuntimeID,
		"agent_private_key":  privateKey,
		"chatgpt_account_id": "account-test",
	}}}
	service := NewExecutionAgentIdentity(coordinator, repo, register, nil)
	require.NoError(t, service.Ensure(context.Background(), account, ""))
	require.Equal(t, "task-persisted", account.View().GetCredential("task_id"))
	require.Equal(t, "task-persisted", repo.credentials["task_id"])
	require.True(t, accountcore.IsSensitiveCredentialKey("agent_private_key"))
	redacted := make(map[string]any)
	for key, value := range account.Record.Credentials {
		if !accountcore.IsSensitiveCredentialKey(key) {
			redacted[key] = value
		}
	}
	require.NotContains(t, string(mustAgentIdentityJSON(t, redacted)), privateKey)
}

func TestEnsureAgentIdentityTaskSharesLockAcrossServicesForSameAccount(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	account := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9001, Type: capability.AccountTypeOAuth, Platform: capability.PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":         accountcore.OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":  key.RuntimeID,
		"agent_private_key": privateKey,
	}}}
	repo := &agentIdentityCredentialsRepo{account: account}
	registerCalls := 0
	var registerMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerMu.Lock()
		registerCalls++
		registerMu.Unlock()
		_, _ = w.Write([]byte(`{"task_id":"task-shared"}`))
	}))
	defer server.Close()
	coordinator := &accountcore.OpenAITaskCoordinator{}
	register := func(ctx context.Context, value *accountcore.Record) (string, error) {
		return accountprovider.RegisterAgentIdentityTask(ctx, value, server.URL)
	}

	start := make(chan struct{})
	errors := make(chan error, 2)
	requests := []*ExecutionAccount{cloneAgentIdentityTestAccount(account), cloneAgentIdentityTestAccount(account)}
	for _, request := range requests {
		go func() {
			<-start
			errors <- NewExecutionAgentIdentity(coordinator, repo, register, nil).Ensure(context.Background(), request, "")
		}()
	}
	close(start)
	require.NoError(t, <-errors)
	require.NoError(t, <-errors)
	registerMu.Lock()
	defer registerMu.Unlock()
	require.Equal(t, 1, registerCalls)
	require.Equal(t, "task-shared", repo.account.View().GetCredential("task_id"))
}

func cloneAgentIdentityTestAccount(account *ExecutionAccount) *ExecutionAccount {
	copy := *account
	copy.Record.Credentials = querycache.ShallowMap(account.Record.Credentials)
	return &copy
}

type agentIdentityCredentialsRepo struct {
	ExecutionAccountStore

	credentials map[string]any
	account     *ExecutionAccount
	mu          sync.Mutex
}

func (r *agentIdentityCredentialsRepo) GetByID(_ context.Context, _ int64) (*ExecutionAccount, error) {
	return r.account, nil
}

func (r *agentIdentityCredentialsRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credentials = credentials
	return nil
}

func mustAgentIdentityJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

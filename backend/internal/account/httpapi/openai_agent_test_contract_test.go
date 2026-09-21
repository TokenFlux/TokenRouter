package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccountTestServiceOpenAICompactAgentIdentityUsesFreshAssertion(t *testing.T) {
	key, privateKey := newProbeAgentKey(t)
	account := accountcore.Record{
		ID:          21,
		Name:        "agent-identity",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":                  accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":           key.RuntimeID,
			"agent_private_key":          privateKey,
			"task_id":                    key.TaskID,
			"chatgpt_account_id":         "account-agent-test",
			"chatgpt_account_is_fedramp": true,
		},
	}
	repo := &probeAgentStore{account: &account}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &accountprovider.OpenAIAccountTest{Store: repo, Transport: upstream, EnsureTask: probeAgentTasks(repo, "", nil).Ensure}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/21/test", bytes.NewReader(nil))

	require.NoError(t, executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeCompact))
	require.Equal(t, "AgentAssertion", strings.SplitN(upstream.lastReq.Header.Get("Authorization"), " ", 2)[0])
	require.Equal(t, "account-agent-test", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "true", upstream.lastReq.Header.Get("x-openai-fedramp"))
	require.NotContains(t, upstream.lastReq.Header.Get("Authorization"), privateKey)
}

func TestAccountTestServiceOpenAICompactAgentIdentityRecoversInvalidTaskOnce(t *testing.T) {
	key, privateKey := newProbeAgentKey(t)
	account := &accountcore.Record{
		ID:          22,
		Name:        "agent-identity-recovery",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   key.RuntimeID,
			"agent_private_key":  privateKey,
			"task_id":            "task-compact-old",
			"chatgpt_account_id": "account-agent-compact-recovery",
		},
	}
	repo := &probeAgentStore{account: account}
	registerCalls := 0
	registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalls++
		_, _ = io.WriteString(w, `{"task_id":"task-compact-new"}`)
	}))
	defer registerServer.Close()

	upstream := &openAIProbeTransport{responses: []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody))},
	}}
	invalidator := &probeAgentInvalidations{}
	svc := &accountprovider.OpenAIAccountTest{Store: repo, Transport: upstream, EnsureTask: probeAgentTasks(repo, registerServer.URL, invalidator).Ensure}
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/22/test", bytes.NewReader(nil))

	require.NoError(t, executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeCompact))
	require.Equal(t, 1, registerCalls)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "task-compact-new", account.GetCredential("task_id"))
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, []int64{account.ID}, invalidator.accountIDs)
}

// 本地密钥仅用于真实签名器，测试不访问外部认证服务。
func newProbeAgentKey(t *testing.T) (openai.AgentIdentityKey, string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	require.NoError(t, err)
	return openai.AgentIdentityKey{RuntimeID: "runtime-test", TaskID: "task-test", PrivateKey: private}, base64.StdEncoding.EncodeToString(der)
}

type probeAgentStore struct {
	accountprovider.OpenAIAccountTestStore
	account       *accountcore.Record
	setErrorCalls int
}

func (r *probeAgentStore) GetByID(context.Context, int64) (*accountcore.Record, error) {
	return accountcore.CloneRecord(r.account), nil
}
func (r *probeAgentStore) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.account.Credentials = credentials
	return nil
}
func (*probeAgentStore) Update(context.Context, *accountcore.Record) error {
	return fmt.Errorf("unexpected full account update")
}
func (*probeAgentStore) UpdateExtra(context.Context, int64, map[string]any) error { return nil }
func (r *probeAgentStore) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

type probeAgentInvalidations struct{ accountIDs []int64 }

func (r *probeAgentInvalidations) Invalidate(id int64) { r.accountIDs = append(r.accountIDs, id) }
func probeAgentTasks(store *probeAgentStore, endpoint string, invalidator *probeAgentInvalidations) *accountprovider.ProbeTasks {
	options := accountcore.OpenAITaskOptions{Read: store.GetByID,
		Register: func(ctx context.Context, value *accountcore.Record) (string, error) {
			return accountprovider.RegisterAgentIdentityTask(ctx, value, endpoint)
		},
		Persist: func(ctx context.Context, value *accountcore.Record, credentials map[string]any) error {
			_, err := accountcore.PersistCredentials(ctx, store, value, credentials, nil)
			return err
		}}
	if invalidator != nil {
		options.Invalidate = invalidator.Invalidate
	}
	return &accountprovider.ProbeTasks{Coordinator: &accountcore.OpenAITaskCoordinator{}, Options: options}
}

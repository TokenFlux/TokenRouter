package account_test

import (
	"context"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

func TestProtocolSaveEntrypointsAndBulkRejectBeforeWrite(t *testing.T) {
	repo := &accountServiceTestRepo{accounts: map[int64]*accountcore.Record{}}
	svc := newOriginalAccountEditor(repo)
	created, err := svc.CreateAccount(context.Background(), &accountcore.CreateAccountInput{Name: "native", Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, SkipDefaultGroupBind: true, Credentials: map[string]any{"api_key": "test", accountcore.UpstreamProtocolsKey: []string{"anthropic_messages", "openai_responses", "openai_chat_completions"}, "api_base_urls": map[string]any{"responses": "https://relay.example/v1"}}})
	require.NoError(t, err)
	require.NotContains(t, created.Credentials, "api_protocol")
	require.Len(t, created.UpstreamProtocols(), 3)
	updated, err := svc.UpdateAccount(context.Background(), created.ID, &accountcore.UpdateAccountInput{Name: "renamed"})
	require.NoError(t, err)
	require.Len(t, updated.UpstreamProtocols(), 3)
	require.Equal(t, map[string]any{"responses": "https://relay.example/v1"}, updated.Credentials["api_base_urls"])
	rotated, err := svc.UpdateAccount(context.Background(), created.ID, &accountcore.UpdateAccountInput{Credentials: map[string]any{"api_key": "rotated"}})
	require.NoError(t, err)
	require.Len(t, rotated.UpstreamProtocols(), 3)
	require.Equal(t, map[string]any{"responses": "https://relay.example/v1"}, rotated.Credentials["api_base_urls"])
	repo.accounts[99] = &accountcore.Record{ID: 99, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []string{"openai_chat_completions"}}}
	_, err = svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{AccountIDs: []int64{created.ID, 99}, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []string{"openai_responses"}}})
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
	require.Empty(t, repo.bulkUpdates)
}

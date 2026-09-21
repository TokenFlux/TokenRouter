//go:build unit

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type crsDeprecatedExtraAccountRepo struct {
	accountcore.CRSAccountStore
	accounts map[string]*accountcore.Record
	nextID   int64
}

type crsOpenAIDeprecatedExtraSource struct {
	collection  string
	credentials map[string]any
	extra       map[string]any
}

func newCRSDeprecatedExtraAccountRepo(existing ...*accountcore.Record) *crsDeprecatedExtraAccountRepo {
	repo := &crsDeprecatedExtraAccountRepo{accounts: make(map[string]*accountcore.Record)}
	for _, account := range existing {
		if account == nil {
			continue
		}
		crsID, _ := account.Extra["crs_account_id"].(string)
		repo.accounts[crsID] = account
		if account.ID > repo.nextID {
			repo.nextID = account.ID
		}
	}
	return repo
}

func (r *crsDeprecatedExtraAccountRepo) Create(_ context.Context, account *accountcore.Record) error {
	r.nextID++
	account.ID = r.nextID
	crsID, _ := account.Extra["crs_account_id"].(string)
	r.accounts[crsID] = account
	return nil
}

func (r *crsDeprecatedExtraAccountRepo) Update(_ context.Context, account *accountcore.Record) error {
	crsID, _ := account.Extra["crs_account_id"].(string)
	r.accounts[crsID] = account
	return nil
}

func (r *crsDeprecatedExtraAccountRepo) GetByCRSAccountID(_ context.Context, crsID string) (*accountcore.Record, error) {
	return r.accounts[crsID], nil
}

func (r *crsDeprecatedExtraAccountRepo) ListShadowsByParent(_ context.Context, _ int64) ([]*accountcore.Record, error) {
	return nil, nil
}

func TestCRSSyncDiscardsDeprecatedOpenAILongContextBillingExtra(t *testing.T) {
	tests := []struct {
		name          string
		collection    string
		credentials   map[string]any
		sourceValue   any
		existingValue any
		wantAction    string
	}{
		{name: "OAuth create discards true", collection: "openaiOAuthAccounts", credentials: map[string]any{"access_token": "oauth-token"}, sourceValue: true, wantAction: "created"},
		{name: "OAuth create discards malformed", collection: "openaiOAuthAccounts", credentials: map[string]any{"access_token": "oauth-token"}, sourceValue: "false", wantAction: "created"},
		{name: "API key create discards false", collection: "openaiResponsesAccounts", credentials: map[string]any{"api_key": "sk-test"}, sourceValue: false, wantAction: "created"},
		{name: "OAuth update discards existing", collection: "openaiOAuthAccounts", credentials: map[string]any{"access_token": "oauth-token"}, existingValue: true, wantAction: "updated"},
		{name: "API key update discards source and existing", collection: "openaiResponsesAccounts", credentials: map[string]any{"api_key": "sk-test"}, sourceValue: []bool{true}, existingValue: false, wantAction: "updated"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const crsID = "crs-openai-1"
			var existing *accountcore.Record
			if tt.existingValue != nil {
				accountType := capability.AccountTypeOAuth
				if tt.collection == "openaiResponsesAccounts" {
					accountType = capability.AccountTypeAPIKey
				}
				existing = &accountcore.Record{
					ID:       41,
					Platform: capability.PlatformOpenAI,
					Type:     accountType,
					Extra: map[string]any{
						"crs_account_id":                      crsID,
						"openai_long_context_billing_enabled": tt.existingValue,
						"existing_preserved":                  true,
					},
				}
			}
			repo := newCRSDeprecatedExtraAccountRepo(existing)
			sourceExtra := map[string]any{"source_preserved": true}
			if tt.sourceValue != nil {
				sourceExtra["openai_long_context_billing_enabled"] = tt.sourceValue
			}
			result := runCRSOpenAIDeprecatedExtraSync(t, repo, crsOpenAIDeprecatedExtraSource{
				collection:  tt.collection,
				credentials: tt.credentials,
				extra:       sourceExtra,
			})

			require.Len(t, result.Items, 1)
			require.Equal(t, tt.wantAction, result.Items[0].Action)
			stored := repo.accounts[crsID]
			require.NotNil(t, stored)
			require.NotContains(t, stored.Extra, "openai_long_context_billing_enabled")
			require.Equal(t, true, stored.Extra["source_preserved"])
			if existing != nil {
				require.Equal(t, true, stored.Extra["existing_preserved"])
			}
		})
	}
}

func runCRSOpenAIDeprecatedExtraSync(t *testing.T, repo accountcore.CRSAccountStore, source crsOpenAIDeprecatedExtraSource) *accountcore.SyncFromCRSResult {
	t.Helper()
	account := map[string]any{
		"kind":        "openai",
		"id":          "crs-openai-1",
		"name":        "OpenAI CRS",
		"isActive":    true,
		"schedulable": true,
		"credentials": source.credentials,
		"extra":       source.extra,
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/web/auth/login" {
			_, _ = response.Write([]byte(`{"success":true,"token":"admin-token"}`))
			return
		}
		require.Equal(t, "/admin/sync/export-accounts", request.URL.Path)
		require.NoError(t, json.NewEncoder(response).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{source.collection: []any{account}},
		}))
	}))
	t.Cleanup(server.Close)

	service := accountcore.NewCRSSync(repo, nil, NewCRSClient(CRSClientOptions{Configured: true, AllowInsecureHTTP: true}), accountcore.CRSOptions{})
	result, err := service.SyncFromCRS(context.Background(), accountcore.SyncFromCRSInput{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)
	return result
}

// 夹具保存本次配置投影，保持原替身的无并发更新行为。
func (r *crsDeprecatedExtraAccountRepo) UpdateConfiguration(ctx context.Context, record *accountcore.Record, _ accountcore.ConfigurationChange) error {
	return r.Update(ctx, record)
}

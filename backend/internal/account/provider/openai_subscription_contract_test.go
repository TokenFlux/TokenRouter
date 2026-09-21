package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func TestFetchChatGPTSubscriptionExpiresAt(t *testing.T) {
	const wantExpiresAt = "2026-06-10T02:52:15Z"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/backend-api/subscriptions", r.URL.Path)
		require.Equal(t, "acc_123", r.URL.Query().Get("account_id"))
		require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan_type":    "plus",
			"active_until": wantExpiresAt,
			"will_renew":   true,
			"id":           "sub_123",
		})
	}))
	defer server.Close()

	client := upstreamopenai.PrivacyClient{Endpoints: upstreamopenai.PrivacyEndpoints{Subscriptions: server.URL + "/backend-api/subscriptions"}}

	got := client.FetchChatGPTSubscriptionExpiresAt(context.Background(), func(proxyURL string) (*req.Client, error) {
		return req.C().SetTimeout(5 * time.Second), nil
	}, "access-token", "", "acc_123")

	require.Equal(t, wantExpiresAt, got)
}

func TestFetchChatGPTAccountInfo_SkipsExpiredWorkspaceCandidate(t *testing.T) {
	expiredAt := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/backend-api/accounts/check/v4-2023-04-27", r.URL.Path)
		require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accounts": map[string]any{
				"org-expired-workspace": map[string]any{
					"account": map[string]any{
						"plan_type":  "self_serve_business_usage_based",
						"is_default": true,
					},
					"entitlement": map[string]any{
						"expires_at": expiredAt,
					},
				},
				"personal-account": map[string]any{
					"account": map[string]any{
						"plan_type": "free",
					},
				},
			},
		})
	}))
	defer server.Close()

	client := upstreamopenai.PrivacyClient{Endpoints: upstreamopenai.PrivacyEndpoints{Accounts: server.URL + "/backend-api/accounts/check/v4-2023-04-27"}}

	got := client.FetchChatGPTAccountInfo(context.Background(), func(proxyURL string) (*req.Client, error) {
		return req.C().SetTimeout(5 * time.Second), nil
	}, "access-token", "", "org-expired-workspace")

	require.NotNil(t, got)
	require.Equal(t, "free", got.PlanType)
	require.Empty(t, got.SubscriptionExpiresAt)
}

func TestFetchChatGPTAccountInfo_SkipsDeactivatedWorkspaceCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/backend-api/accounts/check/v4-2023-04-27", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accounts": map[string]any{
				"org-deactivated-workspace": map[string]any{
					"account": map[string]any{
						"plan_type":      "self_serve_business_usage_based",
						"is_default":     true,
						"is_deactivated": true,
					},
				},
				"personal-account": map[string]any{
					"account": map[string]any{
						"plan_type": "pro",
					},
				},
			},
		})
	}))
	defer server.Close()

	client := upstreamopenai.PrivacyClient{Endpoints: upstreamopenai.PrivacyEndpoints{Accounts: server.URL + "/backend-api/accounts/check/v4-2023-04-27"}}

	got := client.FetchChatGPTAccountInfo(context.Background(), func(proxyURL string) (*req.Client, error) {
		return req.C().SetTimeout(5 * time.Second), nil
	}, "access-token", "", "org-deactivated-workspace")

	require.NotNil(t, got)
	require.Equal(t, "pro", got.PlanType)
}

func TestFetchChatGPTAccountInfo_ReportsObjectAccountID(t *testing.T) {
	futureAt := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	endpoints := configureChatGPTBackendTestServer(t, chatGPTBackendTestConfig{
		accounts: map[string]any{
			"accounts": map[string]any{
				"default": map[string]any{
					"account": map[string]any{
						"account_id": "personal-account-a",
						"plan_type":  "plus",
						"is_default": true,
					},
					"entitlement": map[string]any{"expires_at": futureAt},
				},
			},
		},
	})

	client := upstreamopenai.PrivacyClient{Endpoints: endpoints}
	got := client.FetchChatGPTAccountInfo(
		context.Background(),
		newLocalPrivacyClientFactory(),
		"access-token",
		"",
		"",
	)
	require.NotNil(t, got)
	require.Equal(t, "plus", got.PlanType)
	require.Equal(t, "personal-account-a", got.AccountID)
}

func TestEnrichTokenInfo_WorkspaceExpiryDoesNotOverridePersonalSubscription(t *testing.T) {
	const (
		personalAccountID   = "personal-account-a"
		workspaceAccountID  = "personal-workspace-b"
		personalActiveUntil = "2027-03-01T00:00:00Z"
	)
	workspaceExpiresAt := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	var subscriptionCalls atomic.Int32
	var requestedAccountID atomic.Value
	endpoints := configureChatGPTBackendTestServer(t, chatGPTBackendTestConfig{
		accounts: map[string]any{
			"accounts": map[string]any{
				workspaceAccountID: map[string]any{
					"account": map[string]any{
						"account_id": workspaceAccountID,
						"plan_type":  "pro",
						"is_default": true,
					},
					"entitlement": map[string]any{"expires_at": workspaceExpiresAt},
				},
			},
		},
		subscription: func(accountID string) map[string]any {
			subscriptionCalls.Add(1)
			requestedAccountID.Store(accountID)
			return map[string]any{
				"plan_type":    "pro",
				"active_until": personalActiveUntil,
				"will_renew":   true,
			}
		},
	})

	tokenInfo := &account.OpenAITokenInfo{
		AccessToken:      "access-token",
		ChatGPTAccountID: personalAccountID,
		OrganizationID:   workspaceAccountID,
		PlanType:         "pro",
	}
	service := newOpenAIAuthorizationForTest(t, nil, nil, &OpenAIAuthorizationDependencies{PrivacyFactory: newLocalPrivacyClientFactory(), PrivacyEndpoints: endpoints})
	service.EnrichTokenInfo(context.Background(), tokenInfo, "")

	require.Equal(t, "pro", tokenInfo.PlanType)
	require.Equal(t, personalActiveUntil, tokenInfo.SubscriptionExpiresAt)
	require.NotEqual(t, workspaceExpiresAt, tokenInfo.SubscriptionExpiresAt)
	require.Equal(t, int32(1), subscriptionCalls.Load())
	require.Equal(t, personalAccountID, requestedAccountID.Load())
}

func TestEnrichTokenInfo_AccountMatchKeepsEntitlementExpiry(t *testing.T) {
	const personalAccountID = "personal-account-a"
	entitlementExpiresAt := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	var subscriptionCalls atomic.Int32
	endpoints := configureChatGPTBackendTestServer(t, chatGPTBackendTestConfig{
		accounts: map[string]any{
			"accounts": map[string]any{
				personalAccountID: map[string]any{
					"account": map[string]any{
						"account_id": personalAccountID,
						"plan_type":  "plus",
						"is_default": true,
					},
					"entitlement": map[string]any{"expires_at": entitlementExpiresAt},
				},
			},
		},
		subscription: func(string) map[string]any {
			subscriptionCalls.Add(1)
			return nil
		},
	})

	tokenInfo := &account.OpenAITokenInfo{
		AccessToken:      "access-token",
		ChatGPTAccountID: personalAccountID,
		OrganizationID:   personalAccountID,
		PlanType:         "plus",
	}
	service := newOpenAIAuthorizationForTest(t, nil, nil, &OpenAIAuthorizationDependencies{PrivacyFactory: newLocalPrivacyClientFactory(), PrivacyEndpoints: endpoints})
	service.EnrichTokenInfo(context.Background(), tokenInfo, "")

	require.Equal(t, entitlementExpiresAt, tokenInfo.SubscriptionExpiresAt)
	require.Zero(t, subscriptionCalls.Load())
}

func TestEnrichTokenInfo_WorkspacePlanKeepsItsOwnExpiry(t *testing.T) {
	const workspaceAccountID = "workspace-b"
	workspaceExpiresAt := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	var subscriptionCalls atomic.Int32
	endpoints := configureChatGPTBackendTestServer(t, chatGPTBackendTestConfig{
		accounts: map[string]any{
			"accounts": map[string]any{
				workspaceAccountID: map[string]any{
					"account": map[string]any{
						"account_id": workspaceAccountID,
						"plan_type":  "self_serve_business_usage_based",
						"is_default": true,
					},
					"entitlement": map[string]any{"expires_at": workspaceExpiresAt},
				},
			},
		},
		subscription: func(string) map[string]any {
			subscriptionCalls.Add(1)
			return nil
		},
	})

	tokenInfo := &account.OpenAITokenInfo{
		AccessToken:      "access-token",
		ChatGPTAccountID: "personal-account-a",
		OrganizationID:   workspaceAccountID,
	}
	service := newOpenAIAuthorizationForTest(t, nil, nil, &OpenAIAuthorizationDependencies{PrivacyFactory: newLocalPrivacyClientFactory(), PrivacyEndpoints: endpoints})
	service.EnrichTokenInfo(context.Background(), tokenInfo, "")

	require.Equal(t, "self_serve_business_usage_based", tokenInfo.PlanType)
	require.Equal(t, workspaceExpiresAt, tokenInfo.SubscriptionExpiresAt)
	require.Zero(t, subscriptionCalls.Load())
}

type chatGPTBackendTestConfig struct {
	accounts     map[string]any
	subscription func(accountID string) map[string]any
}

// configureChatGPTBackendTestServer 在本地接管账号、订阅和隐私设置端点。
func configureChatGPTBackendTestServer(t *testing.T, config chatGPTBackendTestConfig) upstreamopenai.PrivacyEndpoints {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/backend-api/accounts/check/v4-2023-04-27":
			_ = json.NewEncoder(w).Encode(config.accounts)
		case "/backend-api/subscriptions":
			body := map[string]any{}
			if config.subscription != nil {
				if result := config.subscription(r.URL.Query().Get("account_id")); result != nil {
					body = result
				}
			}
			_ = json.NewEncoder(w).Encode(body)
		case "/backend-api/settings/account_user_setting":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": false})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	t.Cleanup(server.Close)
	return upstreamopenai.PrivacyEndpoints{
		Accounts:      server.URL + "/backend-api/accounts/check/v4-2023-04-27",
		Subscriptions: server.URL + "/backend-api/subscriptions",
		Settings:      server.URL + "/backend-api/settings/account_user_setting",
	}
}

func newLocalPrivacyClientFactory() upstreamopenai.PrivacyClientFactory {
	return func(string) (*req.Client, error) {
		return req.C().SetTimeout(5 * time.Second), nil
	}
}

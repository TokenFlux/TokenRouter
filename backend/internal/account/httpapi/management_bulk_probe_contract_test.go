package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	provider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerBulkUpdateOpenAIAPIKeyDoesNotProbe(t *testing.T) {

	account := accountcore.Record{
		ID:          11,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-new",
			"base_url": "http://upstream.example",
		},
	}
	adminSvc := newManagementMutationFixture()
	adminSvc.accounts = []accountcore.Record{account}

	repo := &bulkUpdateProbeAccountRepo{
		accounts: map[int64]*accountcore.Record{account.ID: &account},
		done:     make(chan int64, 1),
	}
	upstream := &bulkUpdateProbeHTTPUpstream{}
	store := repo
	policy := &provider.OpenAIProbePolicy{Available: true, Read: store.GetByID}
	executor := &provider.OpenAIAccountTest{Store: store, Transport: upstream, ValidateURL: (egress.OperatorURLPolicy{AllowInsecureHTTP: true}).Validate, Prepare: policy.Prepare, ApplyRouting: policy.ApplyTestRouting, ResolveTLS: policy.ResolveTestTLS}
	tests := accountcore.NewTestService(&provider.TestTargets{Read: store.GetByID, OpenAI: executor}, accountcore.TestOptions{Now: time.Now})

	router := gin.New()
	accountHandler := newMutationHandler(adminSvc, nil)
	// 与生产路由一致，探测入口独立绑定；普通批量更新不会触发它。
	router.POST("/api/v1/admin/accounts/:id/test", NewTestHandler(tests, nil).Test)
	router.POST("/api/v1/admin/accounts/bulk-update", accountHandler.BulkUpdate)

	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{account.ID},
		"credentials": map[string]any{
			"api_key": "sk-new",
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/bulk-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// 等待异步配置同步结束，确认没有向 OpenAI 上游发探测或写回能力状态。
	select {
	case <-repo.done:
		t.Fatal("OpenAI capability probe must not run")
	case <-time.After(50 * time.Millisecond):
	}
	upstream.mu.Lock()
	require.Empty(t, upstream.urls)
	upstream.mu.Unlock()

	repo.mu.Lock()
	require.NotContains(t, repo.accounts[account.ID].Extra, "openai_responses_probe_status")
	repo.mu.Unlock()
}

type bulkUpdateProbeAccountRepo struct {
	provider.OpenAIAccountTestStore
	mu       sync.Mutex
	accounts map[int64]*accountcore.Record
	done     chan int64
}

func (r *bulkUpdateProbeAccountRepo) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return nil, accountcore.ErrAccountNotFound
	}
	copy := *account
	return &copy, nil
}

func (r *bulkUpdateProbeAccountRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	account := r.accounts[id]
	if account != nil {
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		for key, value := range updates {
			account.Extra[key] = value
		}
	}
	r.mu.Unlock()

	if _, ok := updates["openai_responses_probe_status"]; ok && r.done != nil {
		select {
		case r.done <- id:
		default:
		}
	}
	return nil
}

type bulkUpdateProbeHTTPUpstream struct {
	mu   sync.Mutex
	urls []string
}

func (u *bulkUpdateProbeHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *bulkUpdateProbeHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	u.urls = append(u.urls, req.URL.String())
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
	}, nil
}

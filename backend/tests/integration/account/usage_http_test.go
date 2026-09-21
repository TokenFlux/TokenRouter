package account_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type upstreamUsageHandlerRepo struct {
	accounts map[int64]*accountcore.Record
}

func (r *upstreamUsageHandlerRepo) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	account := r.accounts[id]
	if account == nil {
		return nil, accountcore.ErrAccountNotFound
	}
	copy := *account
	return &copy, nil
}

type upstreamUsageHandlerHTTP struct {
	body string
}

func (u *upstreamUsageHandlerHTTP) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, concurrency, nil)
}

func (u *upstreamUsageHandlerHTTP) DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func newUpstreamUsageHandlerRouter(body string, accounts map[int64]*accountcore.Record) *gin.Engine {
	repo := &upstreamUsageHandlerRepo{accounts: accounts}
	options := accountprovider.UsageHTTPOptions{Available: true, Policy: egress.UsageURLPolicy{Configured: true, AllowInsecureHTTP: true}, Do: (&upstreamUsageHandlerHTTP{body: body}).DoWithTLS}
	usage := accountcore.NewUpstreamUsageService(repo, accountprovider.NewUpstreamUsageHTTPExecution(options), accountcore.UpstreamUsageOptions{})
	handler := accounthttp.NewUpstreamUsageHandler(usage)
	router := gin.New()
	router.POST("/admin/accounts/:id/upstream-usage/query", handler.QueryUpstreamUsage)
	router.POST("/admin/accounts/upstream-usage/query/batch", handler.QueryBatchUpstreamUsage)
	return router
}

func TestAccountHandlerQueryUpstreamUsageReturnsNormalizedResult(t *testing.T) {
	account := &accountcore.Record{
		ID: 7, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-handler-secret", "base_url": "http://upstream.example"},
	}
	router := newUpstreamUsageHandlerRouter(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":12.5,"balance":12.5}`, map[int64]*accountcore.Record{7: account})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/accounts/7/upstream-usage/query", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data accountcore.UpstreamUsageQueryResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, int64(7), envelope.Data.AccountID)
	require.Equal(t, accountcore.UpstreamUsageAdapterSub2API, envelope.Data.Adapter)
	require.Equal(t, "balance", envelope.Data.Mode)
	require.Equal(t, 12.5, *envelope.Data.Balance.Remaining)
	require.NotContains(t, recorder.Body.String(), `"usage"`)
	require.NotContains(t, recorder.Body.String(), "sk-handler-secret")
}

func TestAccountHandlerQueryBatchUpstreamUsageValidatesIDs(t *testing.T) {
	router := newUpstreamUsageHandlerRouter(`{}`, nil)
	for _, payload := range []string{`{}`, `{"account_ids":[]}`, `{"account_ids":[0]}`, `{"account_ids":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45,46,47,48,49,50,51,52,53,54,55,56,57,58,59,60,61,62,63,64,65,66,67,68,69,70,71,72,73,74,75,76,77,78,79,80,81,82,83,84,85,86,87,88,89,90,91,92,93,94,95,96,97,98,99,100,101]}`} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/accounts/upstream-usage/query/batch", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusBadRequest, recorder.Code, payload)
	}

	var envelope struct {
		Reason string `json:"reason"`
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts/upstream-usage/query/batch", strings.NewReader(`{"account_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, "UPSTREAM_USAGE_BATCH_INVALID", envelope.Reason)
}

func TestAccountHandlerQueryUpstreamUsageValidatesAccountID(t *testing.T) {
	router := newUpstreamUsageHandlerRouter(`{}`, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/accounts/not-an-id/upstream-usage/query", nil))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var envelope struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, "UPSTREAM_USAGE_ACCOUNT_INVALID", envelope.Reason)
}

func TestAccountHandlerQueryBatchUpstreamUsageKeepsPerAccountErrors(t *testing.T) {
	account := &accountcore.Record{
		ID: 7, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Status: accountcore.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://upstream.example"},
	}
	router := newUpstreamUsageHandlerRouter(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":1,"balance":1}`, map[int64]*accountcore.Record{7: account})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts/upstream-usage/query/batch", strings.NewReader(`{"account_ids":[7,8]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data struct {
			Usage  map[string]json.RawMessage `json:"usage"`
			Errors map[string]struct {
				Code string `json:"code"`
			} `json:"errors"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Contains(t, envelope.Data.Usage, "7")
	require.Equal(t, "UPSTREAM_USAGE_ACCOUNT_INVALID", envelope.Data.Errors["8"].Code)
}

//go:build unit

package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

type grokQuotaUpstreamStep struct {
	status int
	body   string
	err    error
}

type grokQuotaSequenceUpstream struct {
	mu       sync.Mutex
	steps    []grokQuotaUpstreamStep
	requests []*http.Request
}

func (u *grokQuotaSequenceUpstream) snapshotRequests() []*http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]*http.Request(nil), u.requests...)
}

func healthyGrokQuotaOAuthAccount(id int64) *accountcore.Record {
	return &accountcore.Record{
		ID:          id,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      accountcore.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(2 * accountcore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339),
		},
	}
}

func TestGrokQuotaServiceFetchBillingRetries502ThenSucceeds(t *testing.T) {
	account := healthyGrokQuotaOAuthAccount(401)
	upstream := &grokQuotaSequenceUpstream{steps: []grokQuotaUpstreamStep{
		{status: http.StatusBadGateway, body: `The origin web server returned an invalid or incomplete response to Cloudflare.`},
		{status: http.StatusOK, body: `{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-09T03:25:00Z","end":"2026-07-16T03:25:00Z"},"creditUsagePercent":12}}`},
	}}
	svc := &GrokQuotaTransport{Do: upstream.Do, MapStatus: forward.MapStatus}

	summary, status, err := svc.FetchBilling(context.Background(), account, "access-token", "", true)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, summary)
	require.NotNil(t, summary.UsagePercent)
	require.Equal(t, 12.0, *summary.UsagePercent)
	requests := upstream.snapshotRequests()
	require.Len(t, requests, 2)
	require.Equal(t, http.MethodGet, requests[0].Method)
	require.Equal(t, "/v1/billing", requests[0].URL.Path)
	require.Equal(t, "format=credits", requests[0].URL.RawQuery)
}

func TestGrokQuotaServiceFetchBillingRetriesTransportErrorThenSucceeds(t *testing.T) {
	account := healthyGrokQuotaOAuthAccount(402)
	upstream := &grokQuotaSequenceUpstream{steps: []grokQuotaUpstreamStep{
		{err: errors.New("temporary transport failure")},
		{status: http.StatusOK, body: `{"config":{"currentPeriod":{"type":"WEEKLY"},"creditUsagePercent":8}}`},
	}}
	svc := &GrokQuotaTransport{Do: upstream.Do, MapStatus: forward.MapStatus}

	summary, status, err := svc.FetchBilling(context.Background(), account, "access-token", "", true)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, summary)
	require.Len(t, upstream.snapshotRequests(), 2)
}

func TestGrokQuotaServiceFetchBillingStopsAfterSingleTransientRetry(t *testing.T) {
	account := healthyGrokQuotaOAuthAccount(403)
	upstream := &grokQuotaSequenceUpstream{steps: []grokQuotaUpstreamStep{
		{status: http.StatusBadGateway, body: `cloudflare failure`},
		{status: http.StatusBadGateway, body: `cloudflare failure`},
		{status: http.StatusOK, body: `{"config":{"currentPeriod":{"type":"WEEKLY"}}}`},
	}}
	svc := &GrokQuotaTransport{Do: upstream.Do, MapStatus: forward.MapStatus}

	summary, status, err := svc.FetchBilling(context.Background(), account, "access-token", "", true)

	require.Error(t, err)
	require.Nil(t, summary)
	require.Equal(t, http.StatusBadGateway, status)
	require.Equal(t, "GROK_QUOTA_PROBE_UPSTREAM_ERROR", apperror.Reason(err))
	require.Contains(t, apperror.Message(err), "billing returned 502: cloudflare failure")
	require.Len(t, upstream.snapshotRequests(), 2)
}

func TestGrokQuotaServiceFetchBillingDoesNotRetryNonTransientStatuses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: true},
		{name: "forbidden", status: http.StatusForbidden, wantErr: true},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := healthyGrokQuotaOAuthAccount(404)
			upstream := &grokQuotaSequenceUpstream{steps: []grokQuotaUpstreamStep{
				{status: tt.status, body: `{"error":{"message":"rejected"}}`},
				{status: http.StatusOK, body: `{"config":{"currentPeriod":{"type":"WEEKLY"}}}`},
			}}
			svc := &GrokQuotaTransport{Do: upstream.Do, MapStatus: forward.MapStatus}

			summary, status, err := svc.FetchBilling(context.Background(), account, "access-token", "", true)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Nil(t, summary)
			require.Equal(t, tt.status, status)
			require.Len(t, upstream.snapshotRequests(), 1)
		})
	}
}

// 顺序传输替身只返回原响应步骤，重试仍由真实供应商客户端执行。
func (u *grokQuotaSequenceUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, req)
	index := len(u.requests) - 1
	if index >= len(u.steps) {
		return nil, errors.New("unexpected upstream request")
	}
	step := u.steps[index]
	if step.err != nil {
		return nil, step.err
	}
	return &http.Response{StatusCode: step.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(step.body))}, nil
}

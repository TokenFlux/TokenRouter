package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 上游返回前替换管理员凭据，旧额度不能写入新身份的快照或停调状态。
type qoderObservationIdentityRepo struct {
	account.OAuthUsageReader
	current *account.Record
	writes  int
}

func (r *qoderObservationIdentityRepo) GetByID(context.Context, int64) (*account.Record, error) {
	v := *r.current
	return &v, nil
}
func (r *qoderObservationIdentityRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.writes++
	return nil
}
func (r *qoderObservationIdentityRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.writes++
	return nil
}
func (r *qoderObservationIdentityRepo) ClearRateLimit(context.Context, int64) error {
	r.writes++
	return nil
}

type qoderObservationIdentityUpstream struct {
	qoderUsageHTTPUpstreamStub
	beforeReturn func()
}

func (u *qoderObservationIdentityUpstream) DoWithTLS(req *http.Request, proxy string, id int64, n int, p *tlsfingerprint.Profile) (*http.Response, error) {
	resp, err := u.qoderUsageHTTPUpstreamStub.DoWithTLS(req, proxy, id, n, p)
	u.beforeReturn()
	return resp, err
}
func (u *qoderObservationIdentityUpstream) Do(req *http.Request, proxy string, id int64, n int) (*http.Response, error) {
	return u.DoWithTLS(req, proxy, id, n, nil)
}
func TestQoderObservationCannotWriteNewAdministratorIdentity(t *testing.T) {
	repo := &qoderObservationIdentityRepo{current: &account.Record{ID: 917, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Status: account.StatusActive, Credentials: qoderUsageCredentials("observed")}}
	u := &qoderObservationIdentityUpstream{qoderUsageHTTPUpstreamStub: qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{"userType":"teams","usageType":"credits","isQuotaExceeded":true,"expiresAt":%d,"userQuota":{"total":100,"used":100,"remaining":0}}`, time.Now().Add(time.Hour).UnixMilli())}, beforeReturn: func() { repo.current.Credentials = qoderUsageCredentials("administrator") }}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: account.NewOAuthUsageCache(), httpUpstream: u})
	_, _ = svc.GetUsage(context.Background(), 917)
	require.Zero(t, repo.writes, "旧观测不能给新凭据写入额度快照或停调")
}

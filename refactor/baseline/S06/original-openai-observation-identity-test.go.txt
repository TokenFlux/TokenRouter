package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

// OpenAI 异步写回保留独立取消，但只能写入产生该响应的账号身份。
type openAIObservationIdentityRepo struct {
	qoderObservationIdentityRepo
	written chan struct{}
}

func (r *openAIObservationIdentityRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	select {
	case r.written <- struct{}{}:
	default:
	}
	return nil
}

type openAIObservationIdentityUpstream struct {
	accountUsageHTTPUpstreamStub
	beforeReturn func()
}

func (u *openAIObservationIdentityUpstream) DoWithTLS(req *http.Request, proxy string, id int64, n int, p *tlsfingerprint.Profile) (*http.Response, error) {
	resp, err := u.accountUsageHTTPUpstreamStub.DoWithTLS(req, proxy, id, n, p)
	u.beforeReturn()
	return resp, err
}
func TestOpenAIUsageProbeCannotWriteNewAdministratorIdentity(t *testing.T) {
	observed := &Account{ID: 923, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "observed"}}
	current := *observed
	repo := &openAIObservationIdentityRepo{qoderObservationIdentityRepo: qoderObservationIdentityRepo{current: &current}, written: make(chan struct{}, 1)}
	upstream := &openAIObservationIdentityUpstream{beforeReturn: func() { current.Credentials = map[string]any{"access_token": "administrator"} }}
	svc := &AccountUsageService{accountRepo: repo, httpUpstream: upstream}
	updates, err := svc.probeOpenAICodexSnapshot(context.Background(), observed)
	require.NoError(t, err)
	require.NotEmpty(t, updates)
	select {
	case <-repo.written:
		t.Fatal("迟到的 OpenAI 快照写入了新身份")
	case <-time.After(100 * time.Millisecond):
	}
}

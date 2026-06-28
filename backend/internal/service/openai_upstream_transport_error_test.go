//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// openAITransportAccountRepoStub 只记录临时不可调度调用，其他仓储方法不应被触达。
type openAITransportAccountRepoStub struct {
	AccountRepository
	tempUnschedCalls []tempUnschedCall
}

func (r *openAITransportAccountRepoStub) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls = append(r.tempUnschedCalls, tempUnschedCall{accountID: id, until: until, reason: reason})
	return nil
}

func newOpenAITransportErrTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

func TestHandleOpenAIUpstreamTransportError_PersistentEvictsAndFailsOver(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 4627, Name: "proxy-expired", Platform: PlatformOpenAI}
	c, rec := newOpenAITransportErrTestContext()

	before := time.Now()
	retErr := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account,
		errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": socks connect tcp 85.255.176.68:12324->chatgpt.com:443: username/password authentication failed`), false)
	after := time.Now()

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(retErr, &failoverErr), "persistent error must return *UpstreamFailoverError")
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Len(t, repo.tempUnschedCalls, 1)
	require.Equal(t, int64(4627), repo.tempUnschedCalls[0].accountID)
	require.Contains(t, repo.tempUnschedCalls[0].reason, "authentication failed")
	require.True(t, repo.tempUnschedCalls[0].until.After(before.Add(openAITransportErrorTempUnschedDuration-time.Second)))
	require.True(t, repo.tempUnschedCalls[0].until.Before(after.Add(openAITransportErrorTempUnschedDuration+time.Second)))
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 0, rec.Body.Len())
}

func TestHandleOpenAIUpstreamTransportError_TransientFailsOverWithoutEviction(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 99, Name: "flaky", Platform: PlatformOpenAI}
	c, rec := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account,
		errors.New(`Post "https://chatgpt.com/...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), false)

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "transient error must return *UpstreamFailoverError")
	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 0, rec.Body.Len())
}

func TestHandleOpenAIUpstreamTransportError_ContextCanceledNoFailover(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 77, Name: "healthy", Platform: PlatformOpenAI}
	c, rec := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, context.Canceled, false)

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "context.Canceled must not return *UpstreamFailoverError")
	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 0, rec.Body.Len())
}

func TestHandleOpenAIUpstreamTransportError_WrappedContextCanceledNoFailover(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 78, Name: "healthy2", Platform: PlatformOpenAI}
	c, _ := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, fmt.Errorf("http request failed: %w", context.Canceled), false)

	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "wrapped context.Canceled must not return *UpstreamFailoverError")
	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

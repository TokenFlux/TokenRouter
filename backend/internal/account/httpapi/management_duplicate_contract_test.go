//go:build unit

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	idempotencytest "github.com/TokenFlux/TokenRouter/internal/idempotency/testkit"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type duplicateAccountAdminServiceStub struct {
	AccountManagement
	account      *accountcore.Record
	calls        int
	recoverCalls int
	accountID    int64
	actorScope   string
	operationKey string
	recoverScope string
	recoverKey   string
	recoverErr   error
	created      bool
}

type blockingDuplicateAdminServiceStub struct {
	AccountManagement
	account      *accountcore.Record
	started      chan struct{}
	release      chan struct{}
	calls        atomic.Int32
	recoverCalls atomic.Int32
	recoverErr   error
}

type failOnceMarkSucceededRepo struct {
	*idempotencytest.MemoryStore
	failNext bool
}

func (r *failOnceMarkSucceededRepo) MarkSucceeded(ctx context.Context, id int64, responseStatus int, responseBody string, expiresAt time.Time) error {
	if r.failNext {
		r.failNext = false
		return errors.New("mark succeeded failed")
	}
	return r.MemoryStore.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}

func (s *duplicateAccountAdminServiceStub) DuplicateAccount(_ context.Context, accountID int64, actorScope, operationKey string) (*accountcore.Record, error) {
	s.calls++
	s.accountID = accountID
	s.actorScope = actorScope
	s.operationKey = operationKey
	s.created = true
	return s.account, nil
}

func (s *duplicateAccountAdminServiceStub) RecoverDuplicateAccount(_ context.Context, _ int64, actorScope, operationKey string) (*accountcore.Record, error) {
	s.recoverCalls++
	s.recoverScope = actorScope
	s.recoverKey = operationKey
	if s.recoverErr != nil {
		return nil, s.recoverErr
	}
	if !s.created {
		return nil, nil
	}
	return s.account, nil
}

func (s *blockingDuplicateAdminServiceStub) DuplicateAccount(_ context.Context, _ int64, _, _ string) (*accountcore.Record, error) {
	s.calls.Add(1)
	close(s.started)
	<-s.release
	return s.account, nil
}

func (s *blockingDuplicateAdminServiceStub) RecoverDuplicateAccount(_ context.Context, _ int64, _, _ string) (*accountcore.Record, error) {
	s.recoverCalls.Add(1)
	return nil, s.recoverErr
}

func setupDuplicateAccountRouter(t *testing.T, svc AccountManagement, coordinators ...*idempotency.IdempotencyCoordinator) *gin.Engine {
	t.Helper()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 77})
		c.Next()
	})
	handler := NewManagementHandler(svc, ManagementOptions{Presenter: NewRuntimePresenter(accountcore.NewRuntimeStatusReader(accountcore.RuntimeStatusOptions{}), svc, nil)})
	if len(coordinators) > 0 {
		handler.BindIdempotency(coordinators[0])
	}
	router.POST("/api/v1/admin/accounts/:id/duplicate", handler.Duplicate)
	return router
}

func TestDuplicateAccountHandlerRedactsCredentials(t *testing.T) {
	svc := &duplicateAccountAdminServiceStub{
		account: &accountcore.Record{
			ID:          43,
			Name:        "primary (Copy)",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: false,
			Credentials: map[string]any{"api_key": "top-secret-key"},
		},
	}
	router := setupDuplicateAccountRouter(t, svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/duplicate", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, svc.calls)
	require.Contains(t, recorder.Body.String(), `"name":"primary (Copy)"`)
	require.NotContains(t, recorder.Body.String(), "top-secret-key")
	var responseBody struct {
		Data struct {
			Credentials map[string]any `json:"credentials"`
			Schedulable bool           `json:"schedulable"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
	require.Empty(t, responseBody.Data.Credentials)
	require.False(t, responseBody.Data.Schedulable)
}

func TestDuplicateAccountHandlerRejectsInvalidID(t *testing.T) {
	svc := &duplicateAccountAdminServiceStub{}
	router := setupDuplicateAccountRouter(t, svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/not-a-number/duplicate", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, svc.calls)
}

func TestDuplicateAccountHandlerReplaysSameIdempotencyKey(t *testing.T) {
	svc := &duplicateAccountAdminServiceStub{
		account: &accountcore.Record{
			ID:          43,
			Name:        "primary (Copy)",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: false,
		},
	}
	repo := idempotencytest.NewMemoryStore()
	coordinator := idempotency.NewIdempotencyCoordinator(repo, idempotency.DefaultIdempotencyConfig())
	router := setupDuplicateAccountRouter(t, svc, coordinator)

	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/duplicate", nil)
		request.Header.Set("Idempotency-Key", "duplicate-account-42")
		router.ServeHTTP(recorder, request)
		return recorder
	}

	first := call()
	second := call()

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, 1, svc.calls)
	require.Equal(t, int64(42), svc.accountID)
	require.Equal(t, "admin:77", svc.actorScope)
	require.Equal(t, "duplicate-account-42", svc.operationKey)
	require.Equal(t, "true", second.Header().Get("X-Idempotency-Replayed"))
}

func TestDuplicateAccountHandlerRecoversAfterMarkSucceededFailure(t *testing.T) {
	svc := &duplicateAccountAdminServiceStub{
		account: &accountcore.Record{
			ID:          43,
			Name:        "primary (Copy)",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: false,
		},
	}
	repo := &failOnceMarkSucceededRepo{MemoryStore: idempotencytest.NewMemoryStore(), failNext: true}
	coordinator := idempotency.NewIdempotencyCoordinator(repo, idempotency.DefaultIdempotencyConfig())
	router := setupDuplicateAccountRouter(t, svc, coordinator)

	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/duplicate", nil)
		request.Header.Set("Idempotency-Key", "duplicate-account-42-recovery")
		router.ServeHTTP(recorder, request)
		return recorder
	}

	first := call()
	second := call()

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "true", first.Header().Get("X-Idempotency-Recovered"))
	require.Equal(t, "true", second.Header().Get("X-Idempotency-Recovered"))
	require.Equal(t, 1, svc.calls, "ambiguous retries must not repeat the create side effect")
	require.Equal(t, 2, svc.recoverCalls)
	require.Equal(t, "admin:77", svc.recoverScope)
	require.Equal(t, "duplicate-account-42-recovery", svc.recoverKey)
	require.Contains(t, second.Body.String(), `"id":43`)
}

func TestDuplicateAccountHandlerPreservesIdempotencyErrorWhenRecoveryLookupFails(t *testing.T) {
	svc := &duplicateAccountAdminServiceStub{
		account: &accountcore.Record{
			ID:          43,
			Name:        "primary (Copy)",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: false,
		},
		recoverErr: errors.New("recovery database unavailable"),
	}
	repo := &failOnceMarkSucceededRepo{MemoryStore: idempotencytest.NewMemoryStore(), failNext: true}
	coordinator := idempotency.NewIdempotencyCoordinator(repo, idempotency.DefaultIdempotencyConfig())
	router := setupDuplicateAccountRouter(t, svc, coordinator)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/duplicate", nil)
	request.Header.Set("Idempotency-Key", "duplicate-account-42-recovery-error")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "IDEMPOTENCY_STORE_UNAVAILABLE")
	require.Equal(t, 1, svc.calls)
	require.Equal(t, 1, svc.recoverCalls)
	require.Equal(t, "admin:77", svc.recoverScope)
}

func TestDuplicateAccountHandlerDoesNotReexecuteWhileOriginalIsProcessing(t *testing.T) {
	svc := &blockingDuplicateAdminServiceStub{
		account: &accountcore.Record{
			ID:          43,
			Name:        "primary (Copy)",
			Platform:    capability.PlatformAnthropic,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: false,
		},
		started:    make(chan struct{}),
		release:    make(chan struct{}),
		recoverErr: errors.New("recovery database unavailable"),
	}
	coordinator := idempotency.NewIdempotencyCoordinator(idempotencytest.NewMemoryStore(), idempotency.DefaultIdempotencyConfig())
	router := setupDuplicateAccountRouter(t, svc, coordinator)

	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/duplicate", nil)
		request.Header.Set("Idempotency-Key", "duplicate-account-42-active")
		router.ServeHTTP(recorder, request)
		return recorder
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- call() }()
	<-svc.started

	second := call()
	require.Equal(t, http.StatusConflict, second.Code)
	require.Contains(t, second.Body.String(), "IDEMPOTENCY_IN_PROGRESS")
	require.Equal(t, int32(1), svc.calls.Load())
	require.Equal(t, int32(1), svc.recoverCalls.Load())

	close(svc.release)
	select {
	case first := <-firstDone:
		require.Equal(t, http.StatusOK, first.Code)
	case <-time.After(time.Second):
		t.Fatal("original duplicate request did not finish")
	}
}

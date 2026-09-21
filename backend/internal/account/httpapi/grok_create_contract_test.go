//go:build unit

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokImportAdminService struct {
	*managementMutationFixture
	mu     sync.Mutex
	nextID int64
}

func newGrokImportAdminService() *grokImportAdminService {
	return &grokImportAdminService{
		managementMutationFixture: newManagementMutationFixture(),
		nextID:                    500,
	}
}

func (s *grokImportAdminService) CreateAccount(_ context.Context, input *account.CreateAccountInput) (*account.Record, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()
	return &account.Record{
		ID:          id,
		Name:        input.Name,
		Platform:    input.Platform,
		Type:        input.Type,
		Credentials: input.Credentials,
		Extra:       input.Extra,
		ProxyID:     input.ProxyID,
		Concurrency: input.Concurrency,
		Status:      billing.StatusActive,
		Schedulable: true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func TestAccountCreateWithoutAutomaticGrokProbeServiceStillSucceeds(t *testing.T) {

	source := newGrokImportAdminService()
	presenter := NewRuntimePresenter(account.NewRuntimeStatusReader(account.RuntimeStatusOptions{}), source, nil)
	handler := NewManagementHandler(source, ManagementOptions{Presenter: presenter, Privacy: source})

	router := gin.New()
	router.POST("/api/v1/admin/accounts", handler.Create)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts",
		strings.NewReader(`{"name":"grok-rt","platform":"grok","type":"oauth","credentials":{"refresh_token":"secret"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
}

//go:build unit

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokImportOAuthClientStub struct{}

func (grokImportOAuthClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

func (grokImportOAuthClientStub) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

func (grokImportOAuthClientStub) LoginWithPassword(context.Context, string, string, string) (*account.GrokPasswordLoginResult, error) {
	return nil, errors.New("unexpected password login")
}

func (grokImportOAuthClientStub) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

func TestGrokSSOBatchImportKeepsCreatedAccountsWhenOneAutomaticProbeFails(t *testing.T) {

	adminService := newGrokImportAdminService()
	oauthService := newGrokAuthorizationForTest(nil, grokImportOAuthClientStub{})
	oauthService.Start()
	defer stopGrokAuthorizationForTest(t, oauthService)
	prober := newGrokImportProbeStub(3)
	prober.failures[502] = apperror.New(502, "GROK_TEST_PROBE_FAILED", "sensitive-upstream-body")
	queue := account.NewGrokImportProbeScheduler(account.GrokImportProbeOptions{Concurrency: 3, Timeout: 25 * time.Second})
	var tasks sync.WaitGroup
	t.Cleanup(func() { tasks.Wait(); require.NoError(t, queue.StopContext(context.Background())) })
	imports := account.NewGrokAccountImport(oauthService, account.GrokAccountImportOptions{
		Get: adminService.GetAccount, Create: adminService.CreateAccount, Update: adminService.UpdateAccount,
		NormalizeToken: xai.NormalizeSSOToken, LogError: func(string, ...any) {},
		Schedule: func(value *account.Record) { snapshot := value.RoutingSnapshot(); queue.Schedule(prober, &snapshot) },
		RunTask:  func(_ string, run func()) { tasks.Add(1); go func() { defer tasks.Done(); run() }() },
	})
	handler := NewGrokOAuthHandler(oauthService, imports, nil, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.POST("/api/v1/admin/grok/sso-to-oauth", handler.CreateAccountsFromSSO)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/grok/sso-to-oauth",
		strings.NewReader(`{"sso_tokens":["sso-one","sso-two","sso-three"]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"created"`)
	require.NotContains(t, recorder.Body.String(), `GROK_TEST_PROBE_FAILED`)
	for i := 0; i < 3; i++ {
		awaitGrokProbeSignal(t, prober.done)
	}
	calls, _, _ := prober.snapshot()
	require.Equal(t, map[int64]int{501: 1, 502: 1, 503: 1}, calls)
}

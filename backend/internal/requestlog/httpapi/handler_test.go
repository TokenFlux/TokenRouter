package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
)

// handler_test.go checks authenticated roles on the request diagnostic routes.
// The handlers check identity before reading the repository fixture, even when
// query parameters claim another user or administrator access. The scenarios
// exercise query, path, and writer-health endpoints and inspect provider and
// alias metadata on successful detail reads.

// requestRepositoryStub records authorized lookup arguments and returns one
// fixed diagnostic detail.
type requestRepositoryStub struct {
	// userID and admin capture the most recent Find inputs, and calls counts
	// repository reads. Each serial handler scenario owns its stub and inspects it
	// after ServeHTTP returns.
	userID int64 // captured authenticated user identifier
	admin  bool  // captured trusted administrator flag
	calls  int   // fixture lookup invocation count
}

// Save reports success without retaining snapshots in the HTTP authorization
// fixture.
func (r *requestRepositoryStub) Save(context.Context, []telemetry.RequestRecord) error { return nil }

// Cleanup reports success without removing the fixed HTTP diagnostic fixture.
func (r *requestRepositoryStub) Cleanup(context.Context, time.Time) error { return nil }

// Find records userID, admin, and one call before returning fixed metadata with
// provider, alias, attempt, error, and audit references. It ignores context and
// lookup text; scenarios inspect arguments after handler completion.
func (r *requestRepositoryStub) Find(_ context.Context, _ string, userID int64, admin bool) ([]requestlog.Detail, error) {
	r.userID, r.admin = userID, admin
	r.calls++
	return []requestlog.Detail{{RequestRecord: telemetry.RequestRecord{RequestID: "request", UserID: 42, ProviderID: 99, Aliases: []telemetry.RequestAlias{{Kind: "upstream", Value: "supplier"}}, Attempts: []telemetry.RequestAttempt{{Number: 1, ProviderID: 99, RequestID: "supplier"}}}, Errors: []requestlog.Failure{{ID: 7, Status: 500}}, AuditIDs: []int64{8}}}, nil
}

// FindProviderNames supplies the configured display name for the fixture attempt.
func (r *requestRepositoryStub) FindProviderNames(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{99: "Primary upstream"}, nil
}

// TestRequestDiagnosticsRequireAdmin exercises anonymous, user, and
// administrator identities across query, path, and health routes. It checks
// status, rejected-read counts, trusted lookup arguments, and successful
// provider ID, display name, and alias count in the response.
func TestRequestDiagnosticsRequireAdmin(t *testing.T) {
	for _, test := range []struct {
		// name identifies the case while role determines whether middleware installs
		// user identity and its authorization role. status is the expected outcome
		// shared by detail and health endpoints.
		name string // authenticated identity scenario label
		// role is the middleware role fixture. The empty string installs no identity;
		// other values install UserID forty-two together with that exact role.
		role   string
		status int // expected HTTP response status
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"user", "user", http.StatusForbidden},
		{"admin", "admin", http.StatusOK},
	} {
		for _, path := range []string{"/requests?request_id=request&user_id=99&admin=true", "/requests/request", "/requests/health"} {
			t.Run(test.name+path, func(t *testing.T) {
				repo := &requestRepositoryStub{}
				handler := NewHandler(requestlog.NewService(repo, 30, nil))
				router := gin.New()
				router.Use(func(c *gin.Context) {
					if test.role != "" {
						c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 42})
						c.Set(authctx.ContextKeyUserRole, test.role)
					}
					c.Next()
				})
				router.GET("/requests", handler.Find)
				router.GET("/requests/health", handler.Health)
				router.GET("/requests/:request_id", handler.Find)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				require.Equal(t, test.status, response.Code)
				if test.role != "admin" || path == "/requests/health" {
					require.Zero(t, repo.calls)
					return
				}
				require.Equal(t, int64(42), repo.userID)
				require.True(t, repo.admin)
				require.Equal(t, 1, repo.calls)
				var body struct {
					Data struct { // decoded response payload object
						Items []requestlog.Detail `json:"items"` // returned diagnostic detail collection
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
				require.Len(t, body.Data.Items, 1)
				require.Equal(t, int64(99), body.Data.Items[0].ProviderID)
				require.Equal(t, map[int64]string{99: "Primary upstream"}, body.Data.Items[0].ProviderNames)
				require.Len(t, body.Data.Items[0].Aliases, 1)
			})
		}
	}
}

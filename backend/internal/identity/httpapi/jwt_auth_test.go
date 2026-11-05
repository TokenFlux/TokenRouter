package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// Panel JWT admission checks credential syntax and current account state
// before publishing Gin user values. The fixtures sign real session tokens,
// supply map-backed users, and record activity calls so responses can
// distinguish missing credentials, stale versions, invalid signatures, and
// disabled accounts.

// stubJWTUserRepo supplies map-backed current accounts and the auxiliary reads
// used by middleware fixtures.
type stubJWTUserRepo struct {
	// UserRepository leaves unused operations unavailable. Tests provide the
	// methods needed for current-account lookup and avatar hydration, so other
	// promoted calls expose unexpected repository access.
	identity.
		UserRepository
	// users retains the supplied map and account pointers. Fixture changes remain
	// visible to requests, and UserService hydration updates the same accounts.
	users map[int64]*identity.User
}

// recordingActivityToucher collects authenticated account IDs passed through
// panel activity recording.
type recordingActivityToucher struct {
	userIDs []int64 // recorded activity account identifiers
}

// GetByID returns the fixture account pointer for the requested ID. Missing
// IDs return an error, and the stored pointers remain shared with tests and
// UserService hydration.
func (r *stubJWTUserRepo) GetByID(_ context.Context, id int64) (*identity.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return u, nil
}

// GetUserAvatar returns no avatar and no error for middleware fixture
// accounts.
func (r *stubJWTUserRepo) GetUserAvatar(_ context.Context, _ int64) (*identity.UserAvatar, error) {
	return nil, nil
}

// UpdateUserLastActiveAt acknowledges fixture activity writes without changing
// account data.
func (r *stubJWTUserRepo) UpdateUserLastActiveAt(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

// TouchLastActiveForUser records the supplied account ID in fixture call
// order. Nil users are ignored, letting the test inspect activity recording
// after a successful middleware authentication.
func (r *recordingActivityToucher) TouchLastActiveForUser(_ context.Context, user *identity.User) {
	if user == nil {
		return
	}
	r.userIDs = append(r.userIDs, user.ID)
}

// newJWTTestEnv builds a protected Gin route and a session service using the
// supplied user map. The caller signs tokens with the returned service;
// repository account pointers remain shared during requests.
func newJWTTestEnv(users map[int64]*identity.User) (*gin.Engine, *identity.SessionService) {
	cfg := &config.Config{}
	cfg.Auth.JWT.Secret = "test-jwt-secret-32bytes-long!!!"
	cfg.Auth.JWT.AccessTokenTTLMinutes = 60

	userRepo := &stubJWTUserRepo{users: users}
	authSvc := identity.NewSessionService(identity.SessionOptions{Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes, RefreshTokenExpireDays: cfg.Auth.JWT.RefreshTokenTTLDays}, userRepo, nil, nil, nil)
	// Fixture background callbacks run independently of the request. These tests
	// assert synchronous authentication output, while the supplied activity
	// writer acknowledges any scheduled account update.
	userSvc := identity.NewUserService(userRepo, nil, nil, nil, func(_ string, fn func()) bool { go fn(); return true })
	mw := identityhttp.JWTAuth(authSvc, userSvc, userSvc, nil, nil)

	r := gin.New()
	r.Use(mw)
	r.GET("/protected", func(c *gin.Context) {
		subject, _ := authctx.GetAuthSubjectFromContext(c)
		role, _ := authctx.GetUserRoleFromContext(c)
		c.JSON(http.StatusOK, gin.H{
			"user_id": subject.UserID,
			"role":    role,
		})
	})
	return r, authSvc
}

// TestJWTAuth_ValidToken supplies a signed token for an active map-backed
// account. It checks HTTP 200 and the user ID and role published to the
// protected response.
func TestJWTAuth_ValidToken(t *testing.T) {
	user := &identity.User{
		ID:           1,
		Email:        "test@example.com",
		Role:         "user",
		Status:       billing.StatusActive,
		Concurrency:  5,
		TokenVersion: 1,
	}
	router, authSvc := newJWTTestEnv(map[int64]*identity.User{1: user})

	token, err := authSvc.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(1), body["user_id"])
	require.Equal(t, "user", body["role"])
}

// TestJWTAuth_ValidToken_LowercaseBearer uses a lowercase bearer scheme with a
// valid signed account token. It checks that case-insensitive scheme admission
// reaches the protected route with HTTP 200.
func TestJWTAuth_ValidToken_LowercaseBearer(t *testing.T) {
	user := &identity.User{
		ID:           1,
		Email:        "test@example.com",
		Role:         "user",
		Status:       billing.StatusActive,
		Concurrency:  5,
		TokenVersion: 1,
	}
	router, authSvc := newJWTTestEnv(map[int64]*identity.User{1: user})

	token, err := authSvc.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestJWTAuth_ValidToken_TouchesLastActive authenticates an active account
// with a recording activity fixture. It checks successful HTTP admission and
// exactly the expected account ID passed to that fixture.
func TestJWTAuth_ValidToken_TouchesLastActive(t *testing.T) {
	user := &identity.User{
		ID:           1,
		Email:        "test@example.com",
		Role:         "user",
		Status:       billing.StatusActive,
		Concurrency:  5,
		TokenVersion: 1,
	}

	cfg := &config.Config{}
	cfg.Auth.JWT.Secret = "test-jwt-secret-32bytes-long!!!"
	cfg.Auth.JWT.AccessTokenTTLMinutes = 60

	userRepo := &stubJWTUserRepo{users: map[int64]*identity.User{1: user}}
	authSvc := identity.NewSessionService(identity.SessionOptions{Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes, RefreshTokenExpireDays: cfg.Auth.JWT.RefreshTokenTTLDays}, userRepo, nil, nil, nil)
	// Fixture background callbacks run independently of the request. These tests
	// assert synchronous authentication output, while the supplied activity
	// writer acknowledges any scheduled account update.
	userSvc := identity.NewUserService(userRepo, nil, nil, nil, func(_ string, fn func()) bool { go fn(); return true })
	toucher := &recordingActivityToucher{}

	r := gin.New()
	r.Use(identityhttp.JWTAuth(authSvc, userSvc, toucher, nil, nil))
	r.GET("/protected", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	token, err := authSvc.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []int64{1}, toucher.userIDs)
}

// TestJWTAuth_MissingAuthorizationHeader omits Authorization while requesting
// the protected route. It checks HTTP 401 and the UNAUTHORIZED response code
// before any current-account lookup can succeed.
func TestJWTAuth_MissingAuthorizationHeader(t *testing.T) {
	router, _ := newJWTTestEnv(nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "UNAUTHORIZED", body.Code)
}

// TestJWTAuth_InvalidHeaderFormat supplies three malformed authorization
// schemes or separators to the protected route. It checks HTTP 401 and
// INVALID_AUTH_HEADER for every input shape.
func TestJWTAuth_InvalidHeaderFormat(t *testing.T) {
	tests := []struct {
		name   string // malformed header case
		header string // submitted authorization header text
	}{
		{"无Bearer前缀", "Token abc123"},
		{"缺少空格分隔", "Bearerabc123"},
		{"仅有单词", "abc123"},
	}
	router, _ := newJWTTestEnv(nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", tt.header)
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code)
			var body httpx.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, "INVALID_AUTH_HEADER", body.Code)
		})
	}
}

// TestJWTAuth_EmptyToken supplies the Bearer prefix with an empty credential
// to the protected route. It checks HTTP 401 and EMPTY_TOKEN in the decoded
// panel error.
func TestJWTAuth_EmptyToken(t *testing.T) {
	router, _ := newJWTTestEnv(nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer ")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "EMPTY_TOKEN", body.Code)
}

// TestJWTAuth_TamperedToken supplies a JWT-shaped credential with an invalid
// signature to the protected route. It checks HTTP 401 and INVALID_TOKEN in
// the panel response.
func TestJWTAuth_TamperedToken(t *testing.T) {
	router, _ := newJWTTestEnv(nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyX2lkIjoxfQ.invalid_signature")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "INVALID_TOKEN", body.Code)
}

// TestJWTAuth_UserNotFound signs a token for an account absent from the
// map-backed repository. It checks HTTP 401 and USER_NOT_FOUND after
// signed-token parsing succeeds.
func TestJWTAuth_UserNotFound(t *testing.T) {
	// Issue a token for a user absent from the repository.
	fakeUser := &identity.User{
		ID:           999,
		Email:        "ghost@example.com",
		Role:         "user",
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}

	router, authSvc := newJWTTestEnv(map[int64]*identity.User{})

	token, err := authSvc.GenerateToken(context.Background(), fakeUser)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "USER_NOT_FOUND", body.Code)
}

// TestJWTAuth_UserInactive signs a token for an account currently marked
// disabled in the fixture. It checks HTTP 401 and USER_INACTIVE when
// middleware reloads that account.
func TestJWTAuth_UserInactive(t *testing.T) {
	user := &identity.User{
		ID:           1,
		Email:        "disabled@example.com",
		Role:         "user",
		Status:       billing.StatusDisabled,
		TokenVersion: 1,
	}
	router, authSvc := newJWTTestEnv(map[int64]*identity.User{1: user})

	token, err := authSvc.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "USER_INACTIVE", body.Code)
}

// TestJWTAuth_TokenVersionMismatch signs version one while current fixture
// state carries version two. It checks HTTP 401 and TOKEN_REVOKED before the
// protected route is admitted.
func TestJWTAuth_TokenVersionMismatch(t *testing.T) {
	// The issued token has version 1 while current user state has version 2.
	userForToken := &identity.User{
		ID:           1,
		Email:        "test@example.com",
		Role:         "user",
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}
	userInDB := &identity.User{
		ID:           1,
		Email:        "test@example.com",
		Role:         "user",
		Status:       billing.StatusActive,
		TokenVersion: 2, // current fixture version seed
	}
	router, authSvc := newJWTTestEnv(map[int64]*identity.User{1: userInDB})

	token, err := authSvc.GenerateToken(context.Background(), userForToken)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var body httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "TOKEN_REVOKED", body.Code)
}

package httpapi_test

import (
	"context"
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
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// Administrator JWT requests compare signed versions with the currently loaded
// account through both Bearer headers and WebSocket subprotocols. The
// repository fixture exposes configured ID lookup and an absent avatar, making
// other authentication repository operations fail if reached.

// stubUserRepo exposes configured account lookup and rejects unexpected
// authentication repository calls.
type stubUserRepo struct {
	// getByID supplies the current account for middleware assertions. Tests
	// configure this callback before invoking lookup; an absent callback causes
	// the fixture to panic.
	getByID func(ctx context.Context, id int64) (*identity.User, error)
}

// TestAdminAuthJWTValidatesTokenVersion signs matching and stale versions for
// the same active administrator. It checks HTTP 200 for matching versions and
// TOKEN_REVOKED for stale Bearer and WebSocket credentials.
func TestAdminAuthJWTValidatesTokenVersion(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{
		Secret:                "test-secret",
		AccessTokenTTLMinutes: 60,
	}}}
	authService := identity.NewSessionService(identity.SessionOptions{Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes, RefreshTokenExpireDays: cfg.Auth.JWT.RefreshTokenTTLDays}, nil, nil, nil, nil)

	admin := &identity.User{
		ID:           1,
		Email:        "admin@example.com",
		Role:         identity.RoleAdmin,
		Status:       billing.StatusActive,
		TokenVersion: 2,
		Concurrency:  1,
	}

	userRepo := &stubUserRepo{
		getByID: func(ctx context.Context, id int64) (*identity.User, error) {
			if id != admin.ID {
				return nil, identity.ErrUserNotFound
			}
			clone := *admin
			return &clone, nil
		},
	}
	// The supplied background runner starts independent callbacks and does not
	// join them. These assertions inspect the synchronous administrator
	// middleware result produced by the fixture.
	userService := identity.NewUserService(userRepo, nil, nil, nil, func(_ string, fn func()) bool { go fn(); return true })

	router := gin.New()
	router.Use(identityhttp.AdminAuth(authService, userService, nil, nil))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	t.Run("token_version_mismatch_rejected", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &identity.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion - 1,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "TOKEN_REVOKED")
	})

	t.Run("token_version_match_allows", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &identity.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("websocket_token_version_mismatch_rejected", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &identity.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion - 1,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "tokenrouter-admin, jwt."+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "TOKEN_REVOKED")
	})

	t.Run("websocket_token_version_match_allows", func(t *testing.T) {
		token, err := authService.GenerateToken(context.Background(), &identity.User{
			ID:           admin.ID,
			Email:        admin.Email,
			Role:         admin.Role,
			TokenVersion: admin.TokenVersion,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "tokenrouter-admin, jwt."+token)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})
}

// Create panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) Create(ctx context.Context, user *identity.User) error {
	panic("unexpected Create call")
}

// CreateWithNormalizedEmailGuard panics when authentication reaches this
// unsupported repository operation in the fixture. The tests configure
// current-user ID lookup and absent avatar data, so this call exposes an
// unexpected dependency.
func (s *stubUserRepo) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	panic("unexpected CreateWithNormalizedEmailGuard call")
}

// GetByID delegates current-user lookup to the configured fixture callback. A
// missing callback panics, so each middleware test supplies the account lookup
// behavior before making an authenticated request.
func (s *stubUserRepo) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	if s.getByID == nil {
		panic("GetByID not stubbed")
	}
	return s.getByID(ctx, id)
}

// GetByEmail panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	panic("unexpected GetByEmail call")
}

// GetFirstAdmin panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) GetFirstAdmin(ctx context.Context) (*identity.User, error) {
	panic("unexpected GetFirstAdmin call")
}

// Update panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) Update(ctx context.Context, user *identity.User, fields identity.UserUpdateFields) error {
	panic("unexpected Update call")
}

// UpdateWithNormalizedEmailGuard panics when authentication reaches this
// unsupported repository operation in the fixture. The tests configure
// current-user ID lookup and absent avatar data, so this call exposes an
// unexpected dependency.
func (s *stubUserRepo) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string, fields identity.UserUpdateFields) error {
	panic("unexpected UpdateWithNormalizedEmailGuard call")
}

// Delete panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

// GetUserAvatar supplies an absent avatar with no error to middleware
// fixtures. UserService can hydrate its current-user result while tests focus
// on credential versions and authentication responses.
func (s *stubUserRepo) GetUserAvatar(ctx context.Context, userID int64) (*identity.UserAvatar, error) {
	return nil, nil
}

// UpsertUserAvatar panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) UpsertUserAvatar(ctx context.Context, userID int64, input identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

// DeleteUserAvatar panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) DeleteUserAvatar(ctx context.Context, userID int64) error {
	panic("unexpected DeleteUserAvatar call")
}

// List panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) List(ctx context.Context, params pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

// ListWithFilters panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

// GetLatestUsedAtByUserIDs panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserIDs call")
}

// GetLatestUsedAtByUserID panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserID call")
}

// UpdateUserLastActiveAt panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	panic("unexpected UpdateUserLastActiveAt call")
}

// UpdateBalance panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected UpdateBalance call")
}

// AddBalance panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) AddBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected AddBalance call")
}

// DeductBalance panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected DeductBalance call")
}

// AdjustBalance panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// UpdateConcurrency panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	panic("unexpected UpdateConcurrency call")
}

// BatchSetConcurrency panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

// BatchAddConcurrency panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}

// BatchUpdateLimits panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
}

// ExistsByEmail panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	panic("unexpected ExistsByEmail call")
}

// ExistsByNormalizedEmail panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	panic("unexpected ExistsByNormalizedEmail call")
}

// LockRegistrationEmail panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	panic("unexpected LockRegistrationEmail call")
}

// RemoveGroupFromAllowedGroups panics when authentication reaches this
// unsupported repository operation in the fixture. The tests configure
// current-user ID lookup and absent avatar data, so this call exposes an
// unexpected dependency.
func (s *stubUserRepo) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

// RemoveGroupFromUserAllowedGroups panics when authentication reaches this
// unsupported repository operation in the fixture. The tests configure
// current-user ID lookup and absent avatar data, so this call exposes an
// unexpected dependency.
func (s *stubUserRepo) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

// AddGroupToAllowedGroups panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

// ListUserAuthIdentities panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) ListUserAuthIdentities(ctx context.Context, userID int64) ([]identity.UserAuthIdentityRecord, error) {
	panic("unexpected ListUserAuthIdentities call")
}

// UnbindUserAuthProvider panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

// UpdateTotpSecret panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	panic("unexpected UpdateTotpSecret call")
}

// EnableTotp panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) EnableTotp(ctx context.Context, userID int64) error {
	panic("unexpected EnableTotp call")
}

// DisableTotp panics when authentication reaches this unsupported repository
// operation in the fixture. The tests configure current-user ID lookup and
// absent avatar data, so this call exposes an unexpected dependency.
func (s *stubUserRepo) DisableTotp(ctx context.Context, userID int64) error {
	panic("unexpected DisableTotp call")
}

// GetByIDIncludeDeleted panics when authentication reaches this unsupported
// repository operation in the fixture. The tests configure current-user ID
// lookup and absent avatar data, so this call exposes an unexpected
// dependency.
func (s *stubUserRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}

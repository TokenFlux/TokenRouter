package apikey_test

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

// errAuthCacheMiss is the external fixture read failure used to make service
// authentication fall through optional shared caching.
var errAuthCacheMiss = errors.New("auth cache miss")

// compositeGroupRepoStub returns shallow configured group views for binding and
// permission tests.
type compositeGroupRepoStub struct {
	routing.GroupRepository                          // unused routing methods require setup
	groups                  map[int64]*routing.Group // configured group identity map
}

// authRepoStub delegates configured authentication and invalidation reads while
// rejecting unsupported repository operations.
type authRepoStub struct {
	// getByKeyForAuth, listKeysByUserID and listKeysByGroupID are set by each
	// scenario before service use. Missing callbacks panic; configured callbacks
	// determine result ownership and returned error identity.
	getByKeyForAuth   func(ctx context.Context, key string) (*apikey.APIKey, error) // optional direct credential lookup callback
	listKeysByUserID  func(ctx context.Context, userID int64) ([]string, error)     // optional creator enumeration callback
	listKeysByGroupID func(ctx context.Context, groupID int64) ([]string, error)    // optional binding enumeration callback
}

// authCacheStub supplies a shared read callback and records cache write or
// deletion identities.
type authCacheStub struct {
	// getAuthCache is installed before reads and controls entry ownership or
	// failure. setAuthKeys and deleteAuthKeys capture writes without
	// synchronization; scenarios sequence or join those calls before inspecting
	// them.
	getAuthCache   func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) // optional shared-entry read callback
	setAuthKeys    []string                                                                    // captured published cache identities
	deleteAuthKeys []string                                                                    // captured deleted cache identities
}

// apiKeyTestDependencies retains caller-owned collaborators used to assemble a
// narrow Key service fixture.
type apiKeyTestDependencies struct {
	apiKeyRepo            apikey.APIKeyRepository            // caller-owned credential repository fixture
	userRepo              identity.UserRepository            // caller-owned identity repository fixture
	groupRepo             routing.GroupRepository            // caller-owned group repository fixture
	userSubRepo           billing.UserSubscriptionRepository // caller-owned subscription repository fixture
	userGroupRateRepo     billing.UserGroupRateRepository    // caller-owned group-rate repository fixture
	teamRepo              team.TeamRepository                // optional team context fixture
	cache                 apikey.APIKeyCache                 // optional shared cache fixture
	cfg                   *config.Config                     // optional deployment configuration input
	concurrencyService    *scheduler.ConcurrencyService      // optional live-count service fixture
	rateLimitCacheInvalid apikey.RateLimitCacheInvalidator   // optional spending-reset notification fixture
}

// GetByID returns a shallow copy of the configured group matching id, or
// group-not-found when absent. Nested maps, lists and pointers remain shared;
// tests treat this returned group as input before snapshot conversion.
func (s *compositeGroupRepoStub) GetByID(_ context.Context, id int64) (*routing.Group, error) {
	group, ok := s.groups[id]
	if !ok {
		return nil, routing.ErrGroupNotFound
	}
	copyGroup := *group
	return &copyGroup, nil
}

// Create panics when the external authentication fixture receives an
// unsupported Create call.
func (s *authRepoStub) Create(ctx context.Context, key *apikey.APIKey) error {
	panic("unexpected Create call")
}

// GetByID panics when the external authentication fixture receives an
// unsupported GetByID call.
func (s *authRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	panic("unexpected GetByID call")
}

// GetKeyAndOwnerID panics when the external authentication fixture receives an
// unsupported GetKeyAndOwnerID call.
func (s *authRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

// GetByKey panics when the external authentication fixture receives an
// unsupported GetByKey call.
func (s *authRepoStub) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKey call")
}

// GetByKeyForAuth delegates the requested credential to its configured lookup
// callback. An absent callback panics to expose an unsupported fixture path;
// returned records and errors belong to the callback implementation.
func (s *authRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	if s.getByKeyForAuth == nil {
		panic("unexpected GetByKeyForAuth call")
	}
	return s.getByKeyForAuth(ctx, key)
}

// RotateCredential panics when the external authentication fixture receives an
// unsupported RotateCredential call.
func (s *authRepoStub) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update panics when the external authentication fixture receives an
// unsupported Update call.
func (s *authRepoStub) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	panic("unexpected Update call")
}

// Delete panics when the external authentication fixture receives an
// unsupported Delete call.
func (s *authRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

// DeleteWithAudit panics when the external authentication fixture receives an
// unsupported DeleteWithAudit call.
func (s *authRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	panic("unexpected DeleteWithAudit call")
}

// ListByUserID panics when the external authentication fixture receives an
// unsupported ListByUserID call.
func (s *authRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

// VerifyOwnership panics when the external authentication fixture receives an
// unsupported VerifyOwnership call.
func (s *authRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

// CountByUserID panics when the external authentication fixture receives an
// unsupported CountByUserID call.
func (s *authRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

// ExistsByKey panics when the external authentication fixture receives an
// unsupported ExistsByKey call.
func (s *authRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

// ListByGroupID panics when the external authentication fixture receives an
// unsupported ListByGroupID call.
func (s *authRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

// SearchAPIKeys panics when the external authentication fixture receives an
// unsupported SearchAPIKeys call.
func (s *authRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

// ClearGroupIDByGroupID panics when the external authentication fixture
// receives an unsupported ClearGroupIDByGroupID call.
func (s *authRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

// UpdateGroupIDByUserAndGroup panics when the external authentication fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *authRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

// CountByGroupID panics when the external authentication fixture receives an
// unsupported CountByGroupID call.
func (s *authRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

// ListKeysByUserID delegates creator enumeration to its configured callback.
// Missing callback panics, and the callback owns result storage and failure;
// service tests use it to observe invalidation input independently of
// persistence.
func (s *authRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	if s.listKeysByUserID == nil {
		panic("unexpected ListKeysByUserID call")
	}
	return s.listKeysByUserID(ctx, userID)
}

// ListKeysByGroupID delegates group enumeration to its configured callback.
// Missing callback panics; the fixture performs no independent binding or
// ownership filtering and leaves result data with the supplied implementation.
func (s *authRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	if s.listKeysByGroupID == nil {
		panic("unexpected ListKeysByGroupID call")
	}
	return s.listKeysByGroupID(ctx, groupID)
}

// UpdateLastUsed panics when the external authentication fixture receives an
// unsupported UpdateLastUsed call.
func (s *authRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	panic("unexpected UpdateLastUsed call")
}

// GetCreateAttemptCount returns zero so fixture creation is not rejected by its
// optional counter.
func (s *authCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount accepts fixture failure-count writes without
// retaining a counter value.
func (s *authCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// DeleteCreateAttemptCount accepts unrelated creation-counter cleanup in the
// authentication cache fixture.
func (s *authCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// IncrementDailyUsage acknowledges daily counter calls while retaining no usage
// observation.
func (s *authCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

// SetDailyUsageExpiry acknowledges fixture counter expiry calls while retaining
// no deadline.
func (s *authCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

// GetAuthCache delegates shared lookup to its configured callback, or reports
// the fixture miss when absent. Result pointers remain with that callback, and
// the fixture enforces no independent TTL or schema validation.
func (s *authCacheStub) GetAuthCache(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
	if s.getAuthCache == nil {
		return nil, errAuthCacheMiss
	}
	return s.getAuthCache(ctx, key)
}

// SetAuthCache records the supplied cache identity but retains no entry or
// expiry. Tests read setAuthKeys to observe publication intent; callers
// sequence fixture writes because its capture slice is unsynchronized.
func (s *authCacheStub) SetAuthCache(ctx context.Context, key string, entry *apikey.APIKeyAuthCacheEntry, ttl time.Duration) error {
	s.setAuthKeys = append(s.setAuthKeys, key)
	return nil
}

// DeleteAuthCache records the supplied identity as a deletion attempt without
// real storage. Tests sequence capture access or join worker use before
// inspection because the retained slice has no mutex.
func (s *authCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

// PublishAuthCacheInvalidation accepts fixture notifications while sending no
// real subscription message.
func (s *authCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

// SubscribeAuthCacheInvalidation returns immediately with nil and invokes no
// handler. The service wrapper can interpret this as a closed subscription;
// tests needing a held subscriber use a dedicated lifecycle fixture.
func (s *authCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

// validTeamAPIKeyForLifecycleTest allocates active team, member, actor and
// owner views for lifecycle assertions. The membership joined before the fresh
// Key creation timestamp; callers mutate their returned fixture to introduce
// one rejection condition.
func validTeamAPIKeyForLifecycleTest() *apikey.APIKey {
	teamID := int64(11)
	createdAt := time.Now()
	return &apikey.APIKey{
		ID:        31,
		UserID:    2,
		TeamID:    &teamID,
		Status:    apikey.StatusAPIKeyActive,
		CreatedAt: createdAt,
		Team:      &team.Team{ID: teamID, Status: team.TeamStatusActive},
		TeamMembership: &team.TeamMembership{
			TeamID:   teamID,
			UserID:   2,
			Role:     team.TeamRoleMember,
			JoinedAt: createdAt.Add(-time.Minute),
		},
		ActorUser: &identity.User{ID: 2, Status: billing.StatusActive},
		User:      &identity.User{ID: 1, Status: billing.StatusActive},
	}
}

// newAPIKeyTestService constructs an unstarted service and binds supplied
// optional team, concurrency and invalidation dependencies. Nil configuration
// becomes an empty deployment config; callers start resources when required and
// coordinate fixture and service lifetime after use.
func newAPIKeyTestService(d apiKeyTestDependencies) *apikey.APIKeyService {
	cfg := d.cfg
	if cfg == nil {
		cfg = &config.Config{}
	}
	s := testkit.NewService(d.apiKeyRepo, d.userRepo, d.groupRepo, d.userSubRepo, d.userGroupRateRepo, d.cache, cfg)
	if d.teamRepo != nil {
		s.SetTeamRepository(d.teamRepo)
	}
	if d.concurrencyService != nil {
		s.SetConcurrencyService(d.concurrencyService)
	}
	if d.rateLimitCacheInvalid != nil {
		s.SetRateLimitCacheInvalidator(d.rateLimitCacheInvalid)
	}
	return s
}

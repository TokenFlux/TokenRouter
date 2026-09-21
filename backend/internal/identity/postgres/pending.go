// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	errors "errors"
	fmt "fmt"
	fnv "hash/fnv"
	sort "sort"
	strings "strings"
	sync "sync"
	time "time"

	dialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	identityadoptiondecision "github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	pendingauthsession "github.com/TokenFlux/TokenRouter/ent/pendingauthsession"
	dbpredicate "github.com/TokenFlux/TokenRouter/ent/predicate"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

type AuthPendingIdentityService struct {
	readTime  func() time.Time
	entClient *dbent.Client
}

var authPendingIdentityScopedKeyLocks = newAuthPendingIdentityScopedKeyLockRegistry()

type authPendingIdentityScopedKeyLockRegistry struct {
	mu    sync.Mutex
	locks map[string]*authPendingIdentityScopedKeyLockEntry
}

type authPendingIdentityScopedKeyLockEntry struct {
	mu   sync.Mutex
	refs int
}

func newAuthPendingIdentityScopedKeyLockRegistry() *authPendingIdentityScopedKeyLockRegistry {
	return &authPendingIdentityScopedKeyLockRegistry{
		locks: make(map[string]*authPendingIdentityScopedKeyLockEntry),
	}
}

func (r *authPendingIdentityScopedKeyLockRegistry) lock(keys ...string) func() {
	normalized := normalizeAuthPendingIdentityLockKeys(keys...)
	if len(normalized) == 0 {
		return func() {}
	}

	entries := make([]*authPendingIdentityScopedKeyLockEntry, 0, len(normalized))
	r.mu.Lock()
	for _, key := range normalized {
		entry := r.locks[key]
		if entry == nil {
			entry = &authPendingIdentityScopedKeyLockEntry{}
			r.locks[key] = entry
		}
		entry.refs++
		entries = append(entries, entry)
	}
	r.mu.Unlock()

	for _, entry := range entries {
		entry.mu.Lock()
	}

	return func() {
		for i := len(entries) - 1; i >= 0; i-- {
			entries[i].mu.Unlock()
		}

		r.mu.Lock()
		defer r.mu.Unlock()
		for idx, key := range normalized {
			entry := entries[idx]
			entry.refs--
			if entry.refs == 0 {
				delete(r.locks, key)
			}
		}
	}
}

func normalizeAuthPendingIdentityLockKeys(keys ...string) []string {
	if len(keys) == 0 {
		return nil
	}

	deduped := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		deduped[trimmed] = struct{}{}
	}
	if len(deduped) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(deduped))
	for key := range deduped {
		normalized = append(normalized, key)
	}
	sort.Strings(normalized)
	return normalized
}

func authPendingIdentityAdvisoryLockHash(key string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return int64(hasher.Sum64())
}

func lockAuthPendingIdentityKeys(ctx context.Context, client *dbent.Client, keys ...string) (func(), error) {
	release := authPendingIdentityScopedKeyLocks.lock(keys...)
	normalized := normalizeAuthPendingIdentityLockKeys(keys...)
	if len(normalized) == 0 || client == nil || client.Driver().Dialect() != dialect.Postgres {
		return release, nil
	}

	for _, key := range normalized {
		var rows entsql.Rows
		if err := client.Driver().Query(ctx, "SELECT pg_advisory_xact_lock($1)", []any{authPendingIdentityAdvisoryLockHash(key)}, &rows); err != nil {
			release()
			return nil, err
		}
		_ = rows.Close()
	}

	return release, nil
}

func pendingIdentityAdoptionLockKeys(pendingAuthSessionID int64, identityID *int64) []string {
	keys := []string{fmt.Sprintf("pending-auth-adoption:pending:%d", pendingAuthSessionID)}
	if identityID != nil && *identityID > 0 {
		keys = append(keys, fmt.Sprintf("pending-auth-adoption:identity:%d", *identityID))
	}
	return keys
}

func NewAuthPendingIdentityService(entClient *dbent.Client, clocks ...func() time.Time) *AuthPendingIdentityService {
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	return &AuthPendingIdentityService{entClient: entClient, readTime: now}
}

func (s *AuthPendingIdentityService) CreatePendingSession(ctx context.Context, input CreatePendingAuthSessionInput) (*dbent.PendingAuthSession, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	sessionToken := strings.TrimSpace(input.SessionToken)
	if sessionToken == "" {
		var err error
		sessionToken, err = randomOpaqueToken(24)
		if err != nil {
			return nil, err
		}
	}

	expiresAt := input.ExpiresAt.UTC()
	if expiresAt.IsZero() {
		expiresAt = s.readTime().UTC().Add(defaultPendingAuthTTL)
	}

	create := s.entClient.PendingAuthSession.Create().
		SetSessionToken(sessionToken).
		SetIntent(strings.TrimSpace(input.Intent)).
		SetProviderType(strings.TrimSpace(input.Identity.ProviderType)).
		SetProviderKey(strings.TrimSpace(input.Identity.ProviderKey)).
		SetProviderSubject(strings.TrimSpace(input.Identity.ProviderSubject)).
		SetRedirectTo(strings.TrimSpace(input.RedirectTo)).
		SetResolvedEmail(strings.TrimSpace(input.ResolvedEmail)).
		SetRegistrationPasswordHash(strings.TrimSpace(input.RegistrationPasswordHash)).
		SetBrowserSessionKey(strings.TrimSpace(input.BrowserSessionKey)).
		SetUpstreamIdentityClaims(copyPendingMap(input.UpstreamIdentityClaims)).
		SetLocalFlowState(copyPendingMap(input.LocalFlowState)).
		SetExpiresAt(expiresAt)
	if input.TargetUserID != nil {
		create = create.SetTargetUserID(*input.TargetUserID)
	}
	return create.Save(ctx)
}

func (s *AuthPendingIdentityService) IssueCompletionCode(ctx context.Context, input IssuePendingAuthCompletionCodeInput) (*IssuePendingAuthCompletionCodeResult, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	session, err := s.entClient.PendingAuthSession.Get(ctx, input.PendingAuthSessionID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrPendingAuthSessionNotFound
		}
		return nil, err
	}

	code, err := randomOpaqueToken(24)
	if err != nil {
		return nil, err
	}
	ttl := input.TTL
	if ttl <= 0 {
		ttl = defaultPendingAuthCompletionTTL
	}
	expiresAt := s.readTime().UTC().Add(ttl)

	update := s.entClient.PendingAuthSession.UpdateOneID(session.ID).
		SetCompletionCodeHash(hashPendingAuthCode(code)).
		SetCompletionCodeExpiresAt(expiresAt)
	if strings.TrimSpace(input.BrowserSessionKey) != "" {
		update = update.SetBrowserSessionKey(strings.TrimSpace(input.BrowserSessionKey))
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, err
	}

	return &IssuePendingAuthCompletionCodeResult{
		Code:      code,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *AuthPendingIdentityService) ConsumeCompletionCode(ctx context.Context, rawCode, browserSessionKey string) (*dbent.PendingAuthSession, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	codeHash := hashPendingAuthCode(strings.TrimSpace(rawCode))
	session, err := s.entClient.PendingAuthSession.Query().
		Where(pendingauthsession.CompletionCodeHashEQ(codeHash)).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrPendingAuthCodeInvalid
		}
		return nil, err
	}

	return s.consumeSession(ctx, session, browserSessionKey, ErrPendingAuthCodeExpired, ErrPendingAuthCodeConsumed)
}

func (s *AuthPendingIdentityService) ConsumeBrowserSession(ctx context.Context, sessionToken, browserSessionKey string) (*dbent.PendingAuthSession, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	session, err := s.getBrowserSession(ctx, sessionToken)
	if err != nil {
		return nil, err
	}

	return s.consumeSession(ctx, session, browserSessionKey, ErrPendingAuthSessionExpired, ErrPendingAuthSessionConsumed)
}

func (s *AuthPendingIdentityService) GetBrowserSession(ctx context.Context, sessionToken, browserSessionKey string) (*dbent.PendingAuthSession, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	session, err := s.getBrowserSession(ctx, sessionToken)
	if err != nil {
		return nil, err
	}
	if err := validatePendingSessionState(session, browserSessionKey, ErrPendingAuthSessionExpired, ErrPendingAuthSessionConsumed, s.readTime); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *AuthPendingIdentityService) getBrowserSession(ctx context.Context, sessionToken string) (*dbent.PendingAuthSession, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return nil, ErrPendingAuthSessionNotFound
	}

	session, err := s.entClient.PendingAuthSession.Query().
		Where(pendingauthsession.SessionTokenEQ(sessionToken)).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrPendingAuthSessionNotFound
		}
		return nil, err
	}
	return session, nil
}

func (s *AuthPendingIdentityService) consumeSession(
	ctx context.Context,
	session *dbent.PendingAuthSession,
	browserSessionKey string,
	expiredErr error,
	consumedErr error,
) (*dbent.PendingAuthSession, error) {
	if err := validatePendingSessionState(session, browserSessionKey, expiredErr, consumedErr, s.readTime); err != nil {
		return nil, err
	}

	sanitizedLocalFlowState := sanitizePendingAuthLocalFlowState(session.LocalFlowState)
	now := s.readTime().UTC()
	update := s.entClient.PendingAuthSession.UpdateOneID(session.ID).
		Where(
			pendingauthsession.ConsumedAtIsNil(),
			pendingauthsession.ExpiresAtGTE(now),
			pendingauthsession.Or(
				pendingauthsession.CompletionCodeExpiresAtIsNil(),
				pendingauthsession.CompletionCodeExpiresAtGTE(now),
			),
		).
		SetConsumedAt(now).
		SetLocalFlowState(sanitizedLocalFlowState).
		SetCompletionCodeHash("").
		ClearCompletionCodeExpiresAt()
	if expectedBrowserSessionKey := strings.TrimSpace(session.BrowserSessionKey); expectedBrowserSessionKey != "" {
		update = update.Where(pendingauthsession.BrowserSessionKeyEQ(expectedBrowserSessionKey))
	}
	updated, err := update.Save(ctx)
	if err == nil {
		return updated, nil
	}
	if !dbent.IsNotFound(err) {
		return nil, err
	}

	current, currentErr := s.entClient.PendingAuthSession.Get(ctx, session.ID)
	if currentErr != nil {
		if dbent.IsNotFound(currentErr) {
			return nil, ErrPendingAuthSessionNotFound
		}
		return nil, currentErr
	}
	if err := validatePendingSessionState(current, browserSessionKey, expiredErr, consumedErr, s.readTime); err != nil {
		return nil, err
	}
	return nil, consumedErr
}

func (s *AuthPendingIdentityService) UpsertAdoptionDecision(ctx context.Context, input PendingIdentityAdoptionDecisionInput) (*dbent.IdentityAdoptionDecision, error) {
	if s == nil || s.entClient == nil {
		return nil, fmt.Errorf("pending auth ent client is not configured")
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return nil, err
	}

	client := s.entClient
	txCtx := ctx
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
		txCtx = dbent.NewTxContext(ctx, tx)
	} else if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		client = existingTx.Client()
	}

	releaseLocks, err := lockAuthPendingIdentityKeys(txCtx, client, pendingIdentityAdoptionLockKeys(input.PendingAuthSessionID, input.IdentityID)...)
	if err != nil {
		return nil, err
	}
	defer releaseLocks()

	if input.IdentityID != nil && *input.IdentityID > 0 {
		if _, err := client.IdentityAdoptionDecision.Update().
			Where(
				identityadoptiondecision.IdentityIDEQ(*input.IdentityID),
				dbpredicate.IdentityAdoptionDecision(func(s *entsql.Selector) {
					col := s.C(identityadoptiondecision.FieldPendingAuthSessionID)
					s.Where(entsql.Or(
						entsql.IsNull(col),
						entsql.NEQ(col, input.PendingAuthSessionID),
					))
				}),
			).
			ClearIdentityID().
			Save(txCtx); err != nil {
			return nil, err
		}
	}

	create := client.IdentityAdoptionDecision.Create().
		SetPendingAuthSessionID(input.PendingAuthSessionID).
		SetAdoptDisplayName(input.AdoptDisplayName).
		SetAdoptAvatar(input.AdoptAvatar).
		SetDecidedAt(s.readTime().UTC())
	if input.IdentityID != nil && *input.IdentityID > 0 {
		create = create.SetIdentityID(*input.IdentityID)
	}

	decisionID, err := create.
		OnConflictColumns(identityadoptiondecision.FieldPendingAuthSessionID).
		UpdateNewValues().
		ID(txCtx)
	if err != nil {
		return nil, err
	}

	decision, err := client.IdentityAdoptionDecision.Get(txCtx, decisionID)
	if err != nil {
		return nil, err
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}

	return decision, nil
}

var ErrPendingAuthSessionNotFound = identity.ErrPendingAuthSessionNotFound
var ErrPendingAuthSessionExpired = identity.ErrPendingAuthSessionExpired
var ErrPendingAuthSessionConsumed = identity.ErrPendingAuthSessionConsumed
var ErrPendingAuthCodeInvalid = identity.ErrPendingAuthCodeInvalid
var ErrPendingAuthCodeExpired = identity.ErrPendingAuthCodeExpired
var ErrPendingAuthCodeConsumed = identity.ErrPendingAuthCodeConsumed
var ErrPendingAuthBrowserMismatch = identity.ErrPendingAuthBrowserMismatch

const defaultPendingAuthTTL = identity.DefaultPendingAuthTTL
const defaultPendingAuthCompletionTTL = identity.DefaultPendingAuthCompletionTTL

type PendingAuthIdentityKey = identity.PendingAuthIdentityKey
type CreatePendingAuthSessionInput = identity.CreatePendingAuthSessionInput
type IssuePendingAuthCompletionCodeInput = identity.IssuePendingAuthCompletionCodeInput
type IssuePendingAuthCompletionCodeResult = identity.IssuePendingAuthCompletionCodeResult
type PendingIdentityAdoptionDecisionInput = identity.PendingIdentityAdoptionDecisionInput

func sanitizePendingAuthLocalFlowState(localFlowState map[string]any) map[string]any {
	return identity.SanitizePendingAuthLocalFlowState(localFlowState)
}
func validatePendingSessionState(session *dbent.PendingAuthSession, browserSessionKey string, expiredErr error, consumedErr error, readTime func() time.Time) error {
	return identity.ValidatePendingSessionStateWithClock(PendingAuthSessionFromEntity(session), browserSessionKey, expiredErr, consumedErr, readTime)
}
func copyPendingMap(in map[string]any) map[string]any { return identity.CopyPendingMap(in) }
func randomOpaqueToken(byteLen int) (string, error)   { return identity.RandomOpaqueToken(byteLen) }
func hashPendingAuthCode(code string) string          { return identity.HashPendingAuthCode(code) }

// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	entsql "entgo.io/ent/dialect/sql"
	errors "errors"
	fmt "fmt"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	authidentity "github.com/TokenFlux/TokenRouter/ent/authidentity"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	slog "log/slog"
	strings "strings"
	time "time"
)

// ApplyProviderDefaultSettingsOnFirstBind applies provider-specific bootstrap
// settings the first time a user binds a third-party identity. The grant is
// idempotent per user/provider pair.
func (s *AuthState) ApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	if s == nil || s.entClient == nil || s.Settings == nil || userID <= 0 {
		return nil
	}

	if dbent.TxFromContext(ctx) != nil {
		return s.AuthApplyProviderDefaultSettingsOnFirstBind(ctx, userID, providerType)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin first bind defaults transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := s.AuthApplyProviderDefaultSettingsOnFirstBind(txCtx, userID, providerType); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *AuthState) AuthApplyProviderDefaultSettingsOnFirstBind(
	ctx context.Context,
	userID int64,
	providerType string,
) error {
	providerDefaults, enabled, err := s.Settings.ResolveAuthSourceGrantSettings(ctx, providerType, true)
	if err != nil {
		return fmt.Errorf("load auth source defaults: %w", err)
	}
	if !enabled {
		return nil
	}

	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}

	var result entsql.Result
	if err := client.Driver().Exec(
		ctx,
		`INSERT INTO user_provider_default_grants (user_id, provider_type, grant_reason)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, provider_type, grant_reason) DO NOTHING`,
		[]any{userID, strings.TrimSpace(providerType), "first_bind"},
		&result,
	); err != nil {
		return fmt.Errorf("record first bind provider grant: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read first bind provider grant result: %w", err)
	}
	if affected == 0 {
		return nil
	}

	if providerDefaults.Balance != 0 {
		if err := billingpostgres.NewBalanceStore(client).AddBalance(ctx, userID, providerDefaults.Balance); err != nil {
			return fmt.Errorf("apply first bind balance default: %w", err)
		}
	}
	if providerDefaults.Concurrency != 0 {
		if err := NewConcurrencyStore(client).UpdateConcurrency(ctx, userID, providerDefaults.Concurrency); err != nil {
			return fmt.Errorf("apply first bind concurrency default: %w", err)
		}
	}
	if s.DefaultSubscriptions != nil {
		for _, item := range providerDefaults.Subscriptions {
			if _, _, err := s.DefaultSubscriptions.AssignOrExtendSubscription(ctx, &identitycore.AssignSubscriptionInput{
				UserID: userID,
				PlanID: item.PlanID,
				Notes:  "auto assigned by first bind defaults",
			}); err != nil {
				return fmt.Errorf("apply first bind subscription default: %w", err)
			}
		}
	}

	return nil
}

func (s *AuthState) AuthCreateRegisteredUser(ctx context.Context, user *identitycore.User, artifacts *identitycore.AuthRegistrationArtifacts) error {
	if user == nil {
		return nil
	}
	// 注册时固化当前默认值，之后修改系统默认值不会追溯已有用户。
	user.APIKeyLimit = identitycore.DefaultUserAPIKeyLimit
	if s.Settings != nil {
		user.APIKeyLimit = s.Settings.GetDefaultUserAPIKeyLimit(ctx)
	}
	if artifacts == nil {
		artifacts = &identitycore.AuthRegistrationArtifacts{}
	}
	normalizedEmail := ""
	if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
		normalizedEmail = identitycore.NormalizeRegistrationEmailAddress(user.Email)
	}

	run := func(runCtx context.Context) error {
		domainLimit := ""
		if artifacts.EnforceEmailDomainQuota {
			var err error
			domainLimit, err = s.Rules.AuthRegistrationEmailDomainLimit(runCtx, user.Email)
			if err != nil {
				return err
			}
		}

		if normalizedEmail != "" {
			if dbent.TxFromContext(runCtx) != nil {
				if err := s.Users.LockRegistrationEmail(runCtx, normalizedEmail); err != nil {
					return err
				}
			}

			existsEmail, err := s.Rules.AuthRegistrationEmailExists(runCtx, user.Email)
			if err != nil {
				return err
			}
			if existsEmail {
				return identitycore.ErrEmailExists
			}
		}

		if domainLimit != "" {
			quotaRepo := s.DomainRegistration
			ok := quotaRepo != nil
			if !ok {
				// 生产装配缺失原子仓储能力时必须拒绝，避免并发绕过额度。
				if s.entClient != nil {
					return identitycore.ErrServiceUnavailable
				}
				if err := s.Users.Create(runCtx, user); err != nil {
					return err
				}
			} else {
				if err := quotaRepo.CreateWithRegistrationEmailGuards(runCtx, user, normalizedEmail, domainLimit); err != nil {
					return err
				}
			}
		} else if err := s.Users.Create(runCtx, user); err != nil {
			return err
		}

		if artifacts.InvitationRedeemCode != nil {
			if err := s.Redeem.Use(runCtx, artifacts.InvitationRedeemCode.ID, user.ID); err != nil {
				return identitycore.ErrInvitationCodeInvalid
			}
		}

		return nil
	}

	if s.entClient == nil {
		return run(ctx)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := run(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *AuthState) AuthEnsureEmailAuthIdentity(ctx context.Context, user *identitycore.User, source string) (*dbent.AuthIdentity, bool) {
	if s == nil || s.entClient == nil || user == nil || user.ID <= 0 {
		return nil, false
	}

	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" || identitycore.IsReservedEmail(email) {
		return nil, false
	}
	if strings.TrimSpace(source) == "" {
		source = "auth_service_dual_write"
	}

	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}

	buildQuery := func() *dbent.AuthIdentityQuery {
		return client.AuthIdentity.Query().Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(email),
		)
	}

	existed, err := buildQuery().Exist(ctx)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to inspect email auth identity: user_id=%d email=%s err=%v", user.ID, email, err)
		return nil, false
	}

	if !existed {
		if err = client.AuthIdentity.Create().
			SetUserID(user.ID).
			SetProviderType("email").
			SetProviderKey("email").
			SetProviderSubject(email).
			SetVerifiedAt(time.Now().UTC()).
			SetMetadata(map[string]any{
				"source": strings.TrimSpace(source),
			}).
			OnConflictColumns(
				authidentity.FieldProviderType,
				authidentity.FieldProviderKey,
				authidentity.FieldProviderSubject,
			).
			DoNothing().
			Exec(ctx); err != nil {
			if isSQLNoRowsError(err) {
				return nil, false
			}
		}
		if err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to ensure email auth identity: user_id=%d email=%s err=%v", user.ID, email, err)
			return nil, false
		}
	}

	identity, err := buildQuery().Only(ctx)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to reload email auth identity: user_id=%d email=%s err=%v", user.ID, email, err)
		return nil, false
	}
	if identity.UserID != user.ID {
		s.Observer.Printf("service.auth", "[Auth] Email auth identity ownership mismatch: user_id=%d email=%s owner_id=%d", user.ID, email, identity.UserID)
		return nil, false
	}

	return identity, !existed
}

func (s *AuthState) AuthEnsureEmailOAuthIdentity(ctx context.Context, userID int64, input identitycore.EmailOAuthIdentityInput) error {
	metadata := map[string]any{
		"email":          strings.TrimSpace(strings.ToLower(input.Email)),
		"email_verified": input.EmailVerified,
	}
	for key, value := range input.UpstreamMetadata {
		metadata[key] = value
	}
	if strings.TrimSpace(input.Username) != "" {
		metadata["username"] = strings.TrimSpace(input.Username)
	}
	if strings.TrimSpace(input.DisplayName) != "" {
		metadata["display_name"] = strings.TrimSpace(input.DisplayName)
	}
	if strings.TrimSpace(input.AvatarURL) != "" {
		metadata["avatar_url"] = strings.TrimSpace(input.AvatarURL)
	}

	providerType := identitycore.AuthNormalizeOAuthSignupSource(input.ProviderType)
	providerKey := strings.TrimSpace(input.ProviderKey)
	providerSubject := strings.TrimSpace(input.ProviderSubject)
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyEQ(providerKey),
			authidentity.ProviderSubjectEQ(providerSubject),
		).
		Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	if identity != nil {
		if identity.UserID != userID {
			return infraerrors.Conflict("AUTH_IDENTITY_OWNERSHIP_CONFLICT", "auth identity already belongs to another user")
		}
		_, err = s.entClient.AuthIdentity.UpdateOneID(identity.ID).
			SetMetadata(metadata).
			Save(ctx)
		return err
	}
	_, err = s.entClient.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType(providerType).
		SetProviderKey(providerKey).
		SetProviderSubject(providerSubject).
		SetMetadata(metadata).
		Save(ctx)
	return err
}

func (s *AuthState) AuthFindEmailOAuthIdentityOwner(ctx context.Context, providerType, providerKey, providerSubject string) (*identitycore.User, error) {
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyEQ(providerKey),
			authidentity.ProviderSubjectEQ(providerSubject),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	user, err := s.Users.GetByID(ctx, identity.UserID)
	if err != nil {
		if errors.Is(err, identitycore.ErrUserNotFound) {
			return nil, nil
		}
		return nil, identitycore.ErrServiceUnavailable
	}
	return user, nil
}

func (s *AuthState) AuthHasProviderGrantRecord(
	ctx context.Context,
	userID int64,
	providerType string,
	grantReason string,
) (bool, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return false, nil
	}

	rows, err := s.entClient.QueryContext(
		ctx,
		`SELECT 1 FROM user_provider_default_grants WHERE user_id = $1 AND provider_type = $2 AND grant_reason = $3 LIMIT 1`,
		userID,
		strings.TrimSpace(providerType),
		strings.TrimSpace(grantReason),
	)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

func (s *AuthState) AuthLoadOAuthRegistrationInvitation(ctx context.Context, invitationCode string) (*identitycore.RedeemCode, error) {
	if client := s.AuthOauthEmailFlowClient(ctx); client != nil {
		return registrationInvitationsForContext(ctx, client).Load(ctx, invitationCode)
	}
	return s.Redeem.GetByCode(ctx, invitationCode)
}

func (s *AuthState) AuthOauthEmailFlowClient(ctx context.Context) *dbent.Client {
	if s == nil || s.entClient == nil {
		return nil
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return s.entClient
}

func (s *AuthState) AuthRestoreOAuthRegistrationInvitation(ctx context.Context, invitationCode string, userID int64) error {
	if s == nil || s.Settings == nil || !s.Settings.IsInvitationCodeEnabled(ctx) {
		return nil
	}
	if s.Redeem == nil && s.AuthOauthEmailFlowClient(ctx) == nil {
		return identitycore.ErrServiceUnavailable
	}

	invitationCode = strings.TrimSpace(invitationCode)
	if invitationCode == "" || userID <= 0 {
		return nil
	}

	redeemCode, err := s.AuthLoadOAuthRegistrationInvitation(ctx, invitationCode)
	if err != nil {
		if errors.Is(err, identitycore.ErrRedeemCodeNotFound) {
			return nil
		}
		return fmt.Errorf("load invitation code: %w", err)
	}
	if redeemCode.Type != identitycore.RedeemTypeInvitation || redeemCode.Status != identitycore.StatusUsed || redeemCode.UsedBy == nil || *redeemCode.UsedBy != userID {
		return nil
	}

	redeemCode.Status = identitycore.StatusUnused
	redeemCode.UsedCount = 0
	redeemCode.UsedBy = nil
	redeemCode.UsedAt = nil
	if err := s.AuthUpdateOAuthRegistrationInvitation(ctx, redeemCode); err != nil {
		return fmt.Errorf("restore invitation code: %w", err)
	}
	return nil
}

func (s *AuthState) AuthRunFailOpenDBStep(ctx context.Context, savepointName string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return fn(ctx)
	}

	client := tx.Client()
	// 非关键注册初始化运行在主事务内时必须用 savepoint 隔离；
	// PostgreSQL 任意一条语句失败都会把整个事务标记为 aborted。
	if _, err := client.ExecContext(ctx, "SAVEPOINT "+savepointName); err != nil {
		return fmt.Errorf("create savepoint %s: %w", savepointName, err)
	}
	if err := fn(ctx); err != nil {
		if _, rollbackErr := client.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepointName); rollbackErr != nil {
			return fmt.Errorf("rollback savepoint %s after %v: %w", savepointName, err, rollbackErr)
		}
		if _, releaseErr := client.ExecContext(ctx, "RELEASE SAVEPOINT "+savepointName); releaseErr != nil {
			return fmt.Errorf("release savepoint %s after %v: %w", savepointName, err, releaseErr)
		}
		return err
	}
	if _, err := client.ExecContext(ctx, "RELEASE SAVEPOINT "+savepointName); err != nil {
		return fmt.Errorf("release savepoint %s: %w", savepointName, err)
	}
	return nil
}

func (s *AuthState) AuthShouldApplyEmailFirstBindDefaults(
	ctx context.Context,
	userID int64,
	identity *dbent.AuthIdentity,
	created bool,
) bool {
	source := identitycore.AuthEmailAuthIdentitySource(identity.Metadata)
	if source == "auth_service_login_backfill" {
		return false
	}
	if created {
		return true
	}
	if s == nil || s.entClient == nil || userID <= 0 || identity == nil || identity.UserID != userID {
		return false
	}
	if source != "auth_service_dual_write" {
		return false
	}

	hasGrant, err := s.AuthHasProviderGrantRecord(ctx, userID, "email", "first_bind")
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to inspect email first bind grant state: user_id=%d err=%v", userID, err)
		return false
	}
	return !hasGrant
}

// identitycore.AuthSnapshotPlatformQuotaDefaults 把 plan.PlatformQuotas（全部允许 platform × 3 window）以
// BulkInsertInitial 形式写入 user_platform_quotas 表。失败 fail-open（仅 warn log）。
func (s *AuthState) AuthSnapshotPlatformQuotaDefaults(ctx context.Context, userID int64, plan *identitycore.AuthSignupGrantPlan) error {
	if s.Quotas == nil || plan == nil || len(plan.PlatformQuotas) == 0 {
		return nil
	}
	// 平台配额快照是 best-effort，必须脱离调用方事务执行。
	// 否则某平台违反 user_platform_quotas 的 CHECK 约束时，PostgreSQL 会把整笔注册事务标记为 aborted。
	ctx = dbent.WithoutTx(ctx)
	records := make([]identitycore.UserPlatformQuotaRecord, 0, len(plan.PlatformQuotas))
	for platform, q := range plan.PlatformQuotas {
		rec := identitycore.UserPlatformQuotaRecord{
			UserID:   userID,
			Platform: platform,
		}
		if q != nil {
			rec.DailyLimitUSD = q.DailyLimitUSD
			rec.WeeklyLimitUSD = q.WeeklyLimitUSD
			rec.MonthlyLimitUSD = q.MonthlyLimitUSD
		}
		records = append(records, rec)
	}

	if err := s.AuthRunFailOpenDBStep(ctx, "auth_platform_quota_snapshot", func(stepCtx context.Context) error {
		return s.Quotas.BulkInsertInitial(stepCtx, records)
	}); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Warning: snapshot platform quota failed user=%d: %v (fail-open)", userID, err)
		return nil // fail-open：返回 nil，让调用方继续
	}
	return nil
}

func (s *AuthState) AuthTouchUserLogin(ctx context.Context, userID int64) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return
	}
	now := time.Now().UTC()
	if err := s.entClient.User.UpdateOneID(userID).
		SetLastLoginAt(now).
		SetLastActiveAt(now).
		Exec(ctx); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to touch login timestamps: user_id=%d err=%v", userID, err)
	}
}

func (s *AuthState) AuthUpdateBoundEmailIdentityTx(
	ctx context.Context,
	currentUser *identitycore.User,
	email string,
	registrationNormalizedEmail string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return s.AuthUpdateBoundEmailIdentityWithClient(ctx, tx.Client(), currentUser, email, registrationNormalizedEmail, hashedPassword, applyFirstBindDefaults)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return identitycore.ErrServiceUnavailable
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := s.AuthUpdateBoundEmailIdentityWithClient(txCtx, tx.Client(), currentUser, email, registrationNormalizedEmail, hashedPassword, applyFirstBindDefaults); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return identitycore.ErrServiceUnavailable
	}
	return nil
}

func (s *AuthState) AuthUpdateBoundEmailIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	currentUser *identitycore.User,
	email string,
	registrationNormalizedEmail string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	if client == nil || currentUser == nil || currentUser.ID <= 0 {
		return identitycore.ErrServiceUnavailable
	}

	oldEmail := currentUser.Email
	if guard := s.EmailAliasGuard; guard != nil {
		// guard 在同一事务内锁定别名身份并复查，关闭前置查重与写入之间的窗口。
		if err := guard.UpdateEmailWithAliasGuard(ctx, currentUser.ID, email, hashedPassword); err != nil {
			return err
		}
	} else {
		// 兼容尚未实现别名 guard 的测试桩，保留 fork 原有的归一化唯一性保护。
		updatedUser := *currentUser
		updatedUser.Email = email
		updatedUser.PasswordHash = hashedPassword
		if registrationNormalizedEmail != "" {
			if err := s.Users.LockRegistrationEmail(ctx, registrationNormalizedEmail); err != nil {
				return identitycore.ErrServiceUnavailable
			}
			exists, err := s.Rules.AuthHasNormalizedEmailBindingConflict(ctx, currentUser, registrationNormalizedEmail)
			if err != nil {
				return identitycore.ErrServiceUnavailable
			}
			if exists {
				return identitycore.ErrEmailExists
			}
		}
		if _, err := client.User.UpdateOneID(currentUser.ID).
			SetEmail(updatedUser.Email).
			SetPasswordHash(updatedUser.PasswordHash).
			Save(ctx); err != nil {
			if dbent.IsConstraintError(err) || errors.Is(err, identitycore.ErrEmailExists) {
				return identitycore.ErrEmailExists
			}
			return identitycore.ErrServiceUnavailable
		}
	}

	if err := AuthReplaceBoundEmailAuthIdentityWithClient(ctx, client, currentUser.ID, oldEmail, email, "auth_service_email_bind"); err != nil {
		if errors.Is(err, identitycore.ErrEmailExists) {
			return identitycore.ErrEmailExists
		}
		return identitycore.ErrServiceUnavailable
	}

	if applyFirstBindDefaults {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, currentUser.ID, "email"); err != nil {
			return fmt.Errorf("apply email first bind defaults: %w", err)
		}
	}

	refreshedUser, err := client.User.Get(ctx, currentUser.ID)
	if err != nil {
		return identitycore.ErrServiceUnavailable
	}
	currentUser.Email = refreshedUser.Email
	currentUser.PasswordHash = refreshedUser.PasswordHash
	currentUser.Balance = refreshedUser.Balance
	currentUser.Concurrency = refreshedUser.Concurrency
	currentUser.UpdatedAt = refreshedUser.UpdatedAt
	return nil
}

func (s *AuthState) AuthUpdateOAuthRegistrationInvitation(ctx context.Context, code *identitycore.RedeemCode) error {
	if code == nil {
		return nil
	}
	if client := s.AuthOauthEmailFlowClient(ctx); client != nil {
		return registrationInvitationsForContext(ctx, client).RestoreSnapshot(ctx, code)
	}
	return s.Redeem.Update(ctx, code)
}

func (s *AuthState) AuthUpdateOAuthSignupSource(ctx context.Context, userID int64, signupSource string) {
	client := s.AuthOauthEmailFlowClient(ctx)
	if client == nil || userID <= 0 || strings.TrimSpace(signupSource) == "" {
		return
	}
	if err := s.AuthRunFailOpenDBStep(ctx, "auth_oauth_signup_source", func(stepCtx context.Context) error {
		stepClient := s.AuthOauthEmailFlowClient(stepCtx)
		if stepClient == nil {
			return identitycore.ErrServiceUnavailable
		}
		return stepClient.User.UpdateOneID(userID).SetSignupSource(signupSource).Exec(stepCtx)
	}); err != nil {
		slog.Warn("[Auth] update oauth signup source failed (fail-open)", "user_id", userID, "source", signupSource, "error", err)
	}
}

func (s *AuthState) AuthUpdateUserSignupSource(ctx context.Context, userID int64, signupSource string) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return
	}
	if strings.TrimSpace(signupSource) == "" {
		return
	}
	if err := s.entClient.User.UpdateOneID(userID).
		SetSignupSource(signupSource).
		Exec(ctx); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to update signup source: user_id=%d source=%s err=%v", userID, signupSource, err)
	}
}

func (s *AuthState) AuthUseOAuthRegistrationInvitation(ctx context.Context, invitationID, userID int64) error {
	if client := s.AuthOauthEmailFlowClient(ctx); client != nil {
		return registrationInvitationsForContext(ctx, client).Consume(ctx, invitationID, userID)
	}
	return s.Redeem.Use(ctx, invitationID, userID)
}

// AuthState 保留原 Ent 事务划分；业务规则通过同一身份用例调用。
type AuthState struct {
	*identitycore.AuthDependencies
	entClient *dbent.Client
	Rules     *identitycore.AuthService
}

func NewAuthState(client *dbent.Client, deps *identitycore.AuthDependencies) *AuthState {
	return &AuthState{AuthDependencies: deps, entClient: client}
}
func (s *AuthState) HasDatabase() bool { return s != nil && s.entClient != nil }

func AuthReplaceBoundEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	oldEmail string,
	newEmail string,
	source string,
) error {
	newSubject := identitycore.AuthNormalizeBoundEmailAuthIdentitySubject(newEmail)
	if err := AuthEnsureBoundEmailAuthIdentityWithClient(ctx, client, userID, newSubject, source); err != nil {
		return err
	}

	oldSubject := identitycore.AuthNormalizeBoundEmailAuthIdentitySubject(oldEmail)
	if oldSubject == "" || oldSubject == newSubject {
		return nil
	}

	_, err := client.AuthIdentity.Delete().
		Where(
			authidentity.UserIDEQ(userID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(oldSubject),
		).
		Exec(ctx)
	return err
}

func AuthEnsureBoundEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	subject string,
	source string,
) error {
	if client == nil || userID <= 0 || subject == "" {
		return nil
	}

	if strings.TrimSpace(source) == "" {
		source = "auth_service_email_bind"
	}

	if err := client.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(subject).
		SetVerifiedAt(time.Now().UTC()).
		SetMetadata(map[string]any{"source": strings.TrimSpace(source)}).
		OnConflictColumns(
			authidentity.FieldProviderType,
			authidentity.FieldProviderKey,
			authidentity.FieldProviderSubject,
		).
		DoNothing().
		Exec(ctx); err != nil {
		if !isSQLNoRowsError(err) {
			return err
		}
	}

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(subject),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil
		}
		return err
	}
	if identity.UserID != userID {
		return identitycore.ErrEmailExists
	}
	return nil
}

// registrationInvitationsForContext 沿用原 Ent context 选定同一个资金参与连接。
func registrationInvitationsForContext(ctx context.Context, client *dbent.Client) *billingpostgres.RegistrationInvitations {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return billingpostgres.RegistrationInvitationsInTx(tx)
	}
	return billingpostgres.NewRegistrationInvitations(client)
}

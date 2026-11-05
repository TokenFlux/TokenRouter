package testkit

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
)

// auth.go assembles authentication test fixtures with PostgreSQL participation
// and optional service adapters.
// Authentication fixtures need the same account persistence participation as
// production use cases. Auth attaches an Ent-backed AuthState and discovers
// optional email/domain capabilities from the supplied user repository, while
// small adapters convert concrete mail and promotion pointers into the
// interfaces authentication expects.
//
// The dependency pointer is retained and updated during assembly. A missing
// observer gets the legacy logger, discovered user capabilities replace
// matching fields, and AuthState.Rules is assigned after the core service
// exists. Other dependency fields keep the caller configuration. Nil concrete
// optional services become nil interfaces, so authentication can observe
// absence instead of receiving a typed-nil implementation.

// affiliate adapts referral creation results to the authentication affiliate
// interface.
type affiliate struct {
	source *promotion.AffiliateService // retained referral service pointer
}

// AuthOptions copies the selected startup authentication settings into a new
// options value. Nil configuration returns nil; the copied fields cover
// account defaults, token settings, server mode, and Turnstile requirement.
func AuthOptions(cfg *config.Config) *identity.AuthOptions {
	if cfg == nil {
		return nil
	}
	out := &identity.AuthOptions{}
	out.Default.UserBalance = cfg.UserDefaults.Balance
	out.Default.UserConcurrency = cfg.UserDefaults.Concurrency
	out.JWT = identity.SessionOptions{Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes, RefreshTokenExpireDays: cfg.Auth.JWT.RefreshTokenTTLDays}
	out.Server.Mode = cfg.Server.Mode
	out.Turnstile.Required = cfg.Auth.RequireCaptcha
	return out
}

// Auth constructs authentication with Ent-backed state and the caller
// dependency pointer. Supply nonnil dependencies, including the services
// required by the exercised flow.
//
// Assembly fills a missing observer and discovers domain registration,
// normalized-email conflict, alias guard, and alias lookup capabilities from
// Users. Matching capabilities replace dependency fields; unmatched fields
// retain their supplied configuration. AuthState and AuthService share those
// dependencies, and the final Rules assignment lets state writes call the
// constructed core validation service.
func Auth(client *dbent.Client, deps *identity.AuthDependencies) *identity.AuthService {
	if deps.Observer.Log == nil {
		deps.Observer.Log = logging.LegacyPrintf
	}
	if source, ok := deps.Users.(identity.RegistrationEmailDomainRepository); ok {
		deps.DomainRegistration = source
	}
	if source, ok := deps.Users.(identity.AuthNormalizedEmailBindingConflictChecker); ok {
		deps.NormalizedEmailConflict = source
	}
	if source, ok := deps.Users.(identity.AuthEmailIdentityAliasGuardRepository); ok {
		deps.EmailAliasGuard = source
	}
	if source, ok := deps.Users.(identity.EmailAliasLookupRepository); ok {
		deps.AliasLookup = source
	}
	if source, ok := deps.Users.(identity.EmailAliasOwnerLookupRepository); ok {
		deps.AliasOwner = source
	}
	state := identitypostgres.NewAuthState(client, deps)
	core := identity.NewAuthService(deps, &identitypostgres.AuthRepository{State: state})
	state.Rules = core
	return core
}

// Email returns the concrete challenge service as an authentication email
// interface. A nil pointer becomes a nil interface, so callers can use
// ordinary interface absence checks for optional mail support.
func Email(value *identity.EmailChallenges) identity.AuthEmail {
	if value == nil {
		return nil
	}
	return value
}

// EmailQueue returns the concrete mail queue as an authentication queue
// interface. A nil pointer becomes a nil interface, so optional queue absence
// is visible to authentication callers.
func EmailQueue(value *notification.EmailQueueService) identity.AuthEmailQueue {
	if value == nil {
		return nil
	}
	return value
}

// Promo returns the concrete signup-promotion service as the authentication
// promotion interface. A nil pointer becomes a nil interface, retaining the
// optional capability check used by the core service.
func Promo(value *promotion.PromoService) identity.AuthPromo {
	if value == nil {
		return nil
	}
	return value
}

// Affiliate wraps the concrete referral service for authentication call
// signatures. Nil input returns a nil interface; the wrapper retains the
// supplied service and delegates creation and inviter-binding operations.
func Affiliate(value *promotion.AffiliateService) identity.AuthAffiliate {
	if value == nil {
		return nil
	}
	return affiliate{value}
}

// EnsureUserAffiliate requests the account referral record and returns the
// service error. The authentication interface discards the created record
// value; the retained source service is required for this operation.
func (a affiliate) EnsureUserAffiliate(ctx context.Context, id int64) error {
	_, err := a.source.EnsureUserAffiliate(ctx, id)
	return err
}

// BindInviterByCode delegates inviter selection for the supplied account and
// code to promotion.
func (a affiliate) BindInviterByCode(ctx context.Context, id int64, code string) error {
	return a.source.BindInviterByCode(ctx, id, code)
}

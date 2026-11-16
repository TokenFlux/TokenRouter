package apikey

import (
	"context"
	"fmt"
)

// authenticate.go validates credential identity and builds the access record
// used by gateway admission. Expired and quota-exhausted states can still
// produce an identity view for retrieval endpoints, while disabled
// credentials, inactive users, invalid team relationships and rejected IP
// addresses stop authentication. A loaded Key can accompany an error so
// diagnostics can retain ownership information.
//
// The resulting access record separates credential creator, hydrated payer and
// acting member. Consumers check the authentication error before admitting
// that record, then perform final group, protocol and funding checks at the
// gateway. The same loaded-key validator serves later connection turns after
// storage reauthorization; it keeps failure-stage information for HTTP
// adapters to choose the corresponding response shape.

// Failure stages let HTTP adapters select status, protocol error text and
// observation callbacks after the same ordered identity checks in authentication.
const (
	AuthenticationLookup       AuthenticationFailureKind = "lookup"        // credential lookup failed
	AuthenticationDisabled     AuthenticationFailureKind = "disabled"      // credential state rejected
	AuthenticationTeam         AuthenticationFailureKind = "team"          // team lifecycle rejected
	AuthenticationMemberLimit  AuthenticationFailureKind = "member_limit"  // member allowance exhausted
	AuthenticationIP           AuthenticationFailureKind = "ip"            // client address rejected
	AuthenticationUserMissing  AuthenticationFailureKind = "user_missing"  // payer record missing
	AuthenticationUserInactive AuthenticationFailureKind = "user_inactive" // payer status rejected
)

// AccessSnapshot records credential ownership, payer and actor after loading
// identity data. Its private Key pointer is the view used for this
// authentication attempt; a returned record accompanied by an error remains
// diagnostic input until callers establish successful admission.
// @project-doc docs/architecture/gateway_request_lifecycle.md#apikey_authentication
type AccessSnapshot struct {
	// KeyID identifies the matched credential. OwnerUserID records its creator,
	// PayerUserID names the hydrated funding user, and ActorUserID names the
	// member acting through it; personal credentials usually share one user ID.
	KeyID       int64  // matched credential record identifier
	OwnerUserID int64  // persisted credential owner identifier
	PayerUserID int64  // hydrated funding user identifier
	ActorUserID int64  // acting credential user identifier
	TeamID      *int64 // copied team scope identifier
	// key remains a shared pointer to this attempt's loaded view. Context
	// copying retains it; runtime group helpers publish request-local replacements.
	key                *APIKey
	fastModePolicy     string // selected request acceleration policy
	forceClaude1hCache bool   // selected Claude 1h cache override
}

// AuthenticationInput supplies the client address and member-allowance
// enforcement choice.
type AuthenticationInput struct {
	ClientIP string // caller-resolved client address text
	// CheckMemberLimits enables team spending checks for consuming entry points.
	// Callers disable it for retrieval operations that still require valid identity.
	CheckMemberLimits bool
}

// AuthenticationFailureKind identifies which ordered authentication check
// rejected a loaded credential.
type AuthenticationFailureKind string

// AuthenticationFailure carries rejection stage, optional cause and denied
// client address.
type AuthenticationFailure struct {
	Kind     AuthenticationFailureKind // rejected authentication check stage
	Cause    error                     // optional underlying failure cause
	ClientIP string                    // denied address for diagnostics
}

// ipAccessDenied is the rejected address rendered in a public denial message.
type ipAccessDenied string

// KeyView returns the Key pointer retained by this authentication attempt. Nil
// access returns nil; callers treat an access record with an authentication
// error as diagnostic data until admission has succeeded.
func (a *AccessSnapshot) KeyView() *APIKey {
	if a == nil {
		return nil
	}
	return a.key
}

// Error returns the underlying cause message when one is available. A failure
// without a cause returns its stage string, allowing HTTP adapters to map those
// stage-only rejections independently.
func (e *AuthenticationFailure) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Kind)
}

// Unwrap returns the optional cause for standard error identity checks.
func (e *AuthenticationFailure) Unwrap() error { return e.Cause }

// Authenticate loads a credential view and checks its identity, returning
// access data on success. Some rejections return loaded access data with an
// error for diagnostics; callers check the error before using that identity.
// Group, protocol, quota and funding admission follow at the gateway so
// retrieval endpoints can reuse the identity checks.
func (s *APIKeyService) Authenticate(ctx context.Context, credential string, input AuthenticationInput) (*AccessSnapshot, error) {
	key, err := s.GetByKey(ctx, credential)
	if err != nil {
		return nil, &AuthenticationFailure{Kind: AuthenticationLookup, Cause: err}
	}
	return s.authenticateKey(key, input)
}

// authenticateKey validates loaded credential, team, optional member allowance,
// IP and payer data in that order. Nil input produces a lookup failure; later
// failures retain the access record for diagnostics. Expired and
// quota-exhausted states pass the initial state check for later spending
// admission. Callers compile IP rules before validation and decide whether
// member allowances apply to the endpoint.
func (s *APIKeyService) authenticateKey(key *APIKey, input AuthenticationInput) (*AccessSnapshot, error) {
	if key == nil {
		return nil, &AuthenticationFailure{Kind: AuthenticationLookup, Cause: ErrAPIKeyNotFound}
	}
	access := &AccessSnapshot{
		KeyID:              key.ID,
		OwnerUserID:        key.UserID,
		ActorUserID:        key.UserID,
		TeamID:             clonePointer(key.TeamID),
		key:                key,
		fastModePolicy:     key.FastModePolicy,
		forceClaude1hCache: key.ForceClaude1hCache,
	}
	if key.User != nil {
		access.PayerUserID = key.User.ID
	}
	if key.ActorUser != nil {
		access.ActorUserID = key.ActorUser.ID
	}
	fail := func(kind AuthenticationFailureKind, err error) (*AccessSnapshot, error) {
		return access, &AuthenticationFailure{Kind: kind, Cause: err}
	}
	if !key.IsActive() && key.Status != StatusAPIKeyExpired && key.Status != StatusAPIKeyQuotaExhausted {
		return fail(AuthenticationDisabled, nil)
	}
	if err := s.ValidateTeamKeyLifecycle(key); err != nil {
		return fail(AuthenticationTeam, err)
	}
	if input.CheckMemberLimits {
		if err := s.CheckTeamMemberLimits(key); err != nil {
			return fail(AuthenticationMemberLimit, err)
		}
	}
	if len(key.IPWhitelist) > 0 || len(key.IPBlacklist) > 0 {
		if allowed, _ := CheckIPRestrictionWithCompiledRules(input.ClientIP, key.CompiledIPWhitelist, key.CompiledIPBlacklist); !allowed {
			ip := input.ClientIP
			// An unavailable resolved address still produces a useful public denial
			// message; record an explicit placeholder when no client address was supplied.
			if ip == "" {
				ip = "unknown"
			}
			return access, &AuthenticationFailure{Kind: AuthenticationIP, ClientIP: ip, Cause: ipAccessDenied(ip)}
		}
	}
	if key.User == nil {
		return fail(AuthenticationUserMissing, nil)
	}
	if !key.User.IsActive() {
		return fail(AuthenticationUserInactive, nil)
	}
	return access, nil
}

// Error formats the rejected address into the fixed public denial message.
func (e ipAccessDenied) Error() string { return fmt.Sprintf("Access denied. Your IP is %s", string(e)) }

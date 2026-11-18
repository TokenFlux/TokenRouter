package qoder

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// authorization.go completes site-specific device authorization and prepares
// credentials with optional metadata warnings.
// Device polling separates an unfinished browser login from a completed token
// exchange. CompleteAuthorization returns a pending result until the access
// token is available, then resolves the identity and machine fields that the
// provider authorization owner will persist.
//
// Current desktop tokens use OpenAPI userinfo on both sites. Global login can
// retain token-response identity when that auxiliary lookup is unavailable,
// with auxiliary lookup failures represented by sanitized warnings. Organization
// tags can supply a missing name for the organization selected in userinfo. Older CN status
// completion remains available only to the legacy credential workflow. The stored flow keeps
// the creation-time profile, machine, and proxy together across polls.

// OAuthClientPort supplies polling and identity lookups for the authorization
// completion flow.
type OAuthClientPort interface {
	// PollDeviceToken queries the nonce with its PKCE verifier and reports token
	// readiness. A pending login returns ready=false with no error; ready=true
	// supplies a usable token response for identity completion.
	PollDeviceToken(ctx context.Context, nonce, verifier string) (*DeviceTokenResponse, bool, error)
	// GetUserInfo loads OpenAPI user metadata using the supplied access token. The
	// authorization caller decides whether a lookup failure aborts CN completion or
	// becomes a warning for global login.
	GetUserInfo(ctx context.Context, token string) (*UserInfo, error)
	// GetOrganizationTags reads metadata for the verified organization ID. The
	// user and organization identifiers address different OpenAPI resources.
	GetOrganizationTags(ctx context.Context, token, organizationID string) (*OrganizationTags, error)
}

// CN20OAuthCompleter turns validated CN OpenAPI tokens into an inference
// identity and expiry.
type CN20OAuthCompleter interface {
	// CompleteQoderCN20Identity completes CN status using the token, user metadata,
	// and stored machine. Callers supply a validated CN token and retain the
	// returned OpenAPI expiry alongside the COSY identity for persistence.
	CompleteQoderCN20Identity(
		ctx context.Context,
		token *DeviceTokenResponse,
		user *UserInfo,
		machine *MachineIdentity,
	) (*AuthIdentity, time.Time, error)
}
type (
	// OAuthClientFactory constructs an authorization client using the stored
	// profile and proxy.
	OAuthClientFactory func(profile Profile, proxyURL string) (OAuthClientPort, error)
	// AuthorizationTokenInfo carries credentials and sanitized warnings returned to
	// the provider authorization owner.
	AuthorizationTokenInfo struct {
		SecurityOauthToken string         `json:"security_oauth_token"`        // inference access token
		RefreshToken       string         `json:"refresh_token,omitempty"`     // credential refresh token
		MachineID          string         `json:"machine_id"`                  // saved machine identifier
		MachineToken       string         `json:"machine_token,omitempty"`     // saved machine token
		MachineType        string         `json:"machine_type,omitempty"`      // saved machine type
		UID                string         `json:"uid,omitempty"`               // COSY user identifier
		AID                string         `json:"aid,omitempty"`               // COSY account identifier
		OrganizationID     string         `json:"organization_id,omitempty"`   // selected organization identifier
		OrganizationName   string         `json:"organization_name,omitempty"` // selected organization name
		Name               string         `json:"name,omitempty"`              // authenticated display name
		UserType           string         `json:"user_type,omitempty"`         // Qoder account classification
		Site               string         `json:"site"`                        // credential site name
		RefreshMode        string         `json:"refresh_mode"`                // credential refresh workflow
		ExpiresAt          string         `json:"expires_at,omitempty"`        // UTC RFC3339 expiry
		Extra              map[string]any `json:"extra,omitempty"`             // sanitized auxiliary warnings
	}
)

// AuthorizationFlow retains the machine, site, and browser proof used throughout
// one device login.
type AuthorizationFlow struct {
	// Nonce, CodeVerifier, and AuthURL retain one browser proof until completion.
	// ProxyURL retains the creation-time transport choice used by the client
	// factory during each poll.
	Nonce, CodeVerifier, AuthURL, ProxyURL string // device polling nonce, private PKCE verifier, browser authorization URL, captured transport proxy
	// Machine is shared across polling and CN completion. The authorization owner
	// retains this pointer for the lifetime of the stored flow.
	Machine *MachineIdentity // flow machine identity
	Site    Site             // credential site name
	Profile Profile          // flow endpoint profile
}

// CompleteAuthorization exchanges browser proof for desktop device credentials.
// A pending poll leaves the flow reusable. Current desktop logins on both sites
// use OpenAPI userinfo directly; the older CN status exchange remains available
// only for credentials explicitly marked with the legacy refresh mode.
//
// CN requires a successful identity lookup. Global responses carrying a user ID
// can retain that identity when the auxiliary lookup fails, with a sanitized
// warning. The persisted device-token mode keeps subsequent refreshes on the
// matching OpenAPI endpoint instead of treating them as Center COSY tokens.
func CompleteAuthorization(ctx context.Context, session *AuthorizationFlow, factory OAuthClientFactory) (*AuthorizationTokenInfo, bool, error) {
	client, err := factory(session.Profile, session.ProxyURL)
	if err != nil {
		return nil, false, err
	}
	token, ready, err := client.PollDeviceToken(ctx, session.Nonce, session.CodeVerifier)
	if err != nil {
		return nil, false, err
	}
	if !ready {
		return nil, true, nil
	}
	if token == nil || token.AccessTokenValue() == "" {
		return nil, false, errors.New("qoder: device authorization returned no access token")
	}
	expires := token.ExpiryTime(time.Now())
	user, userErr := client.GetUserInfo(ctx, token.AccessTokenValue())
	if userErr != nil {
		// Global polling can supply the account ID even when its auxiliary lookup
		// fails. CN lacks that fallback contract and requires the identity lookup.
		if session.Site == SiteCN || token.UserID == "" {
			return nil, false, userErr
		}
		user = &UserInfo{ID: token.UserID}
	}
	identity := BuildIdentityFromDeviceToken(user, token)
	if reader, ok := client.(interface {
		GetAccountType(context.Context, string) (string, error)
	}); ok {
		// Subscription classification is auxiliary metadata. Keep the authenticated
		// identity usable when plan lookup fails, while retaining verified values.
		if accountType, err := reader.GetAccountType(ctx, token.AccessTokenValue()); err == nil && accountType != "" {
			identity.UserType = accountType
		}
	}
	if identity.UID == "" {
		return nil, false, errors.New("qoder: device authorization returned no user identity")
	}
	orgErr := enrichOrganizationName(ctx, identity, client.GetOrganizationTags)
	return BuildAuthorizationTokenInfoForSite(identity, session.Machine, session.Site, RefreshModeDeviceToken, expires, userErr, orgErr), false, nil
}

// enrichOrganizationName fills a missing name for an already identified organization.
// The token's userinfo or the saved credentials establish its organization ID.
// Personal accounts and fully described organizations require no metadata request.
//
// Tags provide descriptive data only. A conflicting ID leaves the bound identity
// intact and produces an auxiliary warning for authorization callers. Session
// builders can still use the verified identity when this optional lookup fails.
func enrichOrganizationName(ctx context.Context, identity *AuthIdentity, lookup OrganizationTagsGetter) error {
	if identity == nil || identity.OrganizationID == "" || identity.OrganizationName != "" || identity.SecurityOauthToken == "" {
		return nil
	}
	tags, err := lookup(ctx, identity.SecurityOauthToken, identity.OrganizationID)
	if err != nil || tags == nil {
		return err
	}
	if id := strings.TrimSpace(tags.OrganizationID); id != "" && id != identity.OrganizationID {
		return errors.New("qoder: organization metadata belongs to another organization")
	}
	identity.OrganizationName = strings.TrimSpace(tags.OrganizationName)
	return nil
}

// BuildAuthorizationTokenInfoForSite assembles the credential response for the
// provider authorization owner. Nil identity selects personal_standard and nil
// machine creates the site's machine shape. Credential strings are trimmed, a
// nonzero expiry becomes UTC RFC3339 text, and auxiliary errors become fixed
// warning maps rather than exposing raw upstream error text.
func BuildAuthorizationTokenInfoForSite(
	identity *AuthIdentity,
	machine *MachineIdentity,
	site Site,
	refreshMode string,
	expiresAt time.Time,
	userErr error,
	orgErr error,
) *AuthorizationTokenInfo {
	if identity == nil {
		identity = &AuthIdentity{UserType: "personal_standard"}
	}
	if machine == nil {
		machine = NewMachineForSite(site)
	}
	extra := map[string]any{}
	if userErr != nil {
		extra["userinfo_warning"] = sanitizedQoderOAuthWarning("userinfo_unavailable", "Qoder user info could not be loaded")
	}
	if orgErr != nil {
		extra["organization_warning"] = sanitizedQoderOAuthWarning("organization_unavailable", "Qoder organization info could not be loaded")
	}
	if len(extra) == 0 {
		extra = nil
	}
	tokenInfo := &AuthorizationTokenInfo{
		SecurityOauthToken: strings.TrimSpace(identity.SecurityOauthToken),
		RefreshToken:       strings.TrimSpace(identity.RefreshToken),
		MachineID:          strings.TrimSpace(machine.MachineID),
		MachineToken:       strings.TrimSpace(machine.MachineToken),
		MachineType:        strings.TrimSpace(machine.MachineType),
		UID:                strings.TrimSpace(identity.UID),
		AID:                strings.TrimSpace(identity.AID),
		OrganizationID:     strings.TrimSpace(identity.OrganizationID),
		OrganizationName:   strings.TrimSpace(identity.OrganizationName),
		Name:               strings.TrimSpace(identity.Name),
		UserType:           FirstNonEmptyQoder(identity.UserType, "personal_standard"),
		Site:               string(site),
		RefreshMode:        refreshMode,
		Extra:              extra,
	}
	if !expiresAt.IsZero() {
		tokenInfo.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}
	return tokenInfo
}

// sanitizedQoderOAuthWarning returns the fixed code and message of an
// authorization warning.
func sanitizedQoderOAuthWarning(code, message string) map[string]string {
	return map[string]string{
		"code":    code,
		"message": message,
	}
}

// DefaultOAuthClientFactory obtains the pooled transport for a site's OAuth
// client. The proxy URL is trimmed and the transport uses a twenty-second
// timeout; callers reuse the returned client for flow polling and metadata
// lookup.
func DefaultOAuthClientFactory(profile Profile, proxyURL string) (OAuthClientPort, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL: strings.TrimSpace(proxyURL),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return NewOAuthClientForProfile(profile, client), nil
}

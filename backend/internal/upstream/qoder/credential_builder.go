package qoder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// credential_builder.go builds Qoder sessions from PAT or imported credentials
// and fills site-specific identity metadata.
// Provider credentials can contain a PAT or an already exchanged COSY token set.
// PAT construction exchanges the selected site's identity first, while imported
// tokens need a saved machine ID and a user or account identifier before an
// encrypted session can be built.
//
// Identity metadata is filled before the session becomes available to the
// provider cache. Organization lookup is best effort and uses the provider
// transport when supplied, so missing descriptive metadata does not reject
// otherwise usable credentials. CN machine reconstruction retains its machine
// ID and clears legacy token and type fields; global reconstruction fills
// missing machine fields for older saved credentials.

// CredentialInput holds private session input assembled from one saved provider
// record.
type CredentialInput struct {
	Name string // provider display name
	// Pat and SecurityOauthToken supply alternate session inputs. BuildSession
	// prefers nonblank Pat; imported SecurityOauthToken requires MachineId and
	// either Uid or Aid before session construction.
	Pat string // personal access token
	// Site and RefreshMode identify credential workflow. Their parsers apply
	// global and COSY legacy defaults. BuildSession validates mode for imported
	// tokens; RefreshExchange validates it before choosing PAT reconstruction.
	Site               string // saved provider site
	RefreshMode        string // saved refresh workflow
	SecurityOauthToken string // saved inference token
	MachineId          string // saved machine identifier
	MachineToken       string // saved machine token
	MachineType        string // saved machine type
	Uid                string // saved user identifier
	Aid                string // saved account identifier
	IdentityName       string // authenticated display name
	UserType           string // saved account classification
	RefreshToken       string // saved refresh token
	OrganizationId     string // saved organization identifier
	OrganizationName   string // saved organization name
}

type (
	// PATExchanger exchanges a global PAT with the supplied machine and request
	// context.
	PATExchanger func(context.Context, string, *MachineIdentity) (*AuthIdentity, error)
	// CNPATExchanger exchanges a CN PAT and returns its identity and OpenAPI
	// expiry.
	CNPATExchanger func(context.Context, string, *MachineIdentity) (*AuthIdentity, time.Time, error)
	// OrganizationTagsGetter looks up optional organization metadata using an
	// access token and organization ID.
	OrganizationTagsGetter func(context.Context, string, string) (*OrganizationTags, error)
)

// SessionBuilder combines optional exchange callbacks with the provider
// transport for session construction.
type SessionBuilder struct {
	// ExchangePAT replaces the global PAT exchange when supplied. A nil callback
	// selects Center and Doer. Overrides supply an identity usable by inference;
	// the builder can fill its metadata in place before encrypted publication.
	ExchangePAT PATExchanger // global PAT override
	// ExchangeCNPAT replaces CN PAT completion when supplied. A nil callback
	// selects OpenAPI and status using Doer. Overrides supply an inference identity
	// that the builder can complete in place before encrypted publication.
	ExchangeCNPAT CNPATExchanger // CN PAT override
	// GetOrgTags replaces optional organization lookup when supplied. A nil
	// callback delegates to GetOrganizationTags with the selected provider site.
	GetOrgTags OrganizationTagsGetter // organization lookup override
	// Doer supplies the provider proxy and transport to default exchanges and
	// metadata calls. Nil selects the platform client's HTTP defaults.
	Doer RequestDoer // provider request transport
}

// String returns a fixed redacted label for credential formatting and logging.
func (v *CredentialInput) String() string { return "qoder credentials [redacted]" }

// GetCredential returns the saved string for a recognized provider credential
// key. Unknown keys return an empty string; callers provide a nonnil
// CredentialInput and apply validation or defaults for each requested field.
func (v *CredentialInput) GetCredential(key string) string {
	switch key {
	case "pat":
		return v.Pat
	case "site":
		return v.Site
	case "refresh_mode":
		return v.RefreshMode
	case "security_oauth_token":
		return v.SecurityOauthToken
	case "machine_id":
		return v.MachineId
	case "machine_token":
		return v.MachineToken
	case "machine_type":
		return v.MachineType
	case "uid":
		return v.Uid
	case "aid":
		return v.Aid
	case "name":
		return v.IdentityName
	case "user_type":
		return v.UserType
	case "refresh_token":
		return v.RefreshToken
	case "organization_id":
		return v.OrganizationId
	case "organization_name":
		return v.OrganizationName
	}
	return ""
}

// credentialSite parses the credential site, selecting global for an empty site
// value. A nil credential input returns an error, allowing builders to reject
// absent provider data before reading individual fields.
func credentialSite(v *CredentialInput) (Site, error) {
	if v == nil {
		return SiteGlobal, fmt.Errorf("qoder: provider is nil")
	}
	return ParseSite(v.Site)
}

// credentialProfile resolves site defaults from a nonnil provider credential
// input. Site parsing errors propagate to callers, and the returned value can be
// copied and overridden independently for request construction.
func credentialProfile(v *CredentialInput) (Profile, error) {
	site, err := credentialSite(v)
	if err != nil {
		return Profile{}, err
	}
	return ProfileForSite(site)
}

// credentialRefreshMode parses the saved refresh workflow and applies the COSY
// default. A nil credential input returns an error before refresh construction,
// while unknown mode strings are rejected by ParseRefreshMode.
func credentialRefreshMode(v *CredentialInput) (string, error) {
	if v == nil {
		return "", fmt.Errorf("qoder: provider is nil")
	}
	return ParseRefreshMode(v.RefreshMode)
}

// BuildSession creates an encrypted session from a PAT or imported COSY
// credentials. PAT takes precedence and uses the selected site's exchange
// callback or default transport. Imported tokens require machine_id and uid or
// aid; qodercn20 imported credentials require the CN site.
//
// The builder fills descriptive metadata before constructing a session that
// retains identity and machine pointers. Global PAT and imported identities can
// query organization tags best effort. CN PAT exchange supplies an expiry, while
// global PAT and imported-token construction return a zero expiry for the cache
// owner to interpret.
func (p *SessionBuilder) BuildSession(ctx context.Context, provider *CredentialInput) (*SessionContext, time.Time, error) {
	site, err := credentialSite(provider)
	if err != nil {
		return nil, time.Time{}, err
	}
	pat := strings.TrimSpace(provider.GetCredential("pat"))
	if pat == "" {
		refreshMode, modeErr := credentialRefreshMode(provider)
		if modeErr != nil {
			return nil, time.Time{}, modeErr
		}
		if refreshMode == RefreshModeQoderCN20 && site != SiteCN {
			return nil, time.Time{}, errors.New("qoder qodercn20 credentials require cn site")
		}
	}
	if pat != "" {
		machine := MachineForCredentials(provider)
		var identity *AuthIdentity
		var expiresAt time.Time
		if site == SiteCN {
			exchangePAT := p.ExchangeCNPAT
			if exchangePAT == nil {
				exchangePAT = p.defaultExchangeCNPAT(provider)
			}
			identity, expiresAt, err = exchangePAT(ctx, pat, machine)
		} else {
			exchangePAT := p.ExchangePAT
			if exchangePAT == nil {
				exchangePAT = p.defaultExchangePAT(provider)
			}
			identity, err = exchangePAT(ctx, pat, machine)
		}
		if err != nil {
			// A failed PAT exchange supplies no identity to cache. Return the wrapped
			// failure so the provider owner can handle rejected credentials or transport
			// failure.
			return nil, time.Time{}, fmt.Errorf("qoder pat exchange: %w", err)
		}
		ApplyIdentityMetadata(identity, provider)
		// The session retains the identity pointer. Resolve optional metadata before
		// publication so later cached reads see the same encrypted identity and
		// fields.
		if site == SiteGlobal {
			p.populateOrganizationFromAPI(ctx, provider, identity)
		}
		session, sessionErr := NewSessionForSite(identity, machine, site)
		return session, expiresAt, sessionErr
	}

	token := strings.TrimSpace(provider.GetCredential("security_oauth_token"))
	machineID := strings.TrimSpace(provider.GetCredential("machine_id"))
	if token != "" {
		if machineID == "" {
			return nil, time.Time{}, errors.New("qoder credentials require machine_id with security_oauth_token")
		}
		if FirstNonEmptyQoder(provider.GetCredential("uid"), provider.GetCredential("aid")) == "" {
			return nil, time.Time{}, errors.New("qoder credentials require uid or aid with security_oauth_token")
		}
		identity := &AuthIdentity{
			Name:               FirstNonEmptyQoder(provider.GetCredential("name"), provider.Name),
			AID:                FirstNonEmptyQoder(provider.GetCredential("aid"), provider.GetCredential("uid")),
			UID:                FirstNonEmptyQoder(provider.GetCredential("uid"), provider.GetCredential("aid")),
			UserType:           FirstNonEmptyQoder(provider.GetCredential("user_type"), "personal_standard"),
			SecurityOauthToken: token,
			RefreshToken:       provider.GetCredential("refresh_token"),
		}
		ApplyIdentityMetadata(identity, provider)
		p.populateOrganizationFromAPI(ctx, provider, identity)
		machine := MachineForCredentials(provider)
		session, sessionErr := NewSessionForSite(identity, machine, site)
		return session, time.Time{}, sessionErr
	}

	return nil, time.Time{}, errors.New("qoder credentials require pat or security_oauth_token+machine_id")
}

// defaultExchangePAT returns a global Center exchange using this builder's
// request doer. The returned callback follows ExchangePATContext defaults and
// borrows the builder transport for each invocation; provider is currently
// unused.
func (p *SessionBuilder) defaultExchangePAT(provider *CredentialInput) PATExchanger {
	return func(ctx context.Context, pat string, machine *MachineIdentity) (*AuthIdentity, error) {
		return ExchangePATContext(ctx, pat, machine, "", p.Doer)
	}
}

// defaultExchangeCNPAT returns the CN OpenAPI and status exchange using this
// builder's doer. The callback selects the CN profile and returns its token
// expiry; provider is currently unused.
func (p *SessionBuilder) defaultExchangeCNPAT(provider *CredentialInput) CNPATExchanger {
	return func(ctx context.Context, pat string, machine *MachineIdentity) (*AuthIdentity, time.Time, error) {
		profile, err := ProfileForSite(SiteCN)
		if err != nil {
			return nil, time.Time{}, err
		}
		return ExchangeQoderCN20PATContext(ctx, pat, machine, profile, p.Doer)
	}
}

// populateOrganizationFromAPI resolves optional organization metadata before session
// publication. Device grants retain their authorized organization. Legacy credentials
// without a stored organization can discover it from authenticated userinfo.
// The tags endpoint accepts only an organization ID, including for PAT sessions.
func (p *SessionBuilder) populateOrganizationFromAPI(ctx context.Context, provider *CredentialInput, identity *AuthIdentity) {
	if p == nil || identity == nil || identity.SecurityOauthToken == "" {
		return
	}
	if identity.OrganizationID != "" && identity.OrganizationID == strings.TrimSpace(provider.OrganizationId) {
		return
	}
	if identity.OrganizationID == "" {
		mode, _ := credentialRefreshMode(provider)
		if mode == RefreshModeDeviceToken && strings.TrimSpace(provider.Pat) == "" {
			return
		}
		profile, err := credentialProfile(provider)
		if err != nil {
			return
		}
		client := NewOAuthClientForProfile(profile, nil)
		client.Doer = p.Doer
		// Legacy credentials may predate persisted organization IDs. Use the grant's
		// own profile before looking up tags so a user ID never selects an organization.
		user, err := client.GetUserInfo(ctx, identity.SecurityOauthToken)
		if err != nil || user.OrganizationID == "" {
			return
		}
		uid := firstNonEmpty(user.UserID, user.ID)
		if uid == "" || uid != identity.UID && uid != identity.AID {
			return
		}
		identity.OrganizationID, identity.OrganizationName = user.OrganizationID, user.OrganizationName
	}
	lookup := p.GetOrgTags
	if lookup == nil {
		lookup = func(ctx context.Context, token, organizationID string) (*OrganizationTags, error) {
			return p.GetOrganizationTags(ctx, provider, token, organizationID)
		}
	}
	// A verified organization ID is sufficient for inference. Unavailable name
	// enrichment must not discard the authorized account or prevent session use.
	_ = enrichOrganizationName(ctx, identity, lookup)
}

// GetOrganizationTags uses the site's OpenAPI reader and the provider transport.
// The caller supplies an organization ID established by the authenticated profile.
// Failed requests return bounded, redacted diagnostics through the shared client.
func (p *SessionBuilder) GetOrganizationTags(ctx context.Context, provider *CredentialInput, token, organizationID string) (*OrganizationTags, error) {
	profile, err := credentialProfile(provider)
	if err != nil {
		return nil, err
	}
	client := NewOAuthClientForProfile(profile, nil)
	client.Doer = p.Doer
	return client.GetOrganizationTags(ctx, token, organizationID)
}

// ApplyIdentityMetadata fills blank descriptive fields from saved provider
// credentials. Existing identity name and organization fields take precedence;
// nil inputs are ignored, and the identity is mutated before session
// publication.
func ApplyIdentityMetadata(identity *AuthIdentity, provider *CredentialInput) {
	if identity == nil || provider == nil {
		return
	}
	if strings.TrimSpace(identity.Name) == "" {
		identity.Name = FirstNonEmptyQoder(provider.GetCredential("name"), provider.Name)
	}
	if strings.TrimSpace(identity.OrganizationID) == "" {
		identity.OrganizationID = provider.GetCredential("organization_id")
	}
	if strings.TrimSpace(identity.OrganizationName) == "" && identity.OrganizationID == provider.GetCredential("organization_id") {
		identity.OrganizationName = provider.GetCredential("organization_name")
	}
}

// MachineForCredentials reconstructs the machine shape from saved credentials
// and site defaults. Nil input creates a global machine, and an invalid site
// uses global defaults. CN keeps only the machine ID; global missing token uses
// that ID and missing type uses the legacy client type.
func MachineForCredentials(provider *CredentialInput) *MachineIdentity {
	if provider == nil {
		return NewMachine()
	}
	site, err := credentialSite(provider)
	// Machine reconstruction can receive an unvalidated stored site. Use global
	// defaults here while the credential builder separately rejects invalid site
	// input before exchange.
	if err != nil {
		site = SiteGlobal
	}
	machineID := strings.TrimSpace(provider.GetCredential("machine_id"))
	if machineID == "" {
		machineID = NewMachineForSite(site).MachineID
	}
	if site == SiteCN {
		// CN Gateway requests send empty machine token and type headers. Reconstruct
		// that shape even when older imported credentials contain random values for
		// them.
		return &MachineIdentity{MachineID: machineID}
	}
	return &MachineIdentity{
		MachineID:    machineID,
		MachineToken: FirstNonEmptyQoder(provider.GetCredential("machine_token"), machineID),
		MachineType:  FirstNonEmptyQoder(provider.GetCredential("machine_type"), "5"),
	}
}

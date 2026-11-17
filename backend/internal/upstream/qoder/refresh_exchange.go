package qoder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// refresh_exchange.go refreshes Qoder credentials through the selected site and
// authentication workflow.
// Saved credentials identify both a Qoder site and a refresh workflow, while PAT
// credentials rebuild their session from the original personal token.
// RefreshExchange chooses that workflow and returns identity, machine, and
// expiry metadata for the provider's existing persistence and cache
// coordination.
//
// CN QoderCN20 refresh uses OpenAPI followed by userinfo and status; imported CN
// COSY tokens use Gateway refresh with user and organization IDs. Global COSY
// refresh uses Center. Callback overrides replace their corresponding token
// exchange, and the provider doer supplies transport to defaults. Credential
// output contains nonempty new identity and machine values, retaining the saved
// refresh token when an exchange omits its replacement.

type (
	// SessionRefresher exchanges global COSY tokens with a retained machine
	// identity.
	SessionRefresher func(context.Context, string, string, *MachineIdentity) (*AuthIdentity, error)
	// CN20SessionRefresher exchanges a CN refresh token and returns identity plus
	// expiry.
	CN20SessionRefresher func(context.Context, string, *MachineIdentity) (*AuthIdentity, time.Time, error)
	// CNCosySessionRefresher refreshes imported CN COSY identity with user and
	// organization IDs.
	CNCosySessionRefresher func(context.Context, string, string, string, string, *MachineIdentity) (*AuthIdentity, error)
	// RefreshExchange combines workflow-specific overrides with one provider's
	// request transport.
	RefreshExchange struct {
		// RefreshSession replaces global COSY exchange when supplied. A nil callback
		// selects Center refresh through Doer.
		RefreshSession SessionRefresher // global refresh override
		// RefreshCN20 replaces CN OpenAPI completion when supplied. A nil callback
		// selects refresh, userinfo, and status through Doer.
		RefreshCN20 CN20SessionRefresher // CN20 refresh override
		// RefreshCNCosy replaces imported CN COSY refresh when supplied. A nil
		// callback selects the Gateway flow through Doer.
		RefreshCNCosy CNCosySessionRefresher // CN COSY override
		// Doer supplies transport for default exchanges. A nil value selects each
		// platform client's HTTP defaults; callback overrides choose their own transport.
		Doer RequestDoer // provider request transport
	}
)

// RefreshResult carries exchanged credentials and workflow metadata for provider
// persistence.
type RefreshResult struct {
	Identity  *AuthIdentity    // exchanged authentication identity
	Machine   *MachineIdentity // retained machine identity
	Site      Site             // selected credential site
	Mode      string           // selected refresh workflow
	ExpiresAt time.Time        // exchanged token expiry
}

// Refresh returns exchanged identity and machine metadata for the provider
// persistence owner. Site and mode are validated first; PAT chooses COSY
// reconstruction, while token-only refresh requires a saved machine ID. The
// returned identity needs a nonempty security token.
//
// Workflow selection remains tied to the saved site and credential source.
// Descriptive metadata is filled after exchange, and the result carries the
// selected mode and any CN OpenAPI expiry. This method performs network exchange
// and validation; the provider owner merges, persists, and invalidates cached
// credentials.
func (r *RefreshExchange) Refresh(ctx context.Context, provider *CredentialInput) (*RefreshResult, error) {
	site, err := credentialSite(provider)
	if err != nil {
		return nil, err
	}
	refreshMode, err := credentialRefreshMode(provider)
	if err != nil {
		return nil, err
	}
	// PAT credentials are reconstructed from their original token. Mark that
	// credential source as COSY even when saved refresh metadata names another
	// workflow.
	if strings.TrimSpace(provider.GetCredential("pat")) != "" {
		refreshMode = RefreshModeCosy
	}
	machineID := strings.TrimSpace(provider.GetCredential("machine_id"))
	if machineID == "" && strings.TrimSpace(provider.GetCredential("pat")) == "" {
		return nil, errors.New("qoder refresh requires machine_id")
	}
	machine := MachineForCredentials(provider)
	doer := r.Doer
	identity, expiresAt, err := r.refreshIdentity(ctx, provider, site, refreshMode, machine, doer)
	if err != nil {
		return nil, fmt.Errorf("qoder refresh token: %w", err)
	}
	if identity == nil {
		return nil, errors.New("qoder refresh returned empty identity")
	}
	if strings.TrimSpace(identity.SecurityOauthToken) == "" {
		return nil, errors.New("qoder refresh returned empty security_oauth_token")
	}
	ApplyIdentityMetadata(identity, provider)

	return &RefreshResult{Identity: identity, Machine: machine, Site: site, Mode: refreshMode, ExpiresAt: expiresAt}, nil
}

// refreshIdentity selects the exchange appropriate to PAT, site, and refresh
// mode. PAT rebuilds CN identity through OpenAPI/status or global identity
// through Center. Token refresh requires a saved refresh token, and qodercn20 is
// rejected outside CN.
//
// CN20, imported CN COSY, and global COSY paths each honor their own callback
// override. Default paths use the supplied doer; imported COSY and global
// refresh return zero expiry, while CN20 returns the OpenAPI expiry used by the
// provider owner.
func (r *RefreshExchange) refreshIdentity(
	ctx context.Context,
	provider *CredentialInput,
	site Site,
	refreshMode string,
	machine *MachineIdentity,
	doer RequestDoer,
) (*AuthIdentity, time.Time, error) {
	pat := strings.TrimSpace(provider.GetCredential("pat"))
	if pat != "" {
		if site == SiteCN {
			profile := MustProfileForSite(SiteCN)
			identity, expiresAt, err := ExchangeQoderCN20PATContext(ctx, pat, machine, profile, doer)
			return identity, expiresAt, err
		}
		identity, err := ExchangePATContext(ctx, pat, machine, "", doer)
		return identity, time.Time{}, err
	}

	refreshToken := strings.TrimSpace(provider.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, time.Time{}, errors.New("no refresh token available")
	}
	securityToken := strings.TrimSpace(provider.GetCredential("security_oauth_token"))
	if refreshMode == RefreshModeDeviceToken {
		client := NewOAuthClientForProfile(MustProfileForSite(site), nil)
		client.Doer = doer
		token, err := client.RefreshDeviceToken(ctx, refreshToken, machine)
		if err != nil {
			return nil, time.Time{}, err
		}
		// Rotation authenticates the already-bound refresh grant. Keep its saved
		// identity so an auxiliary profile outage cannot discard a newly rotated
		// credential after the upstream has invalidated its predecessor.
		uid := FirstNonEmptyQoder(provider.Uid, provider.Aid)
		if returned := strings.TrimSpace(token.UserID); returned != "" && uid != "" && returned != uid {
			return nil, time.Time{}, errors.New("qoder: refreshed device token belongs to another account")
		}
		identity := BuildIdentityFromDeviceToken(nil, token)
		identity.UID = FirstNonEmptyQoder(uid, identity.UID)
		identity.AID = FirstNonEmptyQoder(provider.Aid, identity.UID)
		identity.UserType = FirstNonEmptyQoder(provider.UserType, identity.UserType)
		return identity, token.ExpiryTime(time.Now()), nil
	}
	if refreshMode == RefreshModeQoderCN20 {
		if site != SiteCN {
			return nil, time.Time{}, errors.New("qoder qodercn20 refresh requires cn site")
		}
		if r.RefreshCN20 != nil {
			return r.RefreshCN20(ctx, refreshToken, machine)
		}
		return RefreshQoderCN20SessionContext(ctx, refreshToken, machine, MustProfileForSite(site), doer)
	}
	if site == SiteCN {
		userID := FirstNonEmptyQoder(provider.GetCredential("uid"), provider.GetCredential("aid"))
		organizationID := provider.GetCredential("organization_id")
		if r.RefreshCNCosy != nil {
			identity, err := r.RefreshCNCosy(ctx, refreshToken, securityToken, userID, organizationID, machine)
			return identity, time.Time{}, err
		}
		identity, err := RefreshCosySessionForProfileContext(ctx, MustProfileForSite(site), refreshToken, securityToken, userID, organizationID, machine, doer)
		return identity, time.Time{}, err
	}
	refreshSession := r.RefreshSession
	// The global callback is optional. Bind the provider doer into the Center
	// default so refresh requests use the same selected transport as session
	// construction.
	if refreshSession == nil {
		refreshSession = func(ctx context.Context, refreshToken, securityOauthToken string, machine *MachineIdentity) (*AuthIdentity, error) {
			return RefreshSessionContext(ctx, refreshToken, securityOauthToken, machine, "", doer)
		}
	}
	identity, err := refreshSession(ctx, refreshToken, securityToken, machine)
	return identity, time.Time{}, err
}

// TokenInfoCredentials builds a new credential map from nonempty identity and
// machine fields. Values copied from those inputs are trimmed. A saved provider
// refresh token fills the map only when the new identity omitted one, retaining
// its supplied spelling.
func TokenInfoCredentials(identity *AuthIdentity, provider *CredentialInput, machine *MachineIdentity) map[string]any {
	credentials := map[string]any{}
	if identity != nil {
		if token := strings.TrimSpace(identity.SecurityOauthToken); token != "" {
			credentials["security_oauth_token"] = token
		}
		if refreshToken := strings.TrimSpace(identity.RefreshToken); refreshToken != "" {
			credentials["refresh_token"] = refreshToken
		}
		if uid := strings.TrimSpace(identity.UID); uid != "" {
			credentials["uid"] = uid
		}
		if aid := strings.TrimSpace(identity.AID); aid != "" {
			credentials["aid"] = aid
		}
		if orgID := strings.TrimSpace(identity.OrganizationID); orgID != "" {
			credentials["organization_id"] = orgID
		}
		if orgName := strings.TrimSpace(identity.OrganizationName); orgName != "" {
			credentials["organization_name"] = orgName
		}
		if name := strings.TrimSpace(identity.Name); name != "" {
			credentials["name"] = name
		}
		if userType := strings.TrimSpace(identity.UserType); userType != "" {
			credentials["user_type"] = userType
		}
	}
	if machine != nil {
		if machineID := strings.TrimSpace(machine.MachineID); machineID != "" {
			credentials["machine_id"] = machineID
		}
		if machineToken := strings.TrimSpace(machine.MachineToken); machineToken != "" {
			credentials["machine_token"] = machineToken
		}
		if machineType := strings.TrimSpace(machine.MachineType); machineType != "" {
			credentials["machine_type"] = machineType
		}
	}
	// An exchange can omit its refresh-token replacement. Keep the saved token in
	// that case so the provider's merge still has a token for future refreshes.
	if provider != nil {
		if refreshToken := provider.GetCredential("refresh_token"); refreshToken != "" {
			if _, ok := credentials["refresh_token"]; !ok {
				credentials["refresh_token"] = refreshToken
			}
		}
	}
	return credentials
}

// 本文件只执行 Qoder 刷新协议；是否刷新、缓存与条件持久化仍由账号用例决定。
package qoder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SessionRefresher func(context.Context, string, string, *MachineIdentity) (*AuthIdentity, error)
type CN20SessionRefresher func(context.Context, string, *MachineIdentity) (*AuthIdentity, time.Time, error)
type CNCosySessionRefresher func(context.Context, string, string, string, string, *MachineIdentity) (*AuthIdentity, error)
type RefreshExchange struct {
	RefreshSession SessionRefresher
	RefreshCN20    CN20SessionRefresher
	RefreshCNCosy  CNCosySessionRefresher
	Doer           RequestDoer
}
type RefreshResult struct {
	Identity  *AuthIdentity
	Machine   *MachineIdentity
	Site      Site
	Mode      string
	ExpiresAt time.Time
}

func (r *RefreshExchange) Refresh(ctx context.Context, account *CredentialInput) (*RefreshResult, error) {
	site, err := credentialSite(account)
	if err != nil {
		return nil, err
	}
	refreshMode, err := credentialRefreshMode(account)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		refreshMode = RefreshModeCosy
	}
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if machineID == "" && strings.TrimSpace(account.GetCredential("pat")) == "" {
		return nil, errors.New("qoder refresh requires machine_id")
	}
	machine := MachineForCredentials(account)
	doer := r.Doer
	identity, expiresAt, err := r.refreshIdentity(ctx, account, site, refreshMode, machine, doer)
	if err != nil {
		return nil, fmt.Errorf("qoder refresh token: %w", err)
	}
	if identity == nil {
		return nil, errors.New("qoder refresh returned empty identity")
	}
	if strings.TrimSpace(identity.SecurityOauthToken) == "" {
		return nil, errors.New("qoder refresh returned empty security_oauth_token")
	}
	ApplyIdentityMetadata(identity, account)

	return &RefreshResult{Identity: identity, Machine: machine, Site: site, Mode: refreshMode, ExpiresAt: expiresAt}, nil
}
func (r *RefreshExchange) refreshIdentity(
	ctx context.Context,
	account *CredentialInput,
	site Site,
	refreshMode string,
	machine *MachineIdentity,
	doer RequestDoer,
) (*AuthIdentity, time.Time, error) {
	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		if site == SiteCN {
			profile := MustProfileForSite(SiteCN)
			identity, expiresAt, err := ExchangeQoderCN20PATContext(ctx, pat, machine, profile, doer)
			return identity, expiresAt, err
		}
		identity, err := ExchangePATContext(ctx, pat, machine, "", doer)
		return identity, time.Time{}, err
	}

	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, time.Time{}, errors.New("no refresh token available")
	}
	securityToken := strings.TrimSpace(account.GetCredential("security_oauth_token"))
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
		userID := FirstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid"))
		organizationID := account.GetCredential("organization_id")
		if r.RefreshCNCosy != nil {
			identity, err := r.RefreshCNCosy(ctx, refreshToken, securityToken, userID, organizationID, machine)
			return identity, time.Time{}, err
		}
		identity, err := RefreshCosySessionForProfileContext(ctx, MustProfileForSite(site), refreshToken, securityToken, userID, organizationID, machine, doer)
		return identity, time.Time{}, err
	}
	refreshSession := r.RefreshSession
	if refreshSession == nil {
		refreshSession = func(ctx context.Context, refreshToken, securityOauthToken string, machine *MachineIdentity) (*AuthIdentity, error) {
			return RefreshSessionContext(ctx, refreshToken, securityOauthToken, machine, "", doer)
		}
	}
	identity, err := refreshSession(ctx, refreshToken, securityToken, machine)
	return identity, time.Time{}, err
}
func TokenInfoCredentials(identity *AuthIdentity, account *CredentialInput, machine *MachineIdentity) map[string]any {
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
	if account != nil {
		if refreshToken := account.GetCredential("refresh_token"); refreshToken != "" {
			if _, ok := credentials["refresh_token"]; !ok {
				credentials["refresh_token"] = refreshToken
			}
		}
	}
	return credentials
}

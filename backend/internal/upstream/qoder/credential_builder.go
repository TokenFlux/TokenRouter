// 本文件仅构建 Qoder 原生凭据与执行供应商交换，缓存和失效由 account 拥有。
package qoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CredentialInput 是构建 session 所需的显式秘密投影，禁止用于公开响应。
type CredentialInput struct {
	Name               string
	Pat                string
	Site               string
	RefreshMode        string
	SecurityOauthToken string
	MachineId          string
	MachineToken       string
	MachineType        string
	Uid                string
	Aid                string
	IdentityName       string
	UserType           string
	RefreshToken       string
	OrganizationId     string
	OrganizationName   string
}

func (v *CredentialInput) String() string { return "qoder credentials [redacted]" }
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

type PATExchanger func(context.Context, string, *MachineIdentity) (*AuthIdentity, error)
type CNPATExchanger func(context.Context, string, *MachineIdentity) (*AuthIdentity, time.Time, error)
type OrganizationTagsGetter func(context.Context, string, string) (*OrganizationTags, error)

// SessionBuilder 无缓存或后台状态，Doer 是按本次账号投影生成的传输入口。
type SessionBuilder struct {
	ExchangePAT   PATExchanger
	ExchangeCNPAT CNPATExchanger
	GetOrgTags    OrganizationTagsGetter
	Doer          RequestDoer
}

func credentialSite(v *CredentialInput) (Site, error) {
	if v == nil {
		return SiteGlobal, fmt.Errorf("qoder: account is nil")
	}
	return ParseSite(v.Site)
}
func credentialProfile(v *CredentialInput) (Profile, error) {
	site, err := credentialSite(v)
	if err != nil {
		return Profile{}, err
	}
	return ProfileForSite(site)
}
func credentialRefreshMode(v *CredentialInput) (string, error) {
	if v == nil {
		return "", fmt.Errorf("qoder: account is nil")
	}
	return ParseRefreshMode(v.RefreshMode)
}
func (p *SessionBuilder) BuildSession(ctx context.Context, account *CredentialInput) (*SessionContext, time.Time, error) {
	site, err := credentialSite(account)
	if err != nil {
		return nil, time.Time{}, err
	}
	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat == "" {
		refreshMode, modeErr := credentialRefreshMode(account)
		if modeErr != nil {
			return nil, time.Time{}, modeErr
		}
		if refreshMode == RefreshModeQoderCN20 && site != SiteCN {
			return nil, time.Time{}, errors.New("qoder qodercn20 credentials require cn site")
		}
	}
	if pat != "" {
		machine := MachineForCredentials(account)
		var identity *AuthIdentity
		var expiresAt time.Time
		if site == SiteCN {
			exchangePAT := p.ExchangeCNPAT
			if exchangePAT == nil {
				exchangePAT = p.defaultExchangeCNPAT(account)
			}
			identity, expiresAt, err = exchangePAT(ctx, pat, machine)
		} else {
			exchangePAT := p.ExchangePAT
			if exchangePAT == nil {
				exchangePAT = p.defaultExchangePAT(account)
			}
			identity, err = exchangePAT(ctx, pat, machine)
		}
		if err != nil {
			// PAT exchange 失败通常是永久错误（无效凭据），不跳过缓存
			return nil, time.Time{}, fmt.Errorf("qoder pat exchange: %w", err)
		}
		ApplyIdentityMetadata(identity, account)
		// populateOrganizationFromAPI 在 session 创建前调用，避免缓存后并发修改 identity
		if site == SiteGlobal {
			p.populateOrganizationFromAPI(ctx, account, identity)
		}
		session, sessionErr := NewSessionForSite(identity, machine, site)
		return session, expiresAt, sessionErr
	}

	token := strings.TrimSpace(account.GetCredential("security_oauth_token"))
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if token != "" {
		if machineID == "" {
			return nil, time.Time{}, errors.New("qoder credentials require machine_id with security_oauth_token")
		}
		if FirstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid")) == "" {
			return nil, time.Time{}, errors.New("qoder credentials require uid or aid with security_oauth_token")
		}
		identity := &AuthIdentity{
			Name:               FirstNonEmptyQoder(account.GetCredential("name"), account.Name),
			AID:                FirstNonEmptyQoder(account.GetCredential("aid"), account.GetCredential("uid")),
			UID:                FirstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid")),
			UserType:           FirstNonEmptyQoder(account.GetCredential("user_type"), "personal_standard"),
			SecurityOauthToken: token,
			RefreshToken:       account.GetCredential("refresh_token"),
		}
		ApplyIdentityMetadata(identity, account)
		p.populateOrganizationFromAPI(ctx, account, identity)
		machine := MachineForCredentials(account)
		session, sessionErr := NewSessionForSite(identity, machine, site)
		return session, time.Time{}, sessionErr
	}

	return nil, time.Time{}, errors.New("qoder credentials require pat or security_oauth_token+machine_id")
}
func (p *SessionBuilder) defaultExchangePAT(account *CredentialInput) PATExchanger {
	return func(ctx context.Context, pat string, machine *MachineIdentity) (*AuthIdentity, error) {
		return ExchangePATContext(ctx, pat, machine, "", p.Doer)
	}
}
func (p *SessionBuilder) defaultExchangeCNPAT(account *CredentialInput) CNPATExchanger {
	return func(ctx context.Context, pat string, machine *MachineIdentity) (*AuthIdentity, time.Time, error) {
		profile, err := ProfileForSite(SiteCN)
		if err != nil {
			return nil, time.Time{}, err
		}
		return ExchangeQoderCN20PATContext(ctx, pat, machine, profile, p.Doer)
	}
}
func (p *SessionBuilder) populateOrganizationFromAPI(ctx context.Context, account *CredentialInput, identity *AuthIdentity) {
	if p == nil || identity == nil {
		return
	}
	if strings.TrimSpace(identity.OrganizationID) != "" {
		return
	}
	token := strings.TrimSpace(identity.SecurityOauthToken)
	if token == "" {
		return
	}
	uid := FirstNonEmptyQoder(identity.UID, identity.AID)
	if uid == "" {
		return
	}
	var tags *OrganizationTags
	var err error
	if p.GetOrgTags != nil {
		tags, err = p.GetOrgTags(ctx, token, uid)
	} else {
		tags, err = p.GetOrganizationTags(ctx, account, token, uid)
	}
	if err != nil || tags == nil {
		return
	}
	identity.OrganizationID = strings.TrimSpace(tags.OrganizationID)
	identity.OrganizationName = strings.TrimSpace(tags.OrganizationName)
}
func (p *SessionBuilder) GetOrganizationTags(ctx context.Context, account *CredentialInput, token, uid string) (*OrganizationTags, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, fmt.Errorf("qoder: organization tags require uid")
	}
	profile, err := credentialProfile(account)
	if err != nil {
		return nil, err
	}
	if doer := p.Doer; doer != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.OpenAPIBaseURL+OrganizationTagsPathPrefix+url.PathEscape(uid)+"/tags", nil)
		if err != nil {
			return nil, fmt.Errorf("qoder: create organization tags request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("User-Agent", profile.OpenAPIUserAgent())

		resp, err := doer(req)
		if err != nil {
			return nil, fmt.Errorf("qoder: organization tags request: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, fmt.Errorf("qoder: organization tags failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
		}

		var tags OrganizationTags
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			return nil, fmt.Errorf("qoder: parse organization tags response: %w", err)
		}
		return &tags, nil
	}
	return NewOAuthClientForProfile(profile, nil).GetOrganizationTags(ctx, token, uid)
}
func ApplyIdentityMetadata(identity *AuthIdentity, account *CredentialInput) {
	if identity == nil || account == nil {
		return
	}
	if strings.TrimSpace(identity.Name) == "" {
		identity.Name = FirstNonEmptyQoder(account.GetCredential("name"), account.Name)
	}
	if strings.TrimSpace(identity.OrganizationID) == "" {
		identity.OrganizationID = account.GetCredential("organization_id")
	}
	if strings.TrimSpace(identity.OrganizationName) == "" {
		identity.OrganizationName = account.GetCredential("organization_name")
	}
}

// MachineForCredentials 读取持久化机器身份，并对旧账号使用兼容回退值。
func MachineForCredentials(account *CredentialInput) *MachineIdentity {
	if account == nil {
		return NewMachine()
	}
	site, err := credentialSite(account)
	if err != nil {
		site = SiteGlobal
	}
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if machineID == "" {
		machineID = NewMachineForSite(site).MachineID
	}
	if site == SiteCN {
		// 忽略旧版本曾保存的随机 token/type，保持官方国内客户端的空值语义。
		return &MachineIdentity{MachineID: machineID}
	}
	return &MachineIdentity{
		MachineID:    machineID,
		MachineToken: FirstNonEmptyQoder(account.GetCredential("machine_token"), machineID),
		MachineType:  FirstNonEmptyQoder(account.GetCredential("machine_type"), "5"),
	}
}

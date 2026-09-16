package service

import (
	"context"
	"errors"
	"sync"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type qoderPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
type qoderCNPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error)
type qoderOrganizationTagsGetter func(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error)

type qoderSessionCacheEntry = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]

type qoderSessionAccountState = accountcore.QoderSessionAccountState

// defaultQoderSessionBuildTimeout 限制脱离单个请求生命周期后的共享凭据交换，避免异常上游永久占用 singleflight。
const defaultQoderSessionBuildTimeout = accountcore.DefaultQoderSessionBuildTimeout

// errQoderSessionBuildInvalidated 表示在途构建已被显式失效或新凭据取代。
var errQoderSessionBuildInvalidated = accountcore.ErrQoderSessionBuildInvalidated

// QoderTokenProvider 为 Qoder 账号构建并缓存 COSY session 上下文。
type QoderTokenProvider struct {
	Core                *qoderSessionState
	coreOnce            sync.Once
	exchangePAT         qoderPATExchanger
	exchangeCNPAT       qoderCNPATExchanger
	getOrgTags          qoderOrganizationTagsGetter
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
}

func NewQoderTokenProvider() *QoderTokenProvider {
	return &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry),
		AccountStates:       make(map[int64]qoderSessionAccountState),
		SessionBuildTimeout: defaultQoderSessionBuildTimeout},
	}
}

func (p *QoderTokenProvider) SetHTTPUpstream(httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) {
	if p == nil {
		return
	}
	p.httpUpstream = httpUpstream
	p.tlsFPProfileService = tlsFPProfileService
}

func (p *QoderTokenProvider) GetSession(ctx context.Context, account *Account) (*qoder.SessionContext, error) {
	if p == nil {
		return nil, errors.New("qoder token provider is nil")
	}
	return p.qoderState().GetSession(ctx, AccountRecordView(account), func(ctx context.Context, value *accountcore.Record) (*qoder.SessionContext, time.Time, error) {
		return p.buildSession(ctx, AccountFromRecord(value))
	})
}

func (p *QoderTokenProvider) Invalidate(accountID int64) {
	if p != nil {
		p.qoderState().Invalidate(accountID)
	}
}

func (p *QoderTokenProvider) InvalidateAccount(account *Account) {
	if p != nil {
		p.qoderState().InvalidateAccount(AccountRecordView(account))
	}
}

func qoderCredentialsHash(credentials map[string]any) string {
	return accountcore.QoderCredentialsHash(credentials)
}

func qoderRefreshCredentialsHash(credentials map[string]any) string {
	if len(credentials) == 0 {
		return qoderCredentialsHash(nil)
	}
	keys := []string{
		"pat",
		"security_oauth_token",
		"refresh_token",
		"machine_id",
		"machine_token",
		"machine_type",
		"uid",
		"aid",
		"organization_id",
		"organization_name",
		"name",
		"user_type",
		"site",
		"refresh_mode",
	}
	auth := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := credentials[key]; ok {
			auth[key] = value
		}
	}
	return qoderCredentialsHash(auth)
}

func firstNonEmptyQoder(values ...string) string { return qoder.FirstNonEmptyQoder(values...) }

type qoderSessionState = accountcore.QoderSessions[*qoder.SessionContext]

// qoderState 仅惰性装配账号状态；所有缓存与锁均归该唯一实例。
func (p *QoderTokenProvider) qoderState() *qoderSessionState {
	p.coreOnce.Do(func() {
		if p.Core == nil {
			p.Core = &qoderSessionState{}
		}
	})
	return p.Core
}
func (p *QoderTokenProvider) StopContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.qoderState().StopContext(ctx)
}

// qoderCredentialInput 只投影供应商交换实际读取的字段，缓存快照仍由 account 管理。
func qoderCredentialInput(value *Account) *qoder.CredentialInput {
	if value == nil {
		return nil
	}
	return &qoder.CredentialInput{Name: value.Name,
		Pat:                value.GetCredential("pat"),
		Site:               value.GetCredential("site"),
		RefreshMode:        value.GetCredential("refresh_mode"),
		SecurityOauthToken: value.GetCredential("security_oauth_token"),
		MachineId:          value.GetCredential("machine_id"),
		MachineToken:       value.GetCredential("machine_token"),
		MachineType:        value.GetCredential("machine_type"),
		Uid:                value.GetCredential("uid"),
		Aid:                value.GetCredential("aid"),
		IdentityName:       value.GetCredential("name"),
		UserType:           value.GetCredential("user_type"),
		RefreshToken:       value.GetCredential("refresh_token"),
		OrganizationId:     value.GetCredential("organization_id"),
		OrganizationName:   value.GetCredential("organization_name"),
	}
}
func (p *QoderTokenProvider) sessionBuilder(value *Account) *qoder.SessionBuilder {
	return &qoder.SessionBuilder{ExchangePAT: qoder.PATExchanger(p.exchangePAT), ExchangeCNPAT: qoder.CNPATExchanger(p.exchangeCNPAT), GetOrgTags: qoder.OrganizationTagsGetter(p.getOrgTags), Doer: newQoderRequestDoer(value, p.httpUpstream, p.tlsFPProfileService)}
}
func (p *QoderTokenProvider) buildSession(ctx context.Context, value *Account) (*qoder.SessionContext, time.Time, error) {
	return p.sessionBuilder(value).BuildSession(ctx, qoderCredentialInput(value))
}
func (p *QoderTokenProvider) getOrganizationTagsForAccount(ctx context.Context, value *Account, token, uid string) (*qoder.OrganizationTags, error) {
	return p.sessionBuilder(value).GetOrganizationTags(ctx, qoderCredentialInput(value), token, uid)
}

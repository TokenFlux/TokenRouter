package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type qoderPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
type qoderCNPATExchanger func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error)
type qoderOrganizationTagsGetter func(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error)

type qoderSessionCacheEntry = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]

// errQoderSessionBuildInvalidated 表示在途构建已被显式失效或新凭据取代。
var errQoderSessionBuildInvalidated = accountcore.ErrQoderSessionBuildInvalidated

// QoderTokenProvider 为 Qoder 账号构建并缓存 COSY session 上下文。
type QoderTokenProvider struct {
	Core                *qoderSessionState
	coreOnce            sync.Once
	exchangePAT         qoderPATExchanger
	exchangeCNPAT       qoderCNPATExchanger
	getOrgTags          qoderOrganizationTagsGetter
	httpUpstream        QoderTransport
	tlsFPProfileService *provider.TLSProfiles
}

func NewQoderTokenProvider(builder qoder.SessionBuilder) *QoderTokenProvider {
	return &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry),
		AccountStates:       make(map[int64]accountcore.QoderSessionAccountState),
		SessionBuildTimeout: accountcore.DefaultQoderSessionBuildTimeout},
		exchangePAT:   qoderPATExchanger(builder.ExchangePAT),
		exchangeCNPAT: qoderCNPATExchanger(builder.ExchangeCNPAT),
		getOrgTags:    qoderOrganizationTagsGetter(builder.GetOrgTags),
	}
}

func (p *QoderTokenProvider) SetHTTPUpstream(httpUpstream QoderTransport, tlsFPProfileService *provider.TLSProfiles) {
	if p == nil {
		return
	}
	p.httpUpstream = httpUpstream
	p.tlsFPProfileService = tlsFPProfileService
}

func (p *QoderTokenProvider) GetSession(ctx context.Context, account *accountcore.Record) (*qoder.SessionContext, error) {
	if p == nil {
		return nil, errors.New("qoder token provider is nil")
	}
	return p.qoderState().GetSession(ctx, account, func(ctx context.Context, value *accountcore.Record) (*qoder.SessionContext, time.Time, error) {
		return p.buildSession(ctx, value)
	})
}

func (p *QoderTokenProvider) Invalidate(accountID int64) {
	if p != nil {
		p.qoderState().Invalidate(accountID)
	}
}

func (p *QoderTokenProvider) InvalidateAccount(account *accountcore.Record) {
	if p != nil {
		p.qoderState().InvalidateAccount(account)
	}
}

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

// QoderCredentialInput 只投影供应商交换实际读取的字段，缓存快照仍由 account 管理。
func QoderCredentialInput(value *accountcore.Record) *qoder.CredentialInput {
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
func (p *QoderTokenProvider) sessionBuilder(value *accountcore.Record) *qoder.SessionBuilder {
	return &qoder.SessionBuilder{ExchangePAT: qoder.PATExchanger(p.exchangePAT), ExchangeCNPAT: qoder.CNPATExchanger(p.exchangeCNPAT), GetOrgTags: qoder.OrganizationTagsGetter(p.getOrgTags), Doer: QoderRequestDoer(value, p.httpUpstream, p.tlsFPProfileService)}
}
func (p *QoderTokenProvider) buildSession(ctx context.Context, value *accountcore.Record) (*qoder.SessionContext, time.Time, error) {
	return p.sessionBuilder(value).BuildSession(ctx, QoderCredentialInput(value))
}
func (p *QoderTokenProvider) getOrganizationTagsForAccount(ctx context.Context, value *accountcore.Record, token, uid string) (*qoder.OrganizationTags, error) {
	return p.sessionBuilder(value).GetOrganizationTags(ctx, QoderCredentialInput(value), token, uid)
}

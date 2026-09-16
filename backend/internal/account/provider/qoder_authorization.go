// 本文件把 Qoder 原生协议投影给账号授权用例；状态只存在于 Core。
package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type QoderAuthorization struct {
	Core            *account.QoderAuthorization[*qoder.AuthorizationFlow]
	ResolveProxyURL func(context.Context, *int64) (string, error)
	ClientFactory   qoder.OAuthClientFactory
}

func NewQoderAuthorization(resolve func(context.Context, *int64) (string, error), factory qoder.OAuthClientFactory) *QoderAuthorization {
	p := &QoderAuthorization{ResolveProxyURL: resolve, ClientFactory: factory}
	if p.ClientFactory == nil {
		p.ClientFactory = qoder.DefaultOAuthClientFactory
	}
	p.Core = &account.QoderAuthorization[*qoder.AuthorizationFlow]{Store: account.NewQoderAuthorizationStore[*qoder.AuthorizationFlow](), Prepare: p.prepare, Complete: func(ctx context.Context, flow *qoder.AuthorizationFlow) (*account.QoderTokenInfo, bool, error) {
		token, pending, err := qoder.CompleteAuthorization(ctx, flow, p.ClientFactory)
		return (*account.QoderTokenInfo)(token), pending, err
	}}
	return p
}
func (p *QoderAuthorization) ValidateSite(raw string) (string, error) {
	site, err := qoder.ParseSite(raw)
	return string(site), err
}
func (p *QoderAuthorization) prepare(ctx context.Context, site string, proxyID *int64) (string, *account.QoderAuthorizationSession[*qoder.AuthorizationFlow], *account.QoderAuthURLResult, error) {
	profile, err := qoder.ProfileForSite(qoder.Site(site))
	if err != nil {
		return "", nil, nil, err
	}
	req, err := qoder.NewDeviceAuthRequestForProfile(profile)
	if err != nil {
		return "", nil, nil, fmt.Errorf("generate qoder device auth request: %w", err)
	}
	sessionID := qoder.RandomHex(32)
	state := qoder.RandomToken(32)

	proxyURL, err := p.ResolveProxyURL(ctx, proxyID)
	if err != nil {
		return "", nil, nil, err
	}

	// 国内站必须复用授权 URL 中的 UUID machine_id，并保持其余机器字段为空。
	machine := qoder.NewMachineForSite(profile.Site)
	machine.MachineID = req.MachineID
	flow := &qoder.AuthorizationFlow{
		Nonce:        req.Nonce,
		CodeVerifier: req.CodeVerifier,
		Machine:      machine,
		AuthURL:      req.AuthorizationURL(),
		Site:         profile.Site,
		Profile:      profile,
		ProxyURL:     proxyURL,
	}
	session := &account.QoderAuthorizationSession[*qoder.AuthorizationFlow]{State: state, Flow: flow, CreatedAt: time.Now()}
	return sessionID, session, &account.QoderAuthURLResult{AuthURL: flow.AuthURL, SessionID: sessionID, State: state, ExpiresIn: int64(account.QoderOAuthSessionTTL / time.Second), Interval: account.QoderOAuthPollInterval, Site: string(profile.Site)}, nil
}

// 以下委托供 HTTP 消费，实际会话状态和权限顺序由账号用例拥有。
func (p *QoderAuthorization) GenerateAuthURLForSite(ctx context.Context, site string, id *int64) (*account.QoderAuthURLResult, error) {
	return p.Core.GenerateAuthURLForSite(ctx, site, id)
}
func (p *QoderAuthorization) ExchangeCode(ctx context.Context, input *account.QoderExchangeCodeInput) (*account.QoderTokenInfo, error) {
	return p.Core.ExchangeCode(ctx, input)
}
func (p *QoderAuthorization) Poll(ctx context.Context, id, state string, proxy *int64) (*account.QoderPollResult, error) {
	return p.Core.Poll(ctx, id, state, proxy)
}

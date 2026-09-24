//go:build unit

package provider_test

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type grokOAuthClientStub struct {
	refreshResponse     *xai.TokenResponse
	ssoResponse         *xai.TokenResponse
	loginResult         *accountcore.GrokPasswordLoginResult
	loginEmail          string
	loginPassword       string
	exchangeCalls       int
	exchangeRedirectURI string
}

func (s *grokOAuthClientStub) ExchangeCode(_ context.Context, _, _, redirectURI, _, _ string) (*xai.TokenResponse, error) {
	s.exchangeCalls++
	s.exchangeRedirectURI = redirectURI
	return &xai.TokenResponse{AccessToken: "access-token"}, nil
}

func (s *grokOAuthClientStub) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return s.refreshResponse, nil
}

func (s *grokOAuthClientStub) LoginWithPassword(_ context.Context, email, password, _ string) (*accountcore.GrokPasswordLoginResult, error) {
	s.loginEmail = email
	s.loginPassword = password
	return s.loginResult, nil
}

func (s *grokOAuthClientStub) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return s.ssoResponse, nil
}

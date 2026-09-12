// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
)

func linuxDoProviderOptions(cfg config.LinuxDoConnectConfig) provider.LinuxDoOptions {
	return provider.LinuxDoOptions{Enabled: cfg.Enabled, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, AuthorizeURL: cfg.AuthorizeURL, TokenURL: cfg.TokenURL, UserInfoURL: cfg.UserInfoURL, Scopes: cfg.Scopes, RedirectURL: cfg.RedirectURL, FrontendRedirectURL: cfg.FrontendRedirectURL, TokenAuthMethod: cfg.TokenAuthMethod, UsePKCE: cfg.UsePKCE, UserInfoEmailPath: cfg.UserInfoEmailPath, UserInfoIDPath: cfg.UserInfoIDPath, UserInfoUsernamePath: cfg.UserInfoUsernamePath}
}

func oidcProviderOptions(cfg config.OIDCConnectConfig) provider.OIDCOptions {
	return provider.OIDCOptions{Enabled: cfg.Enabled, ProviderName: cfg.ProviderName, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, IssuerURL: cfg.IssuerURL, DiscoveryURL: cfg.DiscoveryURL, AuthorizeURL: cfg.AuthorizeURL, TokenURL: cfg.TokenURL, UserInfoURL: cfg.UserInfoURL, JWKSURL: cfg.JWKSURL, Scopes: cfg.Scopes, RedirectURL: cfg.RedirectURL, FrontendRedirectURL: cfg.FrontendRedirectURL, TokenAuthMethod: cfg.TokenAuthMethod, UsePKCE: cfg.UsePKCE, ValidateIDToken: cfg.ValidateIDToken, UsePKCEExplicit: cfg.UsePKCEExplicit, ValidateIDTokenExplicit: cfg.ValidateIDTokenExplicit, AllowedSigningAlgs: cfg.AllowedSigningAlgs, ClockSkewSeconds: cfg.ClockSkewSeconds, RequireEmailVerified: cfg.RequireEmailVerified, UserInfoEmailPath: cfg.UserInfoEmailPath, UserInfoIDPath: cfg.UserInfoIDPath, UserInfoUsernamePath: cfg.UserInfoUsernamePath}
}

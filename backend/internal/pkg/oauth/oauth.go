// 兼容入口只委托账号授权状态及平台协议，S15/S16 清理。
package oauth

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/oauth"
)

type OAuthSession = account.ClaudeAuthorizationSession
type SessionStore = account.ClaudeAuthorizationSessions

func NewSessionStore() *SessionStore { return account.NewClaudeAuthorizationSessions() }

const ClientID = native.ClientID
const AuthorizeURL = native.AuthorizeURL
const TokenURL = native.TokenURL
const RedirectURI = native.RedirectURI
const ScopeOAuth = native.ScopeOAuth
const ScopeAPI = native.ScopeAPI
const ScopeInference = native.ScopeInference
const SessionTTL = native.SessionTTL

func GenerateRandomBytes(n int) ([]byte, error)    { return native.GenerateRandomBytes(n) }
func GenerateState() (string, error)               { return native.GenerateState() }
func GenerateSessionID() (string, error)           { return native.GenerateSessionID() }
func GenerateCodeVerifier() (string, error)        { return native.GenerateCodeVerifier() }
func GenerateCodeChallenge(verifier string) string { return native.GenerateCodeChallenge(verifier) }
func BuildAuthorizationURL(state, codeChallenge, scope string) string {
	return native.BuildAuthorizationURL(state, codeChallenge, scope)
}

type TokenResponse = native.TokenResponse
type OrgInfo = native.OrgInfo
type AccountInfo = native.AccountInfo

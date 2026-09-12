// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	http "net/http"
)

var ErrPasskeysDisabled = identity.ErrPasskeysDisabled

var ErrPasskeyNotFound = identity.ErrPasskeyNotFound

var ErrPasskeyExists = identity.ErrPasskeyExists

var ErrPasskeySession = identity.ErrPasskeySession

var ErrPasskeyVerify = identity.ErrPasskeyVerify

type PasskeyCredentialRecord = identity.PasskeyCredentialRecord

type PasskeyRepository = identity.PasskeyRepository

type PasskeySession = identity.PasskeySession

type PasskeySessionStore = identity.PasskeySessionStore

type PasskeyCredentialSummary = identity.PasskeyCredentialSummary

// PasskeyService 为旧装配保留名称，状态与算法均由身份模块持有。
type PasskeyService struct{ *identity.PasskeyService }

func NewPasskeyService(cfg *config.Config, repo PasskeyRepository, sessions PasskeySessionStore, users UserRepository) (*PasskeyService, error) {
	options := provider.PasskeyOptions{}
	if cfg != nil {
		options = provider.PasskeyOptions{Enabled: cfg.WebAuthn.Enabled, RPID: cfg.WebAuthn.RPID, RPDisplayName: cfg.WebAuthn.RPDisplayName, RPOrigins: cfg.WebAuthn.RPOrigins}
	}
	verifier, err := provider.NewPasskeyVerifier(options)
	if err != nil {
		return nil, err
	}
	return &PasskeyService{PasskeyService: identity.NewPasskeyService(options.Enabled, verifier, repo, sessions, identitySessionUsers{Repository: users})}, nil
}
func (s *PasskeyService) FinishRegistration(ctx context.Context, userID int64, token, name string, request *http.Request) (*PasskeyCredentialSummary, error) {
	return s.PasskeyService.FinishRegistration(ctx, userID, token, name, request.Body)
}
func (s *PasskeyService) FinishLogin(ctx context.Context, token string, request *http.Request) (*User, error) {
	u, err := s.PasskeyService.FinishLogin(ctx, token, request.Body)
	return UserFromIdentity(u), err
}

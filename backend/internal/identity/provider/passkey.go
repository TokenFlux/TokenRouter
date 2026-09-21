// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	fmt "fmt"
	io "io"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	protocol "github.com/go-webauthn/webauthn/protocol"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
)

// PasskeyOptions 是启动时固定的 RP 配置。
type PasskeyOptions struct {
	Enabled       bool
	RPDisplayName string
	RPID          string
	RPOrigins     []string
}

// WebAuthnVerifier 复用已有 SDK 算法，避免核心持有 HTTP 请求。
type WebAuthnVerifier struct{ *webauthn.WebAuthn }

func NewPasskeyVerifier(options PasskeyOptions) (identity.PasskeyVerifier, error) {
	if !options.Enabled {
		return nil, nil
	}
	instance, err := webauthn.New(&webauthn.Config{RPDisplayName: options.RPDisplayName, RPID: options.RPID, RPOrigins: options.RPOrigins, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
	if err != nil {
		return nil, fmt.Errorf("initialize WebAuthn: %w", err)
	}
	return &WebAuthnVerifier{WebAuthn: instance}, nil
}
func (v *WebAuthnVerifier) FinishRegistration(user webauthn.User, session webauthn.SessionData, response io.Reader) (*webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBody(response)
	if err != nil {
		return nil, err
	}
	return v.CreateCredential(user, session, parsed)
}
func (v *WebAuthnVerifier) FinishPasskeyLogin(handler webauthn.DiscoverableUserHandler, session webauthn.SessionData, response io.Reader) (webauthn.User, *webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBody(response)
	if err != nil {
		return nil, nil, err
	}
	return v.ValidatePasskeyLogin(handler, session, parsed)
}

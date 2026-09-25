//go:build unit

// 验证 SSO worker 的 panic 脱敏，以及失败项与输入索引的对应关系。
package account

import (
	"context"
	"testing"

	wiregrok "github.com/TokenFlux/TokenRouter/internal/protocol/grok"
	"github.com/stretchr/testify/require"
)

type grokSSOPanicClient struct{}

func (grokSSOPanicClient) ExchangeCode(context.Context, string, string, string, string, string) (*wiregrok.TokenResponse, error) {
	return nil, nil
}
func (grokSSOPanicClient) RefreshToken(context.Context, string, string, string) (*wiregrok.TokenResponse, error) {
	return nil, nil
}
func (grokSSOPanicClient) LoginWithPassword(context.Context, string, string, string) (*wiregrok.PasswordLoginResult, error) {
	return nil, nil
}
func (grokSSOPanicClient) ConvertSSOToBuild(_ context.Context, ssoToken, _ string) (*wiregrok.TokenResponse, error) {
	panic(ssoToken)
}
func TestGrokSSOImportWorkerRecoversPanicWithoutExposingToken(t *testing.T) {
	const sensitiveToken = "sensitive-sso-token"
	oauthService := NewGrokAuthorization(grokSSOPanicClient{}, GrokAuthorizationOptions{})
	oauthService.Start()
	defer func() { _ = oauthService.StopContext(context.Background()) }()
	h := &GrokAccountImport{Authorization: oauthService, Options: GrokAccountImportOptions{LogError: func(string, ...any) {}}}

	// worker 必须把 panic 转换为失败项，同时不能在响应中回显令牌。
	result := h.safeCreateAccountFromSSOToken(context.Background(), GrokSSOToOAuthRequest{}, sensitiveToken, 2, 3)
	require.False(t, result.created)
	require.Equal(t, 2, result.item.Index)
	require.Equal(t, "internal worker panic", result.item.Error)
	require.NotContains(t, result.item.Error, sensitiveToken)
}

//go:build unit

package httpapi

import (
	"testing"

	accounterrors "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestAccountTestServiceSkipsShadow 验证影子账号连接测试不再早拒,而是尝试解析母账号凭据。
func TestAccountTestServiceSkipsShadow(t *testing.T) {
	pid := int64(100)
	shadow := &accounterrors.Record{
		ID:              200,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &pid,
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{accountsByID: map[int64]*accounterrors.Record{shadow.ID: shadow}}}
	svc := &provider.OpenAIAccountTest{Store: repo}
	c, _ := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, 200, "", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve spark shadow parent")
}

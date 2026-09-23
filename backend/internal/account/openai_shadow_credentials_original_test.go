package account

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestGetAccessToken_SparkShadowResolvesToParent 验证对影子账号调用 GetAccessToken
// 时能透明地解析到母账号的凭据，防止 refresh_token 脱钩。
// 影子账号不持凭据；断言必须返回母账号的 access_token。
func TestGetAccessToken_SparkShadowResolvesToParent(t *testing.T) {
	ctx := context.Background()

	parentID := int64(100)
	parent := Record{
		ID:       parentID,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"access_token": "parent-access-token",
		},
	}
	shadow := Record{
		ID:              200,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		// 影子账号不持凭据，与生产语义一致
	}

	svc := &OpenAIExecutionCredentials{Parent: func(ctx context.Context, id int64) (*Record, error) {
		require.Equal(t, parentID, id)
		return &parent, nil
	}}

	// 影子自身不持凭据，必须由唯一原生解析器读取母账号。
	// 返回母账号的现有 token，不执行额外刷新或查询。
	token, tokenType, err := svc.Resolve(ctx, &shadow)
	require.NoError(t, err)
	require.Equal(t, "parent-access-token", token)
	require.Equal(t, "oauth", tokenType)
}

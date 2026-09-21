//go:build unit

package account_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestPropagateAccountProxyToShadows 外审第8轮:CRS/AdminService 改母账号 proxy 后,
// 影子 proxy 必须跟随(影子 proxy 恒继承母账号,否则出站漂移)。
func TestPropagateAccountProxyToShadows(t *testing.T) {
	ctx := context.Background()
	repo := newCRSShadowStore()

	oldProxy := int64(11)
	mother := &account.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, ProxyID: &oldProxy}
	require.NoError(t, repo.Create(ctx, mother))
	parentID := mother.ID

	shadow := &account.Record{
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  account.QuotaDimensionSpark,
		ProxyID:         &oldProxy,
	}
	require.NoError(t, repo.Create(ctx, shadow))

	newProxy := int64(22)
	require.NoError(t, account.PropagateAccountProxyToShadows(ctx, repo, parentID, &newProxy))

	got, err := repo.GetByID(ctx, shadow.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ProxyID)
	require.Equal(t, newProxy, *got.ProxyID, "shadow proxy must follow the parent's new proxy")

	// 清空母 proxy 也应传播为 nil。
	require.NoError(t, account.PropagateAccountProxyToShadows(ctx, repo, parentID, nil))
	got, err = repo.GetByID(ctx, shadow.ID)
	require.NoError(t, err)
	require.Nil(t, got.ProxyID, "clearing parent proxy must clear the shadow proxy too")
}

// TestGuardCRSShadowParentInvariant 外审第8/9轮:有 spark 影子的母账号经 CRS 任意分支更新后,目标结果
// 必须仍是 OpenAI OAuth;否则(改 api_key 或跨平台 Anthropic/Gemini)影子读透母凭据失败、spark 全崩。
func TestGuardCRSShadowParentInvariant(t *testing.T) {
	ctx := context.Background()
	repo := newCRSShadowStore()

	mother := &account.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	require.NoError(t, repo.Create(ctx, mother))
	parentID := mother.ID

	// 无影子:任何目标都放行(含改离 OpenAI OAuth)。
	require.NoError(t, account.GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformOpenAI, capability.AccountTypeAPIKey))
	require.NoError(t, account.GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformAnthropic, capability.AccountTypeOAuth))

	// 建一个影子后:
	shadow := &account.Record{
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  account.QuotaDimensionSpark,
	}
	require.NoError(t, repo.Create(ctx, shadow))

	// 翻成 OpenAI api_key 被拒。
	err := account.GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformOpenAI, capability.AccountTypeAPIKey)
	require.Error(t, err, "must reject converting a shadow parent to openai api_key")
	require.Contains(t, err.Error(), "spark-shadow parent")

	// 跨平台改成 Anthropic OAuth(Type 仍 OAuth、仅 Platform 变)也被拒——第8轮只查 Type 的版本会漏。
	require.Error(t, account.GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformAnthropic, capability.AccountTypeOAuth),
		"must reject moving a shadow parent to a non-OpenAI platform even if type stays oauth")

	// 改成 Gemini api_key 被拒。
	require.Error(t, account.GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformGemini, capability.AccountTypeAPIKey))

	// 保持 OpenAI OAuth(重新同步母账号)放行,即便仍有影子。
	require.NoError(t, account.GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformOpenAI, capability.AccountTypeOAuth))
}

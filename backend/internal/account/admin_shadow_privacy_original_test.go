//go:build unit

package account_test

import (
	"context"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

// TestForceOpenAIPrivacy_SkipsShadow 验证外审第4轮:影子隐私设置跳过(由母账号管理),
// 早返不触碰任何依赖(svc 无 deps,若未守卫会 nil panic)。
func TestForceOpenAIPrivacy_SkipsShadow(t *testing.T) {
	svc := accountcore.NewPrivacyService(nil, nil, accountcore.PrivacyOptions{})
	pid := int64(1)
	shadow := &accountcore.Record{ID: 2, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, ParentAccountID: &pid}
	require.Equal(t, "", svc.ForceOpenAIPrivacy(context.Background(), shadow), "影子隐私设置应跳过")
}

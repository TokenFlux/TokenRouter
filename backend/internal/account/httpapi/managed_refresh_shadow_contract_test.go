package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// TestRefreshSingleAccount_RejectsShadow 验证外审第6轮:手动刷新对 spark 影子在调用上游前早拒
// (影子凭据由母账号管理、自身恒空,刷新无意义)。该守卫同时覆盖单账号与批量刷新两入口。
func TestRefreshSingleAccount_RejectsShadow(t *testing.T) {
	h := account.NewManagedRefreshService(account.ManagedRefreshOptions{}) // 影子在使用任何依赖前即返回,无需注入
	parentID := int64(5)
	shadow := &account.Record{
		ID:              9,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth, // IsOAuth()=true,确保不是先撞 NOT_OAUTH
		ParentAccountID: &parentID,
		QuotaDimension:  account.QuotaDimensionSpark,
	}

	_, _, err := h.Refresh(context.Background(), shadow)
	require.Error(t, err, "影子刷新应被早拒")
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
}

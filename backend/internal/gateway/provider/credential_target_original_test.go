package provider

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// stubCredRepo 是最小化 AccountRepository stub，仅实现 GetByID，供 credential_shadow_test 使用。
// 嵌入接口满足完整方法集；未实现的方法若被调用会 panic，从而快速暴露误调用。
type stubCredRepo struct {
	ExecutionAccountStore

	parent *ExecutionAccount
}

func (s *stubCredRepo) GetByID(_ context.Context, _ int64) (*ExecutionAccount, error) {
	return s.parent, nil
}

func newStubCredRepo(parent *ExecutionAccount) ExecutionAccountStore {
	return &stubCredRepo{parent: parent}
}

func TestResolveCredentialAccount(t *testing.T) {
	ctx := context.Background()
	pid := int64(100)

	// 普通账号（非影子）→ 返回自身
	parent := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 100, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}}
	repo := newStubCredRepo(parent)
	got, err := CredentialAccount(ctx, repo, parent)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.Record.ID)

	// 影子账号 + 合法 OpenAI OAuth 母账号 → 返回母账号
	shadow := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 200, ParentAccountID: &pid, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	got, err = CredentialAccount(ctx, repo, shadow)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.Record.ID)

	// 影子账号 + 母账号非 OpenAI OAuth（API Key 类型）→ 返回 error
	badRepo := newStubCredRepo(&ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 100, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}})
	_, err = CredentialAccount(ctx, badRepo, shadow)
	require.Error(t, err)
}

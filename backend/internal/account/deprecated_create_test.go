//go:build unit

// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	require "github.com/stretchr/testify/require"
	testing "testing"
	time "time"
)

func TestAdminServiceCreateAccountDiscardsDeprecatedLongContextBillingExtra(t *testing.T) {
	repo := &deprecatedCreateStore{}
	svc := NewAdmin(repo, AdminOptions{Creation: CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation}, Credentials: CreateCredentialHooks{Validate: func(context.Context, *Record) error { return nil }}})

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "openai-account",
		Platform:             PlatformOpenAI,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "test"},
		Extra:                map[string]any{"openai_long_context_billing_enabled": "malformed", "preserved": true},
		SkipDefaultGroupBind: true,
	})

	require.NoError(t, err)
	require.Same(t, account, repo.createdAccount)
	require.NotContains(t, account.Extra, "openai_long_context_billing_enabled")
	require.Equal(t, true, account.Extra["preserved"])
}

// 指针同一性断言跟随创建实现，兼容层的旧模型投影单独由 HTTP/消费者测试覆盖。
type deprecatedCreateStore struct {
	AdminStore
	createdAccount *Record
}

func (s *deprecatedCreateStore) Create(_ context.Context, value *Record) error {
	value.ID = 1
	s.createdAccount = value
	return nil
}

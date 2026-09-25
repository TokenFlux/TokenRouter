package provider

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// stubChatGPTHeadersRepo 是最小化 AccountRepository stub，仅实现 GetByID，
// 供 TestResolveAndSetOpenAIChatGPTAccountHeaders 使用。
type stubChatGPTHeadersRepo struct {
	ExecutionAccountStore

	byID map[int64]*ExecutionAccount
}

func (r *stubChatGPTHeadersRepo) GetByID(_ context.Context, id int64) (*ExecutionAccount, error) {
	return r.byID[id], nil
}

func TestResolveAndSetOpenAIChatGPTAccountHeaders(t *testing.T) {
	ctx := context.Background()
	pid := int64(100)

	parentCreds := map[string]any{"chatgpt_account_id": "org-parent"}
	parent := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 100,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: parentCreds},
	}
	repo := &stubChatGPTHeadersRepo{byID: map[int64]*ExecutionAccount{100: parent}}

	t.Run("shadow_resolves_to_parent_org", func(t *testing.T) {
		shadow := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 200,
			ParentAccountID: &pid,
			Platform:        capability.PlatformOpenAI,
			Type:            capability.AccountTypeOAuth},
		}
		headers := make(http.Header)
		err := CredentialChatGPTHeaders(ctx, repo, headers, shadow)
		require.NoError(t, err)
		require.Equal(t, "org-parent", headers.Get("chatgpt-account-id"),
			"影子账号应透传母账号的 chatgpt-account-id")
	})

	t.Run("normal_account_passthrough", func(t *testing.T) {
		ownCreds := map[string]any{"chatgpt_account_id": "org-own"}
		normal := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 300,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Credentials: ownCreds},
		}
		headers := make(http.Header)
		err := CredentialChatGPTHeaders(ctx, repo, headers, normal)
		require.NoError(t, err)
		require.Equal(t, "org-own", headers.Get("chatgpt-account-id"),
			"普通账号应透传自身的 chatgpt-account-id")
	})
}

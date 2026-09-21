package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func (s *managementMutationFixture) GetAccount(ctx context.Context, id int64) (*accountcore.Record, error) {
	for i := range s.accounts {
		if s.accounts[i].ID == id {
			account := s.accounts[i]
			return &account, nil
		}
	}
	account := accountcore.Record{ID: id, Name: "account", Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	return &account, nil
}

func (s *managementMutationFixture) GetAccountsByIDs(ctx context.Context, ids []int64) ([]*accountcore.Record, error) {
	out := make([]*accountcore.Record, 0, len(ids))
	for _, id := range ids {
		found := false
		for i := range s.accounts {
			if s.accounts[i].ID == id {
				account := s.accounts[i]
				out = append(out, &account)
				found = true
				break
			}
		}
		if found {
			continue
		}
		account := accountcore.Record{ID: id, Name: "account", Status: billing.StatusActive}
		out = append(out, &account)
	}
	return out, nil
}

func (s *managementMutationFixture) UpdateAccount(ctx context.Context, id int64, input *accountcore.UpdateAccountInput) (*accountcore.Record, error) {
	// 夹具模拟锁内身份条件；无条件管理请求继续保留原行为。
	if input.ExpectedCredentials != nil {
		for i := range s.accounts {
			if s.accounts[i].ID == id && !accountcore.MatchesCredentialVersion(&s.accounts[i], *input.ExpectedCredentials) {
				return nil, accountcore.ErrRefreshAccountStateChanged
			}
		}
	}

	if s.updateAccountErr != nil {
		return nil, s.updateAccountErr
	}
	s.updateAccountInput = input
	for i := range s.accounts {
		if s.accounts[i].ID == id {
			if input.Credentials != nil {
				if input.PatchCredentials {
					s.accounts[i].Credentials = accountcore.MergeCredentials(s.accounts[i].Credentials, input.Credentials)
				} else {
					s.accounts[i].Credentials = input.Credentials
				}
			}
			account := s.accounts[i]
			return &account, nil
		}
	}
	account := accountcore.Record{ID: id, Name: input.Name, Platform: capability.PlatformAnthropic, Type: input.Type, Status: billing.StatusActive, Credentials: input.Credentials}
	return &account, nil
}

func (s *managementMutationFixture) UpdateAccountExtra(ctx context.Context, id int64, updates map[string]any) error {
	s.updateExtraCalls = append(s.updateExtraCalls, updates)
	return nil
}

func (s *managementMutationFixture) ClearAccountError(ctx context.Context, id int64) (*accountcore.Record, error) {
	s.clearAccountErrorIDs = append(s.clearAccountErrorIDs, id)
	account := accountcore.Record{ID: id, Name: "account", Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	return &account, nil
}

func (s *managementMutationFixture) BulkUpdateAccounts(ctx context.Context, input *accountcore.BulkUpdateAccountsInput) (*accountcore.BulkUpdateAccountsResult, error) {
	s.lastBulkUpdateAccountInput = input
	if s.bulkUpdateAccountErr != nil {
		return nil, s.bulkUpdateAccountErr
	}
	return &accountcore.BulkUpdateAccountsResult{Success: len(input.AccountIDs), Failed: 0, SuccessIDs: input.AccountIDs}, nil
}

func (s *managementMutationFixture) EnsureOpenAIPrivacy(ctx context.Context, account *accountcore.Record) string {
	return ""
}

func (s *managementMutationFixture) EnsureAntigravityPrivacy(ctx context.Context, account *accountcore.Record) string {
	return ""
}

// managementMutationFixture 记录配置、凭据与失效输入，复用原独立存储替身语义。
type managementMutationFixture struct {
	checkMixedErr  error
	lastMixedCheck struct {
		accountID int64
		platform  string
		groupIDs  []int64
	}
	managementCreateFixture
	accounts                   []accountcore.Record
	updateAccountInput         *accountcore.UpdateAccountInput
	updateAccountErr           error
	updateExtraCalls           []map[string]any
	clearAccountErrorIDs       []int64
	lastBulkUpdateAccountInput *accountcore.BulkUpdateAccountsInput
	bulkUpdateAccountErr       error
}

func newManagementMutationFixture() *managementMutationFixture { return &managementMutationFixture{} }

// newMutationHandler 直接组合账号用例和展示层，不经过旧管理服务。
func newMutationHandler(source *managementMutationFixture, invalidator accountcore.TokenCacheInvalidator) *ManagementHandler {
	options := accountcore.ManagedRefreshOptions{Store: source, Privacy: source}
	if invalidator != nil {
		options.Invalidate = invalidator.InvalidateToken
	}
	managed := accountcore.NewManagedRefreshService(options)
	presenter := NewRuntimePresenter(accountcore.NewRuntimeStatusReader(accountcore.RuntimeStatusOptions{}), source, nil)
	batch := accountcore.NewManagementBatch(source, managed, accountcore.ManagementCreationOptions{Privacy: source})
	return NewManagementHandler(source, ManagementOptions{Managed: managed, Presenter: presenter, RuntimePresenter: presenter, Privacy: source, Batch: batch})
}

// CheckMixedChannelRisk 保留原管理请求的字段传递及受控冲突。
func (s *managementMutationFixture) CheckMixedChannelRisk(_ context.Context, id int64, platform string, groups []int64) error {
	s.lastMixedCheck.accountID = id
	s.lastMixedCheck.platform = platform
	s.lastMixedCheck.groupIDs = append([]int64(nil), groups...)
	return s.checkMixedErr
}

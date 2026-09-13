package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

func (s *adminServiceImpl) ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]Account, int64, error) {
	v, total, err := s.accountAdministration().ListAccounts(ctx, page, pageSize, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder)
	return AccountsFromRecords(v), total, err
}

func (s *adminServiceImpl) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error) {
	v, err := s.accountAdministration().ListAccountsForSchedulerScoreFilter(ctx, platform, accountType, status, search, groupID, privacyMode)
	return AccountsFromRecords(v), err
}

func (s *adminServiceImpl) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]Account, error) {
	v, err := s.accountAdministration().ListSchedulableAccountsForAdvancedSchedulerScore(ctx, groupID, platform)
	return AccountsFromRecords(v), err
}

func (s *adminServiceImpl) GetAccount(ctx context.Context, id int64) (*Account, error) {
	v, err := s.accountAdministration().GetAccount(ctx, id)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) GetAccountsByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	v, err := s.accountAdministration().GetAccountsByIDs(ctx, ids)
	if v == nil {
		return nil, err
	}
	out := make([]*Account, len(v))
	for i := range v {
		out[i] = AccountFromRecord(v[i])
	}
	return out, err
}

const (
	// 废弃账号扩展键只用于阻止历史客户端重新写入，业务代码不得再读取这些值。
	deprecatedUpstreamBillingProbeExtraKey        = "upstream_billing_probe"
	deprecatedUpstreamBillingProbeEnabledExtraKey = "upstream_billing_probe_enabled"
	deprecatedOpenAILongContextBillingExtraKey    = "openai_long_context_billing_enabled"
)

func DiscardDeprecatedAccountExtra(extra map[string]any) {
	acctcore.DiscardDeprecatedAccountExtra(extra)
}

func NormalizeDeprecatedAccountExtraUpdate(extra map[string]any) (map[string]any, bool) {
	return acctcore.NormalizeDeprecatedAccountExtraUpdate(extra)
}

func (s *adminServiceImpl) RecoverDuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Account, error) {
	v, err := s.accountAdministration().RecoverDuplicateAccount(ctx, id, actorScope, operationKey)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) DuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Account, error) {
	v, err := s.accountAdministration().DuplicateAccount(ctx, id, actorScope, operationKey)
	return AccountFromRecord(v), err
}

func normalizeCNProviderCredentials(account *Account, isCreate bool) error {
	v := protocolRecord(account)
	err := acctcore.NormalizeCNProviderCredentials(v, isCreate)
	applyProtocolRecord(account, v)
	return err
}

func ValidateGrokMediaEligibilityExtra(platform string, extra map[string]any) error {
	return acctcore.ValidateGrokMediaEligibilityExtra(platform, extra)
}

func buildAccountForCreate(input *CreateAccountInput, accountExtra map[string]any) (*Account, error) {
	v, err := acctcore.BuildAccountForCreate(input, accountExtra, acctcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: newCodexFingerprintSeed})
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) CreateAccount(ctx context.Context, input *CreateAccountInput) (*Account, error) {
	v, err := s.accountAdministration().CreateAccount(ctx, input)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) UpdateAccount(ctx context.Context, id int64, input *UpdateAccountInput) (*Account, error) {
	v, err := s.accountAdministration().UpdateAccount(ctx, id, input)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) UpdateAccountExtra(ctx context.Context, id int64, updates map[string]any) error {
	return s.accountAdministration().UpdateAccountExtra(ctx, id, updates)
}

func (s *adminServiceImpl) BulkUpdateAccounts(ctx context.Context, input *BulkUpdateAccountsInput) (*BulkUpdateAccountsResult, error) {
	return s.accountAdministration().BulkUpdateAccounts(ctx, input)
}

func (s *adminServiceImpl) DeleteAccount(ctx context.Context, id int64) error {
	return s.accountAdministration().DeleteAccount(ctx, id)
}

func (s *adminServiceImpl) RefreshAccountCredentials(ctx context.Context, id int64) (*Account, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// TODO: Implement refresh logic
	return account, nil
}

func (s *adminServiceImpl) ClearAccountError(ctx context.Context, id int64) (*Account, error) {
	v, err := s.accountAdministration().ClearAccountError(ctx, id)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) SetAccountError(ctx context.Context, id int64, errorMsg string) error {
	return s.accountAdministration().SetAccountError(ctx, id, errorMsg)
}

func (s *adminServiceImpl) SetAccountSchedulable(ctx context.Context, id int64, schedulable bool) (*Account, error) {
	v, err := s.accountAdministration().SetAccountSchedulable(ctx, id, schedulable)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) RevertAccountProxyFallback(ctx context.Context, id int64) error {
	return s.accountAdministration().RevertAccountProxyFallback(ctx, id)
}

func (s *adminServiceImpl) CreateShadow(ctx context.Context, parentID int64, opts ShadowOptions) (*Account, error) {
	v, err := s.accountAdministration().CreateShadow(ctx, parentID, opts)
	return AccountFromRecord(v), err
}

func (s *adminServiceImpl) CheckMixedChannelRisk(ctx context.Context, currentAccountID int64, currentAccountPlatform string, groupIDs []int64) error {
	return s.accountAdministration().CheckMixedChannelRisk(ctx, currentAccountID, currentAccountPlatform, groupIDs)
}

type MixedChannelError = acctcore.MixedChannelError

func (s *adminServiceImpl) ResetAccountQuota(ctx context.Context, id int64) error {
	return s.accountAdministration().ResetAccountQuota(ctx, id)
}

func (s *adminServiceImpl) EnsureOpenAIPrivacy(ctx context.Context, account *Account) string {
	view := privacyRecord(account)
	mode := s.accountAdministration().Privacy().EnsureOpenAIPrivacy(ctx, view)
	if account != nil && view != nil {
		account.Extra = view.Extra
	}
	return mode
}

func (s *adminServiceImpl) ForceOpenAIPrivacy(ctx context.Context, account *Account) string {
	view := privacyRecord(account)
	mode := s.accountAdministration().Privacy().ForceOpenAIPrivacy(ctx, view)
	if account != nil && view != nil {
		account.Extra = view.Extra
	}
	return mode
}

func (s *adminServiceImpl) EnsureAntigravityPrivacy(ctx context.Context, account *Account) string {
	view := privacyRecord(account)
	mode := s.accountAdministration().Privacy().EnsureAntigravityPrivacy(ctx, view)
	if account != nil && view != nil {
		account.Extra = view.Extra
	}
	return mode
}

func (s *adminServiceImpl) ForceAntigravityPrivacy(ctx context.Context, account *Account) string {
	view := privacyRecord(account)
	mode := s.accountAdministration().Privacy().ForceAntigravityPrivacy(ctx, view)
	if account != nil && view != nil {
		account.Extra = view.Extra
	}
	return mode
}

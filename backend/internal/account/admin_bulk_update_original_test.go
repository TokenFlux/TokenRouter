//go:build unit

package account_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type accountRepoStubForBulkUpdate struct {
	accountcore.AdminStore
	bulkUpdateErr    error
	bulkUpdateIDs    []int64
	lastBulkUpdate   accountcore.AccountBulkUpdate
	bindGroupErrByID map[int64]error
	bindGroupsCalls  []int64
	getByIDsAccounts []*accountcore.Record
	getByIDsErr      error
	getByIDsCalled   bool
	getByIDsIDs      []int64
	getByIDAccounts  map[int64]*accountcore.Record
	getByIDErrByID   map[int64]error
	getByIDCalled    []int64
	listByGroupData  map[int64][]accountcore.Record
	listByGroupErr   map[int64]error
	listData         []accountcore.Record
	listResult       *pagination.PaginationResult
	listErr          error
	listCalled       bool
	lastListParams   pagination.PaginationParams
	lastListFilters  struct {
		platform    string
		accountType string
		status      string
		search      string
		groupID     int64
		privacyMode string
	}
}

func (s *accountRepoStubForBulkUpdate) BulkUpdate(_ context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	s.bulkUpdateIDs = append([]int64{}, ids...)
	s.lastBulkUpdate = updates
	if s.bulkUpdateErr != nil {
		return 0, s.bulkUpdateErr
	}
	return int64(len(ids)), nil
}

func TestAdminServiceBulkUpdateAccountsNormalizesLegacyOpenAIConfiguration(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*accountcore.Record{{
			ID:       1,
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
		}},
	}
	svc := newOriginalAccountEditor(repo)
	input := &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Credentials: map[string]any{
			accountcore.LegacyOpenAICapabilitiesCredentialKey: []any{"chat_completions"},
		},
		Extra: map[string]any{
			accountcore.LegacyOpenAIResponsesModeExtraKey: "auto",
			"openai_responses_supported":                  false,
		},
	}

	_, err := svc.BulkUpdateAccounts(context.Background(), input)

	require.NoError(t, err)
	require.Equal(t, []string{"text_generation"}, repo.lastBulkUpdate.Credentials[accountcore.OpenAIWorkloadCapabilitiesCredentialKey])
	require.NotContains(t, repo.lastBulkUpdate.Credentials, accountcore.LegacyOpenAICapabilitiesCredentialKey)
	require.Equal(t, "preserve_client_protocol", repo.lastBulkUpdate.Extra["openai_text_route_mode"])
	require.NotContains(t, repo.lastBulkUpdate.Extra, "openai_responses_probe_status")
	require.NotContains(t, repo.lastBulkUpdate.Extra, accountcore.LegacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, repo.lastBulkUpdate.Extra, "openai_responses_supported")
}

func TestAdminServiceBulkUpdateAccountsNormalizesOpenAIWorkloadAndTextRoute(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*accountcore.Record{
		{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	}}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Credentials: map[string]any{
			accountcore.OpenAIWorkloadCapabilitiesCredentialKey: []any{"text_generation", "embeddings"},
		},
		Extra: map[string]any{accountcore.ExtraKeyTextRouteMode: "force_responses"},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Equal(t, []string{"text_generation", "embeddings"}, repo.lastBulkUpdate.Credentials[accountcore.OpenAIWorkloadCapabilitiesCredentialKey])
	require.Equal(t, "force_responses", repo.lastBulkUpdate.Extra[accountcore.ExtraKeyTextRouteMode])
}

func TestAdminServiceBulkUpdateAccountsNormalizesOpenAIContinuationCapability(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*accountcore.Record{
		{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	}}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			accountcore.ExtraKeyResponsesContinuationSupported: true,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Equal(t, true, repo.lastBulkUpdate.Extra[accountcore.ExtraKeyResponsesContinuationSupported])
}

func TestAdminServiceBulkUpdateAccountsRejectsContinuationForNonOpenAIAPIKey(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*accountcore.Record{
		{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
	}}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra: map[string]any{
			accountcore.ExtraKeyResponsesContinuationSupported: true,
		},
	})

	require.Nil(t, result)
	var appErr *apperror.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, "OPENAI_CONFIGURATION_TARGET_INVALID", appErr.Reason)
	require.Empty(t, repo.bulkUpdateIDs)
}

func TestAdminServiceBulkUpdateAccountsRejectsInvalidOpenAITargetBeforeWrite(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*accountcore.Record{
		{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
	}}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra:      map[string]any{accountcore.ExtraKeyTextRouteMode: "force_responses"},
	})

	require.Nil(t, result)
	var appErr *apperror.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, "OPENAI_CONFIGURATION_TARGET_INVALID", appErr.Reason)
	require.Empty(t, repo.bulkUpdateIDs)
}

func TestAdminServiceBulkUpdateAccountsRejectsForcedTextRouteWithoutWorkload(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*accountcore.Record{
		{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	}}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Credentials: map[string]any{
			accountcore.OpenAIWorkloadCapabilitiesCredentialKey: []any{"embeddings"},
		},
		Extra: map[string]any{accountcore.ExtraKeyTextRouteMode: "force_responses"},
	})

	require.Nil(t, result)
	var appErr *apperror.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, "OPENAI_TEXT_ROUTE_MODE_INVALID", appErr.Reason)
	require.Empty(t, repo.bulkUpdateIDs)
}

func TestAdminServiceBulkUpdateAccountsRejectsInvalidCNProviderCombination(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*accountcore.Record{{
			ID:       9,
			Platform: capability.PlatformDeepseek,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":      "sk-test",
				"account_mode": accountcore.AccountModePayG,
			},
		}},
	}
	svc := newOriginalAccountEditor(repo)
	_, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs:  []int64{9},
		Credentials: map[string]any{"account_mode": accountcore.AccountModeCoding},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "DeepSeek does not support coding")
	require.Empty(t, repo.bulkUpdateIDs)
}

func (s *accountRepoStubForBulkUpdate) BindGroups(_ context.Context, accountID int64, _ []int64) error {
	s.bindGroupsCalls = append(s.bindGroupsCalls, accountID)
	if err, ok := s.bindGroupErrByID[accountID]; ok {
		return err
	}
	return nil
}

func (s *accountRepoStubForBulkUpdate) GetByIDs(_ context.Context, ids []int64) ([]*accountcore.Record, error) {
	s.getByIDsCalled = true
	s.getByIDsIDs = append([]int64{}, ids...)
	if s.getByIDsErr != nil {
		return nil, s.getByIDsErr
	}
	out := make([]*accountcore.Record, len(s.getByIDsAccounts))
	for i, v := range s.getByIDsAccounts {
		out[i] = accountcore.CloneRecord(v)
	}
	return out, nil
}

func (s *accountRepoStubForBulkUpdate) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	s.getByIDCalled = append(s.getByIDCalled, id)
	if err, ok := s.getByIDErrByID[id]; ok {
		return nil, err
	}
	if account, ok := s.getByIDAccounts[id]; ok {
		return accountcore.CloneRecord(account), nil
	}
	return nil, errors.New("account not found")
}

func (s *accountRepoStubForBulkUpdate) ListByGroup(_ context.Context, groupID int64) ([]accountcore.Record, error) {
	if err, ok := s.listByGroupErr[groupID]; ok {
		return nil, err
	}
	if rows, ok := s.listByGroupData[groupID]; ok {
		return rows, nil
	}
	return nil, nil
}

func (s *accountRepoStubForBulkUpdate) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]accountcore.Record, error) {
	return nil, nil
}

func (s *accountRepoStubForBulkUpdate) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]accountcore.Record, *pagination.PaginationResult, error) {
	s.listCalled = true
	s.lastListParams = params
	s.lastListFilters.platform = platform
	s.lastListFilters.accountType = accountType
	s.lastListFilters.status = status
	s.lastListFilters.search = search
	s.lastListFilters.groupID = groupID
	s.lastListFilters.privacyMode = privacyMode
	if s.listErr != nil {
		return nil, nil, s.listErr
	}
	if s.listResult != nil {
		return s.listData, s.listResult, nil
	}
	return s.listData, &pagination.PaginationResult{Total: int64(len(s.listData))}, nil
}

// TestAdminService_BulkUpdateAccounts_AllSuccessIDs 验证批量更新成功时返回 success_ids/failed_ids。
func TestAdminService_BulkUpdateAccounts_AllSuccessIDs(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{}
	svc := newOriginalAccountEditor(repo)

	schedulable := true
	input := &accountcore.BulkUpdateAccountsInput{
		AccountIDs:  []int64{1, 2, 3},
		Schedulable: &schedulable,
	}

	result, err := svc.BulkUpdateAccounts(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 3, result.Success)
	require.Equal(t, 0, result.Failed)
	require.ElementsMatch(t, []int64{1, 2, 3}, result.SuccessIDs)
	require.Empty(t, result.FailedIDs)
	require.Len(t, result.Results, 3)
}

// TestAdminService_BulkUpdateAccounts_PartialFailureIDs 验证部分失败时 success_ids/failed_ids 正确。
func TestAdminService_BulkUpdateAccounts_PartialFailureIDs(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		bindGroupErrByID: map[int64]error{
			2: errors.New("bind failed"),
		},
	}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{&originalBulkGroups{group: &routing.Group{ID: 10, Name: "g10"}}})

	groupIDs := []int64{10}
	schedulable := false
	input := &accountcore.BulkUpdateAccountsInput{
		AccountIDs:            []int64{1, 2, 3},
		GroupIDs:              &groupIDs,
		Schedulable:           &schedulable,
		SkipMixedChannelCheck: true,
	}

	result, err := svc.BulkUpdateAccounts(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 2, result.Success)
	require.Equal(t, 1, result.Failed)
	require.ElementsMatch(t, []int64{1, 3}, result.SuccessIDs)
	require.ElementsMatch(t, []int64{2}, result.FailedIDs)
	require.Len(t, result.Results, 3)
}

func TestAdminService_BulkUpdateAccounts_NilGroupRepoReturnsError(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{}
	svc := newOriginalAccountEditor(repo)

	groupIDs := []int64{10}
	input := &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		GroupIDs:   &groupIDs,
	}

	result, err := svc.BulkUpdateAccounts(context.Background(), input)
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "group repository not configured")
}

func TestAdminServiceBulkUpdateAccountsRejectsGeminiThirdPartyWithoutCustomBaseURL(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*accountcore.Record{
			{
				ID:       1,
				Platform: capability.PlatformGemini,
				Type:     capability.AccountTypeAPIKey,
				Credentials: map[string]any{
					"base_url": "https://generativelanguage.googleapis.com",
				},
			},
		},
	}
	svc := newOriginalAccountEditor(repo)

	result, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Credentials: map[string]any{
			accountcore.GeminiProviderTypeCredentialKey: accountcore.GeminiProviderTypeThirdParty,
		},
	})

	require.Nil(t, result)
	require.ErrorContains(t, err, "GEMINI_THIRD_PARTY_BASE_URL_REQUIRED")
	require.Empty(t, repo.bulkUpdateIDs)
}

// TestAdminService_BulkUpdateAccounts_MixedChannelPreCheckBlocksOnExistingConflict verifies
// that the global pre-check detects a conflict with existing group members and returns an
// error before any DB write is performed.
func TestAdminService_BulkUpdateAccounts_MixedChannelPreCheckBlocksOnExistingConflict(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*accountcore.Record{
			{ID: 1, Platform: capability.PlatformAntigravity},
		},
		// Group 10 already contains an Anthropic account.
		listByGroupData: map[int64][]accountcore.Record{
			10: {{ID: 99, Platform: capability.PlatformAnthropic}},
		},
	}
	svc := newOriginalAccountEditor(repo, originalShadowGroups{&originalBulkGroups{group: &routing.Group{ID: 10, Name: "target-group"}}})

	groupIDs := []int64{10}
	input := &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		GroupIDs:   &groupIDs,
	}

	result, err := svc.BulkUpdateAccounts(context.Background(), input)
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mixed channel")
	// No BindGroups should have been called since the check runs before any write.
	require.Empty(t, repo.bindGroupsCalls)
}

func TestAdminServiceBulkUpdateAccounts_ResolvesIDsFromFilters(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		listData: []accountcore.Record{
			{ID: 7},
			{ID: 11},
		},
		listResult: &pagination.PaginationResult{Total: 2},
	}
	svc := newOriginalAccountEditor(repo)

	schedulable := true
	input := &accountcore.BulkUpdateAccountsInput{
		Schedulable: &schedulable,
	}

	filtersField := reflect.ValueOf(input).Elem().FieldByName("Filters")
	require.True(t, filtersField.IsValid(), "BulkUpdateAccountsInput should expose Filters for filter-target bulk update")
	require.Equal(t, reflect.Pointer, filtersField.Kind(), "BulkUpdateAccountsInput.Filters should be a pointer field")

	filtersValue := reflect.New(filtersField.Type().Elem())
	filtersValue.Elem().FieldByName("Platform").SetString(capability.PlatformOpenAI)
	filtersValue.Elem().FieldByName("Type").SetString(capability.AccountTypeOAuth)
	filtersValue.Elem().FieldByName("Status").SetString(billing.StatusActive)
	filtersValue.Elem().FieldByName("Group").SetString("12")
	filtersValue.Elem().FieldByName("PrivacyMode").SetString(openai.PrivacyModeCFBlocked)
	filtersValue.Elem().FieldByName("Search").SetString("bulk-target")
	filtersField.Set(filtersValue)

	result, err := svc.BulkUpdateAccounts(context.Background(), input)
	require.NoError(t, err)
	require.True(t, repo.listCalled, "expected filter-target bulk update to resolve matching IDs via account list filters")
	require.Equal(t, capability.PlatformOpenAI, repo.lastListFilters.platform)
	require.Equal(t, capability.AccountTypeOAuth, repo.lastListFilters.accountType)
	require.Equal(t, billing.StatusActive, repo.lastListFilters.status)
	require.Equal(t, "bulk-target", repo.lastListFilters.search)
	require.Equal(t, int64(12), repo.lastListFilters.groupID)
	require.Equal(t, openai.PrivacyModeCFBlocked, repo.lastListFilters.privacyMode)
	require.Equal(t, []int64{7, 11}, repo.bulkUpdateIDs)
	require.Equal(t, 2, result.Success)
	require.Equal(t, 0, result.Failed)
	require.Equal(t, []int64{7, 11}, result.SuccessIDs)
}

// originalBulkGroups 保留批量绑定验证所需的分组存在性读取。
type originalBulkGroups struct {
	routing.GroupRepository
	group *routing.Group
}

func (s *originalBulkGroups) GetByID(context.Context, int64) (*routing.Group, error) {
	return routing.CloneGroup(s.group), nil
}
